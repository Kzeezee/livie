package components

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

// InputMode represents the current interaction mode.
type InputMode int

const (
	ModeChat InputMode = iota
	ModeBash
)

// RunnerStatus is the live health state of the local runner as reported to the HUD.
// Updated by ChatModel on every hudTickMsg.
type RunnerStatus int

const (
	RunnerStatusNone     RunnerStatus = iota // no local runner configured / remote endpoint active
	RunnerStatusStopped                      // configured but not running
	RunnerStatusStarting                     // process up, health not yet passing
	RunnerStatusRunning                      // health check passing
	RunnerStatusError                        // process exited unexpectedly
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
	CWD          string // current working directory, home-shortened (e.g. ~/projects/livie)
	ModelName    string
	EndpointName string
	TokensUsed   int
	TokensMax    int
	SkillCount   int
	StatusMsg    string // e.g. "Ready", "Connecting to model...", "Error loading config"
	StatusOK     bool   // true = green ✓, false = rose ✗

	// Runner chip (Phase 4)
	RunnerStatus RunnerStatus
	RunnerLabel  string // e.g. "llama-server" | "stopped" | "starting"
}

// DefaultHUDState returns stub values for Phase 1.
func DefaultHUDState() HUDState {
	return HUDState{
		Mode:         ModeChat,
		CWD:          "~",
		ModelName:    "(no model)",
		EndpointName: "local",
		TokensUsed:   0,
		TokensMax:    0,
		SkillCount:   0,
		StatusMsg:    "Ready",
		StatusOK:     true,
		RunnerStatus: RunnerStatusNone,
		RunnerLabel:  "",
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
	dir := state.CWD
	if dir == "" {
		dir = "~"
	}
	dirStr := tui.StyleLabel.Render(truncateDir(dir, 36))
	modeTag := renderModeBadge(state.Mode)
	row1Content := dirStr + "  " + modeTag

	// ── Row 2: [runner chip]  tokens · skills (left)   (endpoint) model (right) ─
	chipStr := renderRunnerChip(state)
	chipW := lipgloss.Width(chipStr)

	tokenStr := renderTokens(state.TokensUsed, state.TokensMax)
	skillStr := tui.StyleMuted.Render(fmt.Sprintf("· %d skills", state.SkillCount))
	statsLeft := tokenStr + "  " + skillStr

	endpointStr := tui.StyleMuted.Render("(" + truncate(state.EndpointName, 12) + ")")
	modelStr := tui.StyleValue.Render(" " + truncate(state.ModelName, 24))
	statsRight := endpointStr + modelStr

	lw2 := chipW + lipgloss.Width(statsLeft)
	rw2 := lipgloss.Width(statsRight)
	pad2 := inner - lw2 - rw2
	if pad2 < 1 {
		pad2 = 1
	}
	row2Content := chipStr + statsLeft + strings.Repeat(" ", pad2) + statsRight

	// ── Row 3: ✓/✗ status (left)  key hints (right) ─────────────────────────
	statusStr := renderStatusLine(state.StatusMsg, state.StatusOK)
	hintsStr := tui.StyleMuted.Render("pgup/pgdn scroll · ctrl+y copy · ctrl+q quit")
	sw3 := lipgloss.Width(statusStr)
	hw3 := lipgloss.Width(hintsStr)
	pad3 := inner - sw3 - hw3
	if pad3 < 1 {
		pad3 = 1
	}
	row3Content := statusStr + strings.Repeat(" ", pad3) + hintsStr

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

// renderRunnerChip returns a fixed 18-visible-char wide runner status chip,
// or "" when the chip should be hidden (RunnerStatusNone).
//
// Layout: symbol(1) + space(1) + label padded to 16 = 18 chars total.
//
// | Status   | Symbol | Colour          |
// |----------|--------|-----------------|
// | Stopped  |  ◌     | ColTextMuted    |
// | Starting |  ◎     | ColAccentAmber  |
// | Running  |  ◉     | ColAccentGreen  |
// | Error    |  ◌     | ColAccentRose   |
func renderRunnerChip(state HUDState) string {
	const chipWidth = 18
	const labelWidth = chipWidth - 2 // symbol(1) + space(1)

	var symbol, colour string
	switch state.RunnerStatus {
	case RunnerStatusNone:
		return ""
	case RunnerStatusStopped:
		symbol, colour = "◌", tui.ColTextMuted
	case RunnerStatusStarting:
		symbol, colour = "◎", tui.ColAccentAmber
	case RunnerStatusRunning:
		symbol, colour = "◉", tui.ColAccentGreen
	case RunnerStatusError:
		symbol, colour = "◌", tui.ColAccentRose
	default:
		return ""
	}

	// Pad label to exactly labelWidth visible chars so the chip is always chipWidth wide.
	label := state.RunnerLabel
	if len(label) > labelWidth {
		label = label[:labelWidth]
	}
	padded := label + strings.Repeat(" ", labelWidth-len(label))

	raw := symbol + " " + padded
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(colour)).
		Bold(true).
		Render(raw)
}

func formatInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}
