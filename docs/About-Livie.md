# Livie — Local AI Assistant

> A terminal-native AI assistant built for power users who want full control over their models, memory, and machine.

---

## What Is Livie?

Livie is a TUI-first AI assistant written in Go using the [Bubbletea V2](https://github.com/charmbracelet/bubbletea) framework. It runs entirely inside the terminal and is designed to be fast, composable, and unconstrained. Livie connects to any OpenAI-compatible API endpoint — remote or local — and can run its own GGUF models directly inside the terminal via an embedded model runner.

Livie is not a chatbot wrapper. It is a living assistant that can execute commands, manage skills, index your media, remember context across sessions, and modify its own source code and configuration. It is designed to grow with use.

---

## Core Design Principles

- **Terminal-native** — The entire experience lives in the terminal. No browser, no Electron, no background daemons (unless explicitly started).
- **OpenAI-compatible first** — All model communication uses the OpenAI chat completions API format. This means Livie works with OpenAI, Groq, Together, Mistral, local llama-server, Ollama, LM Studio, and any compatible endpoint out of the box.
- **Composable via skills** — Capabilities are added as discrete, downloadable skill units. Core behaviour is itself a skill.
- **Uncensored and unrestricted** — No content filtering layer. The user is responsible for what they ask and what models they run.
- **Self-aware** — Livie has access to its own source, configuration, and skill files and is capable of modifying them at the AI's or user's direction.

---

## Tech Stack

| Layer | Choice |
|---|---|
| Language | Go |
| TUI Framework | [Bubbletea V2](https://github.com/charmbracelet/bubbletea) |
| TUI Styling | [Lipgloss](https://github.com/charmbracelet/lipgloss) |
| TUI Components | [Bubbles](https://github.com/charmbracelet/bubbles) |
| Model API | OpenAI-compatible REST (configurable base URL) |
| Local Model Runner | `llama-server` (llama.cpp) — managed subprocess |
| Vector Store | [chromem-go](https://github.com/philippgille/chromem-go) (in-process, pure Go, no CGo) |
| Memory / Personality | Obsidian vault — plain Markdown files |
| Config Format | TOML |

---

## Feature Overview

### 1. Interaction Modes

Livie operates in two primary modes, switchable at runtime:

**Answer Mode**
The default conversational mode. The user types a prompt, Livie responds. Streaming output is supported. The assistant has access to its tool-call layer in this mode.

**Bash Execution Mode**
The AI generates shell commands and, with user confirmation or in auto-execute mode, runs them in the current working directory. Output is captured and fed back into context. This enables agentic task completion — the assistant can work iteratively on real system tasks.

Both modes share the same context window and history. Switching modes does not reset the conversation.

---

### 2. HUD (Heads-Up Display)

A persistent information bar rendered at the top or bottom of the terminal viewport. Always visible regardless of mode or scroll position.

**HUD elements include:**
- Active model name and endpoint label (e.g. `gpt-4o @ openai` or `llama-3-8b @ local`)
- Current context token usage vs. model context limit (e.g. `4,821 / 32,768 tokens`)
- Working directory path (truncated intelligently)
- Active mode indicator (`ANSWER` / `BASH`)
- Local model runner status when applicable (`llama-server: running`)
- Active skill set count
- Any pending tool calls or background operations

The HUD is minimal by default and can be expanded for more detail.

---

### 3. Skills System

Skills are the primary extension mechanism for Livie. A skill is a self-contained unit that may include:

- A `SKILL.md` description file (what the skill does, how to invoke it, what tools it exposes)
- Tool definitions the AI can call (function schemas in JSON/TOML)
- Executable handlers or scripts the tool calls invoke
- Optional configuration or state files

**Skill capabilities:**
- Skills can be installed locally from a path or downloaded from a remote source (URL, git repo)
- The AI reads active skill descriptions at context load time so it knows what it can do
- The AI can invoke skill tools during a response using standard tool-call format
- Skills can be enabled, disabled, or removed at runtime
- A built-in meta-skill describes Livie itself: its keybindings, modes, configuration options, and how to use it effectively

**The self-skill (Livie's own documentation skill)** is always loaded. It gives the AI a full picture of the application it is operating inside.

---

### 4. AI Tool Calling

Livie supports the OpenAI tool-call (function calling) format. When a model returns a tool call:

1. Livie intercepts the tool call before displaying a response
2. It matches the tool name against registered skill handlers
3. The handler executes (this may be a local binary, a script, a Go function, or a shell command)
4. The result is injected back into context as a tool result message
5. The model continues generating with the result in context

This is used by skills to expose structured actions to the AI: reading files, writing files, running commands, querying the vector store, fetching URLs, etc.

Multi-step tool use within a single turn is supported (the loop continues until the model produces a non-tool response).

---

### 5. Self-Modification

Livie is designed to be modified by Livie. The AI has access to:

- Its own source code directory (readable and writable via file tools)
- Its skill definitions and handlers
- Its configuration files
- Its memory and personality files (see below)

This means the AI can, when asked or when it determines it necessary:
- Add a new skill or extend an existing one
- Modify its own configuration
- Write new Go source files and trigger a rebuild
- Update its own documentation and memory

**This is a deliberate and first-class capability, not an afterthought.** The self-modification loop is how Livie is expected to evolve during use.

---

### 6. Memory and Personality

Livie maintains persistent context about itself, its user, and ongoing projects through a set of Markdown files stored in a dedicated Obsidian vault, created fresh on first run.

**File types:**

| File | Purpose |
|---|---|
| `personality.md` | Livie's tone, preferences, and behavioural traits |
| `memory.md` | Rolling summary of past interactions, user preferences, and learned facts |
| `user-profile.md` | Information about the user — name, projects, habits, goals |
| `projects/` | Per-project notes and context snippets |

Files are written in Obsidian-compatible Markdown with YAML frontmatter where appropriate. Wikilinks (`[[...]]`) are supported for cross-referencing. The vault is a standard Obsidian vault and can be opened in Obsidian directly at any time.

On first run, Livie initialises the vault at a configurable path (default: `~/.local/share/livie/vault`) and seeds it with default `personality.md` and blank memory files.

At session start, relevant memory files are loaded into the system prompt context. The AI is expected to update memory files during or after sessions when new meaningful information is encountered.

---

### 7. Media Indexing and RAG

Livie includes a local media indexing pipeline using [chromem-go](https://github.com/philippgille/chromem-go) as the vector store — an in-process, pure Go library with no external dependencies, no subprocess, and no CGo. It persists its index to disk between sessions.

**Indexing scope (configurable):**
- Documents: PDF, Markdown, plain text, EPUB
- Code files
- Images (captioned via Gemma 4's native vision capability)
- Audio/video transcripts (via Whisper or compatible)

**Pipeline:**
1. Files are chunked and embedded using Gemma 4's native embedding capability via `llama-server` (`/v1/embeddings`)
2. Embeddings and metadata are stored in chromem-go, persisted to `~/.local/share/livie/index`
3. At query time, the user's message is embedded and nearest-neighbour chunks are retrieved
4. Retrieved context is injected into the prompt before the model generates a response

The AI can also directly invoke the vector search as a tool call, enabling it to decide when to search based on the query rather than always retrieving.

Indexing runs as a background process and does not block the TUI.

---

### 8. GGUF Model Runner

Livie includes integrated management of a local GGUF model runner based on `llama-server` (from llama.cpp).

**Behaviour:**
- `llama-server` is auto-downloaded from the official llama.cpp GitHub releases on first use if not found on `$PATH`
- The correct binary for the host platform (Linux/macOS, CPU/CUDA/Metal) is selected automatically
- Livie starts, stops, and restarts `llama-server` as a managed subprocess from within the TUI
- The server exposes an OpenAI-compatible endpoint locally (`http://localhost:{port}`), which Livie targets like any other configured endpoint
- GGUF model files are pointed to via config or selected interactively from a file picker in the TUI
- The same running `llama-server` instance serves both chat completions (`/v1/chat/completions`) and embeddings (`/v1/embeddings`) for RAG

**Design goals for the runner:**
- Auto-download on first use; zero manual setup
- Minimal resource footprint when idle
- GPU offload layers configurable via Livie config (CUDA, Metal, Vulkan, ROCm all supported by llama.cpp)
- Server stdout/stderr optionally viewable in a dedicated TUI pane
- The HUD reflects running status and loaded model name

**Why llama-server over alternatives:**
- The CGo binding alternatives (`go-llama.cpp` etc.) run inference inline but break cross-compilation, require a C++ toolchain and GPU headers at build time, and are significantly less maintained
- Ollama as a subprocess adds a full service layer with its own model management — unnecessary overhead
- llama-server is purpose-built, actively maintained by the llama.cpp team, supports every major GPU backend, and its OpenAI-compatible endpoint means the rest of Livie's code is agnostic to whether inference is local or remote
- No Python, no Docker, no external runtime dependencies

---

## Application Structure (High Level)

```
livie/
├── main.go                  # Entry point
├── tui/                     # Bubbletea models, views, key bindings
│   ├── app.go               # Root model
│   ├── hud.go               # HUD component
│   ├── chat.go              # Chat view
│   └── ...
├── agent/                   # Core AI loop, tool call handling, context management
├── skills/                  # Built-in skills and skill loader
│   └── livie-self/          # The self-describing meta-skill
├── runner/                  # llama-server subprocess manager
├── memory/                  # Obsidian vault read/write
├── index/                   # Media indexing pipeline and vector store interface
├── config/                  # Config loading (TOML)
├── docs/                    # This file and other documentation
└── vault/                   # Default vault location (symlink or embedded default)
```

---

## Configuration

Livie is configured via a TOML file, located by default at `~/.config/livie/config.toml`.

Key configuration areas:
- Endpoint definitions (name, base URL, API key, default model)
- Active endpoint and model selection
- Vault path
- Media index paths and inclusion/exclusion rules
- Skill directories
- Runner settings (llama-server binary path, default model path, GPU layers)
- HUD layout preferences
- Behaviour flags (auto-execute bash, confirm tool calls, etc.)

---

## Resolved Decisions

| # | Question | Decision |
|---|---|---|
| 1 | Vector store | **chromem-go** — in-process, pure Go, no CGo, persists to disk. Zero external dependencies. |
| 2 | Embedding model | **Gemma 4 natively** via llama-server `/v1/embeddings` — Gemma 4 has native embedding support, same model, no second process |
| 3 | Obsidian vault | **Fresh vault** created on first run at `~/.local/share/livie/vault` |
| 4 | Skill registry | **URL / local path install** for now |
| 5 | Self-modification rebuild | **Manual** — AI modifies files, user triggers rebuild |
| 6 | llama-server distribution | **Auto-download** from llama.cpp GitHub releases on first use, platform-aware |
| 7 | Multi-modal | **In scope** — Gemma 4 is natively multimodal; image input supported from the start |
