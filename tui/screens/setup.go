package screens

// Setup screen — Phase 1 stub.
// Auto-transitions to chat immediately.
// Future phases will use this for first-run setup:
//   - Auto-downloading llama-server
//   - Configuring the first model endpoint
//   - Initialising the Obsidian vault
//   - Installing default skills

import (
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tui "github.com/kez/livie/tui"
)

type SetupModel struct {
	width  int
	height int
}

func NewSetupModel(width, height int) SetupModel {
	return SetupModel{width: width, height: height}
}

func (m SetupModel) Init() tea.Cmd {
	// Auto-transition after a brief pause
	return tea.Tick(800*time.Millisecond, func(t time.Time) tea.Msg {
		return TransitionToChat{}
	})
}

func (m SetupModel) Update(msg tea.Msg) (SetupModel, tea.Cmd) {
	switch msg.(type) {
	case tea.WindowSizeMsg:
		ws := msg.(tea.WindowSizeMsg)
		m.width = ws.Width
		m.height = ws.Height
	}
	return m, nil
}

func (m SetupModel) View() string {
	msg := tui.StyleDim.Render("Setup coming soon — skipping...")
	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Align(lipgloss.Center, lipgloss.Center).
		Render(msg)
}
