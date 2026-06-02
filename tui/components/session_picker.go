package components

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
	"github.com/kez/livie/session"
)

const maxPickerRows = 8

// SessionPickerModel renders a navigable list of recent sessions as an
// overlay above the input bar. Visible after ActionOpenResume fires and
// SummariesLoadedMsg is received.
type SessionPickerModel struct {
	summaries []session.Summary
	cursor    int
	width     int
	visible   bool
	loading   bool // true while ListSummariesCmd is in flight
}

// NewSessionPickerModel creates a new SessionPickerModel at the given width.
func NewSessionPickerModel(width int) SessionPickerModel {
	return SessionPickerModel{width: width}
}

// SetSummaries populates the list and makes the picker visible.
// Resets cursor to 0.
func (m *SessionPickerModel) SetSummaries(summaries []session.Summary) {
	m.summaries = summaries
	m.cursor = 0
	m.visible = true
	m.loading = false
}

// SetLoading sets the loading state (shown while ListSummariesCmd is in flight).
func (m *SessionPickerModel) SetLoading(v bool) {
	m.loading = v
	m.visible = true
}

// IsVisible reports whether the picker should be rendered.
func (m SessionPickerModel) IsVisible() bool { return m.visible }

// IsLoading reports whether the picker is awaiting data.
func (m SessionPickerModel) IsLoading() bool { return m.loading }

// MoveUp moves the cursor up one row, wrapping to the bottom.
func (m *SessionPickerModel) MoveUp() {
	if len(m.summaries) == 0 {
		return
	}
	if m.cursor <= 0 {
		m.cursor = len(m.summaries) - 1
	} else {
		m.cursor--
	}
}

// MoveDown moves the cursor down one row, wrapping to the top.
func (m *SessionPickerModel) MoveDown() {
	if len(m.summaries) == 0 {
		return
	}
	m.cursor = (m.cursor + 1) % len(m.summaries)
}

// Selected returns the currently highlighted summary, or nil if the list is empty.
func (m SessionPickerModel) Selected() *session.Summary {
	if len(m.summaries) == 0 || m.cursor >= len(m.summaries) {
		return nil
	}
	s := m.summaries[m.cursor]
	return &s
}

// Dismiss hides the picker.
func (m *SessionPickerModel) Dismiss() {
	m.visible = false
	m.loading = false
}

// SetWidth updates the picker width.
func (m *SessionPickerModel) SetWidth(w int) {
	m.width = w
}

// Height returns the rendered height in rows.
// For use by syncInputHeight to reserve space in the viewport.
func (m SessionPickerModel) Height() int {
	if m.loading {
		return 4
	}
	rows := len(m.summaries)
	if rows > maxPickerRows {
		rows = maxPickerRows
	}
	if rows == 0 {
		rows = 1 // "no sessions found"
	}
	return rows + 3 // top border/label + rows + hint row + padding
}

// View renders the session picker panel.
func (m SessionPickerModel) View() string {
	if !m.visible {
		return ""
	}

	inner := m.width - 2
	if inner < 20 {
		inner = 20
	}

	var sb strings.Builder

	// Top border with label
	label := " sessions "
	labelStyled := tui.StyleDivider.Render(label)
	dashCount := inner - len(label)
	if dashCount < 0 {
		dashCount = 0
	}
	topBorder := tui.StyleDivider.Render("─") +
		labelStyled +
		tui.StyleDivider.Render(strings.Repeat("─", dashCount))
	sb.WriteString(topBorder)
	sb.WriteString("\n")

	if m.loading {
		sb.WriteString(tui.StyleMsgSystem.Render("  ⠙  loading sessions…"))
		sb.WriteString("\n")
		sb.WriteString(tui.StyleDivider.Render(strings.Repeat("─", m.width)))
		sb.WriteString("\n")
		hint := tui.StyleDim.Render("  esc to dismiss")
		sb.WriteString(hint)
		return sb.String()
	}

	if len(m.summaries) == 0 {
		sb.WriteString(tui.StyleMsgSystem.Render("  no sessions found"))
		sb.WriteString("\n")
		sb.WriteString(tui.StyleDivider.Render(strings.Repeat("─", m.width)))
		sb.WriteString("\n")
		sb.WriteString(tui.StyleDim.Render("  esc to dismiss"))
		return sb.String()
	}

	// Determine visible window
	visibleRows := maxPickerRows
	if len(m.summaries) < visibleRows {
		visibleRows = len(m.summaries)
	}
	start := 0
	if m.cursor >= visibleRows {
		start = m.cursor - visibleRows + 1
	}
	end := start + visibleRows
	if end > len(m.summaries) {
		end = len(m.summaries)
	}

	// Column widths: date(10) + sep(2) + time(5) + sep(2) + model@ep(22) + sep(2) + preview(remaining)
	const dateW = 10
	const timeW = 5
	const metaW = 22
	const sepW = 2
	previewW := inner - dateW - timeW - metaW - sepW*3
	if previewW < 10 {
		previewW = 10
	}

	for i := start; i < end; i++ {
		s := m.summaries[i]

		dateStr := s.UpdatedAt.Format("2006-01-02")
		timeStr := s.UpdatedAt.Format("15:04")

		meta := s.ModelName
		if s.EndpointName != "" {
			meta = s.ModelName + " @ " + s.EndpointName
		}
		if lipgloss.Width(meta) > metaW {
			runes := []rune(meta)
			meta = string(runes[:metaW-1]) + "…"
		}
		meta = fmt.Sprintf("%-*s", metaW, meta)

		preview := s.Preview
		if lipgloss.Width(preview) > previewW {
			runes := []rune(preview)
			if previewW > 3 {
				preview = string(runes[:previewW-1]) + "…"
			} else {
				preview = string(runes[:previewW])
			}
		}
		preview = "\"" + preview + "\""

		row := fmt.Sprintf("  %s  %s  %s  %s",
			dateStr, timeStr, meta, preview)

		if i == m.cursor {
			prefix := tui.StyleAccentCyan.Render("▶")
			row = prefix + row[1:] // replace leading space with marker
			sb.WriteString(tui.StyleAccentCyan.Render(row))
		} else {
			prefix := tui.StyleDim.Render("·")
			row = prefix + row[1:]
			sb.WriteString(tui.StyleDim.Render(row))
		}
		sb.WriteString("\n")
	}

	// Bottom hint
	sb.WriteString(tui.StyleDivider.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")
	hint := tui.StyleDim.Render("  enter to load  ·  esc to dismiss")
	sb.WriteString(hint)

	return sb.String()
}
