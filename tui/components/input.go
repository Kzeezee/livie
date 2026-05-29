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
	ta.SetWidth(width - 2) // account for prefix glyph + space
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
func (m InputModel) TextareaHeight() int { return m.textarea.Height() }

// SetValue sets the textarea content directly.
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
	m.textarea.SetWidth(width - 2)
}

// Height returns the current rendered height of the input in terminal rows.
// This is purely the textarea content rows (no border — the input is borderless).
func (m InputModel) Height() int {
	lines := m.textarea.LineCount()
	if lines < InputMinLines {
		lines = InputMinLines
	}
	if lines > InputMaxLines {
		lines = InputMaxLines
	}
	return lines
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

// View renders the borderless input area.
// The glyph on the left indicates mode; the textarea fills the rest of the line.
func (m InputModel) View() string {
	var glyph string

	switch {
	case m.disabled:
		glyph = tui.StyleMuted.Render("◌")
	case m.IsCommand():
		glyph = tui.StyleAccentPurple.Render("⌘")
	case m.mode == ModeBash:
		glyph = tui.StyleAccentRose.Render("▶")
	default:
		glyph = tui.StyleAccentCyan.Render("▶")
	}

	return lipgloss.JoinHorizontal(lipgloss.Top, glyph+" ", m.textarea.View())
}
