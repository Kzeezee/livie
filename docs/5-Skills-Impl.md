# Livie — Phase 10: Skills System

> **Covers:** The `Skill` interface, `SkillLoader`, migration of the 5 built-in tools
> into a compiled-in core skill, external script-based skills, `SKILL.md` format,
> system prompt injection of skill descriptions, `/skills list` and `/skills install <path>`,
> and HUD skill count.

---

## What Was Done in Phase 9 (Tool Calling)

| Area | What shipped |
|---|---|
| `agent/agent.go` | `pendingToolCall` accumulator; real multi-chunk `PollCmd`; `DispatchToolCmd`, `ContinueAfterToolCmd`, `RejectToolCmd`; `cwd` fixed at launch |
| `agent/msgs.go` | `StreamToolCallMsg` with `ID`/`FinalDelta`; `ToolResultMsg`; `StreamDoneMsg.FinalDelta` |
| `agent/context.go` | `AddToolCall`, `AddToolResult`; `msgTokens` helper |
| `agent/builtins.go` | `RegisterBuiltins` + 5 tools: `bash`, `read_file`, `write_file`, `find_files`, `edit_file` |
| `tui/components/messages.go` | `MsgTool`, `ToolActivity`, `NewToolMessage`, `truncateArgs` |
| `tui/components/tool_confirm.go` | `ToolConfirmModel` — 4-row confirm overlay |
| `tui/screens/chat.go` | Full agentic loop wiring: confirm/auto-dispatch, activity lines, key intercept |
| `agent/agent.go` (perf) | `PollCmd` batches chunks for 16ms → ~60fps renders; `FinalDelta` flushed before `FinalizeStream` |

**What the Phase 9 plan flagged as the migration path for this phase:**
- Move `RegisterBuiltins` out of `agent/` into a `skills/core/` package
- Define a `Skill` interface and a `SkillLoader` that calls `skill.Register(dispatcher)`
- Inject `SKILL.md` content from each loaded skill into the system prompt
- Give `/skills` a real implementation

---

## Scope

**In this phase:**
- Define the `Skill` interface and `SkillLoader` in a new `skills/` package
- Migrate `agent/builtins.go` → `skills/core/` as a compiled-in skill bundle; `SKILL.md` embedded via `//go:embed`
- Introduce a `livie-self` meta-skill: a read-only `SKILL.md` that describes Livie's keybindings, modes, commands, and configuration to the AI
- Script-based external skills: a skill directory under `cfg.Paths.Skills` containing a `SKILL.md` with TOML frontmatter and shell script handlers
- `SkillLoader.LoadAll` replaces `RegisterBuiltins` in `agent.New()`
- System prompt: each loaded skill's `SKILL.md` body appended after `personality.md`
- `/skills list` — real implementation, replaces the stub
- `/skills install <path>` — copies a local skill directory into `cfg.Paths.Skills` and reloads
- HUD `SkillCount` wired to the actual count of loaded skills
- Welcome screen "N loaded" wired to the actual count

**Explicitly out of scope:**
- `/skills install <url>` — URL/git download (future phase)
- Per-skill enable/disable — all discovered skills load (future phase)
- The `/skills enable`, `/skills disable`, `/skills remove` sub-commands — stubs for now
- RAG / vector store integration with skill content
- The `/memory`, `/index`, `/config`, `/usage` stubs — untouched

---

## Architecture Overview

```
agent.New()
  └─ skills.NewLoader(cfg.Paths.Skills, cwd)
       ├─ RegisterBuiltin(&core.Skill{})       ← compiled-in
       ├─ RegisterBuiltin(&livieself.Skill{})  ← compiled-in, read-only SKILL.md
       └─ DiscoverExternal()                   ← walks cfg.Paths.Skills/
            └─ for each valid skill dir:
                 ScriptSkill{SKILL.md, handlers/}
  └─ loader.LoadAll(dispatcher)
       └─ for each skill: skill.Register(dispatcher)
  └─ loader.SystemPromptContent()
       └─ concatenates SKILL.md bodies → injected after personality.md
```

### Skill interface

```go
// skills/skill.go
package skills

import "github.com/kez/livie/agent"

// Skill is the interface every skill implements.
type Skill interface {
    // Name returns the unique skill identifier (e.g. "core-tools", "livie-self").
    Name() string

    // Description returns a single sentence shown in /skills list.
    Description() string

    // Register adds the skill's tools to the dispatcher.
    // Called once at agent construction. No-op for description-only skills.
    Register(d *agent.ToolDispatcher)

    // SkillMD returns the full SKILL.md body to inject into the system prompt.
    // Should be plain Markdown without TOML frontmatter.
    SkillMD() string
}
```

### SkillLoader

```go
// skills/loader.go
package skills

// SkillLoader discovers and holds all active skills.
type SkillLoader struct {
    skills []Skill
    cwd    string
}

func NewLoader(skillsDir, cwd string) *SkillLoader
func (l *SkillLoader) RegisterBuiltin(s Skill)
func (l *SkillLoader) DiscoverExternal() error   // walks skillsDir
func (l *SkillLoader) LoadAll(d *agent.ToolDispatcher)
func (l *SkillLoader) SystemPromptContent() string  // joined SKILL.md bodies
func (l *SkillLoader) Count() int
func (l *SkillLoader) Names() []string
```

---

## SKILL.md Format

All skills have a `SKILL.md`. For external skills it lives on disk; for compiled-in skills it is embedded.

### External skill: frontmatter + body

```
skills/my-skill/
├── SKILL.md
└── handlers/
    ├── my_tool.sh
    └── other_tool.sh
```

**SKILL.md:**

```markdown
---
name: my-skill
version: 1.0.0
description: "One sentence description shown in /skills list"
tools:
  - name: my_tool
    description: "What this tool does"
    handler: handlers/my_tool.sh
    parameters:
      type: object
      properties:
        input:
          type: string
          description: "Input string"
      required: [input]
---

# My Skill

Describe the skill here in plain Markdown. This section is injected into the
system prompt so the AI knows what the skill can do, when to use each tool,
and any important constraints.

## Tools

### `my_tool`
Use this when you need to ...
```

### Frontmatter rules

| Field | Required | Notes |
|---|---|---|
| `name` | Yes | Unique, matches directory name by convention |
| `version` | No | Informational only |
| `description` | Yes | Single sentence, shown in `/skills list` |
| `tools` | No | Array of tool definitions. Omit for description-only skills (like `livie-self`) |
| `tools[].name` | Yes | Tool name registered with the dispatcher |
| `tools[].description` | Yes | Shown to the model |
| `tools[].handler` | Yes | Path relative to the skill directory. Must be executable |
| `tools[].parameters` | Yes | JSON Schema object (inline TOML or embedded JSON string) |

### Script handler protocol

- The dispatcher calls `handler` as a subprocess: `<handler> '<json-args>'`
- Args are passed as the first CLI argument (raw JSON string)
- Handler writes its result to stdout (plain text, not JSON-wrapped)
- Non-zero exit: stderr is captured and returned as the result string (same as `bash` tool behaviour — the AI sees it, it is not a Go error)
- Timeout: 30s default, configurable in frontmatter as `timeout_seconds`
- `cwd` of the subprocess is set to Livie's launch `cwd`

---

## The `core-tools` Built-in Skill

Migrates the existing `agent/builtins.go` into `skills/core/`.

```
skills/core/
├── SKILL.md        ← describes the 5 tools; embedded via //go:embed
├── skill.go        ← implements Skill interface; calls agent.RegisterBuiltins
└── tools.go        ← the 5 tool constructors (moved from agent/builtins.go)
```

`tools.go` is the `agent/builtins.go` content extracted verbatim — same 5 tool constructors, same signatures.

`skill.go`:

```go
package core

import (
    _ "embed"
    "github.com/kez/livie/agent"
)

//go:embed SKILL.md
var skillMD string

type Skill struct{ cwd string }

func New(cwd string) *Skill { return &Skill{cwd: cwd} }

func (s *Skill) Name()        string { return "core-tools" }
func (s *Skill) Description() string { return "Built-in file, shell, and search tools" }
func (s *Skill) SkillMD()     string { return skillMD }
func (s *Skill) Register(d *agent.ToolDispatcher) {
    RegisterTools(d, s.cwd)  // RegisterTools = renamed RegisterBuiltins
}
```

**`agent/builtins.go` is deleted.** `RegisterBuiltins` in `agent.New()` is replaced by the skill loader call.

---

## The `livie-self` Built-in Skill

A description-only skill — no tools, no handlers. Its `SKILL.md` describes Livie to the AI so it knows what application it is running inside.

```
skills/livieself/
├── SKILL.md   ← embedded; describes keybindings, modes, commands, config
└── skill.go   ← Register() is a no-op
```

**`SKILL.md` content covers:**

- What Livie is (one paragraph)
- Input modes: chat, bash
- Key bindings (submit, scroll, quit, copy, mode toggle)
- Slash commands and what each does
- Config file location and key fields
- The vault (personality.md, memory.md, user-profile.md)
- How to modify Livie's own source (edit files, tell the user to rebuild)

This gives the AI enough context to help users with Livie-specific questions and to make sensible self-modification decisions.

---

## Phase Breakdown

### Phase 10A — `skills/` package skeleton

New files:

```
skills/skill.go      ← Skill interface
skills/loader.go     ← SkillLoader
```

**`SkillLoader.DiscoverExternal()`** walks `skillsDir`:
- For each subdirectory: looks for `SKILL.md`
- Parses TOML frontmatter (use `github.com/BurntSushi/toml` — already a transitive dep via config)
- Builds a `ScriptSkill` for each valid directory
- Silently skips directories with no `SKILL.md` or unparseable frontmatter (logs a dim warning line if verbose)

**`SkillLoader.LoadAll(d)`** iterates skills and calls `skill.Register(d)`.

**`SkillLoader.SystemPromptContent()`** joins `skill.SkillMD()` for each skill with a `\n\n---\n\n` separator. Returns `""` if no skills loaded.

### Phase 10B — `skills/core/` — migrate builtins

1. Create `skills/core/SKILL.md` — describes the 5 tools in plain Markdown
2. Create `skills/core/tools.go` — copy of `agent/builtins.go` with package changed to `core`, `RegisterBuiltins` renamed `RegisterTools`
3. Create `skills/core/skill.go` — implements `Skill` interface
4. Delete `agent/builtins.go`

### Phase 10C — `skills/livieself/` — meta-skill

1. Write `skills/livieself/SKILL.md` — Livie self-description (no frontmatter tools section needed)
2. Write `skills/livieself/skill.go` — `Register()` is a no-op

### Phase 10D — Wire loader into `agent.New()`

```go
// agent/agent.go
func New(cfg *config.Config) *Agent {
    // ...existing setup...
    loader := skills.NewLoader(cfg.Paths.Skills, cwd)
    loader.RegisterBuiltin(core.New(cwd))
    loader.RegisterBuiltin(livieself.New())
    if err := loader.DiscoverExternal(); err != nil {
        // non-fatal — log to stderr or ignore
    }

    d := agent.NewToolDispatcher()
    loader.LoadAll(d)

    sysprompt := buildSystemPrompt(cfg, loader)  // ← new helper
    // ...
}
```

**`buildSystemPrompt`** replaces the `LoadSystemPrompt` call:

```go
func buildSystemPrompt(cfg *config.Config, loader *skills.SkillLoader) string {
    base := LoadSystemPrompt(filepath.Join(cfg.Paths.Vault, "system_prompt.md"))
    if extra := loader.SystemPromptContent(); extra != "" {
        return base + "\n\n---\n\n" + extra
    }
    return base
}
```

### Phase 10E — Script-based external skill support

**`ScriptSkill`** in `skills/loader.go` (or `skills/script.go`):

```go
type ScriptSkill struct {
    name        string
    description string
    skillMD     string         // markdown body (frontmatter stripped)
    tools       []scriptTool
    dir         string         // absolute path to skill directory
}

type scriptTool struct {
    Name        string
    Description string
    Handler     string         // absolute path to handler script
    Parameters  json.RawMessage
    TimeoutSecs float64
}
```

`ScriptSkill.Register(d)` creates a `*agent.Tool` per `scriptTool` whose `Handler` func:
1. Builds `exec.CommandContext` with the handler path and JSON args as the first argument
2. Sets `cmd.Dir` to `cwd` (Livie's launch directory, not the skill directory)
3. Captures `CombinedOutput()`
4. Returns the output string (non-zero exit → output + `[exit N]`, same pattern as `bash` tool)

### Phase 10F — `/skills list` real implementation

Replace the stub in `tui/commands.go`:

```go
r.Register(&Command{
    Name:        "skills",
    Description: "List, install, enable or disable skills",
    SubCommands: []*SubCommand{
        {Name: "list",    Description: "List loaded skills"},
        {Name: "install", Description: "Install a skill from a local path"},
    },
    Handler: func(args []string) (string, AppAction) {
        sub := ""
        if len(args) > 0 {
            sub = args[0]
        }
        switch sub {
        case "", "list":
            return actionListSkills, ActionListSkills
        case "install":
            if len(args) < 2 {
                return "usage: /skills install <path>", ActionNone
            }
            return "", ActionInstallSkill{Path: args[1]}
        default:
            return fmt.Sprintf("unknown sub-command %q. Try: list, install", sub), ActionNone
        }
    },
})
```

**`ActionListSkills`** — the chat model handler queries `loader.Names()` and formats a response:

```
  core-tools    Built-in file, shell, and search tools
  livie-self    Livie self-description and keybindings
  my-skill      Does something useful
```

**`ActionInstallSkill`** — copies the source directory into `cfg.Paths.Skills/<dirname>`, calls `loader.DiscoverExternal()` again, calls `loader.LoadAll(dispatcher)` to register new tools, updates the system prompt in the agent. Returns a confirmation message.

### Phase 10G — HUD and welcome screen counts

`agent.New()` (or the chat model init) passes `loader.Count()` to the HUD state and refreshes after load. This replaces the hardcoded `0` in both the HUD and the welcome screen "N loaded" line.

**`ChatModel`** gets a `skillCount int` field populated from `loader.Count()` at construction and after any `/skills install`.

---

## File Map

| File | Status | Change |
|---|---|---|
| `skills/skill.go` | **New** | `Skill` interface |
| `skills/loader.go` | **New** | `SkillLoader`, `ScriptSkill`, `scriptTool`, discovery, `LoadAll`, `SystemPromptContent` |
| `skills/core/SKILL.md` | **New** | Core tools description (embedded) |
| `skills/core/tools.go` | **New** | 5 tool constructors (moved from `agent/builtins.go`) |
| `skills/core/skill.go` | **New** | `Skill` implementation for core tools |
| `skills/livieself/SKILL.md` | **New** | Livie self-description (embedded) |
| `skills/livieself/skill.go` | **New** | Description-only `Skill` implementation |
| `agent/builtins.go` | **Deleted** | Replaced by `skills/core/` |
| `agent/agent.go` | Modify | Replace `RegisterBuiltins` + `LoadSystemPrompt` with `SkillLoader` and `buildSystemPrompt` |
| `agent/system_prompt.go` | Modify | `buildSystemPrompt` helper extracted here |
| `tui/commands.go` | Modify | `/skills list` and `/skills install` real implementations; new `AppAction` types |
| `tui/screens/chat.go` | Modify | Handle `ActionListSkills`, `ActionInstallSkill`; `skillCount` field; pass to HUD |
| `tui/components/hud.go` | Modify | `SkillCount` already exists — just wire the real value |
| `tui/screens/welcome.go` | Modify | Pass real skill count to "N loaded" line |

**No changes needed to:** `agent/msgs.go`, `agent/tools.go`, `agent/context.go`, `config/`, `session/`, `runner/`, `tui/components/messages.go`, `tui/components/tool_confirm.go`

---

## SKILL.md for `core-tools`

The model already receives full tool schemas via the API `tools` field — the
SKILL.md body only needs to note non-obvious behaviours.

```markdown
---
name: core-tools
description: Built-in file, shell, and search tools (bash, read_file, write_file, edit_file, find_files).
---

- `edit_file` validates all edits before writing — failure is atomic
- `bash` non-zero exit returns output + `[exit N]`, not an error
- `find_files` matches filename only; use `bash` + `find` for path patterns
```

---

## SKILL.md for `livie-self`

```markdown
---
name: livie-self
description: Livie application reference — modes, keys, commands, config, vault, self-modification.
---

# Livie

Terminal-native AI assistant (Go + Bubbletea). Two modes: **chat** (default) and **bash**.

## Keys

| Key | Action |
|---|---|
| `enter` | Submit |
| `shift+enter` / `ctrl+j` | Newline |
| `ctrl+c` ×2 / `ctrl+q` | Quit |
| `pgup/pgdn` | Scroll |
| `ctrl+u/d` | Half-page scroll |
| `ctrl+g/e` | Top / bottom |
| `ctrl+y` | Copy last response (OSC 52) |
| `shift+tab` | Toggle bash mode |

## Commands

| Command | Action |
|---|---|
| `/new` | Fresh conversation |
| `/resume` | Resume session |
| `/skills list` | List skills |
| `/skills install <path>` | Install skill |
| `/run start\|stop\|restart` | Runner control |

## Config `~/.config/livie/config.toml`

- `[endpoint] active` — active endpoint name
- `[[endpoints]]` — name, base_url, api_key, model
- `[runner]` — binary_path, model_path, gpu_backend, port
- `[behaviour] confirm_tool_calls` — y/n before each tool
- `[paths]` vault, skills, index

## Vault `~/.local/share/livie/vault/`

`personality.md`, `memory.md`, `user-profile.md` — read/edit with file tools.

## Self-modification

Source is in cwd at launch. Edit `.go` files with file tools; tell the user to rebuild (`go build .`).
```

---

## Testing Checklist

### Pre-flight
```bash
go build ./...
```

### 1 — Skills load at startup

- [ ] `./livie` starts without error
- [ ] Welcome screen shows the correct skill count (≥ 2: core-tools + livie-self)
- [ ] HUD shows the correct skill count

### 2 — `/skills list`

- [ ] `/skills list` displays all loaded skills with names and descriptions
- [ ] No "coming soon" stub text

### 3 — Core tools still work after migration

- [ ] Ask the model to read a file → `read_file` tool fires correctly
- [ ] All 5 tools still function as before (run `go test ./skills/core/...`)

### 4 — System prompt injection

- [ ] Start a fresh session and ask: *"what tools do you have available?"*
- [ ] Model correctly describes all 5 core tools by name
- [ ] Ask: *"what are Livie's key bindings?"* → model correctly lists them from `livie-self`

### 5 — External script skill

Install the bundled example skill:
```bash
mkdir -p ~/.local/share/livie/skills/hello-skill
# write SKILL.md + handlers/hello.sh (see below)
```

`SKILL.md`:
```markdown
---
name: hello-skill
description: "Example script-based skill"
tools:
  - name: greet
    description: "Greets a person by name"
    handler: handlers/greet.sh
    parameters:
      type: object
      properties:
        name:
          type: string
          description: "Name to greet"
      required: [name]
---

# Hello Skill

A minimal example skill. Use `greet` to greet someone by name.
```

`handlers/greet.sh`:
```bash
#!/bin/sh
name=$(echo "$1" | python3 -c "import sys,json; print(json.load(sys.stdin)['name'])")
echo "Hello, $name! (from greet.sh)"
```

- [ ] Restart Livie — skill count increases by 1
- [ ] `/skills list` shows `hello-skill`
- [ ] Ask: *"use the greet tool to greet Alice"* → model calls `greet`, response contains "Hello, Alice!"

### 6 — `/skills install <path>`

```bash
# Create a skill directory somewhere outside the skills dir
mkdir /tmp/test-skill
# ... add SKILL.md ...
```

- [ ] `/skills install /tmp/test-skill` copies the directory and loads it without restart
- [ ] `/skills list` shows the new skill immediately
- [ ] HUD skill count updates

### 7 — Model knows about Livie

- [ ] *"what mode am I in?"* → model answers correctly
- [ ] *"how do I quit Livie?"* → model gives correct key binding
- [ ] *"where is the config file?"* → model gives `~/.config/livie/config.toml`
