// Package app contains the root Bubbletea model and screen state machine.
// It sits above tui/ (shared styles/keys/commands) and tui/screens/ (individual screens)
// to avoid import cycles.
package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kez/livie/config"
	"github.com/kez/livie/tui/screens"
)

type screen int

const (
	screenWelcome screen = iota
	screenSetup
	screenChat
)

// Model is the root Bubbletea model. It owns the screen state machine
// and delegates Init/Update/View to the active screen.
type Model struct {
	current screen
	welcome screens.WelcomeModel
	setup   screens.SetupModel
	chat    screens.ChatModel
	cfg     *config.Config
	width   int
	height  int
}

// New creates the root model.
func New(cfg *config.Config) Model {
	w, h := 120, 36
	return Model{
		current: screenWelcome,
		welcome: screens.NewWelcomeModel(cfg, w, h),
		setup:   screens.NewSetupModel(w, h),
		chat:    screens.NewChatModel(cfg, w, h),
		cfg:     cfg,
		width:   w,
		height:  h,
	}
}

func (m Model) Init() tea.Cmd {
	return m.welcome.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg.(type) {
	case screens.TransitionToChat:
		m.current = screenChat
		return m, m.chat.Init()

	case screens.TransitionToWelcome:
		m.current = screenWelcome
		return m, m.welcome.Init()
	}

	// Propagate window resize to all screens
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
		m.welcome, _ = m.welcome.Update(ws)
		m.setup, _ = m.setup.Update(ws)
		m.chat, _ = m.chat.Update(ws)
		return m, nil
	}

	// Delegate to active screen
	var cmd tea.Cmd
	switch m.current {
	case screenWelcome:
		m.welcome, cmd = m.welcome.Update(msg)
	case screenSetup:
		var setupCmd tea.Cmd
		m.setup, setupCmd = m.setup.Update(msg)
		// Setup stub auto-transitions via TransitionToChat
		_ = setupCmd
		cmd = setupCmd
	case screenChat:
		m.chat, cmd = m.chat.Update(msg)
	}

	return m, cmd
}

func (m Model) View() string {
	switch m.current {
	case screenWelcome:
		return m.welcome.View()
	case screenSetup:
		return m.setup.View()
	case screenChat:
		return m.chat.View()
	}
	return ""
}
