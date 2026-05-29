package components

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/charmbracelet/lipgloss"
	tui "github.com/kez/livie/tui"
)

// InputMode represents the current interaction mode.
type InputMode int

const (
	ModeQuery InputMode = iota
	ModeBash
)

func (m InputMode) String() string {
	switch m {
	case ModeBash:
		return "BASH"
	default:
		return "QUERY"
	}
}

// HUDState holds all data the HUD needs to render. Plain value type — no AI coupling.
type HUDState struct {
	Mode         InputMode
	ModelName    string
	EndpointName string
	TokensUsed   int
	TokensMax    int
	SkillCount   int
	// Future: RunnerStatus, PendingToolCalls, IndexingActive
}

// DefaultHUDState returns stub values for Phase 1.
func DefaultHUDState() HUDState {
	return HUDState{
		Mode:         ModeQuery,
		ModelName:    "(no model)",
		EndpointName: "local",
		TokensUsed:   0,
		TokensMax:    0,
		SkillCount:   0,
	}
}

// RenderHUD renders the single-line HUD bar at the given width.
func RenderHUD(state HUDState, width int) string {
	dir := currentDir()

	// Left segment: ◆ livie  │  ~/dir  │  MODE
	appName := tui.StyleAccentCyan.Render("◆ livie")
	dirStr := tui.StyleLabel.Render(truncateDir(dir, 24))
	modeBadge := renderModeBadge(state.Mode)

	left := appName + tui.StyleDivider.Render("  │  ") +
		dirStr + tui.StyleDivider.Render("  │  ") +
		modeBadge

	// Right segment: model @ endpoint  │  tokens  │  N skills
	modelStr := tui.StyleValue.Render(truncate(state.ModelName, 20)) +
		tui.StyleLabel.Render(" @ ") +
		tui.StyleLabel.Render(state.EndpointName)

	tokenStr := renderTokens(state.TokensUsed, state.TokensMax)
	skillStr := tui.StyleMuted.Render(fmt.Sprintf("%d skills", state.SkillCount))

	right := modelStr + tui.StyleDivider.Render("  │  ") +
		tokenStr + tui.StyleDivider.Render("  │  ") +
		skillStr

	// Measure visible widths and pad middle
	leftWidth := lipgloss.Width(left)
	rightWidth := lipgloss.Width(right)
	padding := width - leftWidth - rightWidth - 2 // 2 for outer padding
	if padding < 1 {
		padding = 1
	}
	mid := strings.Repeat(" ", padding)

	line := tui.StyleHUD.
		Width(width).
		Render(left + mid + right)

	return line
}

func renderModeBadge(mode InputMode) string {
	switch mode {
	case ModeBash:
		return tui.StyleModeBadgeBash.Render(" BASH ")
	default:
		return tui.StyleModeBadgeQuery.Render(" QUERY ")
	}
}

func renderTokens(used, max int) string {
	if max == 0 {
		return tui.StyleMuted.Render("— / — tok")
	}
	ratio := float64(used) / float64(max)
	str := fmt.Sprintf("%s / %s tok",
		formatInt(used),
		formatInt(max),
	)
	switch {
	case ratio > 0.95:
		return tui.StyleAccentRose.Render(str)
	case ratio > 0.80:
		return tui.StyleAccentAmber.Render(str)
	default:
		return tui.StyleLabel.Render(str)
	}
}

func currentDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return "~"
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return dir
	}
	if strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}
	return dir
}

// truncateDir shortens a path like ~/a/very/long/path to ~/a/v/l/path
func truncateDir(dir string, max int) string {
	if len(dir) <= max {
		return dir
	}
	parts := strings.Split(dir, string(filepath.Separator))
	if len(parts) <= 2 {
		return dir
	}
	// Keep first and last, abbreviate middle
	result := parts[0]
	for _, p := range parts[1 : len(parts)-1] {
		if len(p) > 0 {
			result += string(filepath.Separator) + string(p[0])
		}
	}
	result += string(filepath.Separator) + parts[len(parts)-1]
	if len(result) > max {
		return "…" + result[len(result)-max+1:]
	}
	return result
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max-1] + "…"
}

func formatInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}
