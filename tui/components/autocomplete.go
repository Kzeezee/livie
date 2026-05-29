package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

const autocompleteMaxVisible = 5

// Per-frame style allocations hoisted to package level.
var (
	acNameStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentPurple)).Bold(true)
	acDescStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary))
	acMarkerStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentPurple)).Bold(true)
	acCounterStyle = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted))
)

// AutocompleteModel handles /command autocomplete suggestions.
// It maintains a full match list and a sliding 5-row visible window.
type AutocompleteModel struct {
	allMatches  []*tui.Command
	selectedIdx int  // index into allMatches (global, not windowed)
	windowStart int  // first visible row index
	width       int
	dismissed   bool
	lastInput   string // last typed prefix, used to detect real input changes
}

// NewAutocompleteModel creates a new AutocompleteModel at the given width.
func NewAutocompleteModel(width int) AutocompleteModel {
	return AutocompleteModel{width: width}
}

// SetWidth updates the render width.
func (m *AutocompleteModel) SetWidth(width int) {
	m.width = width
}

// SetInput recomputes matches from the current raw input string.
// Safe to call on every keystroke — idempotent for unchanged input.
func (m *AutocompleteModel) SetInput(raw string, r *tui.CommandRegistry) {
	raw = strings.TrimSpace(raw)

	if !strings.HasPrefix(raw, "/") {
		m.clear()
		return
	}

	// Extract the typed prefix after the slash
	typed := strings.ToLower(raw[1:])

	// Once the user has typed a space (past the command name) hide suggestions
	if strings.ContainsAny(typed, " \t") {
		m.clear()
		return
	}

	// Clear dismissed state only when the typed prefix actually changes
	if typed != m.lastInput {
		m.dismissed = false
		m.lastInput = typed
	}

	m.allMatches = r.Suggest(typed)

	// Clamp selectedIdx in case the match list shrank
	if m.selectedIdx >= len(m.allMatches) {
		m.selectedIdx = 0
		m.windowStart = 0
	}
}

// Dismiss hides the popup until the typed prefix changes.
func (m *AutocompleteModel) Dismiss() {
	m.dismissed = true
}

// MoveDown advances selection by one, cycling back to 0 at the end.
func (m *AutocompleteModel) MoveDown() {
	if len(m.allMatches) == 0 {
		return
	}
	m.dismissed = false
	m.selectedIdx = (m.selectedIdx + 1) % len(m.allMatches)
	m.adjustWindow()
}

// MoveUp retreats selection by one, cycling to the last item from the first.
func (m *AutocompleteModel) MoveUp() {
	if len(m.allMatches) == 0 {
		return
	}
	m.dismissed = false
	m.selectedIdx = (m.selectedIdx - 1 + len(m.allMatches)) % len(m.allMatches)
	m.adjustWindow()
}

// adjustWindow slides the visible 5-row window to keep selectedIdx in view.
func (m *AutocompleteModel) adjustWindow() {
	total := len(m.allMatches)

	// Wrap-around to first → snap window to start
	if m.selectedIdx == 0 {
		m.windowStart = 0
		return
	}
	// Wrap-around to last → snap window to end
	if m.selectedIdx == total-1 {
		if total > autocompleteMaxVisible {
			m.windowStart = total - autocompleteMaxVisible
		}
		return
	}
	// Scroll up
	if m.selectedIdx < m.windowStart {
		m.windowStart = m.selectedIdx
		return
	}
	// Scroll down
	if m.selectedIdx >= m.windowStart+autocompleteMaxVisible {
		m.windowStart = m.selectedIdx - autocompleteMaxVisible + 1
	}
}

// Selected returns the highlighted Command, or nil when not visible.
func (m *AutocompleteModel) Selected() *tui.Command {
	if !m.IsVisible() || len(m.allMatches) == 0 {
		return nil
	}
	return m.allMatches[m.selectedIdx]
}

// IsVisible returns true when the popup should be rendered.
func (m *AutocompleteModel) IsVisible() bool {
	return !m.dismissed && len(m.allMatches) > 0
}

// Height returns the number of terminal rows the popup occupies (0 when hidden).
func (m *AutocompleteModel) Height() int {
	if !m.IsVisible() {
		return 0
	}
	rows := len(m.allMatches)
	if rows > autocompleteMaxVisible {
		rows = autocompleteMaxVisible
	}
	return rows + 1 // +1 for the counter line
}

// View renders the autocomplete popup — plain text, no border.
func (m *AutocompleteModel) View() string {
	if !m.IsVisible() {
		return ""
	}

	total := len(m.allMatches)
	visibleCount := total
	if visibleCount > autocompleteMaxVisible {
		visibleCount = autocompleteMaxVisible
	}

	end := m.windowStart + visibleCount
	if end > total {
		end = total
	}
	window := m.allMatches[m.windowStart:end]

	// Measure longest command name in window for column alignment
	maxNameLen := 0
	for _, cmd := range window {
		if len(cmd.Name) > maxNameLen {
			maxNameLen = len(cmd.Name)
		}
	}

	nameStyle    := acNameStyle
	descStyle    := acDescStyle
	markerStyle  := acMarkerStyle
	counterStyle := acCounterStyle

	// Available width for the description column:
	//   2 (left pad) + 1 (marker) + 1 (space) + 1 ("/") + maxNameLen + 2 (gap)
	prefixCost := 2 + 1 + 1 + 1 + maxNameLen + 2
	descAvail := m.width - prefixCost

	var sb strings.Builder

	for i, cmd := range window {
		globalIdx := m.windowStart + i
		isSelected := globalIdx == m.selectedIdx

		var marker string
		if isSelected {
			marker = markerStyle.Render("▶")
		} else {
			marker = " "
		}

		nameStr := nameStyle.Render("/" + cmd.Name)
		// Pad the name column so descriptions line up
		gap := strings.Repeat(" ", maxNameLen-len(cmd.Name)+2)

		desc := cmd.Description
		if descAvail > 3 && len(desc) > descAvail {
			desc = desc[:descAvail-1] + "…"
		}
		descStr := descStyle.Render(desc)

		sb.WriteString("  " + marker + " " + nameStr + gap + descStr + "\n")
	}

	// Counter line: selectedIdx+1 / total
	counter := fmt.Sprintf("%d/%d", m.selectedIdx+1, total)
	sb.WriteString(counterStyle.Render("  " + counter))

	return sb.String()
}

// clear resets all state to the zero value.
func (m *AutocompleteModel) clear() {
	m.allMatches = nil
	m.selectedIdx = 0
	m.windowStart = 0
	m.dismissed = false
	m.lastInput = ""
}
