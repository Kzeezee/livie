package screens

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kez/livie/config"
	"github.com/kez/livie/tui"
	"github.com/kez/livie/tui/components"
)

// quitConfirmMsg fires when the second ctrl+c window expires.
type quitConfirmMsg struct{}

// ChatModel is the primary screen — welcome block + chat in one viewport.
type ChatModel struct {
	cfg      *config.Config
	keys     tui.KeyMap
	registry *tui.CommandRegistry

	hud      components.HUDState
	messages components.MessagesModel
	input    components.InputModel

	width  int
	height int

	mode        components.InputMode
	quitFirst   bool
	quitFirstAt  time.Time
}

const (
	hudHeight = components.HUDHeight // 3 rows
	divHeight = 2                    // divider above input + divider above HUD
)

func NewChatModel(cfg *config.Config, width, height int) ChatModel {
	inputModel := components.NewInputModel(width)
	vpH := viewportH(height, inputModel.Height())

	m := ChatModel{
		cfg:      cfg,
		keys:     tui.DefaultKeyMap(),
		registry: tui.NewCommandRegistry(),
		hud:      components.DefaultHUDState(),
		messages: components.NewMessagesModel(width, vpH),
		input:    inputModel,
		width:    width,
		height:   height,
		mode:     components.ModeChat,
	}

	m.registry.Register(newCmd(m))
	m.showWelcome()
	return m
}

// showWelcome renders the welcome block into the top of the viewport.
func (m *ChatModel) showWelcome() {
	block := RenderWelcomeBlock(m.cfg, m.width)
	// Insert as a raw block message so it sits above any conversation
	m.messages.AddRaw(block)
	m.messages.GotoBottom()
}

func (m ChatModel) Init() tea.Cmd {
	return m.input.Init()
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
	switch {
	// ctrl+j always works. shift+enter only fires on Kitty-protocol terminals;
	// on standard terminals it is indistinguishable from plain enter.
	case msg.String() == "shift+enter" || msg.String() == "ctrl+j":
		m.input.InsertNewline()
		return true, nil

	case key.Matches(msg, m.keys.Quit):
		if m.quitFirst && time.Since(m.quitFirstAt) < 500*time.Millisecond {
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

	// Regular message — Phase 2 wires in AI backend
	m.messages.AddMessage(components.NewMessage(components.MsgUser, text))
	m.messages.AddMessage(components.NewMessage(components.MsgSystem, "AI backend not yet connected"))
	m.messages.GotoBottom()
	return nil
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
		vpH := viewportH(m.height, m.input.Height())
		m.messages = components.NewMessagesModel(m.width, vpH)
		m.showWelcome()

	case tui.ActionSetModeChat:
		m.setMode(components.ModeChat)

	case tui.ActionSetModeBash:
		m.setMode(components.ModeBash)
	}

	return nil
}

func (m *ChatModel) setMode(mode components.InputMode) {
	m.mode = mode
	m.hud.Mode = mode
	m.input.SetMode(mode)
	switch mode {
	case components.ModeBash:
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "switched to BASH mode"))
	default:
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "switched to CHAT mode"))
	}
	m.messages.GotoBottom()
}

// syncInputHeight resizes the messages viewport whenever the input box height changes.
func (m *ChatModel) syncInputHeight() {
	m.messages.SetSize(m.width, viewportH(m.height, m.input.Height()))
}

func (m *ChatModel) relayout() {
	m.input.SetWidth(m.width)
	vpH := viewportH(m.height, m.input.Height())
	m.messages.SetSize(m.width, vpH)
}

func (m ChatModel) View() tea.View {
	hud := components.RenderHUD(m.hud, m.width)
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColBorder)).
		Render(strings.Repeat("─", m.width))

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		m.messages.View(),
		divider,
		m.input.View(),
		divider,
		hud,
	)
	v := tea.NewView(content)
	v.AltScreen = true
	return v
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
// total terminal height and the current input box height.
func ViewportH(totalH, inputH int) int { return viewportH(totalH, inputH) }

func viewportH(totalH, inputH int) int {
	h := totalH - hudHeight - divHeight - inputH
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
