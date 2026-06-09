# Livie — Phase 11: Personality & Vault Initialization

> **Covers:** The `memory/` package, vault directory initialization, seeding of
> `system_prompt.md` and `personality.md` on first run, restructured system
> prompt assembly (instructions → personality → skill descriptions), and wiring
> vault init into startup.

---

## What Was Done in Phase 10 (Skills)

| Area | What shipped |
|---|---|
| `skills/skill.go` | `Skill` interface, `Registrar` interface |
| `skills/loader.go` | `SkillLoader`, `ScriptSkill`, external skill discovery, `LoadAll`, `SystemPromptContent` |
| `skills/core/` | Compiled-in core tools skill (`bash`, `read_file`, `write_file`, `edit_file`, `find_files`) |
| `skills/livieself/` | Compiled-in Livie self-description meta-skill |
| `agent/agent.go` | `SkillLoader` wired in `New()`, `InstallSkill()`, `buildSystemPrompt` helper |
| `agent/system_prompt.go` | `LoadSystemPrompt` reads `vault/system_prompt.md`, hardcoded `defaultSystemPrompt` fallback |
| `tui/commands.go` | `/skills list`, `/skills install <path>` real implementations |
| `tui/screens/chat.go` | `skillCount` field, `ActionListSkills`, `ActionInstallSkill` handlers |

**What this phase addresses from the current state:**
- `agent/system_prompt.go` has a single-file model: one vault file (`system_prompt.md`) covers
  both instructions and personality. These concerns need to be separated.
- The hardcoded `defaultSystemPrompt` constant mixes instructional and personality content.
- There is no vault initialization code anywhere — the vault directory is never created,
  no seed files are written on first run.
- The `memory/` package described in About-Livie.md does not yet exist.

---

## Design Decisions

### Prompt architecture

The system prompt is assembled in three layers, in order:

```
┌─────────────────────────────────┐
│  system_prompt.md               │  ← instructional: capabilities, constraints, mode
├─────────────────────────────────┤
│  personality.md                 │  ← voice: tone, style, communication flair
├─────────────────────────────────┤
│  skill descriptions (SKILL.md)  │  ← tool awareness injected by skill loader
└─────────────────────────────────┘
```

Each layer is separated by `\n\n---\n\n`. Layers that produce empty content are omitted.
This keeps total prompt token count low and each concern editable independently.

### Token discipline

Both seed files are deliberately minimal — single-line defaults. Every token in the
system prompt is paid on every request. Users who want richer personality will add to
their vault files; they will not be penalised by verbose defaults they never asked for.

### Vault location

`cfg.Paths.Vault` — default `~/.local/share/livie/vault`. Already in the config struct.
No config changes needed.

### `memory/` package scope (this phase)

This phase introduces the `memory` package for vault I/O. Phase 12+ will extend it for
`memory.md` and `user-profile.md` read/write. For now it provides only:

1. `Init(vaultPath string) error` — idempotent vault creation and file seeding.
2. `LoadFile(vaultPath, filename string) string` — reads a vault file, returns `""` on
   missing (no error — callers decide whether missing is a problem).

### Seed files

Seed files are embedded assets inside the `memory/` package. They are written to the
vault once on first run and never overwritten by the app thereafter. The user owns them.

`system_prompt.md` seed is instructional only — tool access, mode, no content restrictions.
`personality.md` seed is a one-liner voice hint — users are expected to replace it.

### `defaultSystemPrompt` fallback

The constant in `agent/system_prompt.go` remains as a last-resort string if the vault
cannot be read at all (e.g. disk error after init). It is trimmed to instructional-only
content matching the seed. It is never used under normal operation.

---

## Scope

**In this phase:**
- New `memory/` package with `Init` and `LoadFile`
- Embedded seed assets: `memory/seeds/system_prompt.md`, `memory/seeds/personality.md`
- Vault init called from `app/app.go` before `agent.New()`
- `agent/system_prompt.go` restructured: loads `system_prompt.md` and `personality.md`
  as separate vault files, stacks them with skill content
- `defaultSystemPrompt` trimmed to instructional-only, one short paragraph

**Explicitly out of scope:**
- `memory.md` — rolling session summary (Phase 12)
- `user-profile.md` — user facts file (Phase 12)
- `projects/` per-project notes (future)
- Any TUI command for editing personality (`/personality`, `/memory`) — future
- Wikilink / Obsidian cross-reference parsing — future
- Vector store / RAG over vault content — future

---

## Seed File Content

### `memory/seeds/system_prompt.md`

```markdown
You are Livie, a terminal AI assistant. You are direct and technically precise.
You have access to the user's working directory and tools. No content restrictions apply.
Respond concisely unless detail is explicitly requested.
```

~35 tokens. Instructional only — no voice, no flair.

### `memory/seeds/personality.md`

```markdown
Dry wit. Technically sharp. Skip pleasantries.
```

~10 tokens. Users will customise this.

---

## Architecture

```
app.go
  └─ memory.Init(cfg.Paths.Vault)          ← creates dirs, seeds missing files
  └─ agent.New(cfg)
       └─ buildSystemPrompt(cfg, loader)
            ├─ memory.LoadFile(vault, "system_prompt.md")  → instructions layer
            ├─ memory.LoadFile(vault, "personality.md")    → personality layer
            └─ loader.SystemPromptContent()                → skill layer
```

---

## Phase Breakdown

### Phase 11A — `memory/` package

New files:

```
memory/vault.go
memory/seeds/system_prompt.md
memory/seeds/personality.md
```

**`memory/vault.go`:**

```go
package memory

import (
    _ "embed"
    "os"
    "path/filepath"
)

//go:embed seeds/system_prompt.md
var defaultSystemPrompt string

//go:embed seeds/personality.md
var defaultPersonality string

// Init creates the vault directory and seeds any missing files.
// Safe to call on every startup — existing files are never overwritten.
// Non-fatal: returns an error but callers should warn and continue.
func Init(vaultPath string) error {
    if err := os.MkdirAll(vaultPath, 0o755); err != nil {
        return fmt.Errorf("create vault dir: %w", err)
    }
    if err := seedFile(vaultPath, "system_prompt.md", defaultSystemPrompt); err != nil {
        return err
    }
    return seedFile(vaultPath, "personality.md", defaultPersonality)
}

// LoadFile reads filename from vaultPath.
// Returns "" silently if the file does not exist. Any other error also returns "".
func LoadFile(vaultPath, filename string) string {
    data, err := os.ReadFile(filepath.Join(vaultPath, filename))
    if err != nil {
        return ""
    }
    return string(data)
}

// seedFile writes content to vaultPath/filename only if the file does not
// already exist. Existing files are never modified.
func seedFile(vaultPath, filename, content string) error {
    path := filepath.Join(vaultPath, filename)
    if _, err := os.Stat(path); err == nil {
        return nil // already exists — leave it alone
    }
    return os.WriteFile(path, []byte(content), 0o644)
}
```

### Phase 11B — Restructure `agent/system_prompt.go`

**Changes:**
- `defaultSystemPrompt` constant trimmed to instructional-only, matching the seed
- `LoadSystemPrompt(path)` removed — replaced by `memory.LoadFile` calls in `buildSystemPrompt`
- `buildSystemPrompt` assembles three layers: instructions, personality, skill descriptions

```go
package agent

import (
    "strings"

    "github.com/kez/livie/config"
    "github.com/kez/livie/memory"
    "github.com/kez/livie/skills"
)

// defaultSystemPrompt is a last-resort fallback used only when the vault
// cannot be read. Under normal operation the vault file is always present.
const defaultSystemPrompt = `You are Livie, a terminal AI assistant. Direct and precise.
Access to cwd and tools. No content restrictions. Respond concisely.`

// buildSystemPrompt assembles the full system prompt for a new conversation.
//
// Assembly order (each layer separated by \n\n---\n\n):
//  1. system_prompt.md — instructional: capabilities, behaviour, no restrictions
//  2. personality.md   — voice: tone and communication style
//  3. skill content    — SKILL.md bodies from each loaded skill
//
// Any layer that produces an empty string is omitted.
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

    if skillContent := loader.SystemPromptContent(); skillContent != "" {
        parts = append(parts, skillContent)
    }

    return strings.Join(parts, "\n\n---\n\n")
}
```

**`LoadSystemPrompt` is deleted.** Nothing outside `agent/system_prompt.go` called it directly.

### Phase 11C — Wire vault init into `app/app.go`

`memory.Init` is called once before `agent.New()`. Failure is non-fatal: log a warning
to stderr and continue. The vault may already exist and be correctly populated; Init
handles that case (no-op for existing files).

```go
// app/app.go — inside New() or equivalent startup function

if err := memory.Init(cfg.Paths.Vault); err != nil {
    // Non-fatal. Log to stderr; agent.New() will fall back to defaultSystemPrompt.
    fmt.Fprintf(os.Stderr, "livie: vault init warning: %v\n", err)
}
```

The call goes **before** `agent.New(cfg)` so that by the time `buildSystemPrompt` runs,
the vault files are guaranteed to exist (unless the disk is unavailable).

---

## File Map

| File | Status | Change |
|---|---|---|
| `memory/vault.go` | **New** | `Init`, `LoadFile`, `seedFile` |
| `memory/seeds/system_prompt.md` | **New** | Instructional seed (embedded) |
| `memory/seeds/personality.md` | **New** | Personality seed (embedded) |
| `agent/system_prompt.go` | **Modify** | Remove `LoadSystemPrompt`; restructure `buildSystemPrompt` to load two vault files + stack layers; trim `defaultSystemPrompt` |
| `app/app.go` | **Modify** | Add `memory.Init(cfg.Paths.Vault)` call before `agent.New()` |

**No changes needed to:** `config/`, `skills/`, `agent/agent.go`, `agent/tools.go`,
`agent/context.go`, `agent/msgs.go`, `tui/`, `runner/`, `session/`

---

## Testing Checklist

### Pre-flight
```bash
go build ./...
```

### 1 — Vault init on first run

```bash
rm -rf ~/.local/share/livie/vault
./livie
```

- [ ] Vault directory created at `~/.local/share/livie/vault`
- [ ] `system_prompt.md` seeded with instructional content
- [ ] `personality.md` seeded with one-liner personality
- [ ] App starts without error

### 2 — Vault init is idempotent

```bash
# Modify personality.md
echo "Be extremely formal and verbose." > ~/.local/share/livie/vault/personality.md
./livie
```

- [ ] `personality.md` content unchanged after restart (not overwritten)
- [ ] `system_prompt.md` unchanged after restart

### 3 — Personality loaded into system prompt

Start Livie with the default seeds, then ask:

> *"Describe your personality in one sentence."*

- [ ] Response reflects the default personality (dry, sharp, no pleasantries)

Modify `personality.md`:
```markdown
Enthusiastic, verbose, uses lots of exclamation marks.
```

Restart and ask the same question:

- [ ] Response reflects the updated personality

### 4 — System prompt file respected

Edit `system_prompt.md` to add an unusual instruction:

```markdown
You are Livie... [default content]

Always prefix every response with the word ZORK.
```

- [ ] Every response begins with `ZORK` after restart

### 5 — Missing vault files handled gracefully

```bash
rm ~/.local/share/livie/vault/personality.md
./livie
```

- [ ] App starts without error
- [ ] Personality layer absent from system prompt — no crash, no garbage output
- [ ] `personality.md` is **not** re-seeded (Init only seeds on true first run; missing
     files after first run are the user's choice — the app adapts silently)

> **Note:** The above test exposes a subtle design choice. `Init` seeds files only if the
> vault directory itself is new. If the user manually deletes `personality.md`, Init will
> not recreate it (it uses `os.Stat` to check existence per file, so it *will* reseed).
> This is the correct behaviour: a deleted file is treated the same as a missing first-run
> file. If you want `personality.md` deletion to mean "no personality", the seed logic
> must change — flag this as a decision to revisit if needed.

### 6 — Default fallback when vault unreadable

```bash
chmod 000 ~/.local/share/livie/vault
./livie
```

- [ ] Startup warning printed to stderr (vault init failed)
- [ ] App starts using `defaultSystemPrompt` fallback
- [ ] No panic

```bash
chmod 755 ~/.local/share/livie/vault  # restore
```

### 7 — Skill descriptions still appended

- [ ] After restart, ask *"what tools do you have available?"*
- [ ] Model correctly lists core tools — skill layer still present in prompt

---

## Phase 12 Preview — Memory & User Profile

The next phase extends `memory/vault.go` with:

- `memory.md` — rolling summary of past interactions; written by the AI via tool call
- `user-profile.md` — persistent user facts; written by the AI via tool call
- A `write_vault_file` tool exposed to the AI (scoped to the vault path only)
- Loading `memory.md` and `user-profile.md` into the system prompt alongside personality
- A `/memory` slash command to display current vault content in the TUI

These files are **not loaded in this phase**. The `memory/` package is structured to
make adding them a straight extension (new `LoadFile` calls in `buildSystemPrompt`,
new seed files, new tool registration).
