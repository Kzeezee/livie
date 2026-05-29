package components

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	tui "github.com/kez/livie/tui"
)

// InputModel wraps a textarea with mode-aware styling.
type InputModel struct {
	textarea textarea.Model
	mode     InputMode
	disabled bool
	width    int
}

// NewInputModel creates a new InputModel.
func NewInputModel(width int) InputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (/help for commands)"
	ta.ShowLineNumbers = false
	ta.MaxHeight = 6 * 2 // 6 lines
	ta.SetWidth(width - 4)
	ta.SetHeight(1)
	ta.Focus()

	// Remove the default textarea border — we draw our own
	ta.FocusedStyle.Base = lipgloss.NewStyle()
	ta.BlurredStyle.Base = lipgloss.NewStyle()
	ta.FocusedStyle.CursorLine = lipgloss.NewStyle() // no highlight — keeps text readable
	ta.FocusedStyle.Placeholder = tui.StyleDim
	ta.BlurredStyle.Placeholder = tui.StyleDim
	ta.FocusedStyle.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary))
	ta.BlurredStyle.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary))

	return InputModel{
		textarea: ta,
		mode:     ModeQuery,
		width:    width,
	}
}

// SetMode updates the mode and redraws the border style.
func (m *InputModel) SetMode(mode InputMode) {
	m.mode = mode
}

// SetDisabled enables or disables the input (during streaming).
func (m *InputModel) SetDisabled(disabled bool) {
	m.disabled = disabled
	if disabled {
		m.textarea.Blur()
		m.textarea.Placeholder = "Livie is thinking..."
	} else {
		m.textarea.Focus()
		m.textarea.Placeholder = "Type a message... (/help for commands)"
	}
}

// SetWidth updates the input width.
func (m *InputModel) SetWidth(width int) {
	m.width = width
	m.textarea.SetWidth(width - 6) // account for border + prefix
}

// Value returns the current input text.
func (m InputModel) Value() string {
	return m.textarea.Value()
}

// Reset clears the input field.
func (m *InputModel) Reset() {
	m.textarea.Reset()
}

// IsCommand returns true when the input starts with /.
func (m InputModel) IsCommand() bool {
	return strings.HasPrefix(strings.TrimSpace(m.textarea.Value()), "/")
}

// Focus gives focus to the input.
func (m *InputModel) Focus() tea.Cmd {
	return m.textarea.Focus()
}

// Init implements tea.Model.
func (m InputModel) Init() tea.Cmd {
	return textarea.Blink
}

// Update forwards messages to the textarea.
func (m InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	if m.disabled {
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

// View renders the styled input bar.
func (m InputModel) View() string {
	// Choose prefix glyph and border style based on mode / command state
	var prefix string
	var borderStyle lipgloss.Style

	if m.disabled {
		prefix = tui.StyleMuted.Render("◌")
		borderStyle = tui.StyleBorder
	} else if m.IsCommand() {
		prefix = tui.StyleAccentPurple.Render("⌘")
		borderStyle = tui.StyleBorder.BorderForeground(lipgloss.Color(tui.ColAccentPurple))
	} else {
		switch m.mode {
		case ModeBash:
			prefix = tui.StyleAccentRose.Render("▶")
			borderStyle = tui.StyleBorderFocusBash
		default:
			prefix = tui.StyleAccentCyan.Render("▶")
			borderStyle = tui.StyleBorderFocusQuery
		}
	}

	// Submit hint
	var hint string
	if m.disabled {
		hint = tui.StyleMuted.Render("[…]")
	} else if len(strings.TrimSpace(m.textarea.Value())) > 0 {
		hint = tui.StyleAccentCyan.Render("[↵]")
	} else {
		hint = tui.StyleMuted.Render("[↵]")
	}

	inner := lipgloss.JoinHorizontal(
		lipgloss.Center,
		prefix+" ",
		m.textarea.View(),
		" "+hint,
	)

	return borderStyle.Width(m.width - 2).Render(inner)
}
