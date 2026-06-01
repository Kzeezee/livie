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
//
// Sub-argument mode:
// When the user types past the command name (adds a space) the model walks
// the SubArg tree registered on that command iteratively — one level per
// space-separated, fully-committed token — then filters on the final partial
// token.  This scales to arbitrary nesting with no recursion.
type AutocompleteModel struct {
	allMatches  []*tui.Command
	selectedIdx int  // index into allMatches or subMatches (global, not windowed)
	windowStart int  // first visible row index
	width       int
	dismissed   bool
	lastInput   string // last lowercased typed string; used to detect real changes

	// sub-argument mode
	inSubMode      bool
	subMatches     []tui.SubArg // filtered entries at the current tree depth
	subInputPrefix string       // raw input prefix before the current partial token
	                            // e.g. "/run start " — appended with sub.Name on accept
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
//
// Sub-argument walk (iterative, arbitrary depth):
//   1. Split off the root command name at the first space.
//   2. Tokenise the remainder.  Tokens before the last are "committed" (the
//      user pressed space after them); the last token is the live prefix.
//   3. For each committed token walk one level down the SubArg tree by exact
//      match.  If any token has no match, hide the popup.
//   4. Filter the current level's SubArgs by the prefix of the last token.
func (m *AutocompleteModel) SetInput(raw string, r *tui.CommandRegistry) {
	// Only strip leading whitespace — trailing spaces are meaningful here:
	// a space after the command name (e.g. "/run ") signals that the user has
	// committed it and wants sub-argument suggestions.
	raw = strings.TrimLeft(raw, " \t\n\r")

	if !strings.HasPrefix(raw, "/") {
		m.clear()
		return
	}

	// Lower-case copy for matching; preserve raw for building subInputPrefix.
	typed := strings.ToLower(raw[1:])

	// ── Normal command completion (no space yet) ──────────────────────────
	spaceIdx := strings.IndexAny(typed, " \t")
	if spaceIdx < 0 {
		if typed != m.lastInput {
			m.dismissed = false
			m.lastInput = typed
		}
		m.inSubMode = false
		m.subMatches = nil
		m.subInputPrefix = ""
		m.allMatches = r.Suggest(typed)
		if m.selectedIdx >= len(m.allMatches) {
			m.selectedIdx = 0
			m.windowStart = 0
		}
		return
	}

	// ── Sub-argument mode ─────────────────────────────────────────────────
	cmdName := typed[:spaceIdx]
	cmd := r.FindCommand(cmdName)
	if cmd == nil || len(cmd.Subcommands) == 0 {
		m.clear()
		return
	}

	// Everything after the first space, lowercased.
	rest := typed[spaceIdx+1:]

	// Does rest end with whitespace?  If so, the last token is committed too.
	trailingSpace := len(rest) > 0 && (rest[len(rest)-1] == ' ' || rest[len(rest)-1] == '\t')
	tokens := strings.Fields(rest) // committed + optional partial last token

	// Walk the SubArg tree.
	currentSubs := cmd.Subcommands
	navigated := make([]string, 0, len(tokens)) // committed tokens (for subInputPrefix)

	committedCount := len(tokens)
	if !trailingSpace && len(tokens) > 0 {
		// Last token is the live partial prefix — don't navigate into it.
		committedCount = len(tokens) - 1
	}

	for i := 0; i < committedCount; i++ {
		tok := tokens[i]
		var next []tui.SubArg
		for _, sub := range currentSubs {
			if strings.ToLower(sub.Name) == tok {
				next = sub.SubArgs
				break
			}
		}
		if next == nil && len(currentSubs) > 0 {
			// Token doesn't match any sub at this level — hide popup.
			m.clear()
			return
		}
		navigated = append(navigated, tok)
		currentSubs = next
	}

	// Determine the live prefix for filtering.
	var prefix string
	if !trailingSpace && len(tokens) > 0 {
		prefix = tokens[len(tokens)-1]
	}

	// Filter current level by prefix.
	var subs []tui.SubArg
	for _, sub := range currentSubs {
		if prefix == "" || strings.HasPrefix(strings.ToLower(sub.Name), prefix) {
			subs = append(subs, sub)
		}
	}
	if len(subs) == 0 {
		m.clear()
		return
	}

	// Build the input prefix that acceptance will prepend.
	// e.g. "/run start " when navigated=["start"].
	subInputPrefix := "/" + cmdName + " "
	if len(navigated) > 0 {
		subInputPrefix += strings.Join(navigated, " ") + " "
	}

	if typed != m.lastInput {
		m.dismissed = false
		m.lastInput = typed
	}
	m.inSubMode = true
	m.subInputPrefix = subInputPrefix
	m.subMatches = subs
	m.allMatches = nil
	if m.selectedIdx >= len(m.subMatches) {
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
	total := m.matchCount()
	if total == 0 {
		return
	}
	m.dismissed = false
	m.selectedIdx = (m.selectedIdx + 1) % total
	m.adjustWindow()
}

// MoveUp retreats selection by one, cycling to the last item from the first.
func (m *AutocompleteModel) MoveUp() {
	total := m.matchCount()
	if total == 0 {
		return
	}
	m.dismissed = false
	m.selectedIdx = (m.selectedIdx - 1 + total) % total
	m.adjustWindow()
}

// matchCount returns the number of current matches (command or sub mode).
func (m *AutocompleteModel) matchCount() int {
	if m.inSubMode {
		return len(m.subMatches)
	}
	return len(m.allMatches)
}

// adjustWindow slides the visible 5-row window to keep selectedIdx in view.
func (m *AutocompleteModel) adjustWindow() {
	total := m.matchCount()

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

// Selected returns the highlighted Command, or nil when in sub mode or not visible.
func (m *AutocompleteModel) Selected() *tui.Command {
	if !m.IsVisible() || m.inSubMode || len(m.allMatches) == 0 {
		return nil
	}
	return m.allMatches[m.selectedIdx]
}

// SelectedSub returns the highlighted SubArg when in sub mode, or nil otherwise.
func (m *AutocompleteModel) SelectedSub() *tui.SubArg {
	if !m.IsVisible() || !m.inSubMode || len(m.subMatches) == 0 {
		return nil
	}
	return &m.subMatches[m.selectedIdx]
}

// InSubMode reports whether the popup is currently showing sub-arguments.
func (m *AutocompleteModel) InSubMode() bool { return m.inSubMode && m.IsVisible() }

// SubInputPrefix returns the raw input prefix before the current partial token
// (e.g. "/run start ").  Concatenate with a sub.Name + " " on accept.
func (m *AutocompleteModel) SubInputPrefix() string { return m.subInputPrefix }

// IsVisible returns true when the popup should be rendered.
func (m *AutocompleteModel) IsVisible() bool {
	if m.dismissed {
		return false
	}
	if m.inSubMode {
		return len(m.subMatches) > 0
	}
	return len(m.allMatches) > 0
}

// Height returns the number of terminal rows the popup occupies (0 when hidden).
func (m *AutocompleteModel) Height() int {
	if !m.IsVisible() {
		return 0
	}
	rows := m.matchCount()
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
	if m.inSubMode {
		return m.viewSub()
	}
	return m.viewCommands()
}

// viewCommands renders the normal command-list popup.
func (m *AutocompleteModel) viewCommands() string {
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

// viewSub renders the sub-argument popup at the current tree depth.
// The counter line includes the breadcrumb path (e.g. "/run start · 2/5")
// so the user always knows how deep they are.
func (m *AutocompleteModel) viewSub() string {
	total := len(m.subMatches)
	visibleCount := total
	if visibleCount > autocompleteMaxVisible {
		visibleCount = autocompleteMaxVisible
	}

	end := m.windowStart + visibleCount
	if end > total {
		end = total
	}
	window := m.subMatches[m.windowStart:end]

	// Measure longest sub name in window for column alignment
	maxNameLen := 0
	for _, sub := range window {
		if len(sub.Name) > maxNameLen {
			maxNameLen = len(sub.Name)
		}
	}

	nameStyle    := acNameStyle
	descStyle    := acDescStyle
	markerStyle  := acMarkerStyle
	counterStyle := acCounterStyle

	// Available width for the description column:
	//   2 (left pad) + 1 (marker) + 1 (space) + maxNameLen + 2 (gap)
	// (no leading "/" for sub-args)
	prefixCost := 2 + 1 + 1 + maxNameLen + 2
	descAvail := m.width - prefixCost

	var sb strings.Builder

	for i, sub := range window {
		globalIdx := m.windowStart + i
		isSelected := globalIdx == m.selectedIdx

		var marker string
		if isSelected {
			marker = markerStyle.Render("▶")
		} else {
			marker = " "
		}

		nameStr := nameStyle.Render(sub.Name)
		gap := strings.Repeat(" ", maxNameLen-len(sub.Name)+2)

		desc := sub.Description
		if descAvail > 3 && len(desc) > descAvail {
			desc = desc[:descAvail-1] + "…"
		}
		descStr := descStyle.Render(desc)

		sb.WriteString("  " + marker + " " + nameStr + gap + descStr + "\n")
	}

	// Breadcrumb counter, e.g. "/run start · 2/5"
	breadcrumb := strings.TrimRight(m.subInputPrefix, " ")
	counter := fmt.Sprintf("%s · %d/%d", breadcrumb, m.selectedIdx+1, total)
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
	m.inSubMode = false
	m.subInputPrefix = ""
	m.subMatches = nil
}
