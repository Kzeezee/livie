package agent

import tea "charm.land/bubbletea/v2"

// StreamStartMsg fires once when the HTTP connection is established and the
// stream begins. Signals the TUI to open a streaming message slot.
type StreamStartMsg struct{}

// StreamChunkMsg carries a single content delta from the model.
// ChatModel re-issues PollCmd on every receipt.
type StreamChunkMsg struct {
	Delta string
}

// StreamDoneMsg fires when the stream closes cleanly (finish_reason = stop).
type StreamDoneMsg struct {
	FullContent string
	Usage       UsageSnapshot
}

// StreamErrMsg fires when the stream fails at any point.
type StreamErrMsg struct {
	Err error
}

// StreamToolCallMsg fires when the model requests a tool call
// (finish_reason = tool_calls). Phase 6: no tools are executed —
// ChatModel renders this as an informational system message.
type StreamToolCallMsg struct {
	Name string
	Args string // raw JSON arguments string
}

// ContextTruncatedMsg fires before the stream starts when the conversation
// history had to be trimmed to fit within the context window.
// It carries a Next() cmd — ChatModel fires it after displaying the warning.
type ContextTruncatedMsg struct {
	MessagesDropped int
	EstPct          int     // estimated fill % before truncation
	next            tea.Cmd // unexported: the pending streamStartCmd
}

// Next returns the tea.Cmd that begins the actual stream request.
// ChatModel calls this after displaying the truncation warning.
func (m ContextTruncatedMsg) Next() tea.Cmd { return m.next }

// UsageSnapshot holds token counts from the API's final stream chunk.
type UsageSnapshot struct {
	PromptTokens     int
	CompletionTokens int
	TotalTokens      int
}
