# livie

> A terminal-native AI assistant for power users who want full control over their models, memory, and machine.

![Livie chat interface](media/image.png)

---

## What is Livie?

Livie is a TUI-first AI assistant written in Go. It runs entirely inside the terminal and is designed to be fast, composable, and unconstrained. Livie connects to any OpenAI-compatible API endpoint — remote or local — and can manage its own GGUF model runner directly from within the TUI via an embedded `llama-server` manager.

It is not a chatbot wrapper. It is a living assistant that can execute commands, manage skills, remember context across sessions, and eventually modify its own source and configuration. It is designed to grow with use.

---

## Features

### Dual Interaction Modes
Switch at runtime between **Chat mode** (conversational AI) and **Bash mode** (AI-generated shell commands, with confirmation before execution). Both modes share the same context window — switching modes does not reset the conversation.

### Persistent HUD
A status bar fixed to the top of the terminal showing active model, endpoint, working directory, current mode, token usage, and local runner state. Always visible regardless of scroll position.

### Local Model Runner
Livie manages `llama-server` (from [llama.cpp](https://github.com/ggerganov/llama.cpp)) as a subprocess, exposing a local OpenAI-compatible endpoint. The binary is auto-downloaded from the official llama.cpp GitHub releases on first use — no manual setup required. GPU offload is configurable (CPU, CUDA, Metal, Vulkan).

### OpenAI-Compatible Endpoints
Configure multiple named endpoints and switch between them at runtime. Works out of the box with OpenAI, Groq, Together, Mistral, Ollama, LM Studio, and any server with a compatible API.

### Session Persistence
Conversations are saved to disk and can be resumed later via `/resume`. Sessions are indexed by timestamp and a short summary.

### Tool Calling
Supports the OpenAI tool-call (function calling) format. When a model returns a tool call, Livie intercepts it, dispatches to the registered skill handler, injects the result back into context, and continues the response loop — transparently, within a single turn.

### Skills System *(in development)*
Capabilities are added as discrete, installable skill units. Each skill provides a `SKILL.md` description and optional tool definitions that the AI can invoke. Skills can be enabled, disabled, and installed from local paths or remote URLs.

### Memory and Personality *(in development)*
Livie maintains persistent context across sessions through an Obsidian-compatible Markdown vault (`~/.local/share/livie/vault`). Memory files track user preferences, ongoing projects, and learned facts across conversations.

### Autocomplete
Slash-command autocomplete with full tab-navigation, including nested sub-argument suggestions (e.g. `/run start`, `/endpoint list`).

---

## Installation

### Prerequisites

- Go 1.22+
- A terminal with true colour support (e.g. Kitty, WezTerm, iTerm2, Ghostty)

### Build from Source

```bash
git clone https://github.com/kez/livie
cd livie
go build -o livie .
./livie
```

On first run, Livie will launch a setup wizard to configure your model and endpoint.

### Model

Place a GGUF model file in a `model/` directory next to the binary and Livie will detect it automatically:

```bash
mkdir model
# download a GGUF from HuggingFace, e.g.:
# wget -O model/gemma-4.gguf https://...
./livie
```

Alternatively, configure `model_path` in `~/.config/livie/config.toml` directly.

---

## Configuration

Config lives at `~/.config/livie/config.toml`. It is created automatically on first run.

```toml
[endpoint]
active = "local"

[[endpoints]]
name    = "local"
base_url = "http://localhost:8080/v1"
api_key  = ""
model    = ""

[[endpoints]]
name     = "openai"
base_url = "https://api.openai.com/v1"
api_key  = "sk-..."
model    = "gpt-4o"

[runner]
model_path   = "/path/to/model.gguf"
gpu_backend  = "cpu"       # cpu | cuda | metal | vulkan
port         = 8080
gpu_layers   = 0           # 0 = CPU only; -1 = all layers to GPU
context_size = 16384
flash_attn   = true
verbose      = false

[behaviour]
auto_execute_bash  = false  # execute bash commands without confirmation
confirm_tool_calls = true   # prompt before executing tool calls

[hud]
position = "bottom"         # top | bottom

[paths]
vault  = "~/.local/share/livie/vault"
skills = "~/.local/share/livie/skills"
index  = "~/.local/share/livie/index"
```

---

## Commands

Type any of these in the input box:

| Command | Description |
|---|---|
| `/help` | Show all commands and keyboard shortcuts |
| `/new` | Start a new conversation |
| `/resume` | Resume a previous conversation |
| `/run [start\|stop\|restart\|status\|log]` | Manage the local llama-server runner |
| `/model [path]` | Show or switch the active model file |
| `/endpoint [name\|list]` | Show or switch the active API endpoint |
| `/setup` | Re-open the setup wizard |
| `/usage` | Token usage and cost estimate *(coming soon)* |
| `/skills` | List, install, or manage skills *(coming soon)* |
| `/memory` | View or edit memory files *(coming soon)* |
| `/exit` | Quit |

---

## Keyboard Shortcuts

| Key | Action |
|---|---|
| `enter` | Submit message |
| `shift+tab` | Toggle Chat / Bash mode |
| `tab` | Accept autocomplete suggestion |
| `↑` / `↓` | Navigate autocomplete suggestions |
| `ctrl+j` / `shift+enter` | Insert newline |
| `ctrl+u` | Clear input |
| `ctrl+y` | Copy last response to clipboard |
| `pgup` / `pgdn` | Scroll message history |
| `ctrl+home` / `ctrl+end` | Jump to top / bottom of history |
| `esc` | Cancel / return to Chat mode |
| `ctrl+c` × 2 | Quit |

---

## Project Structure

```
livie/
├── main.go          # Entry point — wires config, runner, agent, TUI
├── agent/           # LLM request loop, streaming, tool call dispatch, conversation history
├── app/             # Root Bubbletea model — screen switching (welcome → setup → chat)
├── config/          # Config schema, TOML loading/saving, defaults
├── runner/          # llama-server subprocess manager, auto-download, health checks
├── session/         # Conversation persistence (save, load, list)
├── tui/
│   ├── commands.go  # Slash-command registry and built-in command handlers
│   ├── keys.go      # Keybinding definitions
│   ├── theme.go     # Colour palette and shared styles
│   ├── sysinfo.go   # System info helpers for the HUD
│   ├── components/  # Reusable TUI components (HUD, messages, input, autocomplete, session picker)
│   └── screens/     # Full-screen Bubbletea models (welcome, setup wizard, chat)
├── docs/            # Design documents and implementation notes
└── media/           # Screenshots
```

---

## Tech Stack

| Layer | Choice |
|---|---|
| Language | Go |
| TUI Framework | [Bubbletea V2](https://charm.land/bubbletea) |
| TUI Styling | [Lipgloss V2](https://charm.land/lipgloss) |
| TUI Components | [Bubbles V2](https://charm.land/bubbles) |
| Markdown Rendering | [Glamour](https://github.com/charmbracelet/glamour) |
| Model API | OpenAI-compatible REST (`github.com/sashabaranov/go-openai`) |
| Local Model Runner | `llama-server` (llama.cpp) — managed subprocess |
| Config Format | TOML (`github.com/BurntSushi/toml`) |

---

## Design Philosophy

- **Terminal-native** — The entire experience lives in the terminal. No browser, no Electron, no background daemons unless explicitly started.
- **OpenAI-compatible first** — All model communication uses the standard chat completions format. Livie is agnostic to whether inference is local or remote.
- **Composable via skills** — Capabilities are added as discrete, installable skill units rather than baked into the core.
- **Uncensored and unrestricted** — No content filtering layer. The user is responsible for what they run.
- **Self-aware** — Livie has access to its own source, configuration, and skill files and is designed to be extended by itself.

---

## License

MIT
