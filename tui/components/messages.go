package components

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/viewport"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/glamour"
	"charm.land/lipgloss/v2"
	tui "github.com/kez/livie/tui"
)

// MsgType classifies a message for rendering.
type MsgType int

const (
	MsgUser MsgType = iota
	MsgAssistant
	MsgSystem
	MsgError
	MsgCommand
	msgRaw // pre-rendered block (e.g. welcome screen)
	// Future: MsgToolCall, MsgStreaming, MsgImage
)

// Message is a single entry in the conversation history.
type Message struct {
	Type      MsgType
	Content   string
	Timestamp time.Time
}

// NewMessage creates a message with the current timestamp.
func NewMessage(t MsgType, content string) Message {
	return Message{Type: t, Content: content, Timestamp: time.Now()}
}

// nudgeStyle is created once and reused for the "↓ new messages" overlay.
var nudgeStyle = lipgloss.NewStyle().Align(lipgloss.Right)

// MessagesModel manages the scrollable message history.
type MessagesModel struct {
	viewport     viewport.Model
	messages     []Message
	renderer     *glamour.TermRenderer
	width        int
	height       int
	atBottom     bool
	newWhileAway bool // new messages arrived while scrolled up
}

// NewMessagesModel creates a new MessagesModel.
func NewMessagesModel(width, height int) MessagesModel {
	vp := viewport.New(viewport.WithWidth(width), viewport.WithHeight(height))
	vp.SetContent("")
	// Disable the viewport's built-in key bindings — all scrolling is
	// controlled programmatically to avoid key conflicts with the input.
	vp.KeyMap = viewport.KeyMap{
		PageDown:     key.NewBinding(key.WithKeys()),
		PageUp:       key.NewBinding(key.WithKeys()),
		HalfPageUp:   key.NewBinding(key.WithKeys()),
		HalfPageDown: key.NewBinding(key.WithKeys()),
		Down:         key.NewBinding(key.WithKeys()),
		Up:           key.NewBinding(key.WithKeys()),
	}

	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-4),
	)

	return MessagesModel{
		viewport: vp,
		width:    width,
		height:   height,
		renderer: r,
		atBottom: true,
	}
}

// SetSize updates the viewport dimensions.
// When only height changes the rendered content is unchanged, so we skip the
// expensive glamour-renderer rebuild and full message re-render.
func (m *MessagesModel) SetSize(width, height int) {
	widthChanged := width != m.width
	m.width = width
	m.height = height
	m.viewport.SetWidth(width)
	m.viewport.SetHeight(height)
	if widthChanged {
		if m.renderer != nil {
			m.renderer, _ = glamour.NewTermRenderer(
				glamour.WithStandardStyle("dark"),
				glamour.WithWordWrap(width-4),
			)
		}
		m.refresh()
	}
}

// AddRaw appends a pre-rendered string block directly to the viewport content
// without wrapping it in a message struct. Used for the welcome block.
func (m *MessagesModel) AddRaw(content string) {
	m.messages = append(m.messages, Message{
		Type:    msgRaw,
		Content: content,
	})
	m.refresh()
}

// AddMessage appends a message and refreshes the viewport.
func (m *MessagesModel) AddMessage(msg Message) {
	m.messages = append(m.messages, msg)
	wasAtBottom := m.atBottom
	m.refresh()
	if wasAtBottom {
		m.viewport.GotoBottom()
		m.atBottom = true
	} else {
		m.newWhileAway = true
	}
}

// Init implements tea.Model.
func (m MessagesModel) Init() tea.Cmd { return nil }

// Update handles scroll events.
func (m MessagesModel) Update(msg tea.Msg) (MessagesModel, tea.Cmd) {
	var cmd tea.Cmd

	prevOffset := m.viewport.YOffset()
	m.viewport, cmd = m.viewport.Update(msg)

	// Track whether we're at the bottom
	atBottom := m.viewport.AtBottom()
	if atBottom {
		m.atBottom = true
		m.newWhileAway = false
	} else if m.viewport.YOffset() != prevOffset {
		m.atBottom = false
	}

	return m, cmd
}

// GotoBottom scrolls to the bottom.
func (m *MessagesModel) GotoBottom() {
	m.viewport.GotoBottom()
	m.atBottom = true
	m.newWhileAway = false
}

// LinesAbove returns the number of content lines scrolled above the current
// viewport top — used to render the "↑ N more" indicator on the divider.
func (m MessagesModel) LinesAbove() int {
	return m.viewport.YOffset()
}

// Width returns the current viewport width.
func (m MessagesModel) Width() int { return m.width }

// Height returns the current viewport height.
func (m MessagesModel) Height() int { return m.height }

// GotoTop scrolls to the top.
func (m *MessagesModel) GotoTop() {
	m.viewport.GotoTop()
	m.atBottom = false
}

// ScrollUp scrolls up by half the viewport height.
func (m *MessagesModel) ScrollUp() {
	m.viewport.HalfPageUp()
	m.atBottom = false
}

// ScrollDown scrolls down by half the viewport height.
func (m *MessagesModel) ScrollDown() {
	m.viewport.HalfPageDown()
	m.atBottom = m.viewport.AtBottom()
	if m.atBottom {
		m.newWhileAway = false
	}
}

// View renders the message viewport.
func (m MessagesModel) View() string {
	view := m.viewport.View()

	// Show "↓ new messages" nudge when scrolled up and new content arrived
	if m.newWhileAway && !m.atBottom {
		nudge := tui.StyleAccentAmber.Render("↓ new messages")
		nudgeLine := nudgeStyle.Width(m.width).Render(nudge + "  ")
		// Overlay at bottom of viewport area — just append as a suffix line
		view = view + "\n" + nudgeLine
	}

	return view
}

// refresh re-renders all messages into the viewport.
func (m *MessagesModel) refresh() {
	var sb strings.Builder
	for i, msg := range m.messages {
		if i > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(m.renderMessage(msg))
	}
	m.viewport.SetContent(sb.String())
}

func (m *MessagesModel) renderMessage(msg Message) string {
	ts := tui.StyleDim.Render(msg.Timestamp.Format("15:04"))
	sep := tui.StyleDivider.Render(" │ ")

	switch msg.Type {
	case MsgUser:
		prefix := tui.StyleMsgUser.Render("▶ you")
		header := prefix + tui.StyleDim.Render("  "+msg.Timestamp.Format("15:04"))
		body := wrapText(msg.Content, m.width-4)
		return header + "\n" + tui.StyleLabel.Render("  "+body) + "\n"

	case MsgAssistant:
		prefix := tui.StyleMsgAssistant.Render("◆ livie")
		header := prefix + tui.StyleDim.Render("  "+msg.Timestamp.Format("15:04"))
		body := m.renderMarkdown(msg.Content)
		return header + "\n" + body + "\n"

	case MsgSystem:
		_ = ts
		_ = sep
		line := tui.StyleMsgSystem.Render("  — " + msg.Content + " —")
		return line + "\n"

	case MsgError:
		prefix := tui.StyleMsgError.Render("✕ error")
		header := prefix + tui.StyleDim.Render("  "+msg.Timestamp.Format("15:04"))
		body := tui.StyleAccentRose.Render("  " + msg.Content)
		return header + "\n" + body + "\n"

	case MsgCommand:
		prefix := tui.StyleCommand.Render("⌘")
		return prefix + " " + tui.StyleAccentPurple.Render(msg.Content) + "\n"

	case msgRaw:
		return msg.Content

	default:
		return msg.Content + "\n"
	}
}

func (m *MessagesModel) renderMarkdown(content string) string {
	if m.renderer == nil {
		return "  " + content
	}
	rendered, err := m.renderer.Render(content)
	if err != nil {
		return "  " + content
	}
	// Indent slightly
	lines := strings.Split(strings.TrimRight(rendered, "\n"), "\n")
	for i, l := range lines {
		lines[i] = "  " + l
	}
	return strings.Join(lines, "\n")
}

func centred(s string, width int) string {
	vis := lipgloss.Width(s)
	if vis >= width {
		return s
	}
	pad := (width - vis) / 2
	return strings.Repeat(" ", pad) + s
}

func wrapText(s string, width int) string {
	if width <= 0 {
		return s
	}
	// Split on explicit newlines first so multi-line input is preserved.
	paragraphs := strings.Split(s, "\n")
	var result []string
	for _, para := range paragraphs {
		words := strings.Fields(para)
		if len(words) == 0 {
			result = append(result, "")
			continue
		}
		var lines []string
		line := words[0]
		for _, w := range words[1:] {
			if len(line)+1+len(w) <= width {
				line += " " + w
			} else {
				lines = append(lines, line)
				line = w
			}
		}
		lines = append(lines, line)
		result = append(result, strings.Join(lines, "\n  "))
	}
	return strings.Join(result, "\n  ")
}



// Ensure fmt is used
var _ = fmt.Sprintf
