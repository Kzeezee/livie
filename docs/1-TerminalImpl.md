# Livie — Terminal Implementation Plan
### Phase 1: TUI Foundation

> This document covers the design, architecture, and implementation plan for the Livie terminal application shell. It is intentionally scoped to the TUI layer only — AI backends, skills, memory, RAG, and the model runner are acknowledged as future phases and are designed around, but not implemented here.

---

## 1. Overview & Goals

The terminal app is the face of Livie. It must feel like a tool built by someone who cares — not a demo. The design language is **sleek, dark, and precise**: high-information density without clutter, purposeful animation without noise, and a colour system anchored to the primary `#2B2D42` palette.

At the end of Phase 1, running `livie` in any terminal on Arch Linux, macOS, or Windows should:

1. Render a **welcome screen** (neofetch-style, with ASCII portrait and system summary)
2. Optionally show a **setup screen** (skippable — reserved for auto-install tooling)
3. Drop into the **main chat interface** with a live HUD, input bar, and message history
4. Support **mode switching** (Query ↔ Bash) via keyboard shortcut
5. Respond to **/commands** typed in the input bar
6. Support **ASCII art and image embedding** in the message stream
7. Be fully navigable by keyboard with no mouse dependency

---

## 2. Design System

### 2.1 Colour Palette

All colours are defined as Lipgloss `AdaptiveColor` constants in `tui/theme.go`. The palette is built around `#2B2D42` as the signature mid-tone.

| Token | Hex | Role |
|---|---|---|
| `ColorBase` | `#1A1B2E` | Terminal background (very dark navy) |
| `ColorSurface` | `#2B2D42` | Panel backgrounds, borders, HUD fill — **primary brand colour** |
| `ColorSurfaceHi` | `#3D3F5C` | Elevated surfaces, selected rows, hover states |
| `ColorBorder` | `#4A4D6A` | Border strokes, dividers |
| `ColorTextPrimary` | `#E2E8F0` | Primary readable text |
| `ColorTextSecondary` | `#8D99AE` | Labels, metadata, dim info |
| `ColorTextMuted` | `#4A5568` | Placeholder text, disabled states |
| `ColorAccentCyan` | `#4CC9F0` | Query mode indicator, links, cursor highlight |
| `ColorAccentRose` | `#E94560` | Bash mode indicator, errors, destructive actions |
| `ColorAccentAmber` | `#F6A623` | Warnings, pending states, tool-call indicators |
| `ColorAccentGreen` | `#68D391` | Success states, running indicators |
| `ColorAccentPurple` | `#9B72CF` | Skill invocations, AI tool calls |

### 2.2 Typography (Lipgloss)

Livie uses whatever monospace font the user's terminal is configured with (we do not bundle fonts). However, Lipgloss styles are designed assuming a modern Nerd Font is present for glyphs. We gracefully degrade to ASCII fallbacks if glyphs are unavailable (detected at startup by testing a known glyph codepoint against terminal capabilities).

| Style token | Usage |
|---|---|
| `StyleLabel` | HUD labels, section headers — UPPERCASE, `ColorTextSecondary` |
| `StyleValue` | HUD values — `ColorTextPrimary`, bold |
| `StyleDim` | Timestamps, metadata — `ColorTextMuted`, italic |
| `StyleAccent` | Mode badges, active indicators — background `ColorSurface`, foreground accent colour |
| `StyleBorder` | All panel/box borders — `ColorBorder`, `RoundedBorder` |
| `StyleCode` | Inline code in messages — background `ColorSurface`, `ColorAccentCyan` foreground |
| `StyleCommand` | /command tokens — `ColorAccentPurple`, bold |

### 2.3 Border & Layout Language

- All panels use `lipgloss.RoundedBorder()` styled with `ColorBorder`
- Consistent internal padding: `1 2` (top/bottom: 1, left/right: 2)
- Section dividers: a single `─` line rendered at panel width in `ColorBorder`
- The HUD is a single-line bar — it does **not** use a box border, just a filled background of `ColorSurface`
- Minimum supported terminal width: **80 columns**. At narrow widths, elements collapse gracefully (HUD truncates, ASCII art drops to compact mode)

---

## 3. Project File Structure

```
livie/
├── main.go                        # Entry point — parses flags, starts Bubbletea program
├── tui/
│   ├── app.go                     # Root Bubbletea model; screen state machine
│   ├── theme.go                   # All colour tokens and Lipgloss style definitions
│   ├── keys.go                    # Keybinding definitions (uses bubbles/key)
│   ├── screens/
│   │   ├── welcome.go             # Welcome screen model & view
│   │   ├── setup.go               # Setup screen model & view (stub)
│   │   └── chat.go                # Main chat screen model & view
│   ├── components/
│   │   ├── hud.go                 # HUD bar component
│   │   ├── input.go               # Input bar component (wraps bubbles/textarea)
│   │   ├── messages.go            # Message list / viewport component
│   │   ├── modebadge.go           # Mode indicator widget (QUERY / BASH)
│   │   └── spinner.go             # Spinner for streaming / thinking states
│   └── art/
│       ├── livie_portrait.go      # Embedded ASCII art portrait (const string)
│       └── livie_portrait_sm.go   # Compact version for narrow terminals
├── config/
│   └── config.go                  # Config struct + TOML load/save (stub values for Phase 1)
├── docs/
│   ├── About-Livie.md
│   └── 1-TerminalImpl.md          # This file
└── go.mod / go.sum
```

> **Future phases** will add: `agent/`, `skills/`, `runner/`, `memory/`, `index/`. The `tui/` layer is designed with interfaces so these can be wired in without restructuring the TUI.

---

## 4. Architecture: Bubbletea Model Design

### 4.1 Root App Model (`tui/app.go`)

The root model is a simple **screen state machine**. It holds the current screen and delegates `Init`, `Update`, and `View` to the active screen model.

```go
type Screen int

const (
    ScreenWelcome Screen = iota
    ScreenSetup
    ScreenChat
)

type AppModel struct {
    screen      Screen
    welcome     screens.WelcomeModel
    setup       screens.SetupModel
    chat        screens.ChatModel
    config      *config.Config
    width       int
    height      int
}
```

Screen transitions are triggered by **messages** (not direct state mutation):

```go
type TransitionMsg struct{ To Screen }
```

This keeps the flow testable and unidirectional.

**Startup sequence:**
1. `ScreenWelcome` — always shown, even briefly
2. If first run and setup not skipped → `ScreenSetup`
3. Otherwise → `ScreenChat`

For Phase 1, setup is always skipped. The transition from welcome to chat happens after a configurable dwell time (default: the user presses any key, or after 1.5s auto-advance).

### 4.2 Window Resize Handling

`tea.WindowSizeMsg` is handled at the root model level. Width and height are stored on `AppModel` and propagated to all child models via a `ResizeMsg` or by passing dimensions on every delegation. Components must re-layout on resize.

### 4.3 Message Types

All inter-component communication uses typed messages:

```go
// Navigation
type TransitionMsg struct{ To Screen }

// Mode switching
type ModeChangedMsg struct{ Mode InputMode }

// Commands
type CommandMsg struct {
    Name string
    Args []string
}

// Future: AI response chunk (streaming)
type StreamChunkMsg struct{ Content string }

// Future: Tool call started/completed
type ToolCallMsg struct{ Name string; Status string }
```

---

## 5. Welcome Screen

### 5.1 Layout

The welcome screen is directly inspired by `neofetch` / `fastfetch`. It uses a two-column layout rendered with Lipgloss `lipgloss.JoinHorizontal`.

```
┌─────────────────────────────────────────────────────────────────────┐
│                                                                     │
│   [ASCII PORTRAIT]          livie@arch                              │
│                             ───────────                             │
│   (large format,            OS      Arch Linux x86_64              │
│    ~30 cols wide,           Shell   zsh 5.9                        │
│    ~25 rows tall)           Term    kitty / alacritty               │
│                             Go      go1.22.x                        │
│                             Config  ~/.config/livie/config.toml    │
│                             Vault   ~/.local/share/livie/vault      │
│                             Model   (not configured)               │
│                             Skills  0 loaded                        │
│                             ───────────                             │
│                             A local AI assistant                    │
│                             that lives in your terminal.           │
│                                                                     │
│  ─────────────────────────────────────────────────────────────────  │
│                                                                     │
│   Commands: /help  /skills  /usage  /resume  /clear  /exit         │
│   Keys:     Ctrl+B toggle bash mode   Ctrl+L clear   Ctrl+C quit   │
│                                                                     │
│                        [ Press any key to start ]                  │
│                                                                     │
└─────────────────────────────────────────────────────────────────────┘
```

### 5.2 ASCII Portrait

The portrait is a **fixed ASCII art** string stored as a Go constant in `tui/art/livie_portrait.go`. It depicts Livie as a stylised female figure in a monochrome block-character style (think `jp2a` output meets hand-drawn terminal art).

The portrait is built from characters in the set: `█ ▓ ▒ ░ │ ─ ╭ ╮ ╯ ╰ ● · ` and standard ASCII. Colour is applied via Lipgloss foreground gradients cycling through `#2B2D42` → `#8D99AE` → `#E2E8F0` from dark to light, giving a subtle lit-from-above feel.

A compact (`livie_portrait_sm.go`) version (~15 cols × 12 rows) is substituted when terminal width < 90.

### 5.3 Info Panel

The right info panel uses `StyleLabel` / `StyleValue` pairs rendered in a fixed-width column. The username (`livie@hostname`) is rendered in `ColorAccentCyan`, bold. Field labels are right-aligned within their column, values left-aligned.

Detected at startup:
- OS: read from `/etc/os-release` (Linux), `sw_vers` (macOS), `ver` (Windows)
- Shell: `$SHELL` env var
- Terminal: `$TERM` / `$TERM_PROGRAM`
- Go version: baked in at build time with `-ldflags`
- Config path: resolved config file path (or default if not found)
- Vault path: resolved vault path (or default if not found)
- Model: active model from config (or `(not configured)` if none)
- Skills: count of loaded skills

### 5.4 Footer Strip

Below the two-column block, a single divider line, then a compact **command reference** row and **keybinding reference** row. These use `StyleCommand` for `/command` tokens and `StyleLabel` for key names.

The "Press any key to start" prompt pulses with a Bubbletea tick-based animation (opacity toggled every 600ms using colour interpolation between `ColorTextMuted` and `ColorTextPrimary`).

---

## 6. Setup Screen (Stub)

The setup screen is **not implemented in Phase 1** — it is a structural placeholder.

```go
// screens/setup.go
// SetupModel is a stub that immediately emits TransitionMsg{To: ScreenChat}
// on Init(). Future phases will use this screen for:
//   - Auto-downloading llama-server
//   - Configuring the first model endpoint
//   - Initialising the Obsidian vault
//   - Installing default skills
```

It renders a single centred message: `"Setup coming soon — skipping..."` for 800ms then transitions automatically. This allows the plumbing to exist and be activated later without changing the startup sequence logic.

---

## 7. Chat Screen

The chat screen is the **primary interface**. It is composed of three stacked regions:

```
┌─────────────────────────────────────────────────────────────────────┐
│  HUD BAR                                                            │  ← fixed 1-line bar
├─────────────────────────────────────────────────────────────────────┤
│                                                                     │
│  MESSAGE VIEWPORT                                                   │  ← scrollable, fills space
│  (conversation history)                                             │
│                                                                     │
├─────────────────────────────────────────────────────────────────────┤
│  INPUT BAR                                                          │  ← dynamic height, max 6 lines
└─────────────────────────────────────────────────────────────────────┘
```

Height allocation: `HUD(1) + Input(dynamic) + Viewport(remaining)`

### 7.1 HUD Bar (`tui/components/hud.go`)

A **single-line** bar with `ColorSurface` background spanning the full terminal width. No border. Content is split into left-aligned info and right-aligned info using Lipgloss `lipgloss.PlaceHorizontal`.

**Left segment:**
```
 ◆ livie  │  ~/projects/livie  │  QUERY
```

**Right segment:**
```
gpt-4o @ openai  │  4,821 / 32,768 tok  │  0 skills  
```

| Element | Style | Notes |
|---|---|---|
| `◆ livie` | `ColorAccentCyan`, bold | App name / logo glyph. Nerd Font `nf-md-creation` or `◆` fallback |
| Directory | `ColorTextSecondary` | Truncated: `~/projects/livie` → `~/p/livie` if >20 chars |
| Mode badge | Background `ColorSurface`, text colour = `ColorAccentCyan` (QUERY) or `ColorAccentRose` (BASH) | Padded label: ` QUERY ` or ` BASH ` |
| Dividers `│` | `ColorBorder` | |
| Model | `ColorTextPrimary` | Truncated at 20 chars |
| Endpoint | `ColorTextSecondary` | `@ local` or `@ openai` |
| Token usage | `ColorTextSecondary` | `4,821 / 32,768 tok`. Amber when >80%, rose when >95% |
| Skills count | `ColorTextMuted` | `0 skills` or `3 skills` |

Future HUD additions (wired in later): `llama-server: running`, pending tool calls badge, background indexing spinner.

The HUD component accepts a `HUDState` struct — a plain value type with no AI coupling, so Phase 1 can populate it with stub/default values while future phases inject real data.

```go
type HUDState struct {
    Directory    string
    Mode         InputMode
    ModelName    string
    EndpointName string
    TokensUsed   int
    TokensMax    int
    SkillCount   int
    // Future fields added here without breaking HUD render contract
}
```

### 7.2 Message Viewport (`tui/components/messages.go`)

Uses `bubbles/viewport` as the underlying scroll container. Messages are rendered top-to-bottom, newest at the bottom (chat-style, not log-style).

**Message types rendered in Phase 1:**

| Type | Visual |
|---|---|
| User message | Right-aligned? No — left-aligned with `▶` prefix in `ColorAccentCyan`. Name: `you` in `ColorTextSecondary` |
| Assistant message | Left-aligned, `◆` prefix in `ColorAccentPurple`. Name: `livie` in `ColorAccentPurple` |
| System/info | Centred, dim, italic. `ColorTextMuted`. Used for mode changes, command confirmations |
| Error | Left-aligned, `✕` prefix, `ColorAccentRose` |
| Command echo | Left-aligned, `/command` in `ColorAccentPurple`, args in `ColorTextSecondary` |

**Inline formatting (Phase 1 — rendered statically):**
- Code blocks: wrapped in a `ColorSurface`-background box with a language label, using `StyleCode`
- Inline code: `ColorAccentCyan` on `ColorSurface` background
- `**bold**`, `*italic*` — rendered via Glamour or manual Lipgloss (decision: use [`glamour`](https://github.com/charmbracelet/glamour) with a custom dark theme derived from the colour palette)

**Image embedding:**
- When a message contains an image path or the AI returns an image reference, the viewport renders it as either:
  - **Sixel / Kitty graphics protocol** — if the terminal supports it (detected via `$TERM` and terminal queries at startup). Rendered inline at configurable max width.
  - **ASCII art fallback** — if graphics protocol unavailable, the image is converted to block-character ASCII using a Go `image` → ASCII renderer (lightweight, no cgo). Max width 60 cols.
- Images are rendered inside a rounded border box with a `[image]` caption below
- The capability detection result is stored in `AppModel.termCaps` and consulted by the message renderer

**Scrolling behaviour:**
- Viewport auto-scrolls to bottom on new message
- If user scrolls up manually (mouse wheel or `k`/`↑`), auto-scroll is suspended
- A subtle `↓ new messages` indicator appears at the bottom-right when suspended and new content arrives
- Auto-scroll resumes on `G` or when user scrolls back to bottom

### 7.3 Input Bar (`tui/components/input.go`)

Uses `bubbles/textarea` (single-line mode by default, expands to multi-line with `Shift+Enter`).

```
╭─────────────────────────────────────────────────────────────────────╮
│  ▶  Type a message... (/help for commands)                    [↵]  │
╰─────────────────────────────────────────────────────────────────────╯
```

- Border: `RoundedBorder`, `ColorBorder`. On focus, border colour transitions to `ColorAccentCyan` (query mode) or `ColorAccentRose` (bash mode)
- Prefix glyph: `▶` in mode accent colour
- Placeholder: dim, italic, `ColorTextMuted`
- Submit hint `[↵]` dims to `ColorTextMuted`, brightens to `ColorTextPrimary` when content is non-empty
- When input starts with `/`, the prefix glyph changes to `⌘` in `ColorAccentPurple` as a visual cue that a command is being typed

**Multi-line:** `Shift+Enter` inserts a newline. The input box grows up to 6 lines, then scrolls internally. `Enter` alone always submits.

**Streaming state:** While Livie is generating a response, the input bar is disabled (grayed out, placeholder changes to `"Livie is thinking..."`) and a spinner is shown in place of the submit hint. The user can press `Ctrl+C` to interrupt (generates a `StopStreamMsg`).

---

## 8. Interaction Modes

### 8.1 Mode Definition

```go
type InputMode int

const (
    ModeQuery InputMode = iota  // Default — conversational AI query
    ModeBash                    // AI generates and optionally executes shell commands
)
```

Modes share the same message history and context. Switching does not clear the conversation.

### 8.2 Keybindings for Mode Switching

Modelled closely on Claude Code's UX — shortcut is memorable and quick.

| Key | Action |
|---|---|
| `Ctrl+B` | Toggle between Query and Bash mode |
| `Escape` | Return to Query mode if in Bash mode |

On mode switch:
- HUD badge updates immediately
- Input border colour transitions (Lipgloss animated via tick messages — 3-step colour blend over 150ms)
- A system message is appended to the viewport: `— switched to BASH mode —` or `— switched to QUERY mode —` in dim style
- A soft animation: the mode badge briefly scales via padding change (1 tick flash to `ColorSurface` then back)

### 8.3 Full Keybinding Reference

| Key | Context | Action |
|---|---|---|
| `Enter` | Input | Submit message / command |
| `Shift+Enter` | Input | Insert newline (multi-line input) |
| `Ctrl+B` | Global | Toggle Query / Bash mode |
| `Ctrl+L` | Global | Clear message history (with confirmation prompt) |
| `Ctrl+C` | Streaming | Interrupt AI response |
| `Ctrl+C` (twice) | Idle | Quit application |
| `Escape` | Global | Cancel input / return to Query mode |
| `↑` / `k` | Viewport | Scroll up |
| `↓` / `j` | Viewport | Scroll down |
| `G` | Viewport | Jump to bottom (resume auto-scroll) |
| `g` | Viewport | Jump to top |
| `Ctrl+U` | Input | Clear input field |
| `Tab` | Command | Autocomplete /command name (Phase 2) |
| `↑` | Input (empty) | Recall previous user message |
| `F1` / `?` | Global | Show keybinding help overlay |

Keybindings are defined as `bubbles/key.Binding` instances in `tui/keys.go` and attached to a `keyMap` struct, making them remappable later via config.

---

## 9. Commands System

### 9.1 Design

When the user submits input beginning with `/`, it is intercepted **before** being sent to the AI. The command name is parsed and dispatched to a registered handler.

```go
type Command struct {
    Name        string
    Aliases     []string
    Description string
    Handler     func(args []string, m *ChatModel) tea.Cmd
}
```

Commands are registered in a `CommandRegistry` (a `map[string]*Command`) in `tui/screens/chat.go`. This registry is the extension point for future phases — skills and built-in Go functions both register here.

### 9.2 Command Dispatch Flow

1. User types `/clear` and presses Enter
2. Input bar calls `ParseCommand("/clear")` → `{Name: "clear", Args: []}`
3. `ChatModel.Update` receives `CommandMsg{Name: "clear"}`
4. Registry lookup finds the handler
5. Handler returns a `tea.Cmd` (may be a pure UI update, an async operation, or a future skill invocation)
6. Command echo message is appended to viewport: `⌘ /clear`

Unknown commands: a system error message is appended — `Unknown command: /foo. Type /help for a list.`

### 9.3 Phase 1 Commands (Implemented)

| Command | Description | Handler type |
|---|---|---|
| `/help` | Show all available commands with descriptions | Pure UI — renders a formatted list in the viewport |
| `/clear` | Clear message history (with y/n confirmation) | UI mutation |
| `/mode [query\|bash]` | Explicitly set mode | Emits `ModeChangedMsg` |
| `/exit` or `/quit` | Quit the application | Emits `tea.Quit` |
| `/version` | Show Livie version and build info | Pure UI |
| `/about` | Show the welcome screen again | Emits `TransitionMsg{To: ScreenWelcome}` |

### 9.4 Phase 2+ Commands (Stubs in Phase 1)

These are registered as stubs that print a `"Coming soon"` message, so the command names are reserved and discoverable via `/help`:

| Command | Future purpose |
|---|---|
| `/skills` | List, install, enable, disable skills |
| `/usage` | Show token usage, cost estimate, session stats |
| `/resume` | Resume a previous conversation session |
| `/model [name]` | Switch active model |
| `/endpoint [name]` | Switch active API endpoint |
| `/memory` | View/edit Livie's memory files |
| `/index` | Manage the media index |
| `/run` | Trigger the llama-server runner |
| `/config` | Open config in editor |

The stub implementation pattern:

```go
var cmdSkills = &Command{
    Name:        "skills",
    Description: "Manage Livie skills (coming in Phase 2)",
    Handler: func(args []string, m *ChatModel) tea.Cmd {
        return appendSystemMsg(m, "Skills system coming soon. Stay tuned.")
    },
}
```

---

## 10. Image & ASCII Art Embedding

### 10.1 Terminal Capability Detection

At startup, the app probes terminal capabilities and stores results in:

```go
type TermCaps struct {
    HasSixel     bool
    HasKitty     bool   // Kitty graphics protocol
    HasTrueColor bool
    HasNerdFont  bool   // Heuristic: test a known glyph codepoint
    Width        int
    Height       int
}
```

Detection methods:
- **Kitty**: check `$TERM == "xterm-kitty"` or `$KITTY_WINDOW_ID`
- **Sixel**: send `\033[c` (DA1 query) and check response for `4` in the list
- **True colour**: check `$COLORTERM == "truecolor"` or `"24bit"`
- **Nerd Font**: render a test glyph and compare rendered width using `x/term`

### 10.2 Inline Image Rendering

Images can appear in the message stream in two ways:

**a) User-supplied (Phase 2 — multimodal input):** User drags a file or pastes a path. For now, Phase 1 only renders images that appear as content in messages (no upload UI yet).

**b) AI-referenced (Phase 2):** The AI returns an image URL or file path as part of a tool call result.

For Phase 1, the image renderer is implemented as a standalone component `tui/components/image.go` that accepts a file path and renders it, but it is only exercised by a test `/image <path>` command (Phase 1 dev tool, not user-facing).

**Rendering pipeline:**
```
FilePath → Decode (Go stdlib image package) → 
  if HasKitty → encode as Kitty protocol escape sequence
  if HasSixel → encode as Sixel escape sequence  
  else        → convert pixels to block chars (▀ ▄ █) + Lipgloss 24-bit colours
→ Embed in message viewport as a fixed-height block
```

The block character fallback (using `▀` and `▄` with foreground/background colours to get 2 pixels per character row) provides a clean ASCII-native result that looks intentional, not broken.

### 10.3 ASCII Art in Messages

The message renderer detects fenced code blocks annotated with ` ```ascii ` or ` ```art ` and renders them with the `ColorSurface` background + `ColorAccentCyan` foreground, preserving exact whitespace.

The welcome screen portrait is a static constant — no runtime generation needed.

---

## 11. Dependencies

Add to `go.mod`:

```
github.com/charmbracelet/bubbletea     v0.27+
github.com/charmbracelet/bubbles       v0.20+
github.com/charmbracelet/lipgloss      v1.0+
github.com/charmbracelet/glamour       v0.8+
github.com/muesli/termenv              v0.15+
golang.org/x/term                      latest
```

> All Charmbracelet library versions should be locked to the latest stable at init time. `termenv` is pulled in transitively but pinned explicitly for capability detection.

**No CGo. No system libraries. No non-Go build steps.** The entire TUI compiles with `go build ./...`.

---

## 12. Multi-Platform Considerations

| Concern | Approach |
|---|---|
| Windows terminal | Use `os.Getenv("WT_SESSION")` / `ConEmu` detection. Disable Sixel/Kitty, fall back to block chars. Windows Console supports true colour in Windows Terminal. |
| macOS | iTerm2 supports Sixel and inline images. Terminal.app does not. Detection via `$TERM_PROGRAM`. |
| Arch Linux (primary target) | Full Kitty/Sixel support assumed in kitty/alacritty/foot. Nerd Font assumed present. |
| Terminal width < 80 | HUD collapses: hide token count and skill count, truncate directory more aggressively. Portrait uses `livie_portrait_sm`. |
| No true colour | Lipgloss `AdaptiveColor` fallbacks to 256-colour and 16-colour approximations automatically. |

Build targets:

```makefile
build-linux:   GOOS=linux  GOARCH=amd64 go build -ldflags="-s -w -X main.Version=$(VERSION)" -o livie ./
build-mac:     GOOS=darwin GOARCH=arm64 go build ...
build-windows: GOOS=windows GOARCH=amd64 go build ...
```

---

## 13. Implementation Phases (within Phase 1)

### Step 1 — Scaffold & Theme (Est. 1–2h)
- `go mod init github.com/yourusername/livie`
- Add all dependencies
- Create `tui/theme.go` with full colour palette and all Lipgloss style definitions
- Create `tui/keys.go` with all keybindings
- Create stub `main.go` that starts a Bubbletea program and renders "hello livie"

### Step 2 — Welcome Screen (Est. 2–3h)
- Design and embed ASCII portrait in `tui/art/livie_portrait.go`
- Implement `screens/welcome.go` — two-column neofetch layout
- Implement OS/shell/term detection helpers
- Add blinking "Press any key" prompt with tick animation
- Wire transition to chat screen on keypress

### Step 3 — Setup Screen Stub (Est. 30m)
- Implement `screens/setup.go` as an auto-transitioning stub
- Wire into startup sequence with `config.IsFirstRun()` check

### Step 4 — HUD Component (Est. 1–2h)
- Implement `components/hud.go` with `HUDState` struct
- Render all HUD elements with proper truncation
- Wire `tea.WindowSizeMsg` for responsive truncation

### Step 5 — Message Viewport (Est. 2–3h)
- Implement `components/messages.go` wrapping `bubbles/viewport`
- Implement all message type renderers
- Implement Glamour integration for markdown
- Implement scroll suspend / "new messages" indicator
- Add `tui/art/livie_portrait.go` — actual ASCII art design

### Step 6 — Input Bar (Est. 1–2h)
- Implement `components/input.go` wrapping `bubbles/textarea`
- Implement mode-aware border colour
- Implement `/` prefix detection for command glyph
- Implement multi-line behaviour
- Implement streaming disabled state + spinner

### Step 7 — Chat Screen Assembly (Est. 1–2h)
- Implement `screens/chat.go` composing HUD + Viewport + Input
- Implement height allocation and resize handling
- Wire mode switching (Ctrl+B) with animation
- Wire all keybindings

### Step 8 — Commands System (Est. 1–2h)
- Implement `CommandRegistry` and `ParseCommand`
- Implement all Phase 1 commands
- Implement stub commands for Phase 2 features
- Implement command echo rendering

### Step 9 — Image Renderer (Est. 2h)
- Implement `TermCaps` detection at startup
- Implement `components/image.go` with block-char fallback renderer
- Implement Kitty protocol encoder
- Wire into message renderer
- Add `/image` dev command for testing

### Step 10 — Polish & Cross-Platform Testing (Est. 1–2h)
- Test on Arch Linux (primary)
- Test narrow terminal widths (80, 100 cols)
- Test no-truecolor terminal (`COLORTERM=` unset)
- Test no-Nerd-Font terminal (glyph fallbacks)
- Verify Windows build compiles (even if not runtime-tested)
- Final Lipgloss style pass — check every rendered element for spacing and alignment

---

## 14. Future Phase Hooks

These are design decisions made now that leave clean seams for future phases:

| Hook | Where | For |
|---|---|---|
| `HUDState` is a plain value struct injected into HUD | `hud.go` | Phase 2 wires in live AI state (tokens, model, etc.) |
| `CommandRegistry` is a map, not hardcoded | `chat.go` | Phase 2 skills register their own commands |
| `TermCaps` is computed at startup and passed around | `app.go` | Phase 2 multimodal input uses it for image upload |
| `ModeChangedMsg` is a typed tea.Msg | `app.go` | Phase 2 AI backend subscribes to mode for prompt shaping |
| `StopStreamMsg` is defined but unused | `chat.go` | Phase 2 streaming response handler listens for it |
| `TransitionMsg` drives screen changes | `app.go` | Setup screen will be activated by Phase 2 (first-run detection) |
| `config.Config` struct exists with stub load | `config/config.go` | Phase 2 TOML loading populates HUD state, model config, etc. |
| Message type field in message structs | `messages.go` | Phase 2 adds `TypeToolCall`, `TypeStreaming`, `TypeImage` |

---

## 15. Open Questions (to resolve before/during implementation)

| # | Question | Default answer for Phase 1 |
|---|---|---|
| 1 | Should the welcome screen auto-advance after 1.5s or only on keypress? | **Keypress only** — feels more intentional |
| 2 | HUD at top or bottom? | **Top** — consistent with most TUI tools (lazygit, htop), keeps input at very bottom |
| 3 | Should `/clear` require confirmation? | **Yes** — single `y` keypress, shown as inline prompt in viewport |
| 4 | ASCII portrait: hand-crafted or auto-generated from a reference image? | **Hand-crafted** — bespoke, memorable, intentional |
| 5 | Glamour for markdown, or custom Lipgloss renderer? | **Glamour** with custom theme — less maintenance, better output |
| 6 | Should Ctrl+C quit immediately or require double-press? | **Double-press** (within 500ms) — prevents accidental quit |

