// Package app contains the root Bubbletea model and screen state machine.
// Flow: Setup (first-run stub) → Chat (welcome block + conversation, combined).
package app

import (
	tea "github.com/charmbracelet/bubbletea"
	"github.com/kez/livie/config"
	"github.com/kez/livie/tui/screens"
)

type screen int

const (
	screenSetup screen = iota
	screenChat
)

// Model is the root Bubbletea model.
type Model struct {
	current screen
	setup   screens.SetupModel
	chat    screens.ChatModel
	cfg     *config.Config
	width   int
	height  int
}

// New creates the root model. Always starts at setup (stub auto-transitions).
func New(cfg *config.Config) Model {
	w, h := 120, 36
	return Model{
		current: screenSetup,
		setup:   screens.NewSetupModel(w, h),
		chat:    screens.NewChatModel(cfg, w, h),
		cfg:     cfg,
		width:   w,
		height:  h,
	}
}

func (m Model) Init() tea.Cmd {
	return m.setup.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// Transition from setup → chat
	if _, ok := msg.(screens.TransitionToChat); ok {
		m.current = screenChat
		return m, m.chat.Init()
	}

	// Propagate resize to all screens
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
		m.setup, _ = m.setup.Update(ws)
		m.chat, _ = m.chat.Update(ws)
		return m, nil
	}

	var cmd tea.Cmd
	switch m.current {
	case screenSetup:
		m.setup, cmd = m.setup.Update(msg)
	case screenChat:
		m.chat, cmd = m.chat.Update(msg)
	}
	return m, cmd
}

func (m Model) View() string {
	switch m.current {
	case screenSetup:
		return m.setup.View()
	case screenChat:
		return m.chat.View()
	}
	return ""
}
