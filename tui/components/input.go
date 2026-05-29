package components

import (
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

const (
	InputMinLines = 1
	InputMaxLines = 6
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
	ta.MaxHeight = InputMaxLines
	ta.SetWidth(width - 6) // account for border + prefix
	ta.SetHeight(InputMinLines)
	ta.Focus()

	// Disable the textarea's own enter-to-newline binding entirely.
	// Newlines are inserted manually via InsertNewline() called from chat.go,
	// triggered by shift+enter or ctrl+j in the Update loop.
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys()) // no keys

	s := textarea.DefaultDarkStyles()
	s.Focused.Base = lipgloss.NewStyle()
	s.Blurred.Base = lipgloss.NewStyle()
	s.Focused.CursorLine = lipgloss.NewStyle() // no highlight — keeps text readable
	s.Focused.Placeholder = tui.StyleDim
	s.Blurred.Placeholder = tui.StyleDim
	s.Focused.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary))
	s.Blurred.Text = lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary))
	ta.SetStyles(s)

	return InputModel{
		textarea: ta,
		mode:     ModeChat,
		width:    width,
	}
}

// Mode returns the current input mode.
func (m InputModel) Mode() InputMode { return m.mode }

// IsDisabled returns whether the input is currently disabled.
func (m InputModel) IsDisabled() bool { return m.disabled }

// Width returns the configured outer width of the input box.
func (m InputModel) Width() int { return m.width }

// TextareaHeight returns the textarea's current rendered height in rows.
// This is the value controlled by SetHeight and determines how many lines
// are visible inside the box — useful for verifying auto-grow behaviour.
func (m InputModel) TextareaHeight() int { return m.textarea.Height() }

// SetValue sets the textarea content directly.
// Useful for tests and for programmatic pre-population.
func (m *InputModel) SetValue(s string) {
	m.textarea.SetValue(s)
	m.autoGrow()
}

// SetMode updates the current mode.
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
	m.textarea.SetWidth(width - 6)
}

// Height returns the current rendered height of the input box in terminal rows,
// including the border (2 rows) and the textarea content rows.
func (m InputModel) Height() int {
	lines := m.textarea.LineCount()
	if lines < InputMinLines {
		lines = InputMinLines
	}
	if lines > InputMaxLines {
		lines = InputMaxLines
	}
	return lines + 2 // +2 for top/bottom border
}

// Value returns the current input text.
func (m InputModel) Value() string {
	return m.textarea.Value()
}

// Reset clears the input field.
func (m *InputModel) Reset() {
	m.textarea.Reset()
	m.autoGrow()
}

// InsertNewline appends a newline at the end of the current value.
// Note: this always appends to the end (cursor position is not preserved)
// because the bubbles textarea does not expose an insert-at-cursor API.
// shift+enter only fires on terminals supporting the Kitty keyboard protocol;
// ctrl+j is the universal fallback.
func (m *InputModel) InsertNewline() {
	m.textarea.SetValue(m.textarea.Value() + "\n")
	m.autoGrow()
}

// autoGrow adjusts the textarea's rendered height to match its current line
// count, clamped between InputMinLines and InputMaxLines.
func (m *InputModel) autoGrow() {
	desired := m.textarea.LineCount()
	if desired < InputMinLines {
		desired = InputMinLines
	}
	if desired > InputMaxLines {
		desired = InputMaxLines
	}
	if m.textarea.Height() != desired {
		m.textarea.SetHeight(desired)
	}
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
	// textarea.Blink() returns a tea.Msg, wrap it as a Cmd.
	return func() tea.Msg { return textarea.Blink() }
}

// Update forwards messages to the textarea and auto-grows its height.
func (m InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	if m.disabled {
		return m, nil
	}
	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	m.autoGrow()
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
