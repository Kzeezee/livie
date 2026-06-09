package agent

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/config"
	"github.com/kez/livie/skills"
	"github.com/kez/livie/skills/core"
	"github.com/kez/livie/skills/livieself"
	openai "github.com/sashabaranov/go-openai"
)

// pendingToolCall accumulates streamed tool-call fields across multiple chunks.
// The OpenAI API streams id/name/arguments across many deltas; we must
// concatenate them before dispatching.
type pendingToolCall struct {
	id   string
	name string
	args strings.Builder
}

// SkillInfo is a name/description pair returned by SkillList.
type SkillInfo struct {
	Name        string
	Description string
}

// Agent drives the LLM request–response cycle and owns conversation history.
type Agent struct {
	cfg    *config.Config
	conv   *Conversation
	tools  *ToolDispatcher
	loader *skills.SkillLoader
	cwd    string // working directory fixed at launch

	// Active stream state — non-nil only during a streaming response.
	activeStream *openai.ChatCompletionStream
	streamBuf    strings.Builder
	lastUsage    *openai.Usage // populated when the final chunk includes usage

	// Tool call accumulator — cleared after each tool_calls finish_reason.
	pendingTool pendingToolCall
}

// New creates a new Agent from the given config.
// It constructs the skill loader, registers built-in and external skills,
// and builds the initial system prompt from the vault + skill descriptions.
func New(cfg *config.Config) *Agent {
	cwd, err := os.Getwd()
	if err != nil {
		cwd = "." // fallback — should never fail in practice
	}

	loader := skills.NewLoader(cfg.Paths.Skills, cwd)
	loader.RegisterBuiltin(core.New(cwd))
	loader.RegisterBuiltin(livieself.New())
	_ = loader.DiscoverExternal() // non-fatal — missing dir is fine

	d := NewToolDispatcher()
	loader.LoadAll(d)

	sysprompt := buildSystemPrompt(cfg, loader)
	maxTok := contextLimit(cfg)

	return &Agent{
		cfg:    cfg,
		conv:   NewConversation(sysprompt, maxTok),
		tools:  d,
		loader: loader,
		cwd:    cwd,
	}
}

// ── Skill accessors ───────────────────────────────────────────────────────────

// SkillCount returns the number of currently loaded skills.
func (a *Agent) SkillCount() int { return a.loader.Count() }

// SkillList returns a name/description pair for every loaded skill.
func (a *Agent) SkillList() []SkillInfo {
	loaded := a.loader.Skills()
	out := make([]SkillInfo, len(loaded))
	for i, s := range loaded {
		out[i] = SkillInfo{Name: s.Name(), Description: s.Description()}
	}
	return out
}

// InstallSkill copies srcPath into cfg.Paths.Skills/<basename>, then
// rediscovers external skills and reloads tools onto the dispatcher.
// The system prompt is updated so subsequent conversations include the
// new skill's SKILL.md body.
func (a *Agent) InstallSkill(srcPath string) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return fmt.Errorf("source path not found: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("source must be a directory")
	}

	if err := os.MkdirAll(a.cfg.Paths.Skills, 0o755); err != nil {
		return fmt.Errorf("create skills directory: %w", err)
	}

	destName := filepath.Base(srcPath)
	destPath := filepath.Join(a.cfg.Paths.Skills, destName)
	if err := copyDir(srcPath, destPath); err != nil {
		return fmt.Errorf("copy skill: %w", err)
	}

	// Re-discover external skills (addSkill deduplicates by name).
	_ = a.loader.DiscoverExternal()
	// Register any newly discovered tools (idempotent via overwrite).
	a.loader.LoadAll(a.tools)

	// Rebuild the system prompt to include the new skill's SKILL.md.
	a.conv.UpdateSystemPrompt(buildSystemPrompt(a.cfg, a.loader))

	return nil
}

// ── Helpers ───────────────────────────────────────────────────────────────────

// copyDir recursively copies src into dst, creating directories as needed.
func copyDir(src, dst string) error {
	return filepath.Walk(src, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		dstPath := filepath.Join(dst, rel)
		if info.IsDir() {
			return os.MkdirAll(dstPath, info.Mode())
		}
		return copyFile(path, dstPath)
	})
}

// copyFile copies a single file from src to dst.
func copyFile(src, dst string) error {
	in, err := os.Open(src)
	if err != nil {
		return err
	}
	defer in.Close()

	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}

	out, err := os.Create(dst)
	if err != nil {
		return err
	}
	defer out.Close()

	_, err = io.Copy(out, in)
	return err
}

// resetPendingTool clears the tool call accumulator after each dispatch.
func (a *Agent) resetPendingTool() {
	a.pendingTool.id = ""
	a.pendingTool.name = ""
	a.pendingTool.args.Reset()
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

// PollCmd drains the active stream for up to 16 ms before returning to the
// event loop, batching multiple chunks into a single StreamChunkMsg. This
// keeps renders near 60 fps regardless of how fast the model streams tokens,
// instead of paying a full glamour re-render cost on every individual chunk.
//
// Terminal states (EOF, tool_calls, error) are returned immediately.
// FinalDelta carries any content accumulated in the same batch window as a
// terminal state so the TUI can flush it before finalising the stream.
func (a *Agent) PollCmd() tea.Cmd {
	return func() tea.Msg {
		const batchWindow = 16 * time.Millisecond
		deadline := time.Now().Add(batchWindow)
		var delta strings.Builder

		for {
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
				return StreamDoneMsg{
					FullContent: content,
					FinalDelta:  delta.String(),
					Usage:       usage,
				}
			}
			if err != nil {
				a.closeStream()
				return StreamErrMsg{Err: fmt.Errorf("read stream: %w", err)}
			}

			if len(resp.Choices) == 0 {
				// Keep-alive chunk — count toward the deadline but don't break early.
				if time.Now().After(deadline) {
					break
				}
				continue
			}

			choice := resp.Choices[0]

			// Accumulate tool call fields streamed across multiple chunks.
			for _, tc := range choice.Delta.ToolCalls {
				if tc.ID != "" {
					a.pendingTool.id = tc.ID
				}
				if tc.Function.Name != "" {
					a.pendingTool.name = tc.Function.Name
				}
				a.pendingTool.args.WriteString(tc.Function.Arguments)
			}

			if choice.FinishReason == openai.FinishReasonToolCalls {
				id := a.pendingTool.id
				name := a.pendingTool.name
				args := a.pendingTool.args.String()
				a.conv.AddToolCall(id, name, args)
				a.closeStream()
				a.resetPendingTool()
				return StreamToolCallMsg{
					ID:         id,
					Name:       name,
					Args:       args,
					FinalDelta: delta.String(),
				}
			}

			if d := choice.Delta.Content; d != "" {
				delta.WriteString(d)
				a.streamBuf.WriteString(d)
			}

			if time.Now().After(deadline) {
				break
			}
		}

		return StreamChunkMsg{Delta: delta.String()}
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

// DispatchToolCmd executes the named tool and returns ToolResultMsg.
// Runs in a Bubbletea goroutine — safe to call concurrently.
func (a *Agent) DispatchToolCmd(id, name, args string) tea.Cmd {
	return func() tea.Msg {
		start := time.Now()
		result, err := a.tools.Dispatch(name, args)
		elapsed := time.Since(start)
		if err != nil {
			result = fmt.Sprintf("error: %s", err)
		}
		return ToolResultMsg{
			ID:      id,
			Name:    name,
			Args:    args,
			Result:  result,
			Elapsed: elapsed,
			Err:     err,
		}
	}
}

// ContinueAfterToolCmd injects the tool result into conversation history
// and restarts the stream. Mirrors StreamCmd's truncation handling.
func (a *Agent) ContinueAfterToolCmd(id, result string) tea.Cmd {
	return func() tea.Msg {
		a.conv.AddToolResult(id, result)
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
		return startCmd()
	}
}

// RejectToolCmd injects a rejection notice and restarts the stream so the
// model can respond knowing the tool was declined.
func (a *Agent) RejectToolCmd(id string) tea.Cmd {
	return a.ContinueAfterToolCmd(id, "tool call rejected by user")
}
