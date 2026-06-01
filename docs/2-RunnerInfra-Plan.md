# Livie — Phase 4 & 5 Implementation Plan

> **Predecessor:** `docs/1-Runner-Impl-Plan.md` — defines the full runner architecture,  
> config schema, setup wizard, and the complete Bubbletea message catalogue.  
> **This document covers only what remains:** HUD integration, chat–runner wiring,  
> and command implementations.
>
> **Current state at time of writing:**  
> Phases 1–3 are complete and reviewed. The runner backend is fully built.  
> The setup wizard works end-to-end. The chat screen that follows is  
> entirely disconnected from the runner — no HUD status, no commands, no AI calls.

---

## Table of Contents

1. [What Is and Isn't Done](#1-what-is-and-isnt-done)
2. [Phase 4 — HUD + Chat Integration](#2-phase-4--hud--chat-integration)
3. [Phase 5 — Command Implementations](#3-phase-5--command-implementations)
4. [New Message Catalogue Entries](#4-new-message-catalogue-entries)
5. [File Change Map](#5-file-change-map)
6. [Decisions & Rationale](#6-decisions--rationale)

---

## 1. What Is and Isn't Done

### Done (Phases 1–3)

| Area | Status |
|------|--------|
| `config/` — full TOML schema, Load/Save | ✅ complete |
| `runner/platform.go` — GPUBackend, Platform, asset suffix table | ✅ complete |
| `runner/detect.go` — binary search order, data-dir path | ✅ complete |
| `runner/download.go` — GitHub API, streaming download, ZIP extraction | ✅ complete |
| `runner/process.go` — subprocess lifecycle, SIGTERM/SIGKILL, ring buffer | ✅ complete |
| `runner/manager.go` — state machine, StartCmd/StopCmd/HealthCheckCmd/StartAndPollCmd | ✅ complete |
| `runner/msgs.go` — all exported message types | ✅ complete |
| `tui/screens/setup.go` — full 11-step wizard | ✅ complete |
| `app/app.go` — holds `*runner.Manager`, first-run gate, config save on transition | ✅ complete |
| `main.go` — `config.Load()`, `runner.NewManager()` | ✅ complete |

### Not Done (Phases 4–5)

| Area | Status |
|------|--------|
| `HUDState` — no `RunnerStatus` or `RunnerLabel` fields | ❌ not started |
| HUD Row 2 — no runner chip rendered | ❌ not started |
| `ChatModel` — does not hold `*runner.Manager` | ❌ not started |
| `hudTickCmd` — 1-second HUD polling tick | ❌ not started |
| `runner.Manager` — no uptime tracking | ❌ not started |
| `runner.Manager` — no `RestartCmd()` | ❌ not started |
| `/setup` command | ❌ stub |
| `/run` command | ❌ stub |
| `/model` command | ❌ stub |
| `/endpoint` command | ❌ stub |
| `app.Model.Update` — no `ActionOpenSetup` handler | ❌ not started |

---

## 2. Phase 4 — HUD + Chat Integration

### 2.1 Overview

Phase 4 connects the already-built `*runner.Manager` to the chat screen. It has four discrete sub-tasks, each of which must compile and leave the app in a working state:

1. Add `RunnerStatus` type and fields to `HUDState`
2. Update `RenderHUD` to render the runner chip in Row 2
3. Extend `runner.Manager` with uptime tracking and `RestartCmd`
4. Wire `ChatModel` to receive the manager, poll it, and update the HUD

---

### 2.2 `runner/manager.go` additions

Two additions before touching the TUI.

#### Uptime tracking

Add `startedAt time.Time` to the `Manager` struct. It is set inside `markRunningLocked()` when the health check first passes. It is zeroed on stop or error.

```go
// In Manager struct:
startedAt time.Time

// In markRunningLocked():
m.proc.MarkRunning()
m.state = StateRunning
if m.startedAt.IsZero() {
    m.startedAt = time.Now()
}

// In stop() / syncProcessStateLocked() when transitioning away from Running:
m.startedAt = time.Time{} // zero

// New public method:
func (m *Manager) Uptime() time.Duration {
    m.mu.Lock()
    defer m.mu.Unlock()
    if m.startedAt.IsZero() {
        return 0
    }
    return time.Since(m.startedAt)
}
```

#### `RestartCmd`

A single blocking cmd that stops, re-starts, and polls to health — used by `/run restart` and `/model <path>` (after switching the model).

```go
// RestartCmd stops the current process then starts a fresh one, polling until
// healthy or the timeout elapses. Returns RunnerStartedMsg.
func (m *Manager) RestartCmd(timeout time.Duration) tea.Cmd {
    return func() tea.Msg {
        _ = m.stop()
        if err := m.start(); err != nil {
            return RunnerStartedMsg{Err: err}
        }
        deadline := time.Now().Add(timeout)
        for time.Now().Before(deadline) {
            if ok, _ := m.healthCheck(); ok {
                m.mu.Lock()
                m.markRunningLocked()
                m.mu.Unlock()
                return RunnerStartedMsg{}
            }
            time.Sleep(500 * time.Millisecond)
        }
        return RunnerStartedMsg{Err: fmt.Errorf("restart did not become healthy within %s", timeout)}
    }
}
```

---

### 2.3 `tui/components/hud.go`

#### New type

```go
// RunnerStatus is the live health state of the local runner, as reported to
// the HUD. It is set by ChatModel on every hudTickMsg.
type RunnerStatus int

const (
    RunnerStatusNone     RunnerStatus = iota // no local runner configured
    RunnerStatusStopped                      // configured but not running
    RunnerStatusStarting                     // process up, health not yet passing
    RunnerStatusRunning                      // health check passing
    RunnerStatusError                        // process exited unexpectedly
)
```

#### `HUDState` additions

```go
type HUDState struct {
    // existing fields unchanged ...
    Mode         InputMode
    ModelName    string
    EndpointName string
    TokensUsed   int
    TokensMax    int
    SkillCount   int
    StatusMsg    string
    StatusOK     bool

    // Phase 4 additions:
    RunnerStatus RunnerStatus
    RunnerLabel  string // e.g. "llama-server" | "openai" | "groq"
}
```

`DefaultHUDState()` sets `RunnerStatus: RunnerStatusNone` and `RunnerLabel: ""`.

#### `RenderHUD` Row 2 update

Row 2 currently renders: `tokens · skills (right-aligned) (endpoint) model`

Updated to prepend the runner chip flush-left when the runner is relevant:

```
◉ llama-server   — / — tok · 0 skills            (local) (no model)
```

Chip rendering rules:

| `RunnerStatus` | Symbol | Colour | Label text |
|----------------|--------|--------|------------|
| `None` | — | omit chip entirely | — |
| `Stopped` | `◌` | `ColTextMuted` | `stopped` |
| `Starting` | `◎` | `ColAccentAmber` | `starting` |
| `Running` | `◉` | `ColAccentGreen` | value of `RunnerLabel` |
| `Error` | `◌` | `ColAccentRose` | `error` |

When `RunnerStatus == RunnerStatusNone` **and** the active endpoint is a named remote (not `"local"`), the chip is omitted entirely — no runner is involved.

The chip width is fixed at 18 characters (symbol + space + label, padded) so the rest of Row 2 is always left-aligned at the same column regardless of label length.

```
renderRunnerChip(state HUDState) string
```

Private helper, returns an 18-char wide chip string or `""`.

---

### 2.4 `tui/screens/chat.go`

#### Signature change

```go
// Before:
func NewChatModel(cfg *config.Config, width, height int) ChatModel

// After:
func NewChatModel(cfg *config.Config, mgr *runner.Manager, width, height int) ChatModel
```

`app/app.go` and `app.go`'s `TransitionToChat` handler both call `NewChatModel` — both call sites are updated.

#### New `ChatModel` fields

```go
type ChatModel struct {
    // existing fields ...
    cfg      *config.Config
    keys     tui.KeyMap
    registry *tui.CommandRegistry
    hud      components.HUDState
    // ...

    // Phase 4:
    runner *runner.Manager
}
```

#### Private message type

```go
// hudTickMsg fires every second to refresh runner state in the HUD.
type hudTickMsg struct{}
```

Defined at the top of `chat.go` alongside `quitConfirmMsg`.

#### `hudTickCmd`

```go
func hudTickCmd() tea.Cmd {
    return tea.Tick(time.Second, func(time.Time) tea.Msg {
        return hudTickMsg{}
    })
}
```

#### `ChatModel.Init()` update

```go
func (m ChatModel) Init() tea.Cmd {
    return tea.Batch(
        m.input.Init(),
        hudTickCmd(), // start HUD polling immediately
    )
}
```

#### `ChatModel.Update()` — handle `hudTickMsg`

```go
case hudTickMsg:
    m.syncHUDRunnerState()
    return m, hudTickCmd() // perpetually re-issue
```

#### `syncHUDRunnerState()`

Private method on `*ChatModel`. Reads `m.runner.State()` and maps it to the HUD fields. Called on every tick. No I/O — `Manager.State()` is a mutex-guarded field read.

```go
func (m *ChatModel) syncHUDRunnerState() {
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
}
```

When the active endpoint is a named remote endpoint (not `"local"`), `RunnerStatus` is forced to `RunnerStatusNone` so the chip is hidden:

```go
if m.cfg.Endpoint.Active != "local" {
    m.hud.RunnerStatus = components.RunnerStatusNone
    m.hud.RunnerLabel = ""
}
```

#### `ChatModel.handleAction` — new runner message handlers

`RunnerStartedMsg` and `RunnerStoppedMsg` now also arrive at `ChatModel.Update` as a result of `/run` commands issued from chat. Handle them:

```go
case runner.RunnerStartedMsg:
    m.syncHUDRunnerState()
    if msg.Err != nil {
        m.messages.AddMessage(components.NewMessage(
            components.MsgSystem,
            fmt.Sprintf("runner failed to start: %s", msg.Err),
        ))
    } else {
        m.messages.AddMessage(components.NewMessage(
            components.MsgSystem, "runner started",
        ))
    }
    m.messages.GotoBottom()

case runner.RunnerStoppedMsg:
    m.syncHUDRunnerState()
    m.messages.AddMessage(components.NewMessage(
        components.MsgSystem, "runner stopped",
    ))
    m.messages.GotoBottom()
```

#### `app/app.go` — updated `TransitionToChat` handler

```go
m.chat = screens.NewChatModel(m.cfg, m.runner, m.width, m.height)
```

---

### 2.5 Phase 4 HUD wireframe

```
 ~/p/livie  (CHAT)
 ◉ llama-server   — / — tok · 0 skills            (local) gemma-4 E2B (Q4_K_M)
 ✓ Ready
```

While starting:
```
 ~/p/livie  (CHAT)
 ◎ starting       — / — tok · 0 skills            (local) gemma-4 E2B (Q4_K_M)
 ✓ Ready
```

Stopped or unconfigured:
```
 ~/p/livie  (CHAT)
 ◌ stopped        — / — tok · 0 skills            (local) (no model)
 ✓ Ready
```

Remote endpoint (chip hidden):
```
 ~/p/livie  (CHAT)
 — / — tok · 0 skills                           (remote) gpt-4o
 ✓ Ready
```

---

## 3. Phase 5 — Command Implementations

### 3.1 Overview

Phase 5 wires four commands: `/setup`, `/run`, `/model`, `/endpoint`. The work is spread across three files:

| File | Change |
|------|--------|
| `tui/commands.go` | New `AppAction` constants; replace stubs with real handlers |
| `tui/screens/chat.go` | Extend `handleAction` for every new action |
| `app/app.go` | Handle `ActionOpenSetup` to re-open the setup wizard |

All four commands follow the same existing flow:
```
user input → CommandRegistry.Dispatch → CommandActionMsg → ChatModel.handleAction → tea.Cmd (if async)
```
No new plumbing is required.

---

### 3.2 New `AppAction` constants (`tui/commands.go`)

```go
const (
    ActionNone         AppAction = iota
    ActionQuit
    ActionNew
    ActionSetModeChat
    ActionSetModeBash
    // Phase 5 additions:
    ActionOpenSetup      // handled by app.Model.Update — triggers screen switch
    ActionRunnerStart    // handled by ChatModel.handleAction
    ActionRunnerStop     // handled by ChatModel.handleAction
    ActionRunnerRestart  // handled by ChatModel.handleAction
)
```

---

### 3.3 `/setup`

**Syntax:** `/setup`

**Handler** (in `registerBuiltins`):

```go
r.Register(&Command{
    Name:        "setup",
    Description: "Re-open the setup wizard",
    Handler: func(args []string) (string, AppAction) {
        return "", ActionOpenSetup
    },
})
```

**`app.Model.Update`** — new case in the switch:

```go
case tui.ActionOpenSetup:
    m.setup = screens.NewSetupModel(m.cfg, m.runner, m.width, m.height)
    m.current = screenSetup
    return m, m.setup.Init()
```

`NewSetupModel` pre-populates all forms from the current config, so the user's existing values are preserved on re-entry.

---

### 3.4 `/run`

**Syntax:** `/run [start | stop | restart | status | log]`

Bare `/run` with no subcommand is an alias for `/run status`.

#### Handler structure

```go
r.Register(&Command{
    Name:        "run",
    Description: "Manage the local llama-server runner",
    Handler: func(args []string) (string, AppAction) {
        sub := "status"
        if len(args) > 0 {
            sub = strings.ToLower(args[0])
        }
        switch sub {
        case "start":
            return "", ActionRunnerStart
        case "stop":
            return "", ActionRunnerStop
        case "restart":
            return "", ActionRunnerRestart
        case "status":
            return runStatus(mgr), ActionNone
        case "log":
            return runLog(mgr), ActionNone
        default:
            return fmt.Sprintf("unknown subcommand: %q\nUsage: /run [start|stop|restart|status|log]", sub), ActionNone
        }
    },
})
```

`mgr *runner.Manager` is captured from `ChatModel` at registration time — see §3.7.

#### `runStatus(mgr)` — inline response string

```
runner: running  (PID 48291)
binary: ~/.local/share/livie/bin/llama-server
model:  gemma-4-E2B-it-uncensored-Q4_K_M.gguf
port:   8080
uptime: 4m 32s
```

Fields shown:
- **runner** — state label; PID only when `StateRunning`
- **binary** — `mgr.ResolvedBinPath()`, home-dir shortened
- **model** — basename of `cfg.Runner.ModelPath`, or `(not configured)`
- **port** — `cfg.Runner.Port`
- **uptime** — `mgr.Uptime()` formatted as `Xm Ys`, only when running; otherwise omitted

#### `runLog(mgr)` — inline response string

Last 20 lines of the ring buffer, rendered as a code block (wrapped in triple-backtick fence so it is passed through glamour as a pre-formatted block).

```go
func runLog(mgr *runner.Manager) string {
    lines := mgr.LogLines(20)
    if len(lines) == 0 {
        return "_No log output captured yet._"
    }
    return "```\n" + strings.Join(lines, "\n") + "\n```"
}
```

#### `handleAction` — runner action cases

```go
case tui.ActionRunnerStart:
    m.messages.AddMessage(components.NewMessage(components.MsgSystem, "starting runner…"))
    m.messages.GotoBottom()
    return m.runner.StartAndPollCmd(30 * time.Second)

case tui.ActionRunnerStop:
    m.messages.AddMessage(components.NewMessage(components.MsgSystem, "stopping runner…"))
    m.messages.GotoBottom()
    return m.runner.StopCmd()

case tui.ActionRunnerRestart:
    m.messages.AddMessage(components.NewMessage(components.MsgSystem, "restarting runner…"))
    m.messages.GotoBottom()
    return m.runner.RestartCmd(30 * time.Second)
```

`RunnerStartedMsg` and `RunnerStoppedMsg` arrive back at `ChatModel.Update` and are handled as described in §2.4 — they update the HUD and display a status message.

---

### 3.5 `/model`

**Syntax:**
```
/model                — show current model name and full path
/model <path>         — switch to a different .gguf file or directory
```

#### Path resolution

`<path>` is first tilde-expanded (`~` → `os.UserHomeDir()`). Then:

1. If it is a file with a `.gguf` extension and it exists → use it directly
2. If it is a directory → scan (non-recursive) for the first `.gguf` file
3. Otherwise → return an error message, do not change the model

#### Handler logic

```go
r.Register(&Command{
    Name:        "model",
    Description: "Show or switch the active model file",
    Handler: func(args []string) (string, AppAction) {
        if len(args) == 0 {
            return modelStatus(cfg), ActionNone
        }
        path, err := resolveModelPath(args[0])
        if err != nil {
            return "✗ " + err.Error(), ActionNone
        }
        cfg.Runner.ModelPath = path
        _ = config.Save(cfg, cfg.ConfigPath)
        mgr.Configure(cfg.Runner)
        if mgr.IsRunning() {
            return fmt.Sprintf(
                "model set to **%s** — restarting runner…",
                filepath.Base(path),
            ), ActionRunnerRestart
        }
        return fmt.Sprintf("model set to **%s**", filepath.Base(path)), ActionNone
    },
})
```

`resolveModelPath` performs tilde expansion, path type detection, and directory scanning. It returns an absolute path or an error.

When `ActionRunnerRestart` is returned alongside the response string:
1. `ChatModel.handleAction` shows the response string as a `MsgAssistant` message
2. Then processes `ActionRunnerRestart` — calls `RestartCmd(30s)` and awaits `RunnerStartedMsg`

#### `modelStatus(cfg)` — inline response string

```
model:    gemma-4-E2B-it-uncensored-Q4_K_M.gguf
path:     ~/projects/livie/model/gemma-4-E2B-it-uncensored-Q4_K_M.gguf
context:  16,384 tokens
backend:  cpu
```

---

### 3.6 `/endpoint`

**Syntax:**
```
/endpoint              — show active endpoint name and base URL
/endpoint list         — list all configured endpoints
/endpoint local        — switch to the local runner endpoint
/endpoint <name>       — switch to a named remote endpoint
```

#### Handler logic

```go
r.Register(&Command{
    Name:        "endpoint",
    Description: "Show or switch the active API endpoint",
    Handler: func(args []string) (string, AppAction) {
        if len(args) == 0 {
            return endpointStatus(cfg), ActionNone
        }
        sub := strings.ToLower(args[0])
        switch sub {
        case "list":
            return endpointList(cfg), ActionNone
        default:
            return switchEndpoint(cfg, mgr, sub)
        }
    },
})
```

#### `switchEndpoint(cfg, mgr, name)` — returns `(string, AppAction)`

1. Look up `name` in `cfg.Endpoints`; error if not found
2. Set `cfg.Endpoint.Active = name`
3. Call `config.Save`
4. If switching to `"local"` and runner is not running:
   - Return `"switched to local endpoint — use /run start to start the runner"`, `ActionNone`
5. If switching away from `"local"` and runner is running:
   - Return `"switched to remote endpoint — runner left running (use /run stop to stop it)"`, `ActionNone`
6. Otherwise return confirmation, `ActionNone`

#### `endpointList(cfg)` — inline response string

```
  local  →  http://localhost:8080/v1            (active)
  remote →  https://api.openai.com/v1
```

Active endpoint name is marked `(active)`. Bullet rows use fixed-width name column (longest name + 2 spaces).

---

### 3.7 Command registration and `mgr` / `cfg` capture

The commands `/run`, `/model`, and `/endpoint` all need access to `*runner.Manager` and `*config.Config`. The existing `registerBuiltins()` function is called from `NewCommandRegistry()` and receives neither.

**Fix:** Replace the parameterless `registerBuiltins()` with a method that receives the dependencies:

```go
// Before:
func NewCommandRegistry() *CommandRegistry {
    r := &CommandRegistry{commands: make(map[string]*Command)}
    r.registerBuiltins()
    return r
}
func (r *CommandRegistry) registerBuiltins() { ... }

// After:
func NewCommandRegistry(cfg *config.Config, mgr *runner.Manager) *CommandRegistry {
    r := &CommandRegistry{commands: make(map[string]*Command)}
    r.registerBuiltins(cfg, mgr)
    return r
}
func (r *CommandRegistry) registerBuiltins(cfg *config.Config, mgr *runner.Manager) { ... }
```

`ChatModel` stores `cfg` and `runner` already. Update `NewChatModel` to pass them:

```go
m.registry = tui.NewCommandRegistry(cfg, mgr)
```

The `/help`, `/version`, `/exit`, `/new`, `/skills`, `/usage`, `/resume`, `/memory`, `/index`, `/config` commands do not use `cfg` or `mgr` and are unchanged.

---

### 3.8 Phase 5 wireframes

#### `/run status` (runner running)

```
runner: running  (PID 48291)
binary: ~/.local/share/livie/bin/llama-server
model:  gemma-4-E2B-it-uncensored-Q4_K_M.gguf
port:   8080
uptime: 4m 32s
```

#### `/run status` (runner stopped)

```
runner: stopped
binary: ~/.local/share/livie/bin/llama-server
model:  gemma-4-E2B-it-uncensored-Q4_K_M.gguf
port:   8080
```

#### `/run log`

````
```
llm_load_tensors: ggml_cuda_init...
llama_new_context_with_model: n_ctx = 16384
llama server listening at http://127.0.0.1:8080
slot available: 0
```
````

#### `/model`

```
model:    gemma-4-E2B-it-uncensored-Q4_K_M.gguf
path:     ~/projects/livie/model/gemma-4-E2B-it-uncensored-Q4_K_M.gguf
context:  16,384 tokens
backend:  cpu
```

#### `/endpoint list`

```
  local  →  http://localhost:8080/v1            (active)
  remote →  https://api.openai.com/v1
```

---

## 4. New Message Catalogue Entries

The table below extends the catalogue from `docs/1-Runner-Impl-Plan.md §11`.

| Message | Owner | Produced by | Consumed in |
|---------|-------|-------------|-------------|
| `hudTickMsg` *(private)* | `tui/screens` | `hudTickCmd()` every 1s | `ChatModel.Update` → `syncHUDRunnerState()` |
| `runner.RunnerStartedMsg` | `runner` | `StartAndPollCmd`, `RestartCmd` | `ChatModel.Update` (Phase 4 addition) |
| `runner.RunnerStoppedMsg` | `runner` | `StopCmd` | `ChatModel.Update` (Phase 4 addition) |

`RunnerStartedMsg` and `RunnerStoppedMsg` were already defined in `runner/msgs.go`. Phase 4 adds their consumption in `ChatModel.Update` (previously they were only handled in `SetupModel.Update`).

No new types are added to `runner/msgs.go`.

---

## 5. File Change Map

Each file is listed with the precise scope of change required.

### Phase 4

| File | Change |
|------|--------|
| `runner/manager.go` | Add `startedAt time.Time` field; update `markRunningLocked` to set it; zero it in `syncProcessStateLocked` on stop/error; add `Uptime() time.Duration`; add `RestartCmd(timeout) tea.Cmd` |
| `tui/components/hud.go` | Add `RunnerStatus` type + constants; add `RunnerStatus`, `RunnerLabel` to `HUDState`; update `DefaultHUDState`; add `renderRunnerChip` helper; update `RenderHUD` Row 2 |
| `tui/screens/chat.go` | Add `runner *runner.Manager` field; update `NewChatModel` signature; add `hudTickMsg` type; add `hudTickCmd()`; update `Init()` to batch `hudTickCmd()`; add `hudTickMsg` case in `Update`; add `syncHUDRunnerState()` method; add `RunnerStartedMsg`/`RunnerStoppedMsg` cases in `Update` |
| `app/app.go` | Update both `NewChatModel` call sites to pass `m.runner` |

### Phase 5

| File | Change |
|------|--------|
| `tui/commands.go` | Add `ActionOpenSetup`, `ActionRunnerStart`, `ActionRunnerStop`, `ActionRunnerRestart` constants; change `NewCommandRegistry()` to accept `(cfg, mgr)`; replace `/setup`, `/run`, `/model`, `/endpoint` stubs with real implementations; add `runStatus`, `runLog`, `modelStatus`, `endpointStatus`, `endpointList`, `switchEndpoint`, `resolveModelPath` helpers |
| `tui/screens/chat.go` | Update `NewCommandRegistry` call site to pass `cfg, mgr`; add `ActionOpenSetup`, `ActionRunnerStart`, `ActionRunnerStop`, `ActionRunnerRestart` cases to `handleAction` |
| `app/app.go` | Add `ActionOpenSetup` case to `Update`'s action handler |

---

## 6. Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | `hudTickCmd` fires every 1 second | Fast enough for a responsive HUD; `Manager.State()` is a mutex read with no I/O so the cost is negligible. Sub-second polling would cause unnecessary re-renders. |
| D2 | Runner chip is omitted when `RunnerStatusNone` and endpoint is remote | The chip is irrelevant when using a remote endpoint. Hiding it reduces noise and avoids confusing users who have never configured a local runner. |
| D3 | Runner chip has a fixed 18-char width | Prevents Row 2 from jumping left/right as the label switches between "stopped" (7 chars) and "llama-server" (12 chars). All elements to the right stay at a stable column. |
| D4 | `RestartCmd` is a single blocking cmd (stop → start → poll) | Two separate cmds (StopCmd then StartCmd) would require the caller to chain them via message handling. A single blocking goroutine is simpler and reduces the number of intermediate states the TUI must render. |
| D5 | `Uptime()` is tracked from the first successful health check, not from `Start()` | The model may take several seconds to load after the process starts. Reporting uptime from process-start would overstate how long the server has actually been serving requests. |
| D6 | `NewCommandRegistry` accepts `cfg` and `mgr` rather than storing them on the registry struct | Closure capture is idiomatic Go for command handlers. The registry does not need to expose these dependencies on its public API — they are private to the handler functions. |
| D7 | `/model` saves config and restarts atomically — no separate "apply" step | Consistent with the principle that a config change is always immediately reflected. The user does not have to remember to `/run restart` after changing the model. |
| D8 | `/endpoint switch` does not auto-start or auto-stop the runner | The runner is an independent concern. Switching to `"local"` when the runner is stopped is a valid configuration state (the user may want to start it manually). The command message tells the user what to do next. |
| D9 | `ActionOpenSetup` is handled in `app.Model.Update`, not `ChatModel.handleAction` | The setup screen is a root-level concern — transitioning to it requires changing `app.Model.current`. `ChatModel` has no visibility of the screen stack and should not. |
| D10 | `/run log` wraps output in a triple-backtick fence | Log lines contain ANSI-ish server output and timestamps. Wrapping in a code fence passes the text through glamour as a pre-formatted block, preventing markdown rendering from mangling it. |
