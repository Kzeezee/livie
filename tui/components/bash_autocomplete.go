package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

const bashACMaxVisible = 8

// Per-frame styles hoisted to package level.
var (
	bashACMarkerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentRose)).Bold(true)
	bashACFileStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary))
	bashACDirStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentAmber))
	bashACCounterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted))
)

// BashAutocompleteModel manages shell completion suggestions shown in bash mode.
//
// Completions are populated on demand (Tab keypress) via bashCompleteCmd.
// Any non-navigation key press dismisses the popup; it does not auto-update
// on every keystroke the way the slash-command autocomplete does, because
// compgen involves forking a bash subprocess.
type BashAutocompleteModel struct {
	completions []string
	selectedIdx int
	windowStart int
	width       int
	visible     bool
	prefix      string // the partial word that was completed (shown in counter line)
	wordStart   int    // byte offset of prefix in the full input string
}

// NewBashAutocompleteModel creates a new model at the given terminal width.
func NewBashAutocompleteModel(width int) BashAutocompleteModel {
	return BashAutocompleteModel{width: width}
}

// SetWidth updates the render width.
func (m *BashAutocompleteModel) SetWidth(w int) { m.width = w }

// SetCompletions replaces the completion list and makes the popup visible.
// Passing an empty slice hides the popup.
func (m *BashAutocompleteModel) SetCompletions(completions []string, prefix string, wordStart int) {
	m.completions = completions
	m.prefix = prefix
	m.wordStart = wordStart
	m.selectedIdx = 0
	m.windowStart = 0
	m.visible = len(completions) > 0
}

// IsVisible returns true when the popup should be rendered.
func (m *BashAutocompleteModel) IsVisible() bool {
	return m.visible && len(m.completions) > 0
}

// Dismiss hides the popup without clearing the completion list.
func (m *BashAutocompleteModel) Dismiss() { m.visible = false }

// Count returns the total number of completions.
func (m *BashAutocompleteModel) Count() int { return len(m.completions) }

// Selected returns the currently highlighted completion string, or "" when hidden.
func (m *BashAutocompleteModel) Selected() string {
	if !m.IsVisible() || len(m.completions) == 0 {
		return ""
	}
	return m.completions[m.selectedIdx]
}

// WordStart returns the byte offset in the input where the completed word begins.
func (m *BashAutocompleteModel) WordStart() int { return m.wordStart }

// MoveDown advances selection by one, cycling back to 0 at the end.
func (m *BashAutocompleteModel) MoveDown() {
	if len(m.completions) == 0 {
		return
	}
	m.selectedIdx = (m.selectedIdx + 1) % len(m.completions)
	m.adjustWindow()
}

// MoveUp retreats selection by one, cycling to the last item from the first.
func (m *BashAutocompleteModel) MoveUp() {
	if len(m.completions) == 0 {
		return
	}
	m.selectedIdx = (m.selectedIdx - 1 + len(m.completions)) % len(m.completions)
	m.adjustWindow()
}

func (m *BashAutocompleteModel) adjustWindow() {
	total := len(m.completions)
	switch {
	case m.selectedIdx == 0:
		m.windowStart = 0
	case m.selectedIdx == total-1 && total > bashACMaxVisible:
		m.windowStart = total - bashACMaxVisible
	case m.selectedIdx < m.windowStart:
		m.windowStart = m.selectedIdx
	case m.selectedIdx >= m.windowStart+bashACMaxVisible:
		m.windowStart = m.selectedIdx - bashACMaxVisible + 1
	}
}

// Height returns the number of terminal rows the popup occupies (0 when hidden).
func (m *BashAutocompleteModel) Height() int {
	if !m.IsVisible() {
		return 0
	}
	rows := len(m.completions)
	if rows > bashACMaxVisible {
		rows = bashACMaxVisible
	}
	return rows + 1 // +1 for the counter/hint line
}

// View renders the completion popup.
func (m *BashAutocompleteModel) View() string {
	if !m.IsVisible() {
		return ""
	}

	total := len(m.completions)
	visibleCount := total
	if visibleCount > bashACMaxVisible {
		visibleCount = bashACMaxVisible
	}
	end := m.windowStart + visibleCount
	if end > total {
		end = total
	}
	window := m.completions[m.windowStart:end]

	var sb strings.Builder
	for i, c := range window {
		globalIdx := m.windowStart + i
		isSelected := globalIdx == m.selectedIdx

		marker := " "
		if isSelected {
			marker = bashACMarkerStyle.Render("▶")
		}

		var nameStr string
		if strings.HasSuffix(c, "/") {
			nameStr = bashACDirStyle.Render(c)
		} else {
			nameStr = bashACFileStyle.Render(c)
		}

		sb.WriteString("  " + marker + " " + nameStr + "\n")
	}

	// Counter line: "prefix  N/total  ↑↓ navigate  enter accept"
	counter := fmt.Sprintf("%d/%d", m.selectedIdx+1, total)
	hint := "↑↓ navigate  enter accept  esc dismiss"
	prefix := ""
	if m.prefix != "" {
		prefix = m.prefix + "  "
	}
	sb.WriteString(bashACCounterStyle.Render("  " + prefix + counter + "  " + hint))

	return sb.String()
}
