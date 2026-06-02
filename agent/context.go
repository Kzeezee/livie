package agent

import (
	openai "github.com/sashabaranov/go-openai"
)

const defaultMaxTokens = 128_000
const truncationWarnPct = 90

// truncatedWarning is an internal helper returned from BuildMessages when
// context was trimmed. Callers within the agent package wrap it into a
// ContextTruncatedMsg for the TUI.
type truncatedWarning struct {
	MessagesDropped int
	EstPct          int
}

// Conversation holds the in-memory message history and produces the
// []openai.ChatCompletionMessage slice for each API request.
type Conversation struct {
	systemPrompt string
	history      []openai.ChatCompletionMessage
	maxTokens    int
}

// NewConversation creates a fresh Conversation.
// maxTokens = 0 uses defaultMaxTokens.
func NewConversation(systemPrompt string, maxTokens int) *Conversation {
	if maxTokens <= 0 {
		maxTokens = defaultMaxTokens
	}
	return &Conversation{
		systemPrompt: systemPrompt,
		maxTokens:    maxTokens,
	}
}

// AddUser appends a user message to history.
// Called in StreamCmd before the API request is made.
func (c *Conversation) AddUser(content string) {
	c.history = append(c.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleUser,
		Content: content,
	})
}

// AddAssistant appends a completed assistant response to history.
// Called when StreamDoneMsg is received with the full accumulated content.
func (c *Conversation) AddAssistant(content string) {
	c.history = append(c.history, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleAssistant,
		Content: content,
	})
}

// BuildMessages returns the API message slice and, when truncation was needed,
// a *truncatedWarning (otherwise nil).
//
// The system prompt is always retained as messages[0].
// Truncation removes the oldest user+assistant pairs (always in pairs to
// preserve conversational coherence) until the estimated token count drops
// below truncationWarnPct % of maxTokens.
func (c *Conversation) BuildMessages() ([]openai.ChatCompletionMessage, *truncatedWarning) {
	threshold := c.maxTokens * truncationWarnPct / 100

	sysTokens := estimateTokens(c.systemPrompt)
	total := sysTokens
	for _, m := range c.history {
		total += estimateTokens(m.Content)
	}

	if total <= threshold {
		msgs := make([]openai.ChatCompletionMessage, 0, len(c.history)+1)
		msgs = append(msgs, openai.ChatCompletionMessage{
			Role:    openai.ChatMessageRoleSystem,
			Content: c.systemPrompt,
		})
		msgs = append(msgs, c.history...)
		return msgs, nil
	}

	// Need to truncate. Drop oldest user+assistant pairs until below threshold.
	dropped := 0
	hist := make([]openai.ChatCompletionMessage, len(c.history))
	copy(hist, c.history)

	for total > threshold && len(hist) >= 2 {
		// Drop the oldest pair
		pair := hist[:2]
		total -= estimateTokens(pair[0].Content) + estimateTokens(pair[1].Content)
		hist = hist[2:]
		dropped += 2
	}

	// Compute the pre-truncation percentage
	origTotal := sysTokens
	for _, m := range c.history {
		origTotal += estimateTokens(m.Content)
	}
	estPct := origTotal * 100 / c.maxTokens

	msgs := make([]openai.ChatCompletionMessage, 0, len(hist)+1)
	msgs = append(msgs, openai.ChatCompletionMessage{
		Role:    openai.ChatMessageRoleSystem,
		Content: c.systemPrompt,
	})
	msgs = append(msgs, hist...)

	return msgs, &truncatedWarning{
		MessagesDropped: dropped,
		EstPct:          estPct,
	}
}

// Reset clears history (retains system prompt). Called by /new.
func (c *Conversation) Reset() {
	c.history = nil
}

// LoadHistory replaces the history wholesale. Used when resuming a session.
func (c *Conversation) LoadHistory(msgs []openai.ChatCompletionMessage) {
	c.history = make([]openai.ChatCompletionMessage, len(msgs))
	copy(c.history, msgs)
}

// History returns a copy of the current history slice.
func (c *Conversation) History() []openai.ChatCompletionMessage {
	out := make([]openai.ChatCompletionMessage, len(c.history))
	copy(out, c.history)
	return out
}

// Len returns the number of messages in history (not counting the system prompt).
func (c *Conversation) Len() int {
	return len(c.history)
}

// estimateTokens returns a rough token count: 1 token ≈ 4 chars.
// This is a deliberate approximation — a real tokenizer would add a CGo
// dependency. Erring slightly conservative means the 90% warning fires
// slightly early, which is the safer failure mode.
func estimateTokens(s string) int {
	return len(s) / 4
}
