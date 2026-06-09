---
name: livie-self
description: "Livie application reference — modes, keys, commands, config, vault, self-modification."
---

# Livie

Terminal-native AI assistant built with Go + Bubbletea. Two input modes: **chat** (default) and **bash**.

## Input Modes

| Mode | Description |
|------|-------------|
| **chat** | AI conversation mode (default) |
| **bash** | Direct shell command execution with tab-completion |

Toggle between modes with `shift+tab`.

## Key Bindings

| Key | Action |
|-----|--------|
| `enter` | Submit message / run command |
| `shift+enter` / `ctrl+j` | Insert newline (shift+enter requires Kitty terminal) |
| `ctrl+c` × 2 | Quit (double-tap within 500ms) |
| `ctrl+q` | Quit immediately |
| `pgup` / `pgdn` | Scroll viewport |
| `ctrl+u` / `ctrl+d` | Half-page scroll |
| `ctrl+g` / `ctrl+e` | Jump to top / bottom |
| `ctrl+y` | Copy last assistant response to clipboard (OSC 52) |
| `shift+tab` | Toggle chat ↔ bash mode |
| `escape` | Exit bash mode / dismiss overlays |
| `tab` | Trigger bash completion (in bash mode) |

## Slash Commands

| Command | Description |
|---------|-------------|
| `/help` | Show all commands and keybindings |
| `/new` | Start a fresh conversation |
| `/resume` | Resume a previous session |
| `/skills list` | List all loaded skills |
| `/skills install <path>` | Install a skill from a local directory |
| `/run [start\|stop\|restart\|status\|log]` | Manage the local llama-server runner |
| `/model [<path>]` | Show or change the active model file |
| `/endpoint [list\|<name>]` | Show or switch the active API endpoint |
| `/version` | Show Livie version |
| `/exit` | Quit Livie |

## Configuration

Config file: `~/.config/livie/config.toml`

Key fields:
- `[endpoint] active` — name of the active endpoint (e.g. `"local"`, `"openai"`)
- `[[endpoints]]` — list of endpoint configs: `name`, `base_url`, `api_key`, `model`, `context_size`
- `[runner]` — `binary_path`, `model_path`, `gpu_backend`, `port`, `context_size`, `gpu_layers`
- `[behaviour] confirm_tool_calls` — whether to prompt before each tool execution
- `[paths]` — `vault`, `skills`, `index` directories

## Vault

Directory: `~/.local/share/livie/vault/`

| File | Purpose |
|------|---------|
| `system_prompt.md` | Primary system prompt (replaces the built-in default) |
| `personality.md` | AI personality and tone overrides |
| `memory.md` | Persistent notes the AI should always recall |
| `user-profile.md` | Information about the user (name, preferences, context) |

These files can be read or edited with the file tools.

## Skills

Skills directory: `~/.local/share/livie/skills/`

Each subdirectory is a skill. A skill has a `SKILL.md` with TOML frontmatter describing its tools and a Markdown body injected into the system prompt.

## Self-Modification

Livie's source code is in the working directory at launch. To modify Livie:
1. Use file tools (`read_file`, `edit_file`, `write_file`) to change `.go` source files
2. Tell the user to rebuild: `go build .`
3. The user restarts Livie to pick up the changes

Do not attempt to rebuild or restart Livie yourself — always ask the user to do it.
