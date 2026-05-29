package screens

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kez/livie/config"
	"github.com/kez/livie/tui"
	"github.com/kez/livie/tui/components"
)

// quitConfirmMsg is sent when ctrl+c is pressed a second time.
type quitConfirmMsg struct{}

// ChatModel is the primary chat interface.
type ChatModel struct {
	cfg      *config.Config
	keys     tui.KeyMap
	registry *tui.CommandRegistry

	hud      components.HUDState
	messages components.MessagesModel
	input    components.InputModel

	width  int
	height int

	mode          components.InputMode
	clearPending  bool   // waiting for y/n confirmation on /clear
	quitFirst     bool   // first ctrl+c press
	quitFirstTime time.Time
}

const (
	hudHeight   = 1
	inputHeight = 3 // minimum (border + 1 line + border)
)

func NewChatModel(cfg *config.Config, width, height int) ChatModel {
	vpHeight := height - hudHeight - inputHeight - 1
	if vpHeight < 1 {
		vpHeight = 1
	}

	m := ChatModel{
		cfg:      cfg,
		keys:     tui.DefaultKeyMap(),
		registry: tui.NewCommandRegistry(),
		hud:      components.DefaultHUDState(),
		messages: components.NewMessagesModel(width, vpHeight),
		input:    components.NewInputModel(width),
		width:    width,
		height:   height,
		mode:     components.ModeQuery,
	}

	// Seed with welcome message
	m.messages.AddMessage(components.NewMessage(
		components.MsgSystem,
		"welcome to livie — type /help for commands",
	))

	return m
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

	case tea.KeyMsg:
		cmd := m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case tui.CommandActionMsg:
		cmd := m.handleCommandAction(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

	case quitConfirmMsg:
		m.quitFirst = false
	}

	// Forward to input when not in a blocking state
	if !m.clearPending {
		var inputCmd tea.Cmd
		m.input, inputCmd = m.input.Update(msg)
		cmds = append(cmds, inputCmd)
	}

	// Forward scroll-related events to the message viewport
	var vpCmd tea.Cmd
	m.messages, vpCmd = m.messages.Update(msg)
	cmds = append(cmds, vpCmd)

	return m, tea.Batch(cmds...)
}

func (m *ChatModel) handleKey(msg tea.KeyMsg) tea.Cmd {
	// Clear confirmation mode — only accept y/n
	if m.clearPending {
		switch msg.String() {
		case "y", "Y":
			m.clearPending = false
			m.messages = components.NewMessagesModel(m.width, m.viewportHeight())
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, "history cleared"))
		case "n", "N", "esc":
			m.clearPending = false
			m.messages.AddMessage(components.NewMessage(components.MsgSystem, "clear cancelled"))
		}
		return nil
	}

	switch {
	case key.Matches(msg, m.keys.Quit):
		if m.quitFirst && time.Since(m.quitFirstTime) < 500*time.Millisecond {
			return tea.Quit
		}
		m.quitFirst = true
		m.quitFirstTime = time.Now()
		m.messages.AddMessage(components.NewMessage(
			components.MsgSystem, "press ctrl+c again to quit",
		))
		return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
			return quitConfirmMsg{}
		})

	case key.Matches(msg, m.keys.Escape):
		if m.mode == components.ModeBash {
			m.setMode(components.ModeQuery)
		}
		return nil

	case key.Matches(msg, m.keys.ToggleMode):
		if m.mode == components.ModeQuery {
			m.setMode(components.ModeBash)
		} else {
			m.setMode(components.ModeQuery)
		}
		return nil

	case key.Matches(msg, m.keys.ClearHistory):
		m.clearPending = true
		m.messages.AddMessage(components.NewMessage(
			components.MsgSystem, "clear history? (y/n)",
		))
		return nil

	case key.Matches(msg, m.keys.ClearInput):
		m.input.Reset()
		return nil

	case key.Matches(msg, m.keys.GotoTop):
		if m.input.Value() == "" {
			m.messages.GotoTop()
			return nil
		}

	case key.Matches(msg, m.keys.GotoBottom):
		m.messages.GotoBottom()
		return nil

	case key.Matches(msg, m.keys.ScrollUp):
		if m.input.Value() == "" {
			m.messages.ScrollUp()
			return nil
		}

	case key.Matches(msg, m.keys.ScrollDown):
		if m.input.Value() == "" {
			m.messages.ScrollDown()
			return nil
		}

	case key.Matches(msg, m.keys.Submit):
		return m.handleSubmit()
	}

	return nil
}

func (m *ChatModel) handleSubmit() tea.Cmd {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return nil
	}
	m.input.Reset()

	// Command dispatch
	if strings.HasPrefix(text, "/") {
		m.messages.AddMessage(components.NewMessage(components.MsgCommand, text))
		response, action := m.registry.Dispatch(text)
		return func() tea.Msg {
			return tui.CommandActionMsg{Response: response, Action: action}
		}
	}

	// Regular message — echo for now; Phase 2 wires in the AI backend
	m.messages.AddMessage(components.NewMessage(components.MsgUser, text))
	m.messages.AddMessage(components.NewMessage(
		components.MsgSystem, "AI backend not yet connected — stay tuned",
	))
	m.messages.GotoBottom()
	return nil
}

func (m *ChatModel) handleCommandAction(msg tui.CommandActionMsg) tea.Cmd {
	// Display response text if any
	if msg.Response != "" {
		m.messages.AddMessage(components.NewMessage(components.MsgAssistant, msg.Response))
		m.messages.GotoBottom()
	}

	switch msg.Action {
	case tui.ActionQuit:
		return tea.Quit

	case tui.ActionClearHistory:
		m.clearPending = true
		m.messages.AddMessage(components.NewMessage(
			components.MsgSystem, "clear history? (y/n)",
		))

	case tui.ActionSwitchToWelcome:
		return func() tea.Msg { return TransitionToWelcome{} }

	case tui.ActionSetModeQuery:
		m.setMode(components.ModeQuery)

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
		m.messages.AddMessage(components.NewMessage(components.MsgSystem, "switched to QUERY mode"))
	}
	m.messages.GotoBottom()
}

func (m *ChatModel) relayout() {
	vpH := m.viewportHeight()
	m.messages.SetSize(m.width, vpH)
	m.input.SetWidth(m.width)
}

func (m ChatModel) viewportHeight() int {
	h := m.height - hudHeight - inputHeight - 1
	if h < 1 {
		h = 1
	}
	return h
}

func (m ChatModel) View() string {
	hud := components.RenderHUD(m.hud, m.width)
	divider := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColBorder)).
		Render(strings.Repeat("─", m.width))
	msgs := m.messages.View()
	input := m.input.View()

	return lipgloss.JoinVertical(
		lipgloss.Left,
		hud,
		divider,
		msgs,
		input,
	)
}

// TransitionToWelcome signals a return to the welcome screen.
type TransitionToWelcome struct{}
