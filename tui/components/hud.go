package components

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

// InputMode represents the current interaction mode.
type InputMode int

const (
	ModeChat InputMode = iota
	ModeBash
)

// HUDHeight is the number of terminal rows the HUD occupies.
const HUDHeight = 3

func (m InputMode) String() string {
	switch m {
	case ModeBash:
		return "BASH"
	default:
		return "CHAT"
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
	StatusMsg    string // e.g. "Ready", "Connecting to model...", "Error loading config"
	StatusOK     bool   // true = green ✓, false = rose ✗
}

// DefaultHUDState returns stub values for Phase 1.
func DefaultHUDState() HUDState {
	return HUDState{
		Mode:         ModeChat,
		ModelName:    "(no model)",
		EndpointName: "local",
		TokensUsed:   0,
		TokensMax:    0,
		SkillCount:   0,
		StatusMsg:    "Ready",
		StatusOK:     true,
	}
}

// RenderHUD renders the 3-row HUD bar at the given width.
//
// Row 1 — working directory + active mode badge
// Row 2 — token usage (left) · endpoint + model (right)
// Row 3 — status indicator
func RenderHUD(state HUDState, width int) string {
	inner := width - 2 // lipgloss Padding(0,1) consumes 1 col each side

	// ── Row 1: ~/dir  (MODE) ─────────────────────────────────────────────────
	dir := currentDir()
	dirStr := tui.StyleLabel.Render(truncateDir(dir, 36))
	modeTag := renderModeBadge(state.Mode)
	row1Content := dirStr + "  " + modeTag

	// ── Row 2: tokens · skills (left)   (endpoint) model (right) ─────────────
	tokenStr := renderTokens(state.TokensUsed, state.TokensMax)
	skillStr := tui.StyleMuted.Render(fmt.Sprintf("· %d skills", state.SkillCount))
	statsLeft := tokenStr + "  " + skillStr

	endpointStr := tui.StyleMuted.Render("(" + truncate(state.EndpointName, 12) + ")")
	modelStr := tui.StyleValue.Render(" " + truncate(state.ModelName, 24))
	statsRight := endpointStr + modelStr

	lw2 := lipgloss.Width(statsLeft)
	rw2 := lipgloss.Width(statsRight)
	pad2 := inner - lw2 - rw2
	if pad2 < 1 {
		pad2 = 1
	}
	row2Content := statsLeft + strings.Repeat(" ", pad2) + statsRight

	// ── Row 3: ✓/✗ status ────────────────────────────────────────────────────
	row3Content := renderStatusLine(state.StatusMsg, state.StatusOK)

	// ── Render each row with surface background ───────────────────────────────
	style := tui.StyleHUD.Width(width)

	r1 := style.Render(row1Content)
	r2 := style.Render(row2Content)
	r3 := style.Render(row3Content)

	return lipgloss.JoinVertical(lipgloss.Left, r1, r2, r3)
}

// renderModeBadge renders the inline mode indicator (no background box — just text).
func renderModeBadge(mode InputMode) string {
	switch mode {
	case ModeBash:
		return tui.StyleAccentRose.Render("(BASH)")
	default:
		return tui.StyleAccentCyan.Render("(CHAT)")
	}
}

func renderStatusLine(msg string, ok bool) string {
	if msg == "" {
		return tui.StyleMuted.Render("—")
	}
	if ok {
		tick := tui.StyleAccentGreen.Render("✓")
		return tick + " " + tui.StyleLabel.Render(msg)
	}
	cross := tui.StyleAccentRose.Render("✗")
	return cross + " " + tui.StyleLabel.Render(msg)
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

var (
	cachedDir     string
	cachedDirOnce sync.Once
)

func currentDir() string {
	cachedDirOnce.Do(func() {
		dir, err := os.Getwd()
		if err != nil {
			cachedDir = "~"
			return
		}
		home, err := os.UserHomeDir()
		if err != nil {
			cachedDir = dir
			return
		}
		if strings.HasPrefix(dir, home) {
			dir = "~" + dir[len(home):]
		}
		cachedDir = dir
	})
	return cachedDir
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
