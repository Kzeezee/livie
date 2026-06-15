package screens

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kez/livie/agent"
	"github.com/kez/livie/config"
	"github.com/kez/livie/index"
	"github.com/kez/livie/runner"
	"github.com/kez/livie/session"
	"github.com/kez/livie/tui"
	"github.com/kez/livie/tui/components"
	openai "github.com/sashabaranov/go-openai"
)

// quitConfirmMsg fires when the second ctrl+c window expires.
type quitConfirmMsg struct{}

// bashCompletionsMsg delivers shell completion results to the update loop.
type bashCompletionsMsg struct {
	completions []string
	prefix      string
	wordStart   int
}

// bashCompleteCmd runs bash's compgen to fetch completions for the current
// input word, then checks whether each result is a directory (appending "/")
// so the model can tell them apart at render time.
//
// Completion strategy:
//   - First word with no path separator: command + file completion (-A command -A file)
//   - All other cases: file completion only (-A file)
//
// Results are capped at 100 entries. The function runs asynchronously so the
// TUI stays responsive while bash spawns.
func bashCompleteCmd(input, cwd string) tea.Cmd {
	return func() tea.Msg {
		// Find the word being completed: everything after the last space.
		wordStart := strings.LastIndex(input, " ") + 1
		prefix := input[wordStart:]

		// Choose compgen mode: command+file for the first token without a path
		// separator, file-only for everything else.
		compgenArgs := "-A file"
		if wordStart == 0 && !strings.ContainsAny(prefix, "/") {
			compgenArgs = "-A command -A file"
		}

		// The wrapper:
		//  - cd to the current bash cwd so relative paths resolve correctly
		//  - run compgen with the prefix from LIVIE_PREFIX (avoids quoting issues)
		//  - append "/" to directory entries so callers can distinguish them
		//  - cap at 100 entries
		script := fmt.Sprintf(
			`cd %s 2>/dev/null
while IFS= read -r __c; do
  if [ -d "$__c" ]; then printf '%%s/\n' "$__c"
  else printf '%%s\n' "$__c"
  fi
done < <(compgen %s -- "$LIVIE_PREFIX" 2>/dev/null | head -100)`,
			shellQuote(cwd), compgenArgs,
		)
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "LIVIE_PREFIX="+prefix)
		out, _ := cmd.Output() // ignore error — empty output = no completions

		var completions []string
		if len(out) > 0 {
			for _, line := range strings.Split(strings.TrimRight(string(out), "\n"), "\n") {
				if line != "" {
					completions = append(completions, line)
				}
			}
		}

		return bashCompletionsMsg{
			completions: completions,
			prefix:      prefix,
			wordStart:   wordStart,
		}
	}
}

// bashPWDSentinel delimits the pwd capture appended to every bash command's
// output. Null bytes are used because they cannot appear in a directory path
// and are vanishingly unlikely in normal command output.
const bashPWDSentinel = "\x00LIVIE_PWD\x00"

// bashResultMsg carries the output of a bash command back to the update loop.
type bashResultMsg struct {
	command  string
	output   string
	exitCode int
	newCwd   string // pwd captured after the command ran
}

// bashExecCmd runs a shell command inside a wrapper script that:
//  1. cds to the current tracked cwd so state persists across commands
//  2. runs the user command via eval of the LIVIE_CMD env var — avoids all
//     quoting/escaping issues and lets cd / pushd / etc. take effect
//  3. prints a null-byte-delimited sentinel with the final pwd
//  4. exits with the user command's exit code
//
// The sentinel is stripped from the output before display and used to update
// the model's bashCwd so the next command starts in the right directory.
func bashExecCmd(command, cwd string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		script := fmt.Sprintf(
			"cd %s 2>/dev/null\neval \"$LIVIE_CMD\"\n__livie_ec=$?\nprintf '\\x00LIVIE_PWD\\x00%%s\\x00' \"$(pwd)\"\nexit $__livie_ec",
			shellQuote(cwd),
		)
		cmd := exec.CommandContext(ctx, "bash", "-c", script)
		cmd.Env = append(os.Environ(), "LIVIE_CMD="+command)
		out, err := cmd.CombinedOutput()

		exitCode := 0
		if err != nil {
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) {
				exitCode = exitErr.ExitCode()
			} else {
				// context deadline or other OS-level failure
				exitCode = -1
				if len(out) == 0 {
					out = []byte(err.Error())
				}
			}
		}

		// Parse the sentinel to extract the new working directory.
		output := string(out)
		newCwd := cwd // fall back if the sentinel is absent (e.g. timeout)
		if idx := strings.Index(output, bashPWDSentinel); idx >= 0 {
			rest := output[idx+len(bashPWDSentinel):]
			if end := strings.IndexByte(rest, '\x00'); end >= 0 {
				if parsed := rest[:end]; parsed != "" {
					newCwd = parsed
				}
			}
			output = output[:idx] // strip sentinel from displayed output
		}

		return bashResultMsg{
			command:  command,
			output:   output,
			exitCode: exitCode,
			newCwd:   newCwd,
		}
	}
}

// shellQuote wraps s in single quotes, escaping any single quotes within.
func shellQuote(s string) string {
	return "'" + strings.ReplaceAll(s, "'", "'\\''"  ) + "'"
}

// shortenHomePath replaces the user's home directory prefix with ~.
func shortenHomePath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// hudTickMsg fires every second to refresh runner state in the HUD.
type hudTickMsg struct{}

// healthTickMsg fires periodically to check whether the local server is still
// reachable. Decoupled from hudTickMsg so the HUD can stay at 1 s while health
// checks run much less frequently.
type healthTickMsg struct{}

// healthCheckInterval is how often Livie pings GET /health while the runner is
// in StateRunning. 15 s is frequent enough to notice a crash quickly without
// hammering the server with constant requests.
const healthCheckInterval = 15 * time.Second

// Styles created once and reused every frame.
var (
	dividerStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColBorder))
	scrollIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentAmber)).
				Bold(true)
)

// indexStatusClearMsg fires 5 s after indexing completes to revert the HUD status.
type indexStatusClearMsg struct{}

// ChatModel is the primary screen — welcome block + chat in one viewport.
type ChatModel struct {
	cfg      *config.Config
	runner   *runner.Manager
	agent    *agent.Agent
	indexer  *index.Indexer // nil when RAG is unavailable
	store    *index.Store   // nil when RAG is unavailable
	keys     tui.KeyMap
	registry *tui.CommandRegistry

	hud          components.HUDState
	messages     components.MessagesModel
	input        components.InputModel
	autocomplete components.AutocompleteModel
	resumePicker components.SessionPickerModel
	toolConfirm  components.ToolConfirmModel
	pendingToolID string // ID of the in-flight tool call, "" when none

	// indexProg is the progress channel from a running /index add operation.
	// nil when no indexing is in progress.
	indexProg <-chan index.Progress

	width  int
	height int

	mode        components.InputMode
	quitFirst   bool
	quitFirstAt time.Time

	sessionID        string
	sessionCreatedAt time.Time

	// bashCwd is the working directory maintained across bash-mode commands.
	// Initialised to the process cwd at startup; updated after every command.
	bashCwd string

	// inputHistory holds previously submitted messages/commands (most recent last).
	// historyIdx == len(inputHistory) means the user is at the live draft slot.
	// historyDraft preserves whatever was in the input when ↑ was first pressed,
	// so it can be restored when ↓ reaches the bottom of the history.
	inputHistory []string
	historyIdx   int
	historyDraft string

	// bashAutocomplete holds shell completion suggestions shown over the input.
	bashAutocomplete components.BashAutocompleteModel
}

const (
	hudHeight = components.HUDHeight // 3 rows
	divHeight = 2                    // divider above input + divider above HUD
)

func NewChatModel(cfg *config.Config, mgr *runner.Manager, agt *agent.Agent, ix *index.Indexer, store *index.Store, width, height int) ChatModel {
	inputModel := components.NewInputModel(width)
	vpH := viewportH(height, inputModel.Height(), 0)

	initCwd, err := os.Getwd()
	if err != nil {
		initCwd = "."
	}

	m := ChatModel{
		cfg:              cfg,
		runner:           mgr,
		agent:            agt,
		indexer:          ix,
		store:            store,
		keys:             tui.DefaultKeyMap(),
		registry:         tui.NewCommandRegistry(cfg, mgr),
		hud:              components.DefaultHUDState(),
		messages:         components.NewMessagesModel(width, vpH),
		input:            inputModel,
		autocomplete:     components.NewAutocompleteModel(width),
		bashAutocomplete: components.NewBashAutocompleteModel(width),
		resumePicker:     components.NewSessionPickerModel(width),
		toolConfirm:      components.NewToolConfirmModel(width),
		width:            width,
		height:           height,
		mode:             components.ModeChat,
		bashCwd:          initCwd,
	}

	m.registry.Register(newCmd(m))
	m.registry.Register(skillsCmd(agt))
	m.hud.CWD = shortenHomePath(initCwd)
	if agt != nil {
		m.hud.SkillCount = agt.SkillCount()
	}
	m.syncHUDState()
	m.showWelcome()
	return m
}

// hudTickCmd returns a tea.Cmd that fires hudTickMsg after one second.
func hudTickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(time.Time) tea.Msg {
		return hudTickMsg{}
	})
}

// healthTickCmd returns a tea.Cmd that fires healthTickMsg after healthCheckInterval.
// Only issued when the runner is in StateRunning.
func healthTickCmd() tea.Cmd {
	return tea.Tick(healthCheckInterval, func(time.Time) tea.Msg {
		return healthTickMsg{}
	})
}

// showWelcome renders the welcome block into the top of the viewport.
func (m *ChatModel) showWelcome() {
	skillCount := 0
	if m.agent != nil {
		skillCount = m.agent.SkillCount()
	}
	block := RenderWelcomeBlock(m.cfg, m.width, skillCount)
	// Insert as a raw block message so it sits above any conversation
	m.messages.AddRaw(block)
	m.messages.GotoBottom()
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		m.input.Init(),
		hudTickCmd(),    // 1 s HUD refresh
		healthTickCmd(), // 15 s liveness probe
	)
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.relayout()

	case tea.KeyPressMsg:
		handled, cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
		if handled {
			// Key was consumed — run auto-grow then return without forwarding
			// the key to the textarea (prevents double-handling).
			m.syncInputHeight()
			return m, tea.Batch(cmds...)
		}

	case tui.CommandActionMsg:
		if cmd := m.handleAction(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}

	case hudTickMsg:
		m.syncHUDState()
		cmds = append(cmds, hudTickCmd()) // perpetually re-issue

	case healthTickMsg:
		// Fire the actual HTTP ping, then re-arm only if still running.
		if m.runner != nil && m.runner.State() == runner.StateRunning {
			cmds = append(cmds, m.runner.HealthCheckCmd())
		}
		// Always re-arm: if the runner starts up later the ticker is already
		// running and will catch the transition to Running.
		cmds = append(cmds, healthTickCmd())

	case runner.HealthCheckMsg:
		if !msg.OK {
			// Health failed — HealthCheckCmd already updated internal state;
			// sync HUD so the chip turns red immediately.
			m.syncHUDState()
		}

	case agent.ContextTruncatedMsg:
		m.messages.AddMessage(components.NewMessage(
			components.MsgSystem,
			fmt.Sprintf("context window ~%d%% full — %d older messages trimmed",
				msg.EstPct, msg.MessagesDropped),
		))
		m.messages.GotoBottom()
		return m, msg.Next()

	case agent.StreamStartMsg:
		m.messages.StartStreaming()
		return m, m.agent.PollCmd()

	case agent.StreamChunkMsg:
		m.messages.AppendStream(msg.Delta)
		return m, m.agent.PollCmd()

	case agent.StreamDoneMsg:
		if msg.FinalDelta != "" {
			m.messages.AppendStream(msg.FinalDelta)
		}
		m.messages.FinalizeStream()
		m.hud.TokensUsed = msg.Usage.TotalTokens
		m.syncHUDState()
		m.saveSession()
		m.messages.GotoBottom()

	case agent.StreamErrMsg:
		m.messages.FinalizeStream()
		m.messages.AddMessage(components.NewMessage(
			components.MsgError,
			fmt.Sprintf("request failed: %s", msg.Err),
		))
		m.messages.GotoBottom()

	case agent.StreamToolCallMsg:
		if msg.FinalDelta != "" {
			m.messages.AppendStream(msg.FinalDelta)
		}
		m.messages.FinalizeStream()
		if m.cfg.Behaviour.ConfirmToolCalls {
			m.toolConfirm.Show(msg.ID, msg.Name, msg.Args)
			m.pendingToolID = msg.ID
			m.syncInputHeight()
			return m, nil // wait for y/n keypress
		}
		// Auto-execute — no confirmation required.
		return m, m.agent.DispatchToolCmd(msg.ID, msg.Name, msg.Args)

	case agent.ToolResultMsg:
		m.toolConfirm.Dismiss()
		m.pendingToolID = ""
		ok := msg.Err == nil
		status := "✓"
		if !ok {
			status = "✗ " + trimError(msg.Err)
		}
		m.messages.AddMessage(components.NewToolMessage(components.ToolActivity{
			Name:    msg.Name,
			Args:    msg.Args,
			Elapsed: msg.Elapsed,
			OK:      ok,
			Status:  status,
		}))
		m.messages.GotoBottom()
		m.syncInputHeight()
		return m, m.agent.ContinueAfterToolCmd(msg.ID, msg.Result)

	case session.SummariesLoadedMsg:
		if msg.Err != nil {
			m.messages.AddMessage(components.NewMessage(components.MsgError,
				fmt.Sprintf("failed to list sessions: %s", msg.Err)))
			return m, nil
		}
		if len(msg.Summaries) == 0 {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem,
				"no previous sessions found"))
			return m, nil
		}
		m.resumePicker.SetLoading(false)
		m.resumePicker.SetSummaries(msg.Summaries)
		m.syncInputHeight()

	case session.SessionLoadedMsg:
		if msg.Err != nil {
			m.messages.AddMessage(components.NewMessage(components.MsgError,
				fmt.Sprintf("failed to load session: %s", msg.Err)))
			return m, nil
		}
		m.loadSession(msg.Session)

	case runner.RunnerStartedMsg:
		m.syncHUDState()
		if msg.Err != nil {
			errText := fmt.Sprintf("runner failed to start: %s", msg.Err)
			if lines := m.runner.LogLines(20); len(lines) > 0 {
				errText += "\n\n```\n" + strings.Join(lines, "\n") + "\n```"
			}
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, errText))
		} else {
			m.messages.AddMessage(components.NewMessage(
				components.MsgSystem, "runner started",
			))
		}
		m.messages.GotoBottom()

	case runner.RunnerStoppedMsg:
		m.syncHUDState()
		if msg.Err != nil {
			m.messages.AddMessage(components.NewMessage(
				components.MsgSystem,
				fmt.Sprintf("runner failed to stop: %s", msg.Err),
			))
		} else {
			m.messages.AddMessage(components.NewMessage(
				components.MsgSystem, "runner stopped",
			))
		}
		m.messages.GotoBottom()

	case bashCompletionsMsg:
		if len(msg.completions) == 1 {
			// Single result: insert directly without showing the popup.
			return m, m.insertBashCompletion(msg.completions[0], msg.wordStart)
		}
		m.bashAutocomplete.SetCompletions(msg.completions, msg.prefix, msg.wordStart)
		m.syncInputHeight()

	case bashResultMsg:
		if msg.newCwd != "" && msg.newCwd != m.bashCwd {
			m.bashCwd = msg.newCwd
			// Keep the OS-level process cwd in sync so that agent tool calls
			// and any other subprocess inherit the updated directory.
			_ = os.Chdir(msg.newCwd)
			m.hud.CWD = shortenHomePath(msg.newCwd)
		}
		m.messages.AddMessage(components.NewBashOutputMessage(msg.output, msg.exitCode))
		m.messages.GotoBottom()

	case index.IndexProgressMsg:
		// Progress update from a running /index add operation.
		if msg.Done {
			m.indexProg = nil
			var statusText, msgText string
			switch {
			case msg.FilesDone == 0 && msg.FilesFailed > 0:
				// Total failure — nothing made it into the index.
				statusText = "index: all failed"
				msgText = fmt.Sprintf(
					"✗ indexing failed — 0/%d files indexed\n\n"+
						"Embedding requires the local runner to be running with a model that supports embeddings.\n"+
						"Try `/run start`, then `/index add` again.",
					msg.FilesTotal,
				)
				m.hud.StatusOK = false
			case msg.FilesFailed > 0:
				// Partial failure — some files indexed, some not.
				statusText = fmt.Sprintf("index: %d/%d", msg.FilesDone, msg.FilesTotal)
				msgText = fmt.Sprintf(
					"⚠ indexing finished — %d/%d files indexed, %d failed, %d chunks",
					msg.FilesDone, msg.FilesTotal, msg.FilesFailed, msg.ChunksStored,
				)
				m.hud.StatusOK = true
			default:
				// Full success.
				statusText = fmt.Sprintf("index ready (%d files)", msg.FilesDone)
				msgText = fmt.Sprintf(
					"✓ index ready — %d files, %d chunks",
					msg.FilesDone, msg.ChunksStored,
				)
				m.hud.StatusOK = true
			}
			m.hud.StatusMsg = statusText
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, msgText))
			m.messages.GotoBottom()
			cmds = append(cmds, tea.Tick(5*time.Second, func(time.Time) tea.Msg {
				return indexStatusClearMsg{}
			}))
		} else if msg.FilesTotal > 0 {
			// Ongoing progress — update the HUD status line.
			m.hud.StatusMsg = fmt.Sprintf("indexing… (%d/%d)", msg.FilesDone, msg.FilesTotal)
			m.hud.StatusOK = true
		}
		// Re-issue poll so progress keeps flowing.
		if m.indexProg != nil {
			cmds = append(cmds, indexPollCmd(m.indexProg))
		}

	case index.IndexDoneMsg:
		// Channel was closed before a Done progress was received (shouldn't
		// happen in normal flow, but handle gracefully).
		m.indexProg = nil

	case indexStatusClearMsg:
		m.hud.StatusMsg = "Ready"
		m.hud.StatusOK = true

	case quitConfirmMsg:
		m.quitFirst = false
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	m.syncInputHeight()

	// If the user typed freely and diverged from the history slot being previewed,
	// snap back to the live-draft position so the next ↑ starts fresh.
	if m.historyIdx < len(m.inputHistory) &&
		m.input.Value() != m.inputHistory[m.historyIdx] {
		m.historyIdx = len(m.inputHistory)
		m.historyDraft = ""
	}

	var c tea.Cmd
	m.messages, c = m.messages.Update(msg)
	cmds = append(cmds, c)

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) handleKey(msg tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	// ── Tool confirm — highest priority ──────────────────────────────────────
	if m.toolConfirm.IsVisible() {
		switch {
		case msg.String() == "y" || key.Matches(msg, m.keys.Submit):
			id := m.toolConfirm.ID()
			name := m.toolConfirm.Name()
			args := m.toolConfirm.Args()
			m.toolConfirm.Dismiss()
			m.syncInputHeight()
			return true, m.agent.DispatchToolCmd(id, name, args)
		case msg.String() == "n" || key.Matches(msg, m.keys.Escape):
			id := m.toolConfirm.ID()
			m.messages.AddMessage(components.NewToolMessage(components.ToolActivity{
				Name:   m.toolConfirm.Name(),
				Args:   m.toolConfirm.Args(),
				OK:     false,
				Status: "✗ rejected",
			}))
			m.messages.GotoBottom()
			m.toolConfirm.Dismiss()
			m.pendingToolID = ""
			m.syncInputHeight()
			return true, m.agent.RejectToolCmd(id)
		}
	}

	// ── Resume picker navigation — takes priority over autocomplete ──────────
	if m.resumePicker.IsVisible() {
		switch {
		case key.Matches(msg, m.keys.AutocompleteDown):
			m.resumePicker.MoveDown()
			return true, nil
		case key.Matches(msg, m.keys.AutocompleteUp):
			m.resumePicker.MoveUp()
			return true, nil
		case key.Matches(msg, m.keys.Submit):
			return true, func() tea.Msg {
				return tui.CommandActionMsg{Action: tui.ActionResumeSession}
			}
		case key.Matches(msg, m.keys.Escape):
			m.resumePicker.Dismiss()
			m.syncInputHeight()
			return true, nil
		}
	}

	// ── Bash autocomplete — only active in bash mode ────────────────────────
	if m.mode == components.ModeBash {
		if m.bashAutocomplete.IsVisible() {
			switch {
			case key.Matches(msg, m.keys.AutocompleteDown): // Tab: cycle forward
				m.bashAutocomplete.MoveDown()
				m.syncInputHeight()
				return true, nil

			case key.Matches(msg, m.keys.AutocompleteUp): // Shift+Tab: cycle backward
				m.bashAutocomplete.MoveUp()
				m.syncInputHeight()
				return true, nil

			case key.Matches(msg, m.keys.Submit): // Enter: accept into input
				return true, m.acceptBashCompletion()

			case key.Matches(msg, m.keys.Escape):
				m.bashAutocomplete.Dismiss()
				m.syncInputHeight()
				return true, nil

			default:
				// Any other key (typing, backspace, ↑/↓ for history…) dismisses
				// the popup and falls through to normal key handling.
				m.bashAutocomplete.Dismiss()
				m.syncInputHeight()
			}
		} else if key.Matches(msg, m.keys.AutocompleteAccept) { // Tab with no popup
			return true, bashCompleteCmd(m.input.Value(), m.bashCwd)
		}
	}

	// ── Input history — ↑/↓ when no overlay is active ──────────────────────
	noOverlay := !m.toolConfirm.IsVisible() &&
		!m.resumePicker.IsVisible() &&
		!(m.mode == components.ModeBash && m.bashAutocomplete.IsVisible())

	if noOverlay {
		switch {
		case key.Matches(msg, m.keys.HistoryPrev) && m.input.CursorOnFirstLine():
			m.autocomplete.Dismiss()
			m.historyNavigate(-1)
			return true, nil
		case key.Matches(msg, m.keys.HistoryNext) && m.input.CursorOnLastLine():
			m.autocomplete.Dismiss()
			m.historyNavigate(+1)
			return true, nil
		}
	}

	// ── Autocomplete navigation (Tab/Shift+Tab) — intercepted before general keys ──
	if m.autocomplete.IsVisible() {
		switch {
		case key.Matches(msg, m.keys.AutocompleteDown): // Tab: accept current + advance
			m.inlineAcceptAndAdvance(+1)
			return true, nil

		case key.Matches(msg, m.keys.AutocompleteUp): // Shift+Tab: retreat + accept
			m.inlineAcceptAndAdvance(-1)
			return true, nil

		case key.Matches(msg, m.keys.Escape):
			m.autocomplete.Dismiss()
			return true, nil
		}
	}

	switch {
	// ctrl+j always works. shift+enter only fires on Kitty-protocol terminals;
	// on standard terminals it is indistinguishable from plain enter.
	case msg.String() == "shift+enter" || msg.String() == "ctrl+j":
		m.input.InsertNewline()
		return true, nil

	case key.Matches(msg, m.keys.QuitAlt):
		// ctrl+q: single-press clean quit.
		m.saveSession()
		return true, tui.QuitCmd()

	case key.Matches(msg, m.keys.Quit):
		if m.quitFirst && time.Since(m.quitFirstAt) < 500*time.Millisecond {
			m.saveSession()
			return true, tui.QuitCmd()
		}
		m.quitFirst = true
		m.quitFirstAt = time.Now()
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "press ctrl+c again to quit"))
		m.messages.GotoBottom()
		return true, tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
			return quitConfirmMsg{}
		})

	case key.Matches(msg, m.keys.Escape):
		if m.mode == components.ModeBash {
			m.setMode(components.ModeChat)
		}
		return true, nil

	case key.Matches(msg, m.keys.ToggleMode):
		if m.mode == components.ModeChat {
			m.setMode(components.ModeBash)
		} else {
			m.setMode(components.ModeChat)
		}
		return true, nil

	case key.Matches(msg, m.keys.ClearInput):
		m.input.Reset()
		return true, nil

	case key.Matches(msg, m.keys.Submit):
		return true, m.handleSubmit()

	case key.Matches(msg, m.keys.ScrollUp):
		m.messages.ScrollUp()
		return true, nil

	case key.Matches(msg, m.keys.ScrollDown):
		m.messages.ScrollDown()
		return true, nil

	case key.Matches(msg, m.keys.ScrollTop):
		m.messages.GotoTop()
		return true, nil

	case key.Matches(msg, m.keys.ScrollBot):
		m.messages.GotoBottom()
		return true, nil

	case key.Matches(msg, m.keys.CopyResponse):
		// OSC 52: copy the last assistant response to the system clipboard.
		// Supported by Kitty, WezTerm, Alacritty, and most modern terminals.
		if content := m.messages.LastAssistantContent(); content != "" {
			m.messages.AddMessage(components.NewMessage(
				components.MsgSystem, "last response copied to clipboard",
			))
			m.messages.GotoBottom()
			return true, tea.SetClipboard(content)
		}
		return true, nil

	case m.mode == components.ModeChat && key.Matches(msg, m.keys.AutocompleteDown):
		// Tab in chat mode with no visible autocomplete popup: consume the key
		// to prevent the textarea from inserting a literal tab character.
		return true, nil
	}

	return false, nil
}

func (m *ChatModel) handleSubmit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}

	// Record in history, deduplicating consecutive identical entries.
	if len(m.inputHistory) == 0 || m.inputHistory[len(m.inputHistory)-1] != text {
		m.inputHistory = append(m.inputHistory, text)
	}
	m.historyIdx = len(m.inputHistory)
	m.historyDraft = ""

	m.input.Reset()

	// Slash commands work in any mode.
	if strings.HasPrefix(text, "/") {
		m.messages.AddMessage(components.NewMessage(components.MsgCommand, text))
		response, action := m.registry.Dispatch(text)
		return func() tea.Msg {
			return tui.CommandActionMsg{Response: response, Action: action}
		}
	}

	// ── Bash mode: execute as a real shell command ───────────────────────────
	if m.mode == components.ModeBash {
		m.messages.AddMessage(components.NewBashCmdMessage(shortenHomePath(m.bashCwd), text))
		m.messages.GotoBottom()
		return bashExecCmd(text, m.bashCwd)
	}

	// ── Chat mode: send to the AI ────────────────────────────────────────────
	// Set session identity on the first user message.
	if m.sessionID == "" {
		m.sessionID = time.Now().Format("2006-01-02T15-04-05")
		m.sessionCreatedAt = time.Now()
	}

	m.messages.AddMessage(components.NewMessage(components.MsgUser, text))
	m.messages.GotoBottom()
	return m.agent.StreamCmd(text)
}

func (m *ChatModel) handleAction(msg tui.CommandActionMsg) tea.Cmd {
	if msg.Response != "" {
		m.messages.AddMessage(components.NewMessage(components.MsgAssistant, msg.Response))
		m.messages.GotoBottom()
	}

	switch msg.Action {
	case tui.ActionQuit:
		return tea.Quit

	case tui.ActionNew:
		if m.agent != nil {
			m.agent.Conversation().Reset()
		}
		m.sessionID = ""
		m.sessionCreatedAt = time.Time{}
		vpH := viewportH(m.height, m.input.Height(), 0)
		m.messages = components.NewMessagesModel(m.width, vpH)
		m.showWelcome()

	case tui.ActionOpenResume:
		m.resumePicker = components.NewSessionPickerModel(m.width)
		m.resumePicker.SetLoading(true)
		m.syncInputHeight()
		return session.ListSummariesCmd()

	case tui.ActionResumeSession:
		if sel := m.resumePicker.Selected(); sel != nil {
			m.resumePicker.Dismiss()
			m.syncInputHeight()
			return session.LoadCmd(sel.ID)
		}

	case tui.ActionSetModeChat:
		m.setMode(components.ModeChat)

	case tui.ActionSetModeBash:
		m.setMode(components.ModeBash)

	case tui.ActionRunnerStart:
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "starting runner…"))
		m.messages.GotoBottom()
		return m.runner.StartAndPollCmd(30 * time.Second)

	case tui.ActionRunnerStop:
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "stopping runner…"))
		m.messages.GotoBottom()
		return m.runner.StopCmd()

	case tui.ActionRunnerRestart:
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "restarting runner…"))
		m.messages.GotoBottom()
		return m.runner.RestartCmd(30 * time.Second)

	case tui.ActionSkillsUpdated:
		// HUD skill count is refreshed via syncHUDState which is called
		// every second by hudTickMsg. Trigger it immediately here too.
		m.syncHUDState()

	case tui.ActionMemoryChanged:
		// Hard-register or hard-remove write_vault_file based on the updated
		// config flag, then rebuild the system prompt to reflect the new state.
		if m.agent != nil {
			m.agent.SetVaultMemory(m.cfg.Memory.Enabled)
		}

	case tui.ActionIndexAdd:
		path := tui.IndexPendingPath
		if path == "" {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, "✗ no path specified"))
			m.messages.GotoBottom()
			return nil
		}
		// Resolve relative paths against the current bash cwd so that
		// `/index add .` means the directory the user navigated to in bash
		// mode, not Livie's launch directory.
		// Tilde paths are left as-is — expandHome in the indexer handles them.
		if !filepath.IsAbs(path) && !strings.HasPrefix(path, "~") {
			path = filepath.Join(m.bashCwd, path)
		}
		if m.indexer == nil {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem,
				"✗ index unavailable — local runner required. Start with /run start"))
			m.messages.GotoBottom()
			return nil
		}
		m.messages.AddMessage(components.NewMessage(components.MsgSystem,
			fmt.Sprintf("indexing %s… (running in background)", path)))
		m.messages.GotoBottom()
		m.hud.StatusMsg = "indexing…"
		m.hud.StatusOK = true
		ctx := context.Background()
		m.indexProg = m.indexer.AddPath(ctx, path)
		return indexPollCmd(m.indexProg)

	case tui.ActionIndexStatus:
		if m.indexer == nil {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem,
				"✗ index unavailable — local runner required"))
		} else {
			m.messages.AddMessage(components.NewMessage(components.MsgAssistant, m.indexer.Status()))
		}
		m.messages.GotoBottom()

	case tui.ActionIndexClear:
		if m.indexer == nil {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem,
				"✗ index unavailable — local runner required"))
			m.messages.GotoBottom()
			return nil
		}
		ctx := context.Background()
		if err := m.indexer.Clear(ctx); err != nil {
			m.messages.AddMessage(components.NewMessage(components.MsgError,
				fmt.Sprintf("✗ clear failed: %v", err)))
		} else {
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, "✓ index cleared"))
		}
		m.messages.GotoBottom()
	}

	return nil
}

// indexPollCmd returns a tea.Cmd that reads one Progress update from ch.
// If the channel is closed it returns IndexDoneMsg.
// Blocks up to 500 ms so we don't busy-loop between events.
func indexPollCmd(ch <-chan index.Progress) tea.Cmd {
	return func() tea.Msg {
		select {
		case p, ok := <-ch:
			if !ok {
				return index.IndexDoneMsg{}
			}
			return index.IndexProgressMsg{Progress: p}
		case <-time.After(500 * time.Millisecond):
			// Heartbeat — no real update but keeps the poll loop alive.
			return index.IndexProgressMsg{}
		}
	}
}

// syncHUDState refreshes all HUD fields: model name, endpoint, token max,
// and runner chip. Renamed from syncHUDRunnerState to reflect its broader scope.
// No I/O — Manager.State() is a mutex-guarded field read.
func (m *ChatModel) syncHUDState() {
	if m.runner == nil {
		m.hud.RunnerStatus = components.RunnerStatusNone
		m.hud.RunnerLabel = ""
		return
	}
	switch m.runner.State() {
	case runner.StateUnconfigured, runner.StateReady:
		m.hud.RunnerStatus = components.RunnerStatusStopped
		m.hud.RunnerLabel = "stopped"
	case runner.StateStarting:
		m.hud.RunnerStatus = components.RunnerStatusStarting
		m.hud.RunnerLabel = "starting"
	case runner.StateRunning:
		m.hud.RunnerStatus = components.RunnerStatusRunning
		m.hud.RunnerLabel = "llama-server"
	case runner.StateStopped:
		m.hud.RunnerStatus = components.RunnerStatusStopped
		m.hud.RunnerLabel = "stopped"
	case runner.StateError:
		m.hud.RunnerStatus = components.RunnerStatusError
		m.hud.RunnerLabel = "error"
	}
	// Hide the chip when the active endpoint is not the local runner.
	if m.cfg.Endpoint.Active != "local" {
		m.hud.RunnerStatus = components.RunnerStatusNone
		m.hud.RunnerLabel = ""
	}

	// ── Model name ────────────────────────────────────────────────────────────
	if m.cfg.Endpoint.Active == "local" {
		m.hud.ModelName = m.cfg.ModelName()
	} else {
		ep := m.cfg.ActiveEndpoint()
		m.hud.ModelName = ep.Model
		if m.hud.ModelName == "" {
			m.hud.ModelName = "(no model)"
		}
	}

	// ── Endpoint name ─────────────────────────────────────────────────────────
	m.hud.EndpointName = m.cfg.Endpoint.Active

	// ── Context window max ────────────────────────────────────────────────────
	ep := m.cfg.ActiveEndpoint()
	if ep.ContextSize > 0 {
		m.hud.TokensMax = ep.ContextSize
	} else if m.cfg.Endpoint.Active == "local" {
		m.hud.TokensMax = m.cfg.Runner.ContextSize
	} else {
		m.hud.TokensMax = 0
	}

	// ── Skill count ───────────────────────────────────────────────────────────
	if m.agent != nil {
		m.hud.SkillCount = m.agent.SkillCount()
	}

	// ── Working directory ─────────────────────────────────────────────────────
	m.hud.CWD = shortenHomePath(m.bashCwd)

	// ── Session indicator ────────────────────────────────────────────────────
	if !m.sessionCreatedAt.IsZero() {
		m.hud.SessionLabel = m.sessionCreatedAt.Format("15:04")
	} else {
		m.hud.SessionLabel = ""
	}
}

// saveSession persists the current conversation to disk.
// Best-effort — errors are silently dropped.
func (m *ChatModel) saveSession() {
	s := m.buildSessionSnapshot()
	if s == nil {
		return
	}
	_ = session.Save(s)
}

func (m *ChatModel) buildSessionSnapshot() *session.Session {
	if m.sessionID == "" || m.agent == nil {
		return nil
	}
	hist := m.agent.Conversation().History()
	if len(hist) == 0 {
		return nil
	}
	ep := m.cfg.ActiveEndpoint()
	msgs := make([]session.Message, 0, len(hist))
	for _, h := range hist {
		msgs = append(msgs, session.Message{
			Role:      session.Role(h.Role),
			Content:   h.Content,
			Timestamp: time.Now(),
		})
	}
	return &session.Session{
		ID:           m.sessionID,
		CreatedAt:    m.sessionCreatedAt,
		UpdatedAt:    time.Now(),
		EndpointName: ep.Name,
		ModelName:    m.hud.ModelName,
		Messages:     msgs,
		TokensUsed:   m.hud.TokensUsed,
	}
}

func (m *ChatModel) loadSession(s *session.Session) {
	// Convert session messages to openai format.
	history := make([]openai.ChatCompletionMessage, 0, len(s.Messages))
	for _, sm := range s.Messages {
		history = append(history, openai.ChatCompletionMessage{
			Role:    string(sm.Role),
			Content: sm.Content,
		})
	}
	if m.agent != nil {
		m.agent.Conversation().LoadHistory(history)
	}

	// Restore session identity so subsequent saves update the same file.
	m.sessionID = s.ID
	m.sessionCreatedAt = s.CreatedAt

	// Rebuild TUI message list from session history.
	vpH := viewportH(m.height, m.input.Height(), 0)
	m.messages = components.NewMessagesModel(m.width, vpH)
	m.showWelcome()
	for _, sm := range s.Messages {
		var t components.MsgType
		switch sm.Role {
		case session.RoleUser:
			t = components.MsgUser
		case session.RoleAssistant:
			t = components.MsgAssistant
		default:
			t = components.MsgSystem
		}
		m.messages.AddMessage(components.NewMessage(t, sm.Content))
	}
	m.messages.GotoBottom()
	m.messages.AddMessage(components.NewMessage(
		components.MsgSystem,
		fmt.Sprintf("session resumed · %s · %d messages", s.ID, len(s.Messages)),
	))
}

func (m *ChatModel) setMode(mode components.InputMode) {
	m.mode = mode
	m.hud.Mode = mode
	m.input.SetMode(mode)
	if mode != components.ModeBash {
		m.bashAutocomplete.Dismiss()
	}
	m.messages.ClearSelection()
	m.messages.GotoBottom()
}

// insertBashCompletion builds the new input value from a single completion
// and an offset into the original string, then fires another completion fetch
// if the result is a directory (so Tab-into-dir feels instant).
func (m *ChatModel) insertBashCompletion(completion string, wordStart int) tea.Cmd {
	current := m.input.Value()
	base := ""
	if wordStart <= len(current) {
		base = current[:wordStart]
	}
	isDir := strings.HasSuffix(completion, "/")
	newInput := base + completion
	if !isDir {
		newInput += " "
	}
	m.input.SetValue(newInput)
	m.syncInputHeight()
	if isDir {
		return bashCompleteCmd(newInput, m.bashCwd)
	}
	return nil
}

// acceptBashCompletion accepts the currently highlighted completion from the
// popup, inserts it into the input, and dismisses the popup.
func (m *ChatModel) acceptBashCompletion() tea.Cmd {
	sel := m.bashAutocomplete.Selected()
	wordStart := m.bashAutocomplete.WordStart()
	m.bashAutocomplete.Dismiss()
	if sel == "" {
		m.syncInputHeight()
		return nil
	}
	return m.insertBashCompletion(sel, wordStart)
}

// inlineAcceptAndAdvance implements bash menu-complete style Tab cycling for
// the slash-command autocomplete popup.
//
// For Tab (delta > 0): accept the currently highlighted item, then advance the
// selection forward — so the first Tab accepts item 0, the second Tab accepts
// item 1, and so on.
//
// For Shift+Tab (delta < 0): retreat the selection first (so the first
// Shift+Tab wraps to the last item) then accept — mirroring bash's
// menu-complete-backward behaviour.
//
// After acceptance, SetInput is called so the popup re-filters with the new
// input value (e.g. switching from command-list to sub-arg mode once a
// trailing space appears). The advance/retreat then operates in the updated
// context. If the new context has no further matches the popup closes and
// subsequent Tab presses are consumed as no-ops by the general switch.
func (m *ChatModel) inlineAcceptAndAdvance(delta int) {
	if delta < 0 {
		// Shift+Tab: retreat first so the first press lands on the last item.
		m.autocomplete.MoveUp()
	}

	// Accept the currently highlighted suggestion into the input.
	if m.autocomplete.InSubMode() {
		if sub := m.autocomplete.SelectedSub(); sub != nil {
			m.input.SetValue(m.autocomplete.SubInputPrefix() + sub.Name + " ")
		}
	} else if sel := m.autocomplete.Selected(); sel != nil {
		m.input.SetValue("/" + sel.Name + " ")
	}

	// Re-filter with the updated input so the popup reflects the new context
	// (e.g. sub-args after a command name is completed with a trailing space).
	m.autocomplete.SetInput(m.input.Value(), m.registry)

	if delta > 0 {
		// Tab: advance after accepting so the next Tab press shows the next item.
		m.autocomplete.MoveDown()
	}
}

// syncInputHeight resizes the messages viewport to account for current input
// and autocomplete heights. Also keeps autocomplete state in sync with input.
// SetSize is skipped when dimensions are unchanged to avoid expensive re-renders.
func (m *ChatModel) syncInputHeight() {
	m.autocomplete.SetInput(m.input.Value(), m.registry)
	overlayH := m.autocomplete.Height()
	if m.resumePicker.IsVisible() {
		overlayH = m.resumePicker.Height()
	} else if m.toolConfirm.IsVisible() {
		overlayH = m.toolConfirm.Height()
	} else if m.mode == components.ModeBash && m.bashAutocomplete.IsVisible() {
		overlayH = m.bashAutocomplete.Height()
	}
	newH := viewportH(m.height, m.input.Height(), overlayH)
	if m.width != m.messages.Width() || newH != m.messages.Height() {
		m.messages.SetSize(m.width, newH)
	}
}

func (m *ChatModel) relayout() {
	m.input.SetWidth(m.width)
	m.autocomplete.SetWidth(m.width)
	m.autocomplete.SetInput(m.input.Value(), m.registry)
	m.bashAutocomplete.SetWidth(m.width)
	overlayH := m.autocomplete.Height()
	if m.mode == components.ModeBash && m.bashAutocomplete.IsVisible() {
		overlayH = m.bashAutocomplete.Height()
	}
	vpH := viewportH(m.height, m.input.Height(), overlayH)
	m.messages.SetSize(m.width, vpH)
}

func (m ChatModel) View() tea.View {
	hud := components.RenderHUD(m.hud, m.width)

	bottomDivider := dividerStyle.Render(strings.Repeat("─", m.width))
	topDivider := renderTopDivider(m.width, m.input.LinesAbove())

	parts := []string{
		m.messages.View(),
		topDivider,
		m.input.View(),
	}
	if m.resumePicker.IsVisible() {
		parts = append(parts, m.resumePicker.View())
	} else if m.toolConfirm.IsVisible() {
		parts = append(parts, m.toolConfirm.View())
	} else if m.mode == components.ModeBash && m.bashAutocomplete.IsVisible() {
		parts = append(parts, m.bashAutocomplete.View())
	} else if m.autocomplete.IsVisible() {
		parts = append(parts, m.autocomplete.View())
	}
	parts = append(parts, bottomDivider, hud)

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	v := tea.NewView(content)
	v.AltScreen = true
	// MouseModeCellMotion (xterm mode 1002) delivers wheel events for scroll.
	// Terminal-native drag selection is superseded by Livie's own drag-to-copy:
	// click and drag inside the message history to select text; releasing
	// automatically copies the selection to the clipboard.
	v.MouseMode = tea.MouseModeCellMotion
	return v
}

// renderTopDivider renders the divider above the input area.
// When there are lines scrolled above the viewport it shows "↑ N more".
func renderTopDivider(width, linesAbove int) string {
	if linesAbove <= 0 {
		return dividerStyle.Render(strings.Repeat("─", width))
	}

	indicator := fmt.Sprintf(" ↑ %d more ", linesAbove)
	indicatorStyled := scrollIndicatorStyle.Render(indicator)

	// Left stub: short fixed dash run before the indicator
	const leftDashes = 2
	left := dividerStyle.Render(strings.Repeat("─", leftDashes))

	// Right fill: remaining width after left stub + indicator
	indicatorWidth := lipgloss.Width(indicator)
	rightDashes := width - leftDashes - indicatorWidth
	if rightDashes < 0 {
		rightDashes = 0
	}
	right := dividerStyle.Render(strings.Repeat("─", rightDashes))

	return left + indicatorStyled + right
}

// Mode returns the current input mode of the chat screen.
func (m ChatModel) Mode() components.InputMode { return m.mode }

// TermWidth returns the last known terminal width.
func (m ChatModel) TermWidth() int { return m.width }

// TermHeight returns the last known terminal height.
func (m ChatModel) TermHeight() int { return m.height }

// InputHeight returns the current rendered height of the input box.
func (m ChatModel) InputHeight() int { return m.input.Height() }

// Input returns a read-only view of the input model for inspection in tests.
func (m ChatModel) Input() components.InputModel { return m.input }

// historyNavigate moves the history cursor by delta (-1 = older, +1 = newer),
// loads the appropriate entry into the input box, and preserves the live draft
// so it can be restored when the user navigates back to the bottom.
func (m *ChatModel) historyNavigate(delta int) {
	if len(m.inputHistory) == 0 {
		return
	}

	// Capture the live draft the first time the user presses ↑.
	if m.historyIdx == len(m.inputHistory) && delta < 0 {
		m.historyDraft = m.input.Value()
	}

	next := m.historyIdx + delta
	if next < 0 {
		next = 0
	}
	if next > len(m.inputHistory) {
		next = len(m.inputHistory)
	}
	m.historyIdx = next

	if m.historyIdx == len(m.inputHistory) {
		m.input.SetValue(m.historyDraft)
	} else {
		m.input.SetValue(m.inputHistory[m.historyIdx])
	}
	m.syncInputHeight()
}

// trimError returns a short single-line string from an error for the tool
// activity status field.
func trimError(err error) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	const maxErrLen = 40
	runes := []rune(s)
	if len(runes) > maxErrLen {
		s = string(runes[:maxErrLen]) + "…"
	}
	return s
}

// TransitionToWelcome is no longer used — welcome is part of the chat screen.
// Kept as a type so existing references compile cleanly.
type TransitionToWelcome struct{}

// ── helpers ──────────────────────────────────────────────────────────────────

// ViewportH returns the height available for the messages viewport given the
// total terminal height and current input box height (autocomplete = 0).
// Used by tests; production code calls viewportH directly with all three args.
func ViewportH(totalH, inputH int) int { return viewportH(totalH, inputH, 0) }

func viewportH(totalH, inputH, autocompleteH int) int {
	h := totalH - hudHeight - divHeight - inputH - autocompleteH
	if h < 1 {
		h = 1
	}
	return h
}

// newCmd returns the /new command registration, capturing the ChatModel pointer
// via the action system rather than a direct reference.
func newCmd(_ ChatModel) *tui.Command {
	return &tui.Command{
		Name:        "new",
		Description: "Start a new conversation and return to the welcome screen",
		Handler: func(args []string) (string, tui.AppAction) {
			return "", tui.ActionNew
		},
	}
}

// skillsCmd returns the /skills command, capturing the agent pointer directly
// so it can call SkillList and InstallSkill without going through the action
// system for values that require data from the loader.
func skillsCmd(agt *agent.Agent) *tui.Command {
	return &tui.Command{
		Name:        "skills",
		Description: "List or install skills",
		Subcommands: []tui.SubArg{
			{Name: "list", Description: "List all loaded skills"},
			{Name: "install", Description: "Install a skill from a local path"},
		},
		Handler: func(args []string) (string, tui.AppAction) {
			sub := ""
			if len(args) > 0 {
				sub = args[0]
			}
			switch sub {
			case "", "list":
				list := agt.SkillList()
				if len(list) == 0 {
					return "_No skills loaded._", tui.ActionNone
				}
				var sb strings.Builder
				sb.WriteString(fmt.Sprintf("**%d skill(s) loaded**\n\n", len(list)))
				for _, s := range list {
					sb.WriteString(fmt.Sprintf("  `%-20s` %s\n", s.Name, s.Description))
				}
				return strings.TrimRight(sb.String(), "\n"), tui.ActionNone
			case "install":
				if len(args) < 2 {
					return "usage: `/skills install <path>`", tui.ActionNone
				}
				path := args[1]
				if err := agt.InstallSkill(path); err != nil {
					return fmt.Sprintf("✗ install failed: %s", err), tui.ActionNone
				}
				return fmt.Sprintf("✓ skill installed from `%s` · %d skill(s) now loaded", path, agt.SkillCount()), tui.ActionSkillsUpdated
			default:
				return fmt.Sprintf("unknown sub-command %q — try: `list`, `install`", sub), tui.ActionNone
			}
		},
	}
}
