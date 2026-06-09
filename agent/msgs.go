package agent

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

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
	FinalDelta  string // any content accumulated in the last batch before EOF
	Usage       UsageSnapshot
}

// StreamErrMsg fires when the stream fails at any point.
type StreamErrMsg struct {
	Err error
}

// StreamToolCallMsg fires when the model requests a tool call
// (finish_reason = tool_calls). The TUI either auto-dispatches or prompts
// the user for confirmation depending on cfg.ConfirmToolCalls.
type StreamToolCallMsg struct {
	ID         string // tool_call_id from the API
	Name       string
	Args       string // raw JSON arguments string
	FinalDelta string // any content accumulated in the last batch before the tool call
}

// ToolResultMsg is returned by agent.DispatchToolCmd after a tool executes.
type ToolResultMsg struct {
	ID      string
	Name    string
	Args    string        // raw JSON (for display)
	Result  string        // string result injected into context
	Elapsed time.Duration
	Err     error         // non-nil = execution error (result still injected)
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
