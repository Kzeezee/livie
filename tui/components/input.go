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
	textarea  textarea.Model
	mode      InputMode
	disabled  bool
	width     int

	// cached values — recomputed only when content changes, not on every frame
	cachedValue string
	cachedIsCmd bool
	valueDirty  bool // true until cachedValue is rebuilt
}

// NewInputModel creates a new InputModel.
func NewInputModel(width int) InputModel {
	ta := textarea.New()
	ta.Placeholder = "Type a message... (/help for commands)"
	ta.ShowLineNumbers = false
	// Do NOT set MaxHeight — that would shrink the internal wrap-memoization
	// cache to 6 entries on the first Update() call, causing SHA-256 cache
	// misses and rewraps on every frame for all lines beyond index 6.
	// Height is managed entirely by autoGrow().
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
	m.valueDirty = true
	m.autoGrow()
}

// SetMode updates the current mode and adjusts the placeholder text.
func (m *InputModel) SetMode(mode InputMode) {
	m.mode = mode
	if mode == ModeBash {
		m.textarea.Placeholder = "$ bash command…"
	} else {
		m.textarea.Placeholder = "Type a message... (/help for commands)"
	}
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

// rebuildCache recomputes the cached Value and IsCommand results.
// Called lazily on first use after any content mutation.
func (m *InputModel) rebuildCache() {
	m.cachedValue = m.textarea.Value()
	m.cachedIsCmd = strings.HasPrefix(strings.TrimSpace(m.cachedValue), "/")
	m.valueDirty = false
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
func (m *InputModel) Value() string {
	if m.valueDirty {
		m.rebuildCache()
	}
	return m.cachedValue
}

// LinesAbove returns the number of input lines scrolled above the visible
// textarea window — used to render the "↑ N more" indicator on the divider.
func (m InputModel) LinesAbove() int {
	return m.textarea.ScrollYOffset()
}

// Reset clears the input field.
func (m *InputModel) Reset() {
	m.textarea.Reset()
	m.valueDirty = true
	m.autoGrow()
}

// InsertNewline inserts a newline at the current cursor position.
func (m *InputModel) InsertNewline() {
	m.textarea.InsertString("\n")
	m.valueDirty = true
	m.autoGrow()
}

// autoGrow adjusts the textarea's rendered height to match its current line
// count, clamped between InputMinLines and InputMaxLines.
//
// SetHeight is called unconditionally (even when height is unchanged) because
// it internally calls repositionView(), which updates the viewport's YOffset.
// Without this, ScrollYOffset() — used by the "↑ N more" divider indicator —
// only refreshes when a blink tick fires textarea.Update() (~300 ms latency).
func (m *InputModel) autoGrow() {
	desired := m.textarea.LineCount()
	if desired < InputMinLines {
		desired = InputMinLines
	}
	if desired > InputMaxLines {
		desired = InputMaxLines
	}
	m.textarea.SetHeight(desired)
}

// IsCommand returns true when the input starts with /.
func (m *InputModel) IsCommand() bool {
	if m.valueDirty {
		m.rebuildCache()
	}
	return m.cachedIsCmd
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
	m.valueDirty = true
	m.autoGrow()
	return m, cmd
}

// View renders the borderless input area.
// The glyph on the left indicates mode; the textarea fills the rest of the line.
func (m *InputModel) View() string {
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
