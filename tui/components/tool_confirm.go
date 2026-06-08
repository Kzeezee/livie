package components

import (
	"strings"

	tui "github.com/kez/livie/tui"
)

// ToolConfirmModel renders a confirmation block above the input bar when the
// model issues a tool call and cfg.ConfirmToolCalls is true.
// Follows the same overlay pattern as SessionPickerModel.
type ToolConfirmModel struct {
	id      string
	name    string
	args    string // raw JSON args string
	width   int
	visible bool
}

// NewToolConfirmModel creates a new ToolConfirmModel at the given width.
func NewToolConfirmModel(width int) ToolConfirmModel {
	return ToolConfirmModel{width: width}
}

// Show makes the confirm block visible for the given tool call.
func (m *ToolConfirmModel) Show(id, name, args string) {
	m.id = id
	m.name = name
	m.args = args
	m.visible = true
}

// Dismiss hides the confirm block and clears state.
func (m *ToolConfirmModel) Dismiss() {
	m.visible = false
	m.id = ""
	m.name = ""
	m.args = ""
}

// IsVisible reports whether the block should be rendered.
func (m ToolConfirmModel) IsVisible() bool { return m.visible }

// ID returns the tool_call_id of the pending call.
func (m ToolConfirmModel) ID() string { return m.id }

// Name returns the tool name of the pending call.
func (m ToolConfirmModel) Name() string { return m.name }

// Args returns the raw JSON args of the pending call.
func (m ToolConfirmModel) Args() string { return m.args }

// Height returns the rendered height in rows — always 4 when visible.
// Used by syncInputHeight to reserve space in the viewport.
func (m ToolConfirmModel) Height() int {
	if !m.visible {
		return 0
	}
	return 4
}

// SetWidth updates the component width.
func (m *ToolConfirmModel) SetWidth(w int) { m.width = w }

// View renders the confirmation block.
//
// ─ ⚙ tool ──────────────────────────────────────────
//
//	bash · rm -rf ./build/dist
//	─────────────────────────────────────────────────
//	y / enter = run   ·   n / esc = reject
func (m ToolConfirmModel) View() string {
	if !m.visible {
		return ""
	}

	inner := m.width - 2
	if inner < 20 {
		inner = 20
	}

	var sb strings.Builder

	// ── Top border with ⚙ tool label ─────────────────────────────────────────
	label := " ⚙ tool "
	labelStyled := tui.StyleAccentAmber.Render(label)
	// lipgloss width of the label without ANSI = bare string length
	dashCount := inner - len(label)
	if dashCount < 0 {
		dashCount = 0
	}
	topBorder := tui.StyleDivider.Render("─") +
		labelStyled +
		tui.StyleDivider.Render(strings.Repeat("─", dashCount))
	sb.WriteString(topBorder)
	sb.WriteString("\n")

	// ── Tool name · args ──────────────────────────────────────────────────────
	maxArgW := m.width - len(m.name) - 6 // "  name · " prefix + margin
	if maxArgW < 10 {
		maxArgW = 10
	}
	argStr := truncateArgs(m.args, maxArgW)
	nameStyled := tui.StyleAccentAmber.Render(m.name)
	argsStyled := tui.StyleDim.Render(argStr)
	sb.WriteString("  " + nameStyled + tui.StyleDim.Render(" · ") + argsStyled)
	sb.WriteString("\n")

	// ── Separator ─────────────────────────────────────────────────────────────
	sb.WriteString(tui.StyleDivider.Render(strings.Repeat("─", m.width)))
	sb.WriteString("\n")

	// ── Hint line ─────────────────────────────────────────────────────────────
	hint := tui.StyleDim.Render("  y / enter = run   ·   n / esc = reject")
	sb.WriteString(hint)

	return sb.String()
}
