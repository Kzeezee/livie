# Livie — Phase 7: Vault Memory & User Profile

> **Covers:** `memory.md` and `user-profile.md` vault files, a `write_vault_file`
> tool exposed to the AI (scoped to memory files only), loading both new files into
> the system prompt, a `/memory` slash command, and updating the `system_prompt.md`
> seed to instruct Livie to maintain its own memory.

---

## What Was Done in Phase 6 (Personality & Vault Init)

| Area | What shipped |
|---|---|
| `memory/vault.go` | `Init`, `LoadFile`, `seedFile` |
| `memory/seeds/system_prompt.md` | Instructional seed — capabilities, no restrictions |
| `memory/seeds/personality.md` | One-liner personality seed |
| `agent/system_prompt.go` | `buildSystemPrompt` assembles instructions → personality → skills |
| `main.go` | `memory.Init(cfg.Paths.Vault)` called before `agent.New()` |

**What this phase addresses from the current state:**

- `memory.md` and `user-profile.md` are described in `About-Livie.md` but do not exist
  in the vault, are not seeded, are not loaded into the system prompt, and are not
  writable by the AI.
- The AI has no tool for updating vault memory files. The only write path is the generic
  `write_file` core tool, which is unrestricted and not semantically scoped to memory.
- There is no `/memory` slash command to inspect vault memory content.
- The `system_prompt.md` seed does not instruct Livie to maintain its memory.

---

## Design Decisions

### Prompt assembly order (extended)

```
┌─────────────────────────────────┐
│  system_prompt.md               │  ← instructional: capabilities, constraints, memory duty
├─────────────────────────────────┤
│  personality.md                 │  ← voice: tone, style
├─────────────────────────────────┤
│  user-profile.md                │  ← who the user is: name, goals, habits, preferences
├─────────────────────────────────┤
│  memory.md                      │  ← rolling context: past topics, decisions, learned facts
├─────────────────────────────────┤
│  skill descriptions (SKILL.md)  │  ← tool awareness
└─────────────────────────────────┘
```

**Rationale for ordering:**
- `user-profile.md` before `memory.md` — identity context before episodic context.
  Models perform better when persona/audience facts come before history.
- Both before skill descriptions — the AI should know who it's talking to and what's been
  discussed before it decides what tools to expose in its self-model.
- Empty layers are omitted (same rule as before — no blank separators).

### `write_vault_file` allowlist

The tool exposes only `memory.md` and `user-profile.md` for AI-initiated writes. The
rationale:

- `system_prompt.md` and `personality.md` are **user-owned configuration**. The AI
  modifying them without explicit instruction would be unexpected and confusing. If the
  user wants the AI to edit them, the existing `write_file` core tool can be used after
  being explicitly directed.
- `memory.md` and `user-profile.md` are **AI-maintained memory**. The AI is actively
  expected to update these. Scoping the vault tool to just these two files makes the
  intent unambiguous and prevents accidental overwrites of config files.
- The allowlist is enforced in the tool handler — not just documented. Attempts to write
  to other filenames return an error string (not a Go error) so the AI sees the refusal
  and can course-correct.

### Memory update discipline

The AI is instructed (via an updated `system_prompt.md` seed) to:

1. Update `user-profile.md` when it learns a new fact about the user — name, project,
   preference, goal, workflow habit. Overwrites with the full updated file.
2. Update `memory.md` when significant context accumulates — a decision was made, a task
   was completed, something the AI would want to know next session. Rolling overwrite —
   the AI maintains the file as a current-state summary, not a log.

**No end-of-session hook.** The AI writes memory proactively during conversation when it
judges something worth persisting. This keeps the architecture simple — no lifecycle hook
needed in `main.go`, no background goroutine, no session-end signal. If automatic
session-end summarization is wanted later, it can be added as a `main.go` shutdown step
that injects a summarize request into the agent; that is explicitly out of scope here.

### `write_vault_file` — overwrite vs append

The tool only supports **full overwrite** for both files. The AI reads the current
content (via the system prompt or via `read_file` tool call), updates it mentally, and
writes the full new content. This is simpler than an append-only interface and gives the
AI full control over file shape. Partial edit tools are future scope.

### Seed file content

Both files seed with empty content (a comment-only placeholder). They are present in the
vault from first run so `LoadFile` always finds them, but they contribute nothing to the
system prompt until the AI writes meaningful content. Empty-string layers are already
omitted from the prompt by `buildSystemPrompt`.

Actually — empty files return `""` from `LoadFile`, which causes the layer to be
omitted. So the seed content for these files must be a non-empty string to be present,
or they can start truly empty and be omitted until written. **Decision: start empty.**
No seed content. The files are created by `Init` as empty files (`0` bytes). `LoadFile`
returns `""` for an empty file (content is `""`), so the layers are omitted. Once the AI
writes something, the layer appears. This is correct — a fresh install has no memory.

> **Implementation note:** `os.WriteFile` with `[]byte("")` creates a zero-byte file.
> `os.ReadFile` on a zero-byte file returns `[]byte{}`, `nil`. `string([]byte{})` is
> `""`. So `LoadFile` returns `""` for an empty seed file, and the layer is omitted from
> the prompt. No special-casing needed.

### New `skills/vault/` package

The `write_vault_file` tool lives in a new compiled-in skill: `skills/vault/`. This
follows the same pattern as `skills/core/` — a `Skill` struct, a `SKILL.md`, and a
`tools.go`. It is registered in `agent.New()` alongside the core skill.

**Why a separate skill package rather than adding the tool to `skills/core/`:**
- Core tools are generic filesystem/shell operations. Vault tools are semantically about
  memory — different concern, different documentation, different invocation intent.
- Keeping them separate means the AI's `SKILL.md` injection clearly explains vault writes
  as a memory operation, not a generic write.
- A future `/skills disable vault` could remove memory write access without touching
  core filesystem tools.

### `/memory` slash command

Returns a formatted Markdown block showing the current contents of `memory.md` and
`user-profile.md` from the vault. Uses `memory.LoadFile` directly — no new API surface
needed. Output is rendered inline in the chat view like all other slash command output.

No new `AppAction` is needed — the command returns `(string, ActionNone)`.

---

## Scope

**In this phase:**
- `memory/vault.go` extended: `WriteFile`, `InitEmpty` seeding for `memory.md` and `user-profile.md`
- `memory/seeds/system_prompt.md` updated: adds memory-maintenance instructions to seed
- New `skills/vault/` skill package: `skill.go`, `tools.go`, `SKILL.md`
- `agent/agent.go`: register `skills/vault` builtin skill
- `agent/system_prompt.go`: `buildSystemPrompt` loads `user-profile.md` and `memory.md` as additional layers
- `tui/commands.go`: `/memory` command registered

**Explicitly out of scope:**
- Auto end-of-session summarization hook
- `projects/` per-project notes (future)
- `/memory edit` — opening vault files in `$EDITOR` (future)
- Wikilink / Obsidian cross-reference parsing
- Vector store / RAG over vault memory content
- `read_vault_file` tool — the AI can use `read_file` from core-tools with the vault path; duplication not warranted yet

---

## Seed File Updates

### `memory/seeds/system_prompt.md` (updated)

The existing seed gains a memory-maintenance paragraph. Token cost is still minimal (~65 tokens total):

```markdown
You are Livie, a terminal AI assistant. You are direct and technically precise.
You have access to the user's working directory and tools. No content restrictions apply.
Respond concisely unless detail is explicitly requested.

You maintain persistent memory via two vault files. Use write_vault_file when:
- You learn something about the user worth remembering (name, project, preference, goal) → update user-profile.md
- A session produces context worth carrying forward (decision made, task completed, important fact) → update memory.md
Write the full file content each time. Both files start empty; build them up over time.
```

~65 tokens. The memory instruction is added only to the seed — existing user-customised `system_prompt.md` files are **never overwritten**.

### `memory.md` seed (empty)

Zero bytes. Layer omitted from prompt until the AI writes to it.

### `user-profile.md` seed (empty)

Zero bytes. Layer omitted from prompt until the AI writes to it.

---

## Architecture

```
main.go
  └─ memory.Init(cfg.Paths.Vault)       ← now also creates empty memory.md, user-profile.md

agent.New(cfg)
  ├─ loader.RegisterBuiltin(vault.New(cfg.Paths.Vault))   ← NEW
  └─ buildSystemPrompt(cfg, loader)
       ├─ memory.LoadFile(vault, "system_prompt.md")   → instructions
       ├─ memory.LoadFile(vault, "personality.md")     → personality
       ├─ memory.LoadFile(vault, "user-profile.md")    → user facts  ← NEW
       ├─ memory.LoadFile(vault, "memory.md")          → session context  ← NEW
       └─ loader.SystemPromptContent()                 → skill descriptions

AI tool call: write_vault_file  →  memory.WriteFile(vault, filename, content)
                                   filename ∈ {"memory.md", "user-profile.md"}

/memory command  →  memory.LoadFile(vault, "memory.md") + memory.LoadFile(vault, "user-profile.md")
                    → formatted string → ChatModel inline display
```

---

## Phase Breakdown

### Phase 7A — Extend `memory/vault.go`

**Changes to `memory/vault.go`:**

1. Update the `system_prompt.md` embedded seed to include memory-maintenance instructions.
2. Add two new empty seed calls in `Init` for `memory.md` and `user-profile.md`.
3. Add `WriteFile(vaultPath, filename, content string) error`.

```go
// WriteFile writes content to vaultPath/filename, creating it if needed.
// Unlike seedFile, this always overwrites. Callers are responsible for
// scoping filenames appropriately.
func WriteFile(vaultPath, filename, content string) error {
    return os.WriteFile(filepath.Join(vaultPath, filename), []byte(content), 0o644)
}
```

Updated `Init`:

```go
func Init(vaultPath string) error {
    if err := os.MkdirAll(vaultPath, 0o755); err != nil {
        return fmt.Errorf("create vault dir: %w", err)
    }
    if err := seedFile(vaultPath, "system_prompt.md", defaultSystemPrompt); err != nil {
        return err
    }
    if err := seedFile(vaultPath, "personality.md", defaultPersonality); err != nil {
        return err
    }
    // Memory files start empty — seeded as zero-byte files so they exist on disk
    // but contribute no content to the system prompt until the AI writes to them.
    if err := seedFile(vaultPath, "memory.md", ""); err != nil {
        return err
    }
    return seedFile(vaultPath, "user-profile.md", "")
}
```

> `seedFile` already skips existing files, so re-running Init never clears memory.

**No new embedded assets needed** for `memory.md` and `user-profile.md` — they are
seeded as empty strings directly (no `//go:embed` directive for empty files; just pass
`""` to `seedFile`).

The updated `system_prompt.md` seed *does* need its embed updated. The embed is already
in place (`//go:embed seeds/system_prompt.md`); only the file content changes.

---

### Phase 7B — New `skills/vault/` skill package

New files:

```
skills/vault/SKILL.md
skills/vault/skill.go
skills/vault/tools.go
```

**`skills/vault/skill.go`:**

```go
package vault

import (
    _ "embed"
    "github.com/kez/livie/skills"
)

//go:embed SKILL.md
var skillMD string

// Skill is the compiled-in vault-memory skill.
type Skill struct{ vaultPath string }

// New returns a Skill that writes to the given vault path.
func New(vaultPath string) *Skill { return &Skill{vaultPath: vaultPath} }

func (s *Skill) Name() string        { return "vault-memory" }
func (s *Skill) Description() string { return "Write to AI-maintained vault memory files" }
func (s *Skill) SkillMD() string     { return skills.StripFrontmatter(skillMD) }

func (s *Skill) Register(r skills.Registrar) {
    RegisterTools(r, s.vaultPath)
}
```

**`skills/vault/tools.go`:**

```go
package vault

import (
    "encoding/json"
    "fmt"

    "github.com/kez/livie/memory"
    "github.com/kez/livie/skills"
)

// allowedFiles is the set of vault files the AI may write.
// system_prompt.md and personality.md are user-owned config — not in this list.
var allowedFiles = map[string]bool{
    "memory.md":       true,
    "user-profile.md": true,
}

func RegisterTools(r skills.Registrar, vaultPath string) {
    r.Register(writeVaultFileTool(vaultPath))
}

func writeVaultFileTool(vaultPath string) *skills.Tool {
    return &skills.Tool{
        Name:        "write_vault_file",
        Description: "Write or overwrite a vault memory file. Use for memory.md (session context) and user-profile.md (user facts). Always write the full file content.",
        Parameters: []byte(`{
            "type": "object",
            "properties": {
                "filename": {
                    "type": "string",
                    "enum": ["memory.md", "user-profile.md"],
                    "description": "The vault file to write"
                },
                "content": {
                    "type": "string",
                    "description": "Full file content to write (replaces existing content)"
                }
            },
            "required": ["filename", "content"]
        }`),
        Handler: func(args string) (string, error) {
            var params struct {
                Filename string `json:"filename"`
                Content  string `json:"content"`
            }
            if err := json.Unmarshal([]byte(args), &params); err != nil {
                return "", fmt.Errorf("invalid args: %w", err)
            }
            if !allowedFiles[params.Filename] {
                // Return as a result string, not a Go error, so the AI sees
                // the refusal and can respond accordingly rather than crashing.
                return fmt.Sprintf("error: %q is not a writable vault memory file. Allowed: memory.md, user-profile.md", params.Filename), nil
            }
            if err := memory.WriteFile(vaultPath, params.Filename, params.Content); err != nil {
                return fmt.Sprintf("error writing %s: %v", params.Filename, err), nil
            }
            return fmt.Sprintf("wrote %s (%d bytes)", params.Filename, len(params.Content)), nil
        },
    }
}
```

**`skills/vault/SKILL.md`:**

```markdown
---
name: vault-memory
version: 1.0.0
---

## Vault Memory

You have persistent memory across sessions via two Markdown files in your vault.

### Tool: `write_vault_file`

Writes a vault memory file. Always provide the **complete file content** — this is a
full overwrite, not an append.

**Allowed files:**

| File | Purpose |
|---|---|
| `memory.md` | Rolling context: topics covered, decisions made, tasks completed, facts learned |
| `user-profile.md` | User facts: name, role, projects, preferences, goals, working style |

**When to call this tool:**

- **user-profile.md** — when you learn something persistent about the user (their name,
  a project they're working on, a preference, a goal). Call it in the same turn you
  learned the fact; don't wait.
- **memory.md** — when a session produces context worth knowing next time (a task was
  completed, a significant decision was made, a multi-step thing was resolved). Use
  judgment — not every turn, but don't defer indefinitely.

**Format guidance:**

Write both files as clean, dense Markdown. Prefer bullet lists and short sentences over
prose. Every token you write here is paid on every future request — be concise.

Example `user-profile.md`:
```markdown
- Name: Alex
- Role: Backend engineer, Go/Rust
- Main project: distributed log aggregator ("Lumberjack")
- Prefers: direct answers, no preamble, code examples over explanations
- Timezone: UTC+1
```

Example `memory.md`:
```markdown
- Helped set up Livie's vault memory system (2026-06-11)
- User prefers sessions without greetings
- Lumberjack project: decided to use WAL-based replication over gossip
- Runner: llama-server running gemma3-27b-q4 on CUDA
```
```

---

### Phase 7C — Update `agent/system_prompt.go`

`buildSystemPrompt` gains two new `LoadFile` calls between personality and skills:

```go
func buildSystemPrompt(cfg *config.Config, loader *skills.SkillLoader) string {
    vault := cfg.Paths.Vault

    instructions := memory.LoadFile(vault, "system_prompt.md")
    if instructions == "" {
        instructions = defaultSystemPrompt
    }

    var parts []string
    parts = append(parts, instructions)

    if personality := memory.LoadFile(vault, "personality.md"); personality != "" {
        parts = append(parts, personality)
    }

    // NEW: user facts and session context — loaded after voice, before tools.
    if userProfile := memory.LoadFile(vault, "user-profile.md"); userProfile != "" {
        parts = append(parts, "## User Profile\n\n"+userProfile)
    }

    if mem := memory.LoadFile(vault, "memory.md"); mem != "" {
        parts = append(parts, "## Memory\n\n"+mem)
    }

    if skillContent := loader.SystemPromptContent(); skillContent != "" {
        parts = append(parts, skillContent)
    }

    return strings.Join(parts, "\n\n---\n\n")
}
```

**Section headers** (`## User Profile`, `## Memory`) are prepended inline here rather
than in the files themselves. Rationale: the files are user/AI-maintained plain content
without structural wrappers — clean to read and edit. The prompt wrapper is a rendering
concern that belongs in assembly code.

---

### Phase 7D — Wire vault skill into `agent/agent.go`

One new line in `New()`, alongside the existing builtin registrations:

```go
// in agent/agent.go, inside New()
loader.RegisterBuiltin(core.New(cwd))
loader.RegisterBuiltin(livieself.New())
loader.RegisterBuiltin(vaultskill.New(cfg.Paths.Vault))  // ← NEW
```

Import added:

```go
"github.com/kez/livie/skills/vault"
```

> The import alias `vaultskill` avoids collision with the `vault` concept used in
> config strings. Alternatively name the package `vaultmemory` to avoid the need for an
> alias — decision deferred to implementation.

---

### Phase 7E — `/memory` slash command in `tui/commands.go`

New command registered in `registerBuiltins`. The command reads vault files directly via
`memory.LoadFile`. The vault path comes from `cfg`, which is already captured by the
command registry closure.

```go
r.Register(&Command{
    Name:        "memory",
    Description: "Show current vault memory (memory.md and user-profile.md)",
    Handler: func(_ []string) (string, AppAction) {
        vault := cfg.Paths.Vault

        var sb strings.Builder

        userProfile := memory.LoadFile(vault, "user-profile.md")
        mem := memory.LoadFile(vault, "memory.md")

        if userProfile == "" && mem == "" {
            return "Vault memory is empty — nothing written yet.", ActionNone
        }

        if userProfile != "" {
            sb.WriteString("## User Profile\n\n")
            sb.WriteString(userProfile)
        }
        if userProfile != "" && mem != "" {
            sb.WriteString("\n\n---\n\n")
        }
        if mem != "" {
            sb.WriteString("## Memory\n\n")
            sb.WriteString(mem)
        }

        return sb.String(), ActionNone
    },
})
```

Import `"github.com/kez/livie/memory"` added to `tui/commands.go` if not already
present.

---

## File Map

| File | Status | Change |
|---|---|---|
| `memory/vault.go` | **Modify** | Add `WriteFile`; update `Init` to seed `memory.md` and `user-profile.md` as empty files |
| `memory/seeds/system_prompt.md` | **Modify** | Add memory-maintenance instruction paragraph |
| `skills/vault/SKILL.md` | **New** | Skill description and tool usage guide for the AI |
| `skills/vault/skill.go` | **New** | `Skill` struct implementing `skills.Skill` interface |
| `skills/vault/tools.go` | **New** | `write_vault_file` tool handler with allowlist |
| `agent/agent.go` | **Modify** | Register `skills/vault` builtin in `New()` |
| `agent/system_prompt.go` | **Modify** | Add `user-profile.md` and `memory.md` layers to `buildSystemPrompt` |
| `tui/commands.go` | **Modify** | Register `/memory` command |

**No changes needed to:** `main.go`, `config/`, `runner/`, `session/`, `tui/screens/`,
`skills/core/`, `skills/livieself/`, `app/`

---

## Testing Checklist

### Pre-flight

```bash
go build ./...
```

### 1 — New vault files created on first run

```bash
rm -rf ~/.local/share/livie/vault
./livie
ls ~/.local/share/livie/vault/
```

- [ ] `system_prompt.md` present (instructional + memory paragraph)
- [ ] `personality.md` present
- [ ] `memory.md` present (empty file, 0 bytes)
- [ ] `user-profile.md` present (empty file, 0 bytes)

### 2 — Empty memory files omitted from system prompt

Start Livie fresh. Ask:

> *"What do you know about me?"*

- [ ] Model responds that it has no information yet (no user-profile.md content in prompt)
- [ ] No blank `## User Profile` section hallucinated into response

### 3 — `write_vault_file` tool available

Ask:

> *"What tools do you have access to?"*

- [ ] Model lists `write_vault_file` among available tools (vault skill's SKILL.md present in prompt)

### 4 — AI writes to `user-profile.md`

Tell Livie your name and ask it to remember it:

> *"My name is Jordan. Remember that."*

- [ ] Model calls `write_vault_file` with `filename: "user-profile.md"` and content containing the name
- [ ] Tool handler returns `"wrote user-profile.md (N bytes)"`
- [ ] `~/.local/share/livie/vault/user-profile.md` contains the written content

Restart Livie. Ask:

> *"What's my name?"*

- [ ] Model responds with "Jordan" (loaded from user-profile.md into system prompt)

### 5 — AI writes to `memory.md`

Tell Livie about something and ask it to remember it for next session:

> *"We just finished setting up vault memory. Add that to your memory file."*

- [ ] Model calls `write_vault_file` with `filename: "memory.md"`
- [ ] `memory.md` contains the written content
- [ ] Restart; ask what was done last session → model recalls

### 6 — Allowlist enforced

> *"Use write_vault_file to overwrite my system_prompt.md with 'hello world'."*

- [ ] Tool handler returns error string: `"error: \"system_prompt.md\" is not a writable vault memory file..."`
- [ ] `system_prompt.md` is unchanged
- [ ] Model acknowledges the refusal gracefully

### 7 — Memory layers appear in correct order

Temporarily add debug output or inspect prompt content via:

> *"Print the exact contents of your system prompt."*

- [ ] Instructions layer present
- [ ] Personality layer present (if non-empty)
- [ ] `## User Profile` section present (if non-empty)
- [ ] `## Memory` section present (if non-empty)
- [ ] Skill descriptions present at end

### 8 — `/memory` command

```
/memory
```

After writing to both files:
- [ ] Both `## User Profile` and `## Memory` sections shown
- [ ] Content matches vault files

Before writing anything:
- [ ] `"Vault memory is empty — nothing written yet."` displayed

After writing only one file:
- [ ] Only the non-empty section shown (no blank heading for the other)

### 9 — Idempotent init — existing memory not overwritten

```bash
echo "- Name: Test User" > ~/.local/share/livie/vault/user-profile.md
./livie
cat ~/.local/share/livie/vault/user-profile.md
```

- [ ] File still contains `- Name: Test User` (Init did not overwrite existing file)

### 10 — Vault skill in skills list

```
/skills list
```

- [ ] `vault-memory` appears with description `"Write to AI-maintained vault memory files"`

---

## Phase 8 Preview — Session Persistence & Resume

The session package already has `Session`, `Summary`, and store infrastructure. The next
phase wires sessions fully into the TUI:

- Auto-save conversation to disk on every assistant response
- `/resume` command with a selectable list of past sessions
- Session metadata in HUD (current session ID or indicator)
- Optional: end-of-session memory update hook — a shutdown step in `main.go` that sends
  a summarize request to the agent and writes the result to `memory.md` automatically
