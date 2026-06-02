package screens

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kez/livie/agent"
	"github.com/kez/livie/config"
	"github.com/kez/livie/runner"
	"github.com/kez/livie/session"
	"github.com/kez/livie/tui"
	"github.com/kez/livie/tui/components"
	openai "github.com/sashabaranov/go-openai"
)

// quitConfirmMsg fires when the second ctrl+c window expires.
type quitConfirmMsg struct{}

// hudTickMsg fires every second to refresh runner state in the HUD.
type hudTickMsg struct{}

// Styles created once and reused every frame.
var (
	dividerStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColBorder))
	scrollIndicatorStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentAmber)).
				Bold(true)
)

// ChatModel is the primary screen — welcome block + chat in one viewport.
type ChatModel struct {
	cfg      *config.Config
	runner   *runner.Manager
	agent    *agent.Agent
	keys     tui.KeyMap
	registry *tui.CommandRegistry

	hud          components.HUDState
	messages     components.MessagesModel
	input        components.InputModel
	autocomplete components.AutocompleteModel
	resumePicker components.SessionPickerModel

	width  int
	height int

	mode        components.InputMode
	quitFirst   bool
	quitFirstAt time.Time

	sessionID        string
	sessionCreatedAt time.Time
}

const (
	hudHeight = components.HUDHeight // 3 rows
	divHeight = 2                    // divider above input + divider above HUD
)

func NewChatModel(cfg *config.Config, mgr *runner.Manager, agt *agent.Agent, width, height int) ChatModel {
	inputModel := components.NewInputModel(width)
	vpH := viewportH(height, inputModel.Height(), 0)

	m := ChatModel{
		cfg:          cfg,
		runner:       mgr,
		agent:        agt,
		keys:         tui.DefaultKeyMap(),
		registry:     tui.NewCommandRegistry(cfg, mgr),
		hud:          components.DefaultHUDState(),
		messages:     components.NewMessagesModel(width, vpH),
		input:        inputModel,
		autocomplete: components.NewAutocompleteModel(width),
		resumePicker: components.NewSessionPickerModel(width),
		width:        width,
		height:       height,
		mode:         components.ModeChat,
	}

	m.registry.Register(newCmd(m))
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

// showWelcome renders the welcome block into the top of the viewport.
func (m *ChatModel) showWelcome() {
	block := RenderWelcomeBlock(m.cfg, m.width)
	// Insert as a raw block message so it sits above any conversation
	m.messages.AddRaw(block)
	m.messages.GotoBottom()
}

func (m ChatModel) Init() tea.Cmd {
	return tea.Batch(
		m.input.Init(),
		hudTickCmd(), // start HUD polling immediately
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
		// Poll server health every tick so an externally-killed process is
		// detected promptly (within ~1 s) even when m.proc == nil.
		if m.runner != nil && m.runner.State() == runner.StateRunning {
			cmds = append(cmds, m.runner.HealthCheckCmd())
		}

	case runner.HealthCheckMsg:
		if !msg.OK {
			// Health check failed — state has already been updated inside
			// HealthCheckCmd; sync the HUD to reflect the new state.
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
		m.messages.FinalizeStream()
		m.messages.AddMessage(components.NewMessage(
			components.MsgSystem,
			fmt.Sprintf("[tool call: %s(%s)] — tool execution coming in a future update",
				msg.Name, msg.Args),
		))
		m.messages.GotoBottom()

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

	case quitConfirmMsg:
		m.quitFirst = false
	}

	var inputCmd tea.Cmd
	m.input, inputCmd = m.input.Update(msg)
	cmds = append(cmds, inputCmd)
	m.syncInputHeight()

	var c tea.Cmd
	m.messages, c = m.messages.Update(msg)
	cmds = append(cmds, c)

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) handleKey(msg tea.KeyPressMsg) (handled bool, cmd tea.Cmd) {
	// ── Resume picker navigation — takes priority over autocomplete ──────────
	if m.resumePicker.IsVisible() {
		switch {
		case key.Matches(msg, m.keys.AutocompleteDown):
			m.resumePicker.MoveDown()
			return true, nil
		case key.Matches(msg, m.keys.AutocompleteUp):
			m.resumePicker.MoveUp()
			return true, nil
		case key.Matches(msg, m.keys.AutocompleteAccept):
			return true, func() tea.Msg {
				return tui.CommandActionMsg{Action: tui.ActionResumeSession}
			}
		case key.Matches(msg, m.keys.Escape):
			m.resumePicker.Dismiss()
			m.syncInputHeight()
			return true, nil
		}
	}

	// ── Autocomplete navigation — intercepted before anything else ───────────
	if m.autocomplete.IsVisible() {
		switch {
		case key.Matches(msg, m.keys.AutocompleteDown):
			m.autocomplete.MoveDown()
			return true, nil

		case key.Matches(msg, m.keys.AutocompleteUp):
			m.autocomplete.MoveUp()
			return true, nil

		case key.Matches(msg, m.keys.AutocompleteAccept):
			if m.autocomplete.InSubMode() {
				if sub := m.autocomplete.SelectedSub(); sub != nil {
					// Prepend the already-typed prefix (e.g. "/run ") and append
					// a trailing space so the next level's subs can appear.
					m.input.SetValue(m.autocomplete.SubInputPrefix() + sub.Name + " ")
					m.autocomplete.SetInput(m.input.Value(), m.registry)
				}
			} else if sel := m.autocomplete.Selected(); sel != nil {
				m.input.SetValue("/" + sel.Name + " ")
				m.autocomplete.SetInput(m.input.Value(), m.registry)
			}
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

	case key.Matches(msg, m.keys.Quit):
		if m.quitFirst && time.Since(m.quitFirstAt) < 500*time.Millisecond {
			m.saveSession()
			return true, tea.Quit
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
	}

	return false, nil
}

func (m *ChatModel) handleSubmit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.Reset()

	if strings.HasPrefix(text, "/") {
		m.messages.AddMessage(components.NewMessage(components.MsgCommand, text))
		response, action := m.registry.Dispatch(text)
		return func() tea.Msg {
			return tui.CommandActionMsg{Response: response, Action: action}
		}
	}

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
	}

	return nil
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
		ModelName:    ep.Model,
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
	m.messages.GotoBottom()
}

// syncInputHeight resizes the messages viewport to account for current input
// and autocomplete heights. Also keeps autocomplete state in sync with input.
// SetSize is skipped when dimensions are unchanged to avoid expensive re-renders.
func (m *ChatModel) syncInputHeight() {
	m.autocomplete.SetInput(m.input.Value(), m.registry)
	overlayH := m.autocomplete.Height()
	if m.resumePicker.IsVisible() {
		overlayH = m.resumePicker.Height()
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
	vpH := viewportH(m.height, m.input.Height(), m.autocomplete.Height())
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
	} else if m.autocomplete.IsVisible() {
		parts = append(parts, m.autocomplete.View())
	}
	parts = append(parts, bottomDivider, hud)

	content := lipgloss.JoinVertical(lipgloss.Left, parts...)
	v := tea.NewView(content)
	v.AltScreen = true
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
