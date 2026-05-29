package screens

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	"github.com/kez/livie/config"
	tui "github.com/kez/livie/tui"
)

// tickMsg drives the blinking prompt animation.
type tickMsg time.Time

// TransitionToChat signals the app to move to the chat screen.
type TransitionToChat struct{}

// WelcomeModel is the welcome screen — neofetch-style layout.
type WelcomeModel struct {
	cfg     *config.Config
	sysinfo tui.SysInfo
	width   int
	height  int
	blink   bool // controls "press any key" visibility
}

// NewWelcomeModel creates a WelcomeModel.
func NewWelcomeModel(cfg *config.Config, width, height int) WelcomeModel {
	return WelcomeModel{
		cfg:     cfg,
		sysinfo: tui.GatherSysInfo(),
		width:   width,
		height:  height,
		blink:   true,
	}
}

func (m WelcomeModel) Init() tea.Cmd {
	return tickCmd()
}

func tickCmd() tea.Cmd {
	return tea.Tick(600*time.Millisecond, func(t time.Time) tea.Msg {
		return tickMsg(t)
	})
}

func (m WelcomeModel) Update(msg tea.Msg) (WelcomeModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height

	case tickMsg:
		m.blink = !m.blink
		return m, tickCmd()

	case tea.KeyMsg:
		// Any key transitions to chat
		_ = msg
		return m, func() tea.Msg { return TransitionToChat{} }
	}
	return m, nil
}

func (m WelcomeModel) View() string {
	portrait := renderPortrait()
	info := m.renderInfoPanel()

	// Two-column: portrait left, info right
	cols := lipgloss.JoinHorizontal(
		lipgloss.Top,
		portrait,
		"   ", // gap
		info,
	)

	divider := tui.StyleDivider.Render(strings.Repeat("─", m.width))

	footer := m.renderFooter()

	// Vertical centering
	content := cols + "\n\n" + divider + "\n\n" + footer

	contentHeight := strings.Count(content, "\n") + 1
	topPad := (m.height - contentHeight) / 2
	if topPad < 1 {
		topPad = 1
	}

	return strings.Repeat("\n", topPad) + content
}

// portrait is the braille ASCII art embedded directly from docs/ASCII-Art.md.
// 53 cols wide × 23 lines tall.
const portrait = `⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣾⠓⠶⣤⠀⠀⠀⠀⣠⠶⣄⡀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢸⠇⠀⢠⡏⠀⠀⢀⡔⠉⠀⢈⡿⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠩⠤⣄⣼⠁⠀⣠⠟⠀⠀⣠⠏⠀⠀⢀⣀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⢀⣀⣀⣀⣀⣀⣀⣀⠀⠀⠀⠁⠀⠀⠣⣤⣀⡼⠃⠀⢀⡴⠋⠈⠳⡄⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣠⣴⣶⣿⡿⠿⠿⠟⠛⠛⠛⠛⠿⠿⣿⣿⣶⣤⣄⠀⠀⠀⠉⠀⢀⡴⠋⠀⠀⣠⠞⠁⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⢀⣴⣾⣿⠿⠋⠉⢀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⠉⠻⢿⣿⣶⣄⠀⠀⠳⣄⠀⣠⠞⢁⡠⢶⡄⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣾⣿⠿⠋⠀⠀⢀⣴⠏⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠑⢤⡈⠛⢿⣿⣦⡀⠈⠛⢡⠚⠃⠀⠀⢹⡆⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⢀⣼⣿⠟⠁⠀⠀⠀⢀⣾⠃⠀⠀⢀⡀⠀⠀⠀⠀⠀⠀⠀⠀⢻⡆⠀⠀⢻⣦⠀⠙⢿⣿⣦⡀⠈⢶⣀⡴⠞⠋⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⣠⣿⡿⠃⠀⠀⠀⠀⢀⣾⡇⢀⡄⠀⢸⡇⠀⠀⠀⠀⠀⠀⣀⠀⢸⣷⡀⠀⠀⠹⣷⡀⠀⠙⢿⣷⡀⠀⠉⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⣰⣿⡟⠀⠀⠀⠀⠀⠀⣾⣿⠃⣼⡇⠀⢸⡇⠀⠀⠀⠀⠀⠀⣿⠀⢸⣿⣷⡀⠀⢀⣾⣿⡤⠐⠊⢻⣿⡀⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⢠⣿⣿⣼⡇⠀⠀⠀⠀⢠⣿⠉⢠⣿⠧⠀⣸⣇⣠⡄⠀⠀⠀⠀⣿⠠⢸⡟⠹⣿⡍⠉⣿⣿⣧⠀⠀⠀⠻⣿⣶⣄⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⢸⣿⣿⡟⠀⠀⠀⠀⠀⣼⡏⢠⡿⣿⣦⣤⣿⡿⣿⡇⠀⠀⠀⢸⡿⠻⣿⣧⣤⣼⣿⡄⢸⡿⣿⡇⠀⠀⢠⣌⠛⢿⣿⣶⣤⣤⣄⡀
⠀⠀⠀⣀⣤⣿⣿⠟⣀⠀⠀⠀⠀⠀⣿⢃⣿⠇⢿⣯⣿⣿⣇⣿⠁⠀⠀⠀⣾⡇⢸⣿⠃⠉⠁⠸⣿⣼⡇⢻⡇⠀⠀⠀⢿⣷⣶⣬⣭⣿⣿⣿⠇
⣾⣿⣿⣿⣿⣻⣥⣾⡇⠀⠀⠀⠀⠀⣿⣿⠇⠀⠘⠿⠋⠻⠿⠿⠶⠶⠾⠿⠿⠍⢛⣧⣰⠶⢀⣀⣼⣿⣴⡸⣿⠀⠀⠀⠸⣿⣿⣿⠉⠛⠉⠀⠀
⠘⠛⠿⠿⢿⣿⠉⣿⠁⠀⠀⠀⠀⢀⣿⡿⣶⣶⣶⣤⣤⣤⣀⣀⠀⠀⠀⠀⠀⠀⢀⣭⣶⣿⡿⠟⠋⠉⠀⠀⣿⠀⡀⡀⠀⣿⣿⣿⡆⠀⠀⠀⠀
⠀⠀⠀⠀⣼⣿⠀⣿⠀⠀⠸⠀⠀⠸⣿⠇⠀⠀⣈⣩⣭⣿⡿⠟⠃⠀⠀⠀⠀⠀⠙⠛⠛⠛⠛⠻⠿⠷⠆⠀⣯⠀⠇⡇⠀⣿⡏⣿⣧⠀⠀⠀⠀
⠀⠀⠀⠀⢿⣿⡀⣿⡆⠀⠀⠀⠀⠀⣿⠰⠿⠿⠛⠋⠉⠀⠀⢀⣴⣶⣶⣶⣶⣶⣦⠀⠀⠀⠀⠀⠀⠀⠀⠀⢹⣧⠀⠀⠀⣿⡇⣿⣿⠀⠀⠀⠀
⠀⠀⠀⠀⢸⣿⡇⢻⣇⠀⠘⣰⡀⠀⣿⠀⠀⠀⠀⠀⠀⠀⠀⢸⣿⠀⠀⠀⠀⢸⡿⠀⠀⠀⠀⠀⠀⠀⠀⠀⣠⣿⠀⠀⠀⣿⣧⣿⡿⠀⠀⠀⠀
⠀⠀⠀⠀⠈⣿⣧⢸⣿⡀⠀⡿⣧⠀⣿⡇⠀⠀⠀⠀⠀⠀⠀⠀⣿⡄⠀⠀⠀⣼⡇⠀⠀⠀⠀⠀⠀⢀⣤⣾⡟⢡⣶⠀⢠⣿⣿⣿⠃⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠹⣿⣿⣿⣷⠀⠇⢹⣷⡸⣿⣶⣦⣄⣀⡀⠀⠀⠀⣿⡇⠀⠀⢠⣿⠁⣀⣀⣠⣤⣶⣾⡿⢿⣿⡇⣼⣿⢀⣿⣿⠿⠏⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠈⠛⠛⣿⣷⣴⠀⢹⣿⣿⣿⡟⠿⠿⣿⣿⣿⣿⣾⣷⣶⣿⣿⣿⣿⡿⠿⠟⠛⠋⠉⠀⢸⣿⣿⣿⣿⣾⣿⠃⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠈⢿⣿⣦⣘⣿⡿⣿⣿⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠘⠛⠛⠻⠿⠋⠁⠀⠀⠀⠀⠀⠀⠀⠀
⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠙⠻⣿⣿⣿⠈⠉⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀⠀`

// ─── Portrait ────────────────────────────────────────────────────────────────

func renderPortrait() string {
	lines := strings.Split(portrait, "\n")
	styled := make([]string, len(lines))
	for i, l := range lines {
		styled[i] = tui.StyleAccentCyan.Render(l)
	}
	return strings.Join(styled, "\n")
}

// ─── Info panel ──────────────────────────────────────────────────────────────

func (m WelcomeModel) renderInfoPanel() string {
	si := m.sysinfo

	// Header: user@host
	header := tui.StyleAccentCyan.Bold(true).Render(si.Username) +
		tui.StyleLabel.Render("@") +
		tui.StyleAccentCyan.Bold(true).Render(si.Hostname)

	sep := tui.StyleDivider.Render(strings.Repeat("─", 32))

	rows := []struct{ label, value string }{
		{"OS", si.OS},
		{"Shell", si.Shell},
		{"Term", si.Terminal},
		{"Go", si.GoVersion},
		{"Config", shortenPath(m.cfg.ConfigPath)},
		{"Vault", shortenPath(m.cfg.VaultPath)},
		{"Model", m.cfg.ModelName},
		{"Skills", fmt.Sprintf("%d loaded", 0)},
	}

	var infoLines []string
	for _, row := range rows {
		label := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextSecondary)).
			Width(8).
			Align(lipgloss.Right).
			Render(row.label)
		value := tui.StyleValue.Render(row.value)
		infoLines = append(infoLines, label+"  "+value)
	}

	sep2 := tui.StyleDivider.Render(strings.Repeat("─", 32))
	tagline := tui.StyleDim.Render("A local AI assistant")
	tagline2 := tui.StyleDim.Render("that lives in your terminal.")

	parts := []string{"", "", header, sep}
	parts = append(parts, infoLines...)
	parts = append(parts, sep2, tagline, tagline2)

	// Pad to match portrait height (23 lines)
	const portraitHeight = 23
	for len(parts) < portraitHeight {
		parts = append(parts, "")
	}

	return strings.Join(parts, "\n")
}

// ─── Footer ──────────────────────────────────────────────────────────────────

func (m WelcomeModel) renderFooter() string {
	cmdLine := "Commands: " +
		tui.StyleCommand.Render("/help") + "  " +
		tui.StyleCommand.Render("/skills") + "  " +
		tui.StyleCommand.Render("/usage") + "  " +
		tui.StyleCommand.Render("/resume") + "  " +
		tui.StyleCommand.Render("/clear") + "  " +
		tui.StyleCommand.Render("/exit")

	keyLine := tui.StyleLabel.Render("Keys:") + "  " +
		tui.StyleValue.Render("ctrl+b") + tui.StyleLabel.Render(" toggle bash mode   ") +
		tui.StyleValue.Render("ctrl+l") + tui.StyleLabel.Render(" clear   ") +
		tui.StyleValue.Render("ctrl+c") + tui.StyleLabel.Render(" quit")

	var prompt string
	if m.blink {
		prompt = tui.StyleAccentCyan.Render("[ Press any key to start ]")
	} else {
		prompt = tui.StyleMuted.Render("[ Press any key to start ]")
	}

	promptLine := lipgloss.NewStyle().
		Width(m.width).
		Align(lipgloss.Center).
		Render(prompt)

	return cmdLine + "\n" + keyLine + "\n\n" + promptLine
}

// ─── Helpers ─────────────────────────────────────────────────────────────────

func shortenPath(p string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		p = "~" + p[len(home):]
	}
	// Abbreviate middle segments
	parts := strings.Split(p, string(filepath.Separator))
	if len(parts) <= 3 {
		return p
	}
	result := parts[0]
	for _, seg := range parts[1 : len(parts)-1] {
		if len(seg) > 0 {
			result += string(filepath.Separator) + string(seg[0])
		}
	}
	result += string(filepath.Separator) + parts[len(parts)-1]
	return result
}
