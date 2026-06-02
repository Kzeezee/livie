package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/config"
	openai "github.com/sashabaranov/go-openai"
)

// Agent drives the LLM request–response cycle and owns conversation history.
type Agent struct {
	cfg   *config.Config
	conv  *Conversation
	tools *ToolDispatcher

	// Active stream state — non-nil only during a streaming response.
	activeStream *openai.ChatCompletionStream
	streamBuf    strings.Builder
	lastUsage    *openai.Usage // populated when the final chunk includes usage
}

// New creates a new Agent from the given config.
func New(cfg *config.Config) *Agent {
	sysprompt := LoadSystemPrompt(
		filepath.Join(cfg.Paths.Vault, "system_prompt.md"),
	)
	maxTok := contextLimit(cfg)
	return &Agent{
		cfg:   cfg,
		conv:  NewConversation(sysprompt, maxTok),
		tools: NewToolDispatcher(),
	}
}

// contextLimit resolves the effective context window size from config.
// Priority: active endpoint's ContextSize → local runner ContextSize → 0 (defaultMaxTokens).
func contextLimit(cfg *config.Config) int {
	ep := cfg.ActiveEndpoint()
	if ep.ContextSize > 0 {
		return ep.ContextSize
	}
	if cfg.Endpoint.Active == "local" && cfg.Runner.ContextSize > 0 {
		return cfg.Runner.ContextSize
	}
	return 0
}

// Conversation returns the agent's Conversation for external manipulation
// (Reset on /new, LoadHistory on /resume).
func (a *Agent) Conversation() *Conversation { return a.conv }

// StreamCmd is the primary entry point. Adds the user message to context,
// builds the API message list, and returns either a ContextTruncatedMsg
// (which carries the pending stream start as Next()) or fires the stream
// start directly.
func (a *Agent) StreamCmd(userInput string) tea.Cmd {
	return func() tea.Msg {
		a.conv.AddUser(userInput)

		msgs, truncated := a.conv.BuildMessages()

		ep := a.cfg.ActiveEndpoint()
		startCmd := a.streamStartCmd(msgs, ep)

		if truncated != nil {
			return ContextTruncatedMsg{
				MessagesDropped: truncated.MessagesDropped,
				EstPct:          truncated.EstPct,
				next:            startCmd,
			}
		}
		// No truncation — execute the stream start immediately.
		return startCmd()
	}
}

// streamStartCmd opens the HTTP connection, stores the stream on the agent,
// and returns StreamStartMsg on success.
func (a *Agent) streamStartCmd(msgs []openai.ChatCompletionMessage, ep config.EndpointConfig) tea.Cmd {
	return func() tea.Msg {
		client := newClient(ep)

		req := openai.ChatCompletionRequest{
			Model:    modelName(ep),
			Messages: msgs,
			Stream:   true,
			StreamOptions: &openai.StreamOptions{
				IncludeUsage: true,
			},
		}
		if defs := a.tools.Definitions(); defs != nil {
			req.Tools = defs
		}

		stream, err := client.CreateChatCompletionStream(context.Background(), req)
		if err != nil {
			return StreamErrMsg{Err: fmt.Errorf("open stream: %w", err)}
		}

		a.activeStream = stream
		a.streamBuf.Reset()
		a.lastUsage = nil
		return StreamStartMsg{}
	}
}

// PollCmd reads the next chunk from the active stream.
// Must only be called when activeStream != nil (i.e. after StreamStartMsg).
// ChatModel re-issues PollCmd on every StreamChunkMsg, creating a perpetual
// polling loop that terminates on StreamDoneMsg, StreamToolCallMsg, or
// StreamErrMsg.
func (a *Agent) PollCmd() tea.Cmd {
	return func() tea.Msg {
		resp, err := a.activeStream.Recv()

		// Usage arrives in the last chunk before EOF when IncludeUsage is set.
		if resp.Usage != nil {
			a.lastUsage = resp.Usage
		}

		if errors.Is(err, io.EOF) {
			content := a.streamBuf.String()
			usage := a.snapshotUsage()
			a.closeStream()
			a.conv.AddAssistant(content)
			return StreamDoneMsg{FullContent: content, Usage: usage}
		}
		if err != nil {
			a.closeStream()
			return StreamErrMsg{Err: fmt.Errorf("read stream: %w", err)}
		}

		if len(resp.Choices) == 0 {
			// Empty chunk (e.g. keep-alive) — poll again.
			return a.PollCmd()()
		}

		choice := resp.Choices[0]

		if choice.FinishReason == openai.FinishReasonToolCalls {
			// Phase 6 scaffold: extract whatever tool name has accumulated.
			name, args := a.extractToolCall(choice)
			a.closeStream()
			return StreamToolCallMsg{Name: name, Args: args}
		}

		delta := choice.Delta.Content
		a.streamBuf.WriteString(delta)
		return StreamChunkMsg{Delta: delta}
	}
}

func (a *Agent) closeStream() {
	if a.activeStream != nil {
		a.activeStream.Close()
		a.activeStream = nil
	}
	a.streamBuf.Reset()
}

func (a *Agent) snapshotUsage() UsageSnapshot {
	if a.lastUsage == nil {
		return UsageSnapshot{}
	}
	return UsageSnapshot{
		PromptTokens:     a.lastUsage.PromptTokens,
		CompletionTokens: a.lastUsage.CompletionTokens,
		TotalTokens:      a.lastUsage.TotalTokens,
	}
}

// extractToolCall returns a best-effort name and args from the current stream
// buffer for Phase 6 display purposes. Full tool call accumulation is out of
// scope for this phase.
func (a *Agent) extractToolCall(choice openai.ChatCompletionStreamChoice) (name, args string) {
	if len(choice.Delta.ToolCalls) > 0 {
		tc := choice.Delta.ToolCalls[0]
		if tc.Function.Name != "" {
			name = tc.Function.Name
		}
		if tc.Function.Arguments != "" {
			args = tc.Function.Arguments
		}
	}
	if name == "" {
		name = "(unknown)"
	}
	if args == "" {
		args = "{}"
	}
	return
}
