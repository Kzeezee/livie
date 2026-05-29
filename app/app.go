// Package app contains the root Bubbletea model and screen state machine.
// Flow: Setup (first-run wizard) → Chat (welcome block + conversation).
package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/config"
	"github.com/kez/livie/runner"
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
	runner  *runner.Manager
	width   int
	height  int
}

// New creates the root model.
//
// It starts at the setup screen when:
//   - cfg.IsFirstRun == true (no config file existed), OR
//   - the active endpoint is "local" and no model path is configured
//
// Otherwise it starts directly at the chat screen.
func New(cfg *config.Config, mgr *runner.Manager) Model {
	w, h := 120, 36

	startAtSetup := cfg.IsFirstRun ||
		(cfg.Endpoint.Active == "local" && cfg.Runner.ModelPath == "")

	current := screenChat
	if startAtSetup {
		current = screenSetup
	}

	return Model{
		current: current,
		setup:   screens.NewSetupModel(cfg, mgr, w, h),
		chat:    screens.NewChatModel(cfg, w, h),
		cfg:     cfg,
		runner:  mgr,
		width:   w,
		height:  h,
	}
}

func (m Model) Init() tea.Cmd {
	if m.current == screenSetup {
		return m.setup.Init()
	}
	return m.chat.Init()
}

func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// ── Setup → Chat transition ──────────────────────────────────────────────
	if tt, ok := msg.(screens.TransitionToChat); ok {
		// Merge config returned by setup into the root cfg.
		if tt.Config != nil {
			m.cfg = tt.Config
			// Persist to disk (best-effort; errors are non-fatal here).
			_ = config.Save(m.cfg, m.cfg.ConfigPath)
			// Re-apply runner config.
			m.runner.Configure(m.cfg.Runner)
		}
		m.current = screenChat
		// Re-create the chat model with the final config so the welcome block
		// reflects any changes made during setup.
		m.chat = screens.NewChatModel(m.cfg, m.width, m.height)
		return m, m.chat.Init()
	}

	// ── Propagate resize to all screens ─────────────────────────────────────
	if ws, ok := msg.(tea.WindowSizeMsg); ok {
		m.width = ws.Width
		m.height = ws.Height
		m.setup, _ = m.setup.Update(ws)
		m.chat, _ = m.chat.Update(ws)
		return m, nil
	}

	// ── Route to active screen ───────────────────────────────────────────────
	var cmd tea.Cmd
	switch m.current {
	case screenSetup:
		m.setup, cmd = m.setup.Update(msg)
	case screenChat:
		m.chat, cmd = m.chat.Update(msg)
	}
	return m, cmd
}

func (m Model) View() tea.View {
	switch m.current {
	case screenSetup:
		return m.setup.View()
	case screenChat:
		return m.chat.View()
	}
	return tea.NewView("")
}
