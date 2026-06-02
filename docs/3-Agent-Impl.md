# Livie — Phase 6, 7 & 8 Implementation Plan

> **Predecessor:** `docs/2-RunnerInfra-Plan.md` — runner infrastructure, HUD wiring, and command implementations are complete.
> **This document covers:** The AI agent package, session persistence, and TUI wiring for live LLM communication.

---

## Table of Contents

1. [Current State & What This Phase Adds](#1-current-state--what-this-phase-adds)
2. [New Package Map](#2-new-package-map)
3. [Dependency Addition](#3-dependency-addition)
4. [Phase 6 — Agent Package](#4-phase-6--agent-package)
5. [Phase 7 — Session Persistence](#5-phase-7--session-persistence)
6. [Phase 8 — TUI Wiring](#6-phase-8--tui-wiring)
7. [New Message Catalogue](#7-new-message-catalogue)
8. [File Change Map](#8-file-change-map)
9. [Decisions & Rationale](#9-decisions--rationale)

---

## 1. Current State & What This Phase Adds

### Current State

The runner is fully built and wired into the TUI. The chat screen accepts input but has no AI backend — every non-command message produces `"AI backend not yet connected"`. All endpoint configuration exists in config but nothing makes HTTP requests. Tokens, model name, and endpoint name in the HUD are static defaults.

### What This Phase Adds

| Area | Description |
|------|-------------|
| `agent/` package | OpenAI-compatible HTTP client, streaming, context building, tool call scaffold |
| `session/` package | JSON session persistence, auto-save on reply and on quit, list/load |
| Streaming TUI | Live chunk-by-chunk rendering in `MessagesModel` with a blinking cursor |
| `ChatModel` wiring | Agent connected, all stream messages handled, sessions auto-saved |
| `/resume` picker | Navigable session list overlay, loads prior conversations into agent context |
| HUD live data | Model name, endpoint name, and token counts populated from live data |
| Config addition | `context_size` field on `EndpointConfig` for remote model context limits |

### Not Done After This Phase

| Area | Notes |
|------|-------|
| Bash execution mode | Mode toggle exists; no execution logic yet |
| Tool execution | Dispatcher scaffold built; no tools registered |
| Skills system | `/skills` remains a stub |
| Memory & vault | `/memory` remains a stub; `personality.md` not yet written |
| Media indexing & RAG | `/index` remains a stub |

---

## 2. New Package Map

```
livie/
├── agent/
│   ├── agent.go           # Agent struct, StreamCmd, PollCmd, the main chat loop
│   ├── client.go          # go-openai client factory, endpoint adapter
│   ├── context.go         # Conversation, BuildMessages, context truncation
│   ├── msgs.go            # Bubbletea message types for streaming
│   ├── system_prompt.go   # Load system prompt from file, hardcoded fallback
│   └── tools.go           # Tool, ToolDispatcher, scaffold dispatch
├── session/
│   ├── session.go         # Session, Message, Summary types
│   └── store.go           # Save, Load, ListSummaries, tea.Cmd wrappers
└── tui/
    └── components/
        └── session_picker.go  # Navigable session list overlay component
```

---

## 3. Dependency Addition

Add to `go.mod`:

```
github.com/sashabaranov/go-openai v1.x
```

This is the de-facto standard Go client for OpenAI-compatible APIs. It provides:

- `openai.NewClientWithConfig` — accepts a custom `BaseURL` and `APIKey`, making it compatible with any OpenAI-compatible endpoint (llama-server, Groq, Together, Ollama, OpenAI, etc.)
- `client.CreateChatCompletionStream` — server-sent events streaming
- `openai.ChatCompletionMessage`, `openai.Tool` — standard request/response types

No other HTTP client or AI library is added.

---

## 4. Phase 6 — Agent Package

### 4.1 `agent/msgs.go`

All Bubbletea messages produced by the agent layer. Every one is consumed in `ChatModel.Update`.

```go
package agent

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
    FullContent string       // complete accumulated response text
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
    EstPct          int      // estimated fill % before truncation
    next            tea.Cmd  // unexported: the pending streamStartCmd
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
```

---

### 4.2 `agent/client.go`

Builds an `*openai.Client` from an `EndpointConfig`. Called on every stream invocation so the active endpoint is always reflected — no caching needed.

```go
package agent

import (
    "github.com/kez/livie/config"
    openai "github.com/sashabaranov/go-openai"
)

// newClient constructs an openai.Client for the given endpoint.
//
// The go-openai library sends "Authorization: Bearer <key>" on every request.
// When APIKey is empty (e.g. local llama-server), the bearer value is empty —
// llama-server and most local servers accept this without complaint.
func newClient(ep config.EndpointConfig) *openai.Client {
    oc := openai.DefaultConfig(ep.APIKey)
    if ep.BaseURL != "" {
        oc.BaseURL = ep.BaseURL
    }
    return openai.NewClientWithConfig(oc)
}

// modelName returns the model string to send in the request.
// Falls back to "default" when EndpointConfig.Model is empty so the request
// remains valid on servers that ignore the model field (e.g. llama-server).
func modelName(ep config.EndpointConfig) string {
    if ep.Model != "" {
        return ep.Model
    }
    return "default"
}
```

---

### 4.3 `agent/context.go`

`Conversation` holds the in-memory message history and produces the `[]openai.ChatCompletionMessage` slice for each API request. It also handles context-window truncation.

#### `Conversation` struct

```go
type Conversation struct {
    systemPrompt string
    history      []openai.ChatCompletionMessage
    maxTokens    int
}

const defaultMaxTokens = 128_000
const truncationWarnPct = 90
```

#### Constructor

```go
// NewConversation creates a fresh Conversation.
// maxTokens = 0 uses defaultMaxTokens.
func NewConversation(systemPrompt string, maxTokens int) *Conversation
```

#### Mutation methods

```go
// AddUser appends a user message to history.
// Called in StreamCmd before the API request is made.
func (c *Conversation) AddUser(content string)

// AddAssistant appends a completed assistant response to history.
// Called when StreamDoneMsg is received with the full accumulated content.
func (c *Conversation) AddAssistant(content string)
```

#### `BuildMessages`

Returns the messages to send and an optional truncation notice.

```go
// BuildMessages returns the API message slice and, when truncation was needed,
// a *ContextTruncatedMsg (otherwise nil).
//
// The system prompt is always retained as messages[0].
// Truncation removes the oldest user+assistant pairs (always in pairs to
// preserve conversational coherence) until the estimated token count drops
// below truncationWarnPct % of maxTokens.
func (c *Conversation) BuildMessages() ([]openai.ChatCompletionMessage, *ContextTruncatedWarning)
```

`ContextTruncatedWarning` is a private helper type used only inside the `agent` package — it carries the truncation metadata that `StreamCmd` wraps into a `ContextTruncatedMsg`.

**Token estimation:** `estimateTokens(s string) int` returns `len(s) / 4`. This is a deliberate approximation — see D6 in §9.

**Truncation algorithm:**
1. Compute total estimated tokens: system prompt + all history messages
2. Threshold = `c.maxTokens * truncationWarnPct / 100`
3. If total ≤ threshold: return messages unchanged, nil
4. Walk the history from oldest to newest, dropping user+assistant pairs (2 messages at a time) until total ≤ threshold
5. Return the trimmed slice and `&ContextTruncatedWarning{MessagesDropped: N, EstPct: totalPct}`

#### Other methods

```go
// Reset clears history (retains system prompt). Called by /new.
func (c *Conversation) Reset()

// LoadHistory replaces the history wholesale. Used when resuming a session.
func (c *Conversation) LoadHistory(msgs []openai.ChatCompletionMessage)

// History returns a copy of the current history slice.
// Used when building a session snapshot for persistence.
func (c *Conversation) History() []openai.ChatCompletionMessage

// Len returns the number of messages in history (not counting the system prompt).
func (c *Conversation) Len() int
```

---

### 4.4 `agent/system_prompt.go`

```go
package agent

const defaultSystemPrompt = `You are Livie, a terminal-native AI assistant.
You are direct, technically precise, and helpful. You run inside the user's
terminal and have access to their working directory and tools.
Respond concisely unless detail is explicitly requested.`

// LoadSystemPrompt reads the system prompt from path.
// If the file does not exist or cannot be read, defaultSystemPrompt is
// returned silently. This makes the function forward-compatible with the
// future personality.md vault file without requiring it to exist yet.
func LoadSystemPrompt(path string) string
```

The path passed in is `filepath.Join(cfg.Paths.Vault, "system_prompt.md")`. For this phase the file will not exist on most installations, so `defaultSystemPrompt` is always used — but the hook is in place for when the vault is built.

---

### 4.5 `agent/tools.go`

Scaffold for the tool dispatch system. No tools are registered in Phase 6.

```go
package agent

import (
    "encoding/json"
    "errors"
    openai "github.com/sashabaranov/go-openai"
)

// Tool describes a single callable tool exposed to the model.
type Tool struct {
    Name        string
    Description string
    // Parameters is a JSON Schema object. Example:
    // {"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}
    Parameters json.RawMessage
    Handler    func(args string) (string, error)
}

// ToolDispatcher holds registered tools and dispatches calls by name.
type ToolDispatcher struct {
    tools map[string]*Tool
}

func NewToolDispatcher() *ToolDispatcher

// Register adds a tool. A duplicate name silently overwrites the prior entry.
func (d *ToolDispatcher) Register(t *Tool)

// Definitions returns the []openai.Tool slice for inclusion in API requests.
// Returns nil (not an empty slice) when no tools are registered, which causes
// the go-openai library to omit the "tools" field from the request entirely —
// models that receive no tools don't attempt tool calls.
func (d *ToolDispatcher) Definitions() []openai.Tool

// Dispatch invokes the named tool with the raw JSON args string.
// Returns ErrToolNotFound when the name is not registered.
// Phase 6: always returns ErrToolNotFound — no tools are registered.
func (d *ToolDispatcher) Dispatch(name, args string) (string, error)

// ErrToolNotFound is returned by Dispatch when the tool name is not registered.
var ErrToolNotFound = errors.New("tool not found")
```

---

### 4.6 `agent/agent.go`

The `Agent` struct and the cmd chain that drives the full request–response cycle.

#### Struct

```go
type Agent struct {
    cfg          *config.Config
    conv         *Conversation
    tools        *ToolDispatcher

    // Active stream state — non-nil only during a streaming response.
    activeStream *openai.ChatCompletionStream
    streamBuf    strings.Builder
    lastUsage    *openai.Usage // populated when the final chunk includes usage
}
```

#### Constructor

```go
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
```

#### `Conversation()` accessor

```go
// Conversation returns the agent's Conversation for external manipulation
// (Reset on /new, LoadHistory on /resume).
func (a *Agent) Conversation() *Conversation { return a.conv }
```

#### `StreamCmd`

The primary entry point. Adds the user message to context, builds the API message list, and returns either a `ContextTruncatedMsg` (which carries the pending stream start as `Next()`) or fires the stream start directly.

```go
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
```

#### `streamStartCmd`

Opens the HTTP connection, stores the stream on the agent, and returns `StreamStartMsg` on success.

```go
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
```

#### `PollCmd`

Reads the next chunk from the active stream. `ChatModel` re-issues `PollCmd` on every `StreamChunkMsg`, creating a perpetual polling loop that terminates on `StreamDoneMsg`, `StreamToolCallMsg`, or `StreamErrMsg`.

```go
// PollCmd reads the next chunk from the active stream.
// Must only be called when activeStream != nil (i.e. after StreamStartMsg).
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
```

#### Internal helpers

```go
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
```

---

## 5. Phase 7 — Session Persistence

### 5.1 `session/session.go`

```go
package session

import "time"

type Role string

const (
    RoleSystem    Role = "system"
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
)

// Message is one entry in the persisted conversation history.
type Message struct {
    Role      Role      `json:"role"`
    Content   string    `json:"content"`
    Timestamp time.Time `json:"timestamp"`
}

// Session is the complete persisted state of one conversation.
type Session struct {
    ID           string    `json:"id"`           // "2026-06-01T14-32-05"
    CreatedAt    time.Time `json:"created_at"`
    UpdatedAt    time.Time `json:"updated_at"`
    EndpointName string    `json:"endpoint_name"`
    ModelName    string    `json:"model_name"`
    Messages     []Message `json:"messages"`
    TokensUsed   int       `json:"tokens_used"`
    Preview      string    `json:"preview"`      // first user message, ≤72 chars; set at save time
}

// Summary is a lightweight view of a session used by the /resume picker.
// Built from a Session's top-level fields — the full Messages slice is not
// included, so list rendering doesn't require reading message bodies.
type Summary struct {
    ID           string
    CreatedAt    time.Time
    UpdatedAt    time.Time
    MessageCount int
    Preview      string // from Session.Preview
    ModelName    string
    EndpointName string
}
```

**Session ID format:** `time.Now().Format("2006-01-02T15-04-05")` — the timestamp of the first user message in the session. This is set in `ChatModel` when the first non-command message is submitted, before `StreamCmd` is called.

---

### 5.2 `session/store.go`

```go
package session

import tea "charm.land/bubbletea/v2"

// Dir returns the sessions directory path: ~/.local/share/livie/sessions/
// Creates the directory if it does not exist.
func Dir() (string, error)

// Save writes s to disk atomically (temp file → rename).
// Sets s.Preview from the first RoleUser message (≤72 chars) before writing.
// Creates the sessions directory if needed.
func Save(s *Session) error

// Load reads and deserialises the session with the given ID.
func Load(id string) (*Session, error)

// ListSummaries reads all *.json files in Dir(), builds a Summary for each,
// and returns the list sorted newest-first by UpdatedAt.
// Files that fail to parse are silently skipped.
func ListSummaries() ([]Summary, error)

// ── tea.Cmd wrappers ──────────────────────────────────────────────────────

// SummariesLoadedMsg is returned by ListSummariesCmd.
type SummariesLoadedMsg struct {
    Summaries []Summary
    Err       error
}

// SessionLoadedMsg is returned by LoadCmd.
type SessionLoadedMsg struct {
    Session *Session
    Err     error
}

// ListSummariesCmd wraps ListSummaries as a tea.Cmd.
func ListSummariesCmd() tea.Cmd {
    return func() tea.Msg {
        summaries, err := ListSummaries()
        return SummariesLoadedMsg{Summaries: summaries, Err: err}
    }
}

// LoadCmd wraps Load as a tea.Cmd.
func LoadCmd(id string) tea.Cmd {
    return func() tea.Msg {
        s, err := Load(id)
        return SessionLoadedMsg{Session: s, Err: err}
    }
}
```

**`Save` — preview generation:**

```go
// Inside Save, before encoding:
for _, m := range s.Messages {
    if m.Role == RoleUser && m.Content != "" {
        preview := m.Content
        if len(preview) > 72 {
            preview = preview[:69] + "…"
        }
        s.Preview = preview
        break
    }
}
```

**`ListSummaries` — summary extraction:**

Each file is fully decoded (it is small), then a `Summary` is built from the top-level fields:

```go
sum := Summary{
    ID:           s.ID,
    CreatedAt:    s.CreatedAt,
    UpdatedAt:    s.UpdatedAt,
    MessageCount: len(s.Messages),
    Preview:      s.Preview,
    ModelName:    s.ModelName,
    EndpointName: s.EndpointName,
}
```

---

### 5.3 Auto-save trigger points

`ChatModel` carries two new fields:

```go
sessionID        string
sessionCreatedAt time.Time
```

`sessionID` is set to `time.Now().Format("2006-01-02T15-04-05")` at the moment the first user message is submitted (in `handleSubmit`, before calling `agent.StreamCmd`). It remains set for the lifetime of the chat screen or until `/new` resets it to `""`.

**Trigger 1 — after every assistant reply** (`StreamDoneMsg` handler):

```go
case agent.StreamDoneMsg:
    m.messages.FinalizeStream()
    m.hud.TokensUsed = msg.Usage.TotalTokens
    m.syncHUDState()
    m.saveSession()          // ← always called; no-op when sessionID == ""
    m.messages.GotoBottom()
```

**Trigger 2 — on quit** (second `ctrl+c` in `handleKey`):

```go
// Second ctrl+c within 500ms:
m.saveSession()              // ← synchronous file write before tea.Quit
return true, tea.Quit
```

The session save is synchronous (blocking). Writing a JSON file is fast enough that the brief pause before the terminal closes is imperceptible.

**`saveSession` implementation:**

```go
func (m *ChatModel) saveSession() {
    s := m.buildSessionSnapshot()
    if s == nil {
        return
    }
    _ = session.Save(s) // best-effort; errors are silently dropped
}

func (m *ChatModel) buildSessionSnapshot() *session.Session {
    if m.sessionID == "" {
        return nil
    }
    hist := m.agent.Conversation().History()
    if len(hist) == 0 {
        return nil
    }
    ep := m.cfg.ActiveEndpoint()
    msgs := make([]session.Message, 0, len(hist))
    for _, h := range hist {
        msgs = append(msgs, session.Message{
            Role:      session.Role(h.Role),
            Content:   h.Content,
            Timestamp: time.Now(), // best-effort; no per-message timestamps in openai type
        })
    }
    return &session.Session{
        ID:           m.sessionID,
        CreatedAt:    m.sessionCreatedAt,
        UpdatedAt:    time.Now(),
        EndpointName: ep.Name,
        ModelName:    ep.Model,
        Messages:     msgs,
        TokensUsed:   m.hud.TokensUsed,
    }
}
```

---

### 5.4 `/resume` picker component — `tui/components/session_picker.go`

An overlay component rendered above the input, following the same pattern as `AutocompleteModel`.

```go
package components

import "github.com/kez/livie/session"

// SessionPickerModel renders a navigable list of recent sessions as an
// overlay above the input bar. Visible after ActionOpenResume fires and
// SummariesLoadedMsg is received.
type SessionPickerModel struct {
    summaries []session.Summary
    cursor    int
    width     int
    visible   bool
    loading   bool  // true while ListSummariesCmd is in flight
}

func NewSessionPickerModel(width int) SessionPickerModel

// SetSummaries populates the list and makes the picker visible.
// Resets cursor to 0.
func (m *SessionPickerModel) SetSummaries(summaries []session.Summary)

// SetLoading sets the loading state (shown while ListSummariesCmd is in flight).
func (m *SessionPickerModel) SetLoading(v bool)

func (m SessionPickerModel) IsVisible() bool
func (m SessionPickerModel) IsLoading() bool
func (m *SessionPickerModel) MoveUp()
func (m *SessionPickerModel) MoveDown()
func (m SessionPickerModel) Selected() *session.Summary  // nil if empty
func (m *SessionPickerModel) Dismiss()
func (m *SessionPickerModel) SetWidth(w int)
func (m SessionPickerModel) Height() int   // for viewport calculation
func (m SessionPickerModel) View() string
```

**Rendering:** Up to 8 sessions are shown in a bordered panel. Each row:

```
  ▶ 2026-06-01  14:32   gpt-4o @ openai      "How do I set up Rust cross-compilation…"
    2026-05-31  09:14   llama-server @ local  "Explain the borrow checker in detail for…"
```

Column widths: date (10) + time (5) + model@endpoint (22) + preview (remaining, clipped).

Active row uses `ColAccentCyan` prefix `▶`; inactive rows use `ColTextMuted` prefix `·`.

Loading state renders a single row: `  ⠙  loading sessions…` (using the same braille spinner tick as setup).

Empty state renders: `  no sessions found`.

The panel has a top border labelled `── sessions ─────…` and a bottom hint row: `enter to load  ·  esc to dismiss`.

`Height()` returns `min(len(summaries), 8) + 3` (1 top border/label + rows + 1 hint row + 1 padding). When `loading`, returns `4`.

---

## 6. Phase 8 — TUI Wiring

### 6.1 `tui/components/messages.go` — Streaming support

Three new methods are added to `MessagesModel` and `refresh()` is extended.

#### New fields

```go
type MessagesModel struct {
    // existing fields unchanged…

    streaming       bool
    streamContent   strings.Builder
    streamStartTime time.Time
}
```

#### `StartStreaming()`

```go
// StartStreaming opens an in-progress streaming slot rendered at the bottom
// of the viewport. Must be called on StreamStartMsg.
func (m *MessagesModel) StartStreaming() {
    m.streaming = true
    m.streamContent.Reset()
    m.streamStartTime = time.Now()
    m.refresh()
    m.viewport.GotoBottom()
    m.atBottom = true
}
```

#### `AppendStream(delta string)`

```go
// AppendStream appends a content delta and refreshes the viewport.
// Called on every StreamChunkMsg.
func (m *MessagesModel) AppendStream(delta string) {
    m.streamContent.WriteString(delta)
    m.refresh()
    if m.atBottom {
        m.viewport.GotoBottom()
    }
}
```

#### `FinalizeStream() string`

```go
// FinalizeStream closes the streaming slot, converts the accumulated content
// to a permanent MsgAssistant entry, and returns the full content string.
// Called on StreamDoneMsg, StreamErrMsg, and StreamToolCallMsg.
func (m *MessagesModel) FinalizeStream() string {
    content := m.streamContent.String()
    m.streaming = false
    m.streamContent.Reset()
    if content != "" {
        m.messages = append(m.messages, Message{
            Type:      MsgAssistant,
            Content:   content,
            Timestamp: m.streamStartTime,
        })
    }
    m.refresh()
    m.viewport.GotoBottom()
    m.atBottom = true
    return content
}
```

#### `refresh()` update

Append the streaming block after all complete messages when `m.streaming` is true:

```go
func (m *MessagesModel) refresh() {
    var sb strings.Builder
    for i, msg := range m.messages {
        if i > 0 {
            sb.WriteString("\n")
        }
        sb.WriteString(m.renderMessage(msg))
    }
    if m.streaming {
        if len(m.messages) > 0 {
            sb.WriteString("\n")
        }
        sb.WriteString(m.renderStreamingBlock())
    }
    m.viewport.SetContent(sb.String())
}
```

#### `renderStreamingBlock()`

```go
func (m *MessagesModel) renderStreamingBlock() string {
    prefix := tui.StyleMsgAssistant.Render("◆ livie")
    ts := tui.StyleDim.Render("  " + m.streamStartTime.Format("15:04"))
    header := prefix + ts

    content := m.streamContent.String()
    cursor := tui.StyleAccentCyan.Render("▌")

    var body string
    if content == "" {
        // Nothing yet — show just the cursor
        body = "  " + cursor
    } else {
        // Append cursor inside content before passing to glamour so it
        // sits at the correct position within formatted output.
        body = m.renderMarkdown(content + "▌")
    }
    return header + "\n" + body + "\n"
}
```

The cursor character `▌` is appended directly to `content` before `renderMarkdown` so it renders inline with the text — after code blocks, list items, etc. — rather than as a floating overlay.

Also add `MsgStreaming` to the `MsgType` constants (reserved for future use, not rendered via `renderMessage`):

```go
const (
    MsgUser      MsgType = iota
    MsgAssistant
    MsgSystem
    MsgError
    MsgCommand
    MsgStreaming  // reserved; streaming is handled by the separate streaming fields
    msgRaw
)
```

---

### 6.2 `tui/screens/chat.go` — Agent wiring

#### New imports

```go
import (
    "github.com/kez/livie/agent"
    "github.com/kez/livie/session"
    openai "github.com/sashabaranov/go-openai"
)
```

#### New `ChatModel` fields

```go
type ChatModel struct {
    // existing fields unchanged…

    agent        *agent.Agent
    resumePicker components.SessionPickerModel

    sessionID        string
    sessionCreatedAt time.Time
}
```

#### `NewChatModel` signature

```go
// Before:
func NewChatModel(cfg *config.Config, mgr *runner.Manager, width, height int) ChatModel

// After:
func NewChatModel(cfg *config.Config, mgr *runner.Manager, agt *agent.Agent, width, height int) ChatModel
```

Inside `NewChatModel`:
- Set `m.agent = agt`
- Set `m.resumePicker = components.NewSessionPickerModel(width)`
- Call `m.syncHUDState()` (renamed — see §6.4)

#### `handleSubmit` update

```go
func (m *ChatModel) handleSubmit() tea.Cmd {
    text := strings.TrimSpace(m.input.Value())
    if text == "" {
        return nil
    }
    m.input.Reset()

    if strings.HasPrefix(text, "/") {
        m.messages.AddMessage(components.NewMessage(components.MsgCommand, text))
        response, action := m.registry.Dispatch(text)
        return func() tea.Msg {
            return tui.CommandActionMsg{Response: response, Action: action}
        }
    }

    // Set session identity on first user message.
    if m.sessionID == "" {
        m.sessionID = time.Now().Format("2006-01-02T15-04-05")
        m.sessionCreatedAt = time.Now()
    }

    m.messages.AddMessage(components.NewMessage(components.MsgUser, text))
    m.messages.GotoBottom()
    return m.agent.StreamCmd(text)  // replaces the stub
}
```

#### New message handlers in `Update`

```go
case agent.ContextTruncatedMsg:
    m.messages.AddMessage(components.NewMessage(
        components.MsgSystem,
        fmt.Sprintf("context window ~%d%% full — %d older messages trimmed",
            msg.EstPct, msg.MessagesDropped),
    ))
    m.messages.GotoBottom()
    return m, msg.Next() // fires the pending streamStartCmd

case agent.StreamStartMsg:
    m.messages.StartStreaming()
    return m, m.agent.PollCmd()

case agent.StreamChunkMsg:
    m.messages.AppendStream(msg.Delta)
    return m, m.agent.PollCmd()

case agent.StreamDoneMsg:
    m.messages.FinalizeStream()
    m.hud.TokensUsed = msg.Usage.TotalTokens
    m.syncHUDState()
    m.saveSession()
    m.messages.GotoBottom()

case agent.StreamErrMsg:
    m.messages.FinalizeStream() // close slot cleanly even on error
    m.messages.AddMessage(components.NewMessage(
        components.MsgError,
        fmt.Sprintf("request failed: %s", msg.Err),
    ))
    m.messages.GotoBottom()

case agent.StreamToolCallMsg:
    m.messages.FinalizeStream()
    m.messages.AddMessage(components.NewMessage(
        components.MsgSystem,
        fmt.Sprintf("[tool call: %s(%s)] — tool execution coming in a future update",
            msg.Name, msg.Args),
    ))
    m.messages.GotoBottom()

case session.SummariesLoadedMsg:
    if msg.Err != nil {
        m.messages.AddMessage(components.NewMessage(components.MsgError,
            fmt.Sprintf("failed to list sessions: %s", msg.Err)))
        return m, nil
    }
    if len(msg.Summaries) == 0 {
        m.messages.AddMessage(components.NewMessage(components.MsgSystem,
            "no previous sessions found"))
        return m, nil
    }
    m.resumePicker.SetLoading(false)
    m.resumePicker.SetSummaries(msg.Summaries)

case session.SessionLoadedMsg:
    if msg.Err != nil {
        m.messages.AddMessage(components.NewMessage(components.MsgError,
            fmt.Sprintf("failed to load session: %s", msg.Err)))
        return m, nil
    }
    m.loadSession(msg.Session)
```

#### `handleAction` additions and updates

```go
case tui.ActionNew:
    m.agent.Conversation().Reset()     // NEW: also reset agent context
    m.sessionID = ""
    m.sessionCreatedAt = time.Time{}
    vpH := viewportH(m.height, m.input.Height(), 0)
    m.messages = components.NewMessagesModel(m.width, vpH)
    m.showWelcome()

case tui.ActionOpenResume:
    m.resumePicker = components.NewSessionPickerModel(m.width)
    m.resumePicker.SetLoading(true)
    return session.ListSummariesCmd()

case tui.ActionResumeSession:
    if sel := m.resumePicker.Selected(); sel != nil {
        m.resumePicker.Dismiss()
        return session.LoadCmd(sel.ID)
    }
```

#### Session picker key handling

Added to `handleKey`, before the autocomplete block (the resume picker takes priority):

```go
if m.resumePicker.IsVisible() {
    switch {
    case key.Matches(msg, m.keys.AutocompleteDown):
        m.resumePicker.MoveDown()
        return true, nil
    case key.Matches(msg, m.keys.AutocompleteUp):
        m.resumePicker.MoveUp()
        return true, nil
    case key.Matches(msg, m.keys.AutocompleteAccept):
        return true, func() tea.Msg {
            return tui.CommandActionMsg{Action: tui.ActionResumeSession}
        }
    case key.Matches(msg, m.keys.Escape):
        m.resumePicker.Dismiss()
        return true, nil
    }
}
```

#### `loadSession`

```go
func (m *ChatModel) loadSession(s *session.Session) {
    // Convert session messages to openai format.
    history := make([]openai.ChatCompletionMessage, 0, len(s.Messages))
    for _, sm := range s.Messages {
        history = append(history, openai.ChatCompletionMessage{
            Role:    string(sm.Role),
            Content: sm.Content,
        })
    }
    m.agent.Conversation().LoadHistory(history)

    // Restore session identity so subsequent saves append to the same file.
    m.sessionID = s.ID
    m.sessionCreatedAt = s.CreatedAt

    // Rebuild TUI message list from session history.
    vpH := viewportH(m.height, m.input.Height(), 0)
    m.messages = components.NewMessagesModel(m.width, vpH)
    m.showWelcome()
    for _, sm := range s.Messages {
        var t components.MsgType
        switch sm.Role {
        case session.RoleUser:
            t = components.MsgUser
        case session.RoleAssistant:
            t = components.MsgAssistant
        default:
            t = components.MsgSystem
        }
        m.messages.AddMessage(components.NewMessage(t, sm.Content))
    }
    m.messages.GotoBottom()
    m.messages.AddMessage(components.NewMessage(
        components.MsgSystem,
        fmt.Sprintf("session resumed · %s · %d messages", s.ID, len(s.Messages)),
    ))
}
```

#### `View()` update

The resume picker is shown as an overlay above the input, in place of the autocomplete drop-down (only one overlay is visible at a time):

```go
parts := []string{
    m.messages.View(),
    topDivider,
    m.input.View(),
}
if m.resumePicker.IsVisible() {
    parts = append(parts, m.resumePicker.View())
} else if m.autocomplete.IsVisible() {
    parts = append(parts, m.autocomplete.View())
}
parts = append(parts, bottomDivider, hud)
```

`syncInputHeight` is updated to account for the resume picker height when visible:

```go
func (m *ChatModel) syncInputHeight() {
    m.autocomplete.SetInput(m.input.Value(), m.registry)
    overlayH := m.autocomplete.Height()
    if m.resumePicker.IsVisible() {
        overlayH = m.resumePicker.Height()
    }
    newH := viewportH(m.height, m.input.Height(), overlayH)
    if m.width != m.messages.Width() || newH != m.messages.Height() {
        m.messages.SetSize(m.width, newH)
    }
}
```

---

### 6.3 HUD live data — `syncHUDState()`

`syncHUDRunnerState()` is renamed to `syncHUDState()` and extended to populate all live HUD fields. All existing call sites update to the new name.

```go
func (m *ChatModel) syncHUDState() {
    ep := m.cfg.ActiveEndpoint()

    // ── Model name ────────────────────────────────────────────────────────
    if m.cfg.Endpoint.Active == "local" {
        m.hud.ModelName = m.cfg.ModelName() // filepath.Base(cfg.Runner.ModelPath)
    } else {
        m.hud.ModelName = ep.Model
        if m.hud.ModelName == "" {
            m.hud.ModelName = "(no model)"
        }
    }

    // ── Endpoint name ─────────────────────────────────────────────────────
    m.hud.EndpointName = m.cfg.Endpoint.Active

    // ── Context window max ────────────────────────────────────────────────
    if ep.ContextSize > 0 {
        m.hud.TokensMax = ep.ContextSize
    } else if m.cfg.Endpoint.Active == "local" {
        m.hud.TokensMax = m.cfg.Runner.ContextSize
    } else {
        m.hud.TokensMax = 0  // unknown; HUD renders "— / — tok"
    }

    // ── Runner chip (existing logic, moved here unchanged) ─────────────────
    if m.runner == nil {
        m.hud.RunnerStatus = components.RunnerStatusNone
        m.hud.RunnerLabel = ""
        return
    }
    switch m.runner.State() {
    case runner.StateUnconfigured, runner.StateReady:
        m.hud.RunnerStatus = components.RunnerStatusStopped
        m.hud.RunnerLabel = "stopped"
    case runner.StateStarting:
        m.hud.RunnerStatus = components.RunnerStatusStarting
        m.hud.RunnerLabel = "starting"
    case runner.StateRunning:
        m.hud.RunnerStatus = components.RunnerStatusRunning
        m.hud.RunnerLabel = "llama-server"
    case runner.StateStopped:
        m.hud.RunnerStatus = components.RunnerStatusStopped
        m.hud.RunnerLabel = "stopped"
    case runner.StateError:
        m.hud.RunnerStatus = components.RunnerStatusError
        m.hud.RunnerLabel = "error"
    }
    if m.cfg.Endpoint.Active != "local" {
        m.hud.RunnerStatus = components.RunnerStatusNone
        m.hud.RunnerLabel = ""
    }
}
```

`TokensUsed` is updated separately in the `StreamDoneMsg` handler (not in `syncHUDState`) because it comes from the API's usage report, not from config.

---

### 6.4 `config/config.go` — `ContextSize` on `EndpointConfig`

```go
type EndpointConfig struct {
    Name        string `toml:"name"`
    BaseURL     string `toml:"base_url"`
    APIKey      string `toml:"api_key"`
    Model       string `toml:"model"`
    ContextSize int    `toml:"context_size"` // NEW: 0 = unknown; agent uses defaultMaxTokens
}
```

`DefaultConfig()` leaves `ContextSize: 0` on the default `local` endpoint — the agent falls back to `cfg.Runner.ContextSize` for local. The setup wizard's `saveRemoteConfig()` does not set `ContextSize`; users configure it manually in `config.toml` when working with models that have a known limit.

---

### 6.5 `app/app.go`

#### New field

```go
type Model struct {
    current screen
    setup   screens.SetupModel
    chat    screens.ChatModel
    cfg     *config.Config
    runner  *runner.Manager
    agent   *agent.Agent   // NEW
    width   int
    height  int
}
```

#### `New` signature update

```go
// Before:
func New(cfg *config.Config, mgr *runner.Manager) Model

// After:
func New(cfg *config.Config, mgr *runner.Manager, agt *agent.Agent) Model
```

Both `NewChatModel` call sites (in `New` and in the `TransitionToChat` handler) are updated to pass `agt`:

```go
chat: screens.NewChatModel(cfg, mgr, agt, w, h),
// and in TransitionToChat:
m.chat = screens.NewChatModel(m.cfg, m.runner, m.agent, m.width, m.height)
```

---

### 6.6 `main.go`

```go
cfg, err := config.Load(config.DefaultPath())
// … error handling …
mgr := runner.NewManager(cfg.Runner)
agt := agent.New(cfg)             // NEW
model := app.New(cfg, mgr, agt)   // updated signature
```

---

### 6.7 `tui/commands.go` — new actions and `/resume`

#### New `AppAction` constants

```go
const (
    // existing…
    ActionOpenResume    // handled by ChatModel — fires session.ListSummariesCmd
    ActionResumeSession // handled by ChatModel — loads the selected session
)
```

#### Updated `/resume`

Replace the stub with:

```go
r.Register(&Command{
    Name:        "resume",
    Description: "Resume a previous conversation",
    Handler: func(args []string) (string, AppAction) {
        return "", ActionOpenResume
    },
})
```

---

## 7. New Message Catalogue

Extends the catalogue from `docs/2-RunnerInfra-Plan.md §4`.

| Message | Package | Produced by | Consumed in |
|---------|---------|-------------|-------------|
| `StreamStartMsg` | `agent` | `agent.streamStartCmd` | `ChatModel.Update` → `messages.StartStreaming()`, fires `PollCmd` |
| `StreamChunkMsg` | `agent` | `agent.PollCmd` (content delta) | `ChatModel.Update` → `messages.AppendStream()`, re-fires `PollCmd` |
| `StreamDoneMsg` | `agent` | `agent.PollCmd` (on `io.EOF`) | `ChatModel.Update` → `messages.FinalizeStream()`, HUD token update, session save |
| `StreamErrMsg` | `agent` | `agent.streamStartCmd` or `agent.PollCmd` (on error) | `ChatModel.Update` → `messages.FinalizeStream()`, error message |
| `StreamToolCallMsg` | `agent` | `agent.PollCmd` (on `finish_reason=tool_calls`) | `ChatModel.Update` → `messages.FinalizeStream()`, scaffold system message |
| `ContextTruncatedMsg` | `agent` | `agent.StreamCmd` (when truncation needed) | `ChatModel.Update` → system message, fires `msg.Next()` |
| `SummariesLoadedMsg` | `session` | `session.ListSummariesCmd` | `ChatModel.Update` → `resumePicker.SetSummaries()` |
| `SessionLoadedMsg` | `session` | `session.LoadCmd` | `ChatModel.Update` → `loadSession()` |

---

## 8. File Change Map

### Phase 6 — New files

| File | Contents |
|------|---------|
| `agent/msgs.go` | `StreamStartMsg`, `StreamChunkMsg`, `StreamDoneMsg`, `StreamErrMsg`, `StreamToolCallMsg`, `ContextTruncatedMsg` (with `Next()`), `UsageSnapshot` |
| `agent/client.go` | `newClient(ep)`, `modelName(ep)` |
| `agent/context.go` | `Conversation` struct + all methods, `estimateTokens`, `ContextTruncatedWarning` (private) |
| `agent/system_prompt.go` | `LoadSystemPrompt(path)`, `defaultSystemPrompt` const |
| `agent/tools.go` | `Tool`, `ToolDispatcher`, `NewToolDispatcher`, `Register`, `Definitions`, `Dispatch`, `ErrToolNotFound` |
| `agent/agent.go` | `Agent` struct, `New`, `contextLimit`, `StreamCmd`, `streamStartCmd`, `PollCmd`, `closeStream`, `snapshotUsage`, `extractToolCall`, `Conversation()` accessor |

### Phase 7 — New files

| File | Contents |
|------|---------|
| `session/session.go` | `Role`, `Message`, `Session`, `Summary` types |
| `session/store.go` | `Dir`, `Save`, `Load`, `ListSummaries`, `ListSummariesCmd`, `LoadCmd`, `SummariesLoadedMsg`, `SessionLoadedMsg` |
| `tui/components/session_picker.go` | `SessionPickerModel` and all methods |

### Phase 8 — Modified files

| File | Change |
|------|--------|
| `go.mod` / `go.sum` | Add `github.com/sashabaranov/go-openai` |
| `config/config.go` | Add `ContextSize int` to `EndpointConfig` |
| `tui/components/messages.go` | Add `streaming`, `streamContent`, `streamStartTime` fields; add `StartStreaming`, `AppendStream`, `FinalizeStream`; update `refresh`; add `renderStreamingBlock`; add `MsgStreaming` constant |
| `tui/screens/chat.go` | Add `agent`, `resumePicker`, `sessionID`, `sessionCreatedAt` fields; update `NewChatModel` signature; update `handleSubmit`; add all stream + session message handlers in `Update`; extend `handleAction` for `ActionNew`, `ActionOpenResume`, `ActionResumeSession`; add picker key handling to `handleKey`; update `View` and `syncInputHeight`; rename `syncHUDRunnerState` → `syncHUDState` with full extension; add `saveSession`, `buildSessionSnapshot`, `loadSession` |
| `tui/commands.go` | Add `ActionOpenResume`, `ActionResumeSession`; replace `/resume` stub |
| `app/app.go` | Add `agent *agent.Agent` field; update `New` signature and both `NewChatModel` call sites |
| `main.go` | Add `agent.New(cfg)`; update `app.New(...)` call |

---

## 9. Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | `github.com/sashabaranov/go-openai` as the HTTP client | Most widely-used Go OpenAI client. Supports custom `BaseURL` natively — identical code path for local llama-server and remote APIs. Avoids hand-rolling SSE parsing and error handling. |
| D2 | `*openai.Client` created per `StreamCmd` invocation, not cached on `Agent` | The active endpoint can change between requests via `/endpoint`. Recreating the client (a cheap struct wrapping an `http.Client`) is simpler than explicit invalidation logic. No measurable overhead on a per-request basis. |
| D3 | Streaming polled via `PollCmd` re-issued on every `StreamChunkMsg` | A single blocking goroutine that streams the full response before returning would freeze the Bubbletea runtime for the entire generation time. The poll pattern keeps the runtime fully responsive — each tick is a separate non-blocking cmd — and follows established Bubbletea convention (same pattern as `hudTickCmd`). |
| D4 | `activeStream` stored on `Agent` struct | `PollCmd` is issued by `ChatModel` but needs to call `stream.Recv()` on the same connection opened by `streamStartCmd`. Storing the stream on `Agent` is the simplest handoff: `streamStartCmd` sets it, `PollCmd` reads it, `closeStream` clears it. |
| D5 | `ContextTruncatedMsg.next` carries the pending stream cmd (unexported); exposed via `Next()` | A `tea.Cmd` returns exactly one message. Carrying the pre-built message slice and pending stream cmd inside the warning message lets `ChatModel` fire the actual stream start as a follow-up without re-building the context. The unexported field keeps the package boundary clean — callers only use `Next()`. |
| D6 | Token estimation: `len(s) / 4` | A real tokenizer (tiktoken, etc.) would require a CGo dependency or an external subprocess. The 4 chars-per-token heuristic is a well-known approximation; it errs conservative for English prose, meaning the 90% warning fires slightly early. The safer failure mode — warning too soon — is preferable to silently overflowing. |
| D7 | Cursor `▌` appended to content before `renderMarkdown`, not overlaid | Appending inside the glamour pass ensures the cursor appears at the correct position within formatted output — after code fences, inside list items, etc. A separate overlay cursor would be misaligned whenever the markdown renderer adds structural whitespace. |
| D8 | Session files are full JSON; no separate summary index | At typical usage (tens to low hundreds of sessions), reading all files for `ListSummaries` is fast (< 10 ms). A secondary index would add write complexity and a consistency surface. The `Preview` field stored at save time avoids re-scanning message bodies during list rendering. |
| D9 | Session ID = timestamp string `"2006-01-02T15-04-05"` | Human-readable, lexicographically sortable, and file-system-safe. Collision probability is negligible. UUIDs are opaque; timestamp IDs are useful when browsing the sessions directory directly. |
| D10 | Resume picker reuses `AutocompleteDown/Up/Accept` key bindings | Consistency — the same keys navigate both overlays. Only one overlay is ever visible at a time, preventing conflicts. New keybindings would increase cognitive load. |
| D11 | Tool call scaffold displays a system message; does not crash or return an error | Future phases will register actual tools. A visible `[tool call: name(args)]` message confirms the tool-calling code path is exercised end-to-end and gives the user transparent feedback. Silent failure would obscure whether the model attempted a tool call. |
| D12 | `syncHUDRunnerState` renamed to `syncHUDState` | The function now manages all HUD fields, not just runner state. The rename prevents future confusion and makes the call sites self-documenting. |
| D13 | `TokensUsed` updated only on `StreamDoneMsg`, not incrementally during streaming | The API reports authoritative usage only in the final chunk. Incrementally estimating `len(delta)/4` during streaming would diverge from the real count and update the HUD on every chunk (causing visual noise). One accurate update on completion is cleaner. |
| D14 | `ActionNew` resets the `Conversation` in addition to the TUI | The original `/new` only cleared the visible message list. If the agent context was not also reset, the next message would carry the full prior history to the API — invisible to the user but consuming tokens and potentially confusing the model. |
| D15 | `loadSession` rebuilds `MessagesModel` from scratch | Inserting history into an existing model risks stale viewport state and scroll offsets. A fresh `MessagesModel` is the clean approach, consistent with how `/new` already works. |
