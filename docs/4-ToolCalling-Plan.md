# Livie — Phase 9: Tool Calling & Built-in Tools

> **Covers:** Real multi-step agentic tool calling, 5 built-in tools (bash, read_file,
> write_file, find_files, edit_file), confirmation UX, conversation history for tool
> messages, and a forward-looking note on migrating built-ins into the skills system.

---

## Scope

**In this phase:**
- Fix the Phase 6 tool call accumulation scaffold into a real implementation
- Implement 5 built-in tools in `agent/builtins.go` — hardcoded for now
- Multi-step agentic loop: tool call → dispatch → inject result → continue stream, repeating until `finish_reason = stop`
- Inline confirmation UX: a lightweight block rendered above the input bar, requiring `y/n` before execution
- Config flag `confirm_tool_calls` (already in TOML schema) controls auto vs. manual mode
- Working directory fixed at Livie launch time (`os.Getwd()`)
- Minimal tool activity display: one dim line added to the viewport after each tool executes

**Explicitly out of scope:**
- Skills framework (SKILL.md parser, skill loader, `/skills` command) — see migration notes at end
- Bash mode is untouched — it is a user-facing direct terminal passthrough, unrelated to AI tool calling

---

## Architecture Overview

```
User sends message
       │
       ▼
agent.StreamCmd()
  └─ conv.BuildMessages()
  └─ opens SSE stream → StreamStartMsg
       │
       ▼
agent.PollCmd() loop
  ├─ StreamChunkMsg     → TUI appends delta to streaming slot
  ├─ finish_reason=stop → StreamDoneMsg → TUI finalises slot, saves session
  └─ finish_reason=tool_calls
           │
           ▼
       conv.AddToolCall(id, name, args)    ← history updated in agent
       StreamToolCallMsg{id, name, args}  ← fired to TUI
           │
           ▼
       ChatModel receives StreamToolCallMsg
         ├─ FinaliseStream() (closes streaming slot)
         ├─ sets pendingTool on model
         ├─ if cfg.ConfirmToolCalls → renders confirm block above input
         └─ if !cfg.ConfirmToolCalls → fires agent.DispatchToolCmd() immediately
           │
           ▼ (after y keypress or auto)
       agent.DispatchToolCmd(id, name, args)
         └─ ToolDispatcher.Dispatch() → runs the tool handler
         └─ returns ToolResultMsg{id, name, result, elapsed, err}
           │
           ▼
       ChatModel receives ToolResultMsg
         ├─ clears pendingTool
         ├─ adds dim tool-activity line to viewport
         └─ fires agent.ContinueAfterToolCmd(id, result)
           │
           ▼
       agent.ContinueAfterToolCmd()
         ├─ conv.AddToolResult(id, result)
         └─ reopens stream → StreamStartMsg
           │
           ▼
       Back to PollCmd() loop (tool call may repeat)
```

---

## The 5 Built-in Tools

All tools are registered in `agent/builtins.go` via `RegisterBuiltins(d *ToolDispatcher, cwd string)`.
All file paths are resolved relative to `cwd` (fixed at launch). Absolute paths are accepted as-is.

### 1. `bash`

Runs a shell command. Captures combined stdout+stderr. Hard timeout default of 30s.

```json
{
  "type": "object",
  "properties": {
    "cmd":     { "type": "string", "description": "Shell command to execute" },
    "timeout": { "type": "number", "description": "Timeout in seconds (default: 30)" }
  },
  "required": ["cmd"]
}
```

- Runs via `exec.CommandContext` with `/bin/sh -c <cmd>`
- `CombinedOutput()` — stdout and stderr merged
- CWD of the subprocess is set to `cwd`
- On timeout: process killed, returns `"error: command timed out after Ns"`
- On non-zero exit: returns output + `"\n[exit N]"` (not an error — the AI sees the output)
- Output truncated to 8,000 chars with a `"\n[... truncated]"` suffix to protect context

### 2. `read_file`

Reads a file, with optional line-range windowing. Returns file contents as a string.

```json
{
  "type": "object",
  "properties": {
    "path":   { "type": "string", "description": "File path to read" },
    "offset": { "type": "number", "description": "Line to start reading from, 1-indexed (default: 1)" },
    "limit":  { "type": "number", "description": "Maximum number of lines to return (default: 2000)" }
  },
  "required": ["path"]
}
```

- Reads the whole file, splits on `\n`, applies `offset`/`limit` window
- Returns `"[lines M-N of T]"` header when windowing is active
- File not found → error string (AI sees it)
- Binary / unreadable → error string

### 3. `write_file`

Writes content to a file. Creates the file and any missing parent directories.
Overwrites if the file already exists.

```json
{
  "type": "object",
  "properties": {
    "path":    { "type": "string", "description": "File path to write" },
    "content": { "type": "string", "description": "Content to write" }
  },
  "required": ["path", "content"]
}
```

- `os.MkdirAll` for parent dirs, then `os.WriteFile`
- Returns `"wrote N bytes to path"` on success

### 4. `find_files`

Walks a directory tree and returns paths matching a glob pattern.

```json
{
  "type": "object",
  "properties": {
    "pattern": { "type": "string", "description": "Glob pattern to match against filename (e.g. '*.go', '*.md')" },
    "dir":     { "type": "string", "description": "Directory to search (default: working directory)" }
  },
  "required": ["pattern"]
}
```

- Uses `filepath.Walk` + `filepath.Match(pattern, filepath.Base(path))`
- Pattern is matched against the **filename only** (not the full path)
- Returns newline-separated list of matching paths relative to `cwd`
- For path-based patterns (containing `/`), match is against the relative path from `dir`
- Result capped at 200 entries with a `"[... N more not shown]"` suffix
- Note: `**` (double-star recursive glob) requires the `bmatcuk/doublestar` package — not included now;
  use `bash` tool with `find` for recursive patterns in the meantime

### 5. `edit_file`

Applies one or more precise old→new text substitutions to an existing file.
Each `old_text` must appear exactly once in the file — ambiguous or absent matches are errors.

```json
{
  "type": "object",
  "properties": {
    "path": { "type": "string", "description": "File path to edit" },
    "edits": {
      "type": "array",
      "description": "List of replacements to apply",
      "items": {
        "type": "object",
        "properties": {
          "old_text": { "type": "string", "description": "Exact text to replace (must appear exactly once)" },
          "new_text": { "type": "string", "description": "Replacement text" }
        },
        "required": ["old_text", "new_text"]
      }
    }
  },
  "required": ["path", "edits"]
}
```

- Reads the file, applies edits sequentially (each against the running buffer after prior edits)
- `strings.Count(content, old_text)` must equal 1 for each edit — fails otherwise with a
  clear error: `"edit 2: old_text not unique (found 3 occurrences)"` or
  `"edit 1: old_text not found"`
- All edits validated before any write — atomically applied or none
- Returns `"applied N edits to path"`

---

## Phase Breakdown

### Phase 9A — Fix Tool Call Accumulation in `agent/agent.go`

**Problem:** The current `extractToolCall` in `agent.go` only reads the final streaming chunk.
Tool call data (ID, name, arguments) is streamed across many chunks and must be accumulated.

**Changes to `agent/agent.go`:**

Add a `pendingToolCall` accumulator to the `Agent` struct:

```go
type pendingToolCall struct {
    id   string
    name string
    args strings.Builder
}
```

Add field to `Agent`:
```go
pendingTool pendingToolCall
```

Add `resetPendingTool()` helper.

In `PollCmd()`, replace the existing `finish_reason == tool_calls` branch with proper accumulation across **all** chunks:

```go
// In the main chunk-processing section (before finish_reason check):
for _, tc := range choice.Delta.ToolCalls {
    if tc.ID != "" {
        a.pendingTool.id = tc.ID
    }
    if tc.Function.Name != "" {
        a.pendingTool.name = tc.Function.Name
    }
    a.pendingTool.args.WriteString(tc.Function.Arguments)
}

// In the finish_reason check:
if choice.FinishReason == openai.FinishReasonToolCalls {
    id   := a.pendingTool.id
    name := a.pendingTool.name
    args := a.pendingTool.args.String()
    a.conv.AddToolCall(id, name, args)   // ← Phase 9B
    a.closeStream()
    a.resetPendingTool()
    return StreamToolCallMsg{ID: id, Name: name, Args: args}
}
```

Delete `extractToolCall()` — no longer needed.

**Update `StreamToolCallMsg` in `agent/msgs.go`:**

```go
type StreamToolCallMsg struct {
    ID   string  // tool_call_id from the API
    Name string
    Args string  // raw JSON args string
}
```

Add new message types:

```go
// ToolResultMsg is returned by agent.DispatchToolCmd.
type ToolResultMsg struct {
    ID      string
    Name    string
    Args    string        // raw JSON (for display)
    Result  string        // string result to inject into context
    Elapsed time.Duration
    Err     error         // non-nil = execution error (result still injected)
}
```

---

### Phase 9B — Conversation History for Tool Messages (`agent/context.go`)

The OpenAI API requires:
1. An **assistant** message containing a `tool_calls` array (no content string)
2. A **tool** message with `role: "tool"`, `tool_call_id`, and the result string

Add two methods to `Conversation`:

```go
// AddToolCall records the assistant's tool call in history.
// Must be called before dispatching, so the API gets the correct message order.
func (c *Conversation) AddToolCall(id, name, args string) {
    c.history = append(c.history, openai.ChatCompletionMessage{
        Role: openai.ChatMessageRoleAssistant,
        ToolCalls: []openai.ToolCall{{
            ID:   id,
            Type: openai.ToolTypeFunction,
            Function: openai.FunctionCall{
                Name:      name,
                Arguments: args,
            },
        }},
    })
}

// AddToolResult records the tool's response in history.
// Called by ContinueAfterToolCmd after execution (success or error).
func (c *Conversation) AddToolResult(toolCallID, content string) {
    c.history = append(c.history, openai.ChatCompletionMessage{
        Role:       openai.ChatMessageRoleTool,
        ToolCallID: toolCallID,
        Content:    content,
    })
}
```

**Update `estimateTokens` usage in `BuildMessages`:** tool-call messages have empty `.Content`
but non-empty args. Update the per-message token estimate to also count `ToolCalls[].Function.Arguments`:

```go
func msgTokens(m openai.ChatCompletionMessage) int {
    n := estimateTokens(m.Content)
    for _, tc := range m.ToolCalls {
        n += estimateTokens(tc.Function.Arguments)
    }
    return n
}
```

Replace `estimateTokens(m.Content)` calls in `BuildMessages` with `msgTokens(m)`.

---

### Phase 9C — Built-in Tool Implementations (`agent/builtins.go`) — new file

```
agent/builtins.go
```

Package `agent`. Contains `RegisterBuiltins(d *ToolDispatcher, cwd string)` which registers
all 5 tools. Each tool is a private function returning `*Tool`.

**Bash tool implementation notes:**
- Parse args JSON into `struct { Cmd string; Timeout float64 }`
- Default timeout 30.0 if zero
- `exec.CommandContext(ctx, "/bin/sh", "-c", cmd)`
- `cmd.Dir = cwd`
- `cmd.CombinedOutput()`
- Truncate to 8000 chars
- Non-zero exit is **not** an error return — output+`[exit N]` is returned as the result

**read_file implementation notes:**
- `os.ReadFile` → split `\n` → apply offset/limit window
- Default offset=1, limit=2000
- Header line: `"[lines 1-2000 of 4821]\n"` when windowing

**write_file implementation notes:**
- Resolve path relative to cwd if not absolute
- `os.MkdirAll(filepath.Dir(path), 0o755)`
- `os.WriteFile(path, []byte(content), 0o644)`

**find_files implementation notes:**
- Resolve `dir` relative to cwd (default = cwd)
- `filepath.Walk(dir, func)` — match `filepath.Base(entry)` against `pattern` via `filepath.Match`
- Collect relative paths (relative to cwd, not dir)
- Cap at 200 results

**edit_file implementation notes:**
- Read file → validate all edits (count occurrences of each `old_text`) → bail if any fail
- Apply edits sequentially to the running buffer
- Write result back
- Return `"applied N edits to <path>"`

---

### Phase 9D — Dispatch & Continue Commands (`agent/agent.go`)

Add `cwd string` field to `Agent` (set at construction from `os.Getwd()`).

Update `agent.New()` to:
1. Call `os.Getwd()` and store it
2. Call `RegisterBuiltins(a.tools, cwd)` after `NewToolDispatcher()`

Add two new Cmd methods:

```go
// DispatchToolCmd executes the named tool and returns ToolResultMsg.
// Safe to call concurrently — ToolDispatcher.Dispatch is stateless per-tool.
func (a *Agent) DispatchToolCmd(id, name, args string) tea.Cmd {
    return func() tea.Msg {
        start := time.Now()
        result, err := a.tools.Dispatch(name, args)
        elapsed := time.Since(start)
        if err != nil {
            result = fmt.Sprintf("error: %s", err)
        }
        return ToolResultMsg{
            ID: id, Name: name, Args: args,
            Result: result, Elapsed: elapsed, Err: err,
        }
    }
}

// ContinueAfterToolCmd injects the tool result into conversation history
// and restarts the stream. Handles ContextTruncatedMsg the same way StreamCmd does.
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

// RejectToolCmd injects a rejection notice and restarts the stream
// so the model can respond without the tool result.
func (a *Agent) RejectToolCmd(id string) tea.Cmd {
    return a.ContinueAfterToolCmd(id, "tool call rejected by user")
}
```

---

### Phase 9E — New MsgType & Rendering (`tui/components/messages.go`)

Add `MsgTool` to the `MsgType` enum:

```go
const (
    MsgUser      MsgType = iota
    MsgAssistant
    MsgSystem
    MsgError
    MsgCommand
    MsgTool      // ← new: dim tool-activity line
    msgRaw
    MsgStreaming
)
```

Add a `ToolActivity` field to `Message` (used only for `MsgTool`; zero-value safe for all others):

```go
type ToolActivity struct {
    Name    string
    Args    string        // raw JSON, will be displayed truncated
    Elapsed time.Duration
    OK      bool          // false = error or rejected
    Status  string        // "✓", "✗ exit 1", "✗ rejected"
}
```

```go
type Message struct {
    Type       MsgType
    Content    string
    Timestamp  time.Time
    Tool       ToolActivity  // populated only when Type == MsgTool
}
```

Add a `NewToolMessage(a ToolActivity) Message` constructor.

Add rendering in `renderMessage` for `MsgTool`:

```go
case MsgTool:
    icon    := tui.StyleDim.Render("  ⚙")
    name    := tui.StyleMuted.Render(" " + msg.Tool.Name)
    args    := tui.StyleDim.Render("(" + truncateArgs(msg.Tool.Args, 40) + ")")
    elapsed := tui.StyleDim.Render(fmt.Sprintf("  %.1fs", msg.Tool.Elapsed.Seconds()))
    var status string
    if msg.Tool.OK {
        status = tui.StyleAccentGreen.Render("  ✓")
    } else {
        status = tui.StyleAccentRose.Render("  " + msg.Tool.Status)
    }
    return icon + name + args + elapsed + status + "\n"
```

`truncateArgs(s string, max int) string` — renders the raw JSON args string, strips outer `{}`,
truncates to `max` chars with `…` if needed.

---

### Phase 9F — Confirmation Block Component (`tui/components/tool_confirm.go`) — new file

A lightweight component following the same pattern as `SessionPickerModel`.
Rendered above the input bar (in the same slot — mutually exclusive with session picker and autocomplete).

```go
// ToolConfirmModel is rendered while a tool call awaits user approval.
type ToolConfirmModel struct {
    id      string
    name    string
    args    string   // raw JSON, truncated for display
    width   int
    visible bool
}

func NewToolConfirmModel(width int) ToolConfirmModel
func (m *ToolConfirmModel) Show(id, name, args string)
func (m *ToolConfirmModel) Dismiss()
func (m ToolConfirmModel) IsVisible() bool
func (m ToolConfirmModel) ID() string
func (m ToolConfirmModel) Height() int   // always 4 rows when visible
func (m ToolConfirmModel) View() string
```

**Rendered output** (4 rows):
```
─ ⚙ tool ──────────────────────────────────────────
  bash · rm -rf ./build/dist
  ─────────────────────────────────────────────────
  y / enter = run   ·   n / esc = reject
```

- Top border uses `tui.StyleDivider` with `⚙ tool` label (same pattern as session picker `sessions` label)
- Tool name in `tui.StyleAccentAmber`
- Args rendered via `truncateArgs(args, width-20)`
- Hint line in `tui.StyleDim`

---

### Phase 9G — Chat Model Wiring (`tui/screens/chat.go`)

**Add fields to `ChatModel`:**

```go
toolConfirm   components.ToolConfirmModel
pendingToolID string   // ID of the in-flight tool call, "" when none pending
```

**Initialise in `NewChatModel()`:**
```go
toolConfirm: components.NewToolConfirmModel(width),
```

**Handle `StreamToolCallMsg` in `Update()`:**

```go
case agent.StreamToolCallMsg:
    m.messages.FinalizeStream()
    if m.cfg.Behaviour.ConfirmToolCalls {
        m.toolConfirm.Show(msg.ID, msg.Name, msg.Args)
        m.pendingToolID = msg.ID
        m.syncInputHeight()
        return m, nil  // wait for y/n
    }
    // Auto-execute
    return m, m.agent.DispatchToolCmd(msg.ID, msg.Name, msg.Args)
```

**Handle `ToolResultMsg` in `Update()`:**

```go
case agent.ToolResultMsg:
    m.toolConfirm.Dismiss()
    m.pendingToolID = ""
    ok := msg.Err == nil
    status := "✓"
    if !ok {
        status = "✗ " + trimError(msg.Err)
    }
    m.messages.AddMessage(components.NewToolMessage(components.ToolActivity{
        Name: msg.Name, Args: msg.Args,
        Elapsed: msg.Elapsed, OK: ok, Status: status,
    }))
    m.messages.GotoBottom()
    m.syncInputHeight()
    return m, m.agent.ContinueAfterToolCmd(msg.ID, msg.Result)
```

**Key handling — add to `handleKey()` before autocomplete block:**

```go
// ── Tool confirm navigation — highest priority ────────────────────────────
if m.toolConfirm.IsVisible() {
    switch {
    case msg.String() == "y" || key.Matches(msg, m.keys.Submit):
        id := m.toolConfirm.ID()
        // peek args/name from confirm model (add Name/Args() accessors)
        m.toolConfirm.Dismiss()
        m.syncInputHeight()
        return true, m.agent.DispatchToolCmd(id, m.toolConfirm.Name(), m.toolConfirm.Args())
    case msg.String() == "n" || key.Matches(msg, m.keys.Escape):
        id := m.toolConfirm.ID()
        m.messages.AddMessage(components.NewToolMessage(components.ToolActivity{
            Name: m.toolConfirm.Name(), Args: m.toolConfirm.Args(),
            OK: false, Status: "✗ rejected",
        }))
        m.toolConfirm.Dismiss()
        m.pendingToolID = ""
        m.syncInputHeight()
        return true, m.agent.RejectToolCmd(id)
    }
}
```

**`syncInputHeight()` update:**

```go
func (m *ChatModel) syncInputHeight() {
    m.autocomplete.SetInput(m.input.Value(), m.registry)
    overlayH := m.autocomplete.Height()
    if m.resumePicker.IsVisible() {
        overlayH = m.resumePicker.Height()
    } else if m.toolConfirm.IsVisible() {   // ← new
        overlayH = m.toolConfirm.Height()
    }
    newH := viewportH(m.height, m.input.Height(), overlayH)
    if m.width != m.messages.Width() || newH != m.messages.Height() {
        m.messages.SetSize(m.width, newH)
    }
}
```

**`View()` update:** render `toolConfirm.View()` in the overlay slot (mutually exclusive with picker and autocomplete):

```go
if m.resumePicker.IsVisible() {
    parts = append(parts, m.resumePicker.View())
} else if m.toolConfirm.IsVisible() {
    parts = append(parts, m.toolConfirm.View())
} else if m.autocomplete.IsVisible() {
    parts = append(parts, m.autocomplete.View())
}
```

---

### Phase 9H — Config & Behaviour

No schema changes needed — `confirm_tool_calls` already exists in `BehaviourConfig`.

**Default value:** `confirm_tool_calls = true` is already the hardcoded default in `DefaultConfig()`.

**TOML documentation comment** (add to any generated/example config):
```toml
[behaviour]
# confirm_tool_calls: require y/n approval before each tool executes.
# Set to false to auto-approve all tool calls (use with caution).
confirm_tool_calls = true
auto_execute_bash  = false
```

---

## Confirmation UX — Full Spec

### When `confirm_tool_calls = true`

1. Model streams, finishes with `finish_reason = tool_calls`
2. `FinalizeStream()` called — streaming slot converts to assistant message (may be empty if model went straight to tool call with no preamble)
3. `ToolConfirmModel.Show()` called — confirm block appears above input bar, keyboard intercepted
4. Input bar remains visible but key input is absorbed by the confirm handler

```
 viewport content ...

 ─ ⚙ tool ──────────────────────────────────────────────────
   bash · rm -rf ./build/dist
   ────────────────────────────────────────────────────────
   y / enter = run   ·   n / esc = reject
 ──────────────────────────────────────────────────────────
 > █
```

5a. User presses `y` or `enter`:
   - Confirm block dismisses
   - `agent.DispatchToolCmd` fires (runs in Bubbletea goroutine)
   - While running: input bar is live but there's nothing blocking it (tool runs async as a Cmd)
   - `ToolResultMsg` arrives → dim tool line added → stream continues

5b. User presses `n` or `esc`:
   - Confirm block dismisses
   - Dim line added: `  ⚙ bash(rm -rf ./build/dist)  ✗ rejected`
   - `agent.RejectToolCmd` fires — injects "rejected by user" into context, restarts stream
   - Model responds knowing the tool was declined

### When `confirm_tool_calls = false`

- No confirm block rendered
- Tool dispatched immediately from the `StreamToolCallMsg` handler
- Dim activity line still added after execution

### Example minimal activity lines

```
  ⚙ read_file("main.go")  0.0s  ✓
  ⚙ bash("go build ./...")  2.3s  ✓
  ⚙ bash("rm -rf /")  0.0s  ✗ rejected
  ⚙ write_file("out.txt")  0.0s  ✗ error: permission denied
  ⚙ find_files("*.go")  0.1s  ✓
```

---

## File Map Summary

| File | Status | Change |
|------|--------|--------|
| `agent/agent.go` | Modify | Accumulator struct, fixed `PollCmd`, `DispatchToolCmd`, `ContinueAfterToolCmd`, `RejectToolCmd`, `cwd` field, `RegisterBuiltins` call |
| `agent/msgs.go` | Modify | `StreamToolCallMsg` gets `ID` field; add `ToolResultMsg` |
| `agent/context.go` | Modify | `AddToolCall`, `AddToolResult`, `msgTokens` helper |
| `agent/builtins.go` | **New** | `RegisterBuiltins` + 5 tool implementations |
| `tui/components/messages.go` | Modify | `MsgTool`, `ToolActivity`, `NewToolMessage`, render case, `truncateArgs` |
| `tui/components/tool_confirm.go` | **New** | `ToolConfirmModel` component |
| `tui/screens/chat.go` | Modify | `toolConfirm` field, `StreamToolCallMsg` handler, `ToolResultMsg` handler, key intercept, `syncInputHeight`, `View` |

**No changes needed to:** `session/`, `runner/`, `config/`, `tui/commands.go`, `app/app.go`

---

## Migration Path to Skills (future — not this phase)

The 5 built-in tools are hardcoded in `agent/builtins.go` for now. When the skills system is built, they should become a first-party skill bundle. The migration path is straightforward:

1. Create `skills/core-tools/` directory with:
   - `SKILL.md` — human+AI-readable description of the 5 tools and when to use them
   - `tools.go` — the same 5 `*Tool` structs, now exported from the skill package
   - `skill.go` — implements a `Skill` interface: `Name() string`, `Register(d *ToolDispatcher)`

2. Create `skills/loader.go` — reads `~/.local/share/livie/skills/`, discovers skill directories, calls `skill.Register(dispatcher)` for each

3. Replace the `RegisterBuiltins(a.tools, cwd)` call in `agent.New()` with:
   ```go
   skillLoader.LoadAll(a.tools, cwd)
   ```

4. `SKILL.md` content is injected into the system prompt at session start (append after `personality.md`), so the AI knows what tools it has available and how to use them correctly.

5. `/skills` command (currently a stub) gets real implementation: `list`, `enable`, `disable`, `install <url>`.

The only coupling between the hardcoded phase and the skills phase is `RegisterBuiltins` — a single call site with a clean signature. No refactor needed beyond extracting the tool structs into the skill package.

---

## Testing Checklist

### Pre-flight
```bash
./livie
# confirm_tool_calls = true in ~/.config/livie/config.toml (or omit — it's the default)
```

### 1 — Confirm block appears

Ask the model to read a file (prompt it explicitly if using a local model):
```
Read the contents of main.go
```
- [ ] Model issues a `read_file` tool call
- [ ] Streaming slot finalises (no dangling cursor)
- [ ] Confirm block appears above input bar: `⚙ tool · read_file · "main.go"`
- [ ] `y / enter = run · n / esc = reject` hint visible
- [ ] Autocomplete does not appear simultaneously

### 2 — Approve executes and continues

Press `y`:
- [ ] Confirm block dismisses
- [ ] Dim line appears: `  ⚙ read_file("main.go")  0.0s  ✓`
- [ ] Stream restarts — model responds with the file contents in its reply
- [ ] No dangling cursor after the final response

### 3 — Reject path

New conversation. Prompt for a file read again.
Press `n` at confirm:
- [ ] Confirm block dismisses
- [ ] Dim line: `  ⚙ read_file(...)  ✗ rejected`
- [ ] Stream restarts — model responds acknowledging it couldn't read the file

### 4 — Multi-step tool loop

Ask the model to read a file, then write a modified version:
```
Read config.go, then write a file called config_copy.go with the same contents
```
- [ ] First tool call (read_file) → confirm → approve → model issues second tool call (write_file)
- [ ] Second confirm block appears for write_file
- [ ] After second approval: `config_copy.go` exists on disk
- [ ] Two dim activity lines visible (one per tool)
- [ ] Model's final response summarises what it did

### 5 — bash tool

```
Run `echo hello from livie` and tell me what it output
```
- [ ] Confirm block shows: `bash · echo hello from livie`
- [ ] Approve → dim line: `  ⚙ bash(echo hello from livie)  0.0s  ✓`
- [ ] Model quotes `"hello from livie"` in its response

### 6 — edit_file tool

Ask the model to make a specific change to a test file you own:
```
Edit /tmp/test.txt — replace the word "hello" with "goodbye"
```
(Create `/tmp/test.txt` with content `hello world` first.)
- [ ] Confirm block shows the edit details
- [ ] After approval: file content on disk is `goodbye world`
- [ ] Model confirms the change

### 7 — find_files tool

```
Find all .go files in the current project
```
- [ ] Model calls `find_files` with `pattern: "*.go"`
- [ ] Confirm → approve → model lists the Go files it found

### 8 — Auto-execute mode

Set `confirm_tool_calls = false` in config. Restart. Run any tool-calling prompt:
- [ ] No confirm block appears
- [ ] Tool executes immediately
- [ ] Dim activity line still appears after execution
- [ ] Model responds normally

### 9 — Error path

Ask the model to read a file that does not exist:
```
Read /nonexistent/path/file.txt
```
- [ ] Confirm → approve
- [ ] Dim line: `  ⚙ read_file(...)  ✗ error: ...`
- [ ] Model receives the error string and responds accordingly (e.g. "the file was not found")

### 10 — Tool call within resumed session

Resume a previous session that involved tool use. Send a follow-up that triggers another tool call:
- [ ] Tool confirm appears and works normally within the resumed session
- [ ] Session saves correctly after tool loop completes
