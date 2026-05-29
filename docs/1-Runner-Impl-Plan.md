# Livie — GGUF Runner & Endpoint Implementation Plan

> **Scope:** `runner/` package, config extensions, setup screen redesign,  
> HUD integration, OpenAI-compatible endpoint support.  
> **Target environment at time of writing:** Linux / amd64 · Go 1.26.2 · No `llama-server` on PATH.

---

## Table of Contents

1. [Goals & Constraints](#1-goals--constraints)
2. [Package Architecture](#2-package-architecture)
3. [New Dependency](#3-new-dependency)
4. [Config Schema](#4-config-schema)
5. [Runner Package Design](#5-runner-package-design)
6. [Setup Screen State Machine](#6-setup-screen-state-machine)
7. [Setup Screen UX Wireframes](#7-setup-screen-ux-wireframes)
8. [HUD Integration](#8-hud-integration)
9. [App-Level Integration](#9-app-level-integration)
10. [Command Implementations](#10-command-implementations)
11. [Bubbletea Message Catalogue](#11-bubbletea-message-catalogue)
12. [Implementation Phases](#12-implementation-phases)
13. [Decisions & Rationale](#13-decisions--rationale)

---

## 1. Goals & Constraints

### Goals

| # | Goal |
|---|------|
| G1 | Auto-detect `llama-server` on PATH and in the Livie data directory |
| G2 | Let the user choose their GPU backend (CPU / NVIDIA / AMD) during setup; download the matching binary |
| G3 | Manage `llama-server` as a supervised subprocess — start, stop, restart from within the TUI |
| G4 | Support OpenAI-compatible remote endpoints as a first-class alternative to the local runner |
| G5 | The setup screen guides first-run users through detection, optional install, GPU choice, and configuration with clear, well-designed UX |
| G6 | Config is persisted to `~/.config/livie/config.toml` and loaded on every start |
| G7 | The HUD reflects live runner status |
| G8 | `/run`, `/model`, `/endpoint`, and `/setup` commands are fully wired |

### Constraints

- **No CGo.** The runner is a subprocess, never inline inference.
- **No new TUI framework concepts.** Use existing Bubbletea V2 patterns already established in this codebase (`tea.Tick`, `tea.Cmd`, `tea.Batch`, message types).
- **No goroutines owned by model structs.** All async work is driven by `tea.Cmd`; channels are used only as the handoff point between a background goroutine and Bubbletea's event loop.
- **Packages are narrow and purposeful.** Each file does one thing. No package imports from `tui/` — data flows up via message types and plain structs.
- **The `runner/` package must be importable without the TUI.** It exposes plain Go types. Bubbletea message types live in `runner/msgs.go`, not scattered through the business logic.

---

## 2. Package Architecture

### New packages

```
runner/
  platform.go   — Available backend detection; user-chosen Platform construction
  detect.go     — Locate existing llama-server binary
  download.go   — GitHub Releases API; ZIP fetch with progress; extraction
  process.go    — exec.Cmd lifecycle; log capture; health polling
  manager.go    — High-level Manager type; RunnerConfig; public API
  msgs.go       — All tea.Msg types exported by this package
```

### Modified packages

```
config/
  config.go     — Extended Config: RunnerConfig, EndpointConfig, multiple endpoints
  toml.go       — NEW: Load / Save to ~/.config/livie/config.toml

tui/screens/
  setup.go      — Complete redesign: multi-step wizard

tui/components/
  hud.go        — RunnerStatus added to HUDState; RenderHUD updated

app/
  app.go        — Holds *runner.Manager; passes it into setup and chat screens

main.go         — Uses config.Load(); instantiates runner.Manager; passes to app.New()
```

### Dependency graph (packages → imports)

```
main
  └─ app              imports: config, runner, tui/screens
       ├─ tui/screens/setup   imports: config, runner
       ├─ tui/screens/chat    imports: config, runner (read-only status)
       └─ tui/components/hud  (no runner import — uses plain HUDState value)

runner              imports: nothing from tui/
config              imports: nothing from tui/
```

The `runner` package never imports from `tui`. Status is communicated upward via `runner.Manager.State()`, polled by the TUI via a `tea.Cmd` ticker.

---

## 3. New Dependency

**`github.com/BurntSushi/toml`** — Config file serialisation/deserialisation.

```
go get github.com/BurntSushi/toml
```

Chosen because:
- The de-facto standard Go TOML library; no CGo; battle-tested.
- `About-Livie.md` specifies TOML as the config format.
- Struct-tag driven (same ergonomics as `encoding/json`).

No other external dependencies are introduced. Download, ZIP extraction, and HTTP are all standard library.

---

## 4. Config Schema

### 4.1 TOML file layout (`~/.config/livie/config.toml`)

```toml
[runner]
binary_path   = ""          # "" = auto-detect / use data dir binary
model_path    = ""          # absolute path to .gguf file
gpu_backend   = "cpu"       # "cpu" | "cuda" | "metal" | "vulkan"
port          = 8080
gpu_layers    = -1          # -1 = offload all layers (llama-server default)
context_size  = 16384       # default: 16k tokens
threads       = 0           # 0 = llama-server chooses
flash_attn    = true
verbose       = false

[endpoint]
active = "local"            # name of the active endpoint

[[endpoints]]
name     = "local"
base_url = "http://localhost:8080/v1"
api_key  = ""
model    = ""               # populated from runner.model_path basename

[[endpoints]]
# Remote entries are only written if the user configures one during setup
name     = "remote"
base_url = ""
api_key  = ""
model    = ""

[behaviour]
auto_execute_bash  = false
confirm_tool_calls = true

[hud]
position = "bottom"         # "top" | "bottom"

[paths]
vault  = "~/.local/share/livie/vault"
skills = "~/.local/share/livie/skills"
index  = "~/.local/share/livie/index"
```

### 4.2 Go structs

```go
// config/config.go

type Config struct {
    Runner    RunnerConfig
    Endpoint  EndpointSelector
    Endpoints []EndpointConfig
    Behaviour BehaviourConfig
    HUD       HUDConfig
    Paths     PathsConfig

    // Runtime-only (not persisted)
    IsFirstRun bool
    ConfigPath string
}

type RunnerConfig struct {
    BinaryPath  string `toml:"binary_path"`
    ModelPath   string `toml:"model_path"`
    GPUBackend  string `toml:"gpu_backend"`  // "cpu" | "cuda" | "metal" | "vulkan"
    Port        int    `toml:"port"`
    GPULayers   int    `toml:"gpu_layers"`
    ContextSize int    `toml:"context_size"`
    Threads     int    `toml:"threads"`
    FlashAttn   bool   `toml:"flash_attn"`
    Verbose     bool   `toml:"verbose"`
}

type EndpointSelector struct {
    Active string `toml:"active"`
}

type EndpointConfig struct {
    Name    string `toml:"name"`
    BaseURL string `toml:"base_url"`
    APIKey  string `toml:"api_key"`
    Model   string `toml:"model"`
}

type BehaviourConfig struct {
    AutoExecuteBash  bool `toml:"auto_execute_bash"`
    ConfirmToolCalls bool `toml:"confirm_tool_calls"`
}

type HUDConfig struct {
    Position string `toml:"position"` // "top" | "bottom"
}

type PathsConfig struct {
    Vault  string `toml:"vault"`
    Skills string `toml:"skills"`
    Index  string `toml:"index"`
}
```

`DefaultConfig()` is updated to:
- Scan `./model/` for any `.gguf` file and pre-populate `Runner.ModelPath`
- Set `Runner.ContextSize = 16384`, `Runner.Port = 8080`, `Runner.GPULayers = -1`
- Default `Runner.GPUBackend = "cpu"` (overridden by the user's setup choice)
- Include a single `"local"` endpoint at `http://localhost:8080/v1`

`config/toml.go` provides:
```go
func Load(path string) (*Config, error)   // returns DefaultConfig() when file absent
func Save(cfg *Config, path string) error  // atomic: write temp → rename
```

---

## 5. Runner Package Design

### 5.1 `runner/platform.go`

**Responsibility:** Detect which GPU backends are available on the host; construct a `Platform` value from a user-chosen backend. Does *not* make the choice itself.

```go
type GPUBackend int

const (
    GPUBackendCPU    GPUBackend = iota
    GPUBackendCUDA             // NVIDIA CUDA
    GPUBackendMetal            // Apple Metal (macOS)
    GPUBackendVulkan           // AMD / generic GPU via Vulkan
)

func (g GPUBackend) String() string   // "CPU" | "CUDA" | "Metal" | "Vulkan"
func (g GPUBackend) TOML() string     // "cpu" | "cuda" | "metal" | "vulkan"

// ParseBackend converts a TOML string back to a GPUBackend.
func ParseBackend(s string) GPUBackend

type Platform struct {
    OS      string     // runtime.GOOS
    Arch    string     // runtime.GOARCH
    GPU     GPUBackend // user-chosen
}

// New constructs a Platform with the user-chosen GPU backend.
func New(gpu GPUBackend) Platform

// DetectAvailable returns which backends are likely usable on this machine.
// Used by the setup screen to populate the GPU selection list.
// The first element is always GPUBackendCPU (always available).
func DetectAvailable() []GPUBackend

// ReleaseAssetSuffix returns the asset name suffix for selecting from GitHub releases.
func (p Platform) ReleaseAssetSuffix() string

// BinaryName returns "llama-server" or "llama-server.exe".
func (p Platform) BinaryName() string

// Description returns a human-readable label, e.g. "linux/amd64 · CUDA".
func (p Platform) Description() string
```

**`DetectAvailable` detection logic:**

| Check | Backend added to list |
|-------|-----------------------|
| Always | `GPUBackendCPU` |
| `runtime.GOOS == "darwin"` | `GPUBackendMetal` |
| `nvidia-smi` on PATH | `GPUBackendCUDA` |
| `/dev/kfd` exists OR `rocm-smi` on PATH | `GPUBackendVulkan` (AMD via Vulkan) |

Detection is informational only — the returned list populates the setup UI. The user makes the final choice. If the user picks a backend that was not detected (e.g., they know they have CUDA but `nvidia-smi` is not on PATH), it is allowed.

**Asset suffix mapping:**

| OS / Arch | GPU | Asset suffix |
|-----------|-----|-------------|
| linux/amd64 | CPU | `ubuntu-x64.zip` |
| linux/amd64 | CUDA | `linux-cuda-cu12.2.0-x64.zip` |
| linux/amd64 | Vulkan | `linux-vulkan-x64.zip` |
| linux/arm64 | CPU | `linux-arm64.zip` |
| darwin/arm64 | Metal | `macos-arm64.zip` |
| darwin/amd64 | Metal | `macos-x64.zip` |
| windows/amd64 | CPU | `win-x64.zip` |
| windows/amd64 | CUDA | `win-cuda-cu12.2.0-x64.zip` |
| windows/amd64 | Vulkan | `win-vulkan-x64.zip` |

---

### 5.2 `runner/detect.go`

**Responsibility:** Return the path to a usable `llama-server` binary, or `("", false)`.

Search order:
1. `cfg.BinaryPath` if non-empty and the file exists and is executable
2. `exec.LookPath("llama-server")` — system PATH
3. `~/.local/share/livie/bin/llama-server` — Livie's own managed binary

```go
func Detect(cfg RunnerConfig) (path string, found bool)
func DataDirBinaryPath() string   // ~/.local/share/livie/bin/<BinaryName>
```

---

### 5.3 `runner/download.go`

**Responsibility:** Fetch the latest llama.cpp release from GitHub, download the ZIP with streaming progress, extract the `llama-server` binary, make it executable, return the final path.

**GitHub Releases API:**
```
GET https://api.github.com/repos/ggerganov/llama.cpp/releases/latest
```
Minimal parse struct extracts `tag_name` and `assets[].{name, browser_download_url, size}`.

**Asset selection:** iterate `assets`, find the one whose name contains `platform.ReleaseAssetSuffix()`.

**Download + progress pattern:**

The download goroutine writes `ProgressUpdate` values to a channel. The setup screen drives progress reads via a blocking `tea.Cmd` — one cmd per update, re-issued until `Done == true`.

```go
type ProgressUpdate struct {
    Downloaded int64
    Total      int64   // 0 = content-length unknown
    Done       bool
    Err        error
    BinaryPath string  // populated when Done && Err == nil
}

// StartDownload launches the download goroutine and returns the progress channel.
// ctx can be cancelled to abort the download.
func StartDownload(ctx context.Context, platform Platform, destDir string) <-chan ProgressUpdate

// DownloadProgressCmd returns a tea.Cmd that blocks until the next ProgressUpdate
// is available on ch, then returns it as a runner.DownloadProgressMsg.
// The caller re-issues this cmd after each non-Done message.
func DownloadProgressCmd(ch <-chan ProgressUpdate) tea.Cmd
```

**ZIP extraction:** walk all entries in the archive; find any entry whose base name matches `platform.BinaryName()`; extract to `destDir`; `os.Chmod` to `0755`.

---

### 5.4 `runner/process.go`

**Responsibility:** Manage the `exec.Cmd` lifecycle. No Bubbletea dependency.

```go
type ProcessState int

const (
    ProcessIdle     ProcessState = iota
    ProcessStarting
    ProcessRunning
    ProcessStopped
    ProcessError
)

type Process struct {
    // unexported fields: cmd, state, logBuf ring buffer, cancel func, mutex
}

func NewProcess(binPath string, args []string) *Process
func (p *Process) Start() error
func (p *Process) Stop() error       // SIGTERM; SIGKILL after 5s
func (p *Process) State() ProcessState
func (p *Process) LogLines(n int) []string
```

**Launch args** built by `manager.go`:

```
llama-server
  --model        <ModelPath>
  --port         <Port>
  --ctx-size     <ContextSize>
  --n-gpu-layers <GPULayers>      // omitted when GPULayers == 0
  --threads      <Threads>        // omitted when Threads == 0
  --flash-attn                    // flag, only when FlashAttn == true
  --host         127.0.0.1
  --log-disable                   // suppress ANSI log noise; captured to ring buffer
```

**Log capture:** both stdout and stderr are piped to a 500-line ring buffer. `LogLines(n)` returns the last `n` lines.

Health polling is **not** done inside `process.go`. It is a `tea.Cmd` concern — see `manager.go`.

---

### 5.5 `runner/manager.go`

**Responsibility:** High-level public API. Owns the resolved binary path, active config, platform, and `*Process`.

```go
type ManagerState int

const (
    StateUnconfigured  ManagerState = iota  // no binary or no model path
    StateReady                               // configured, not started
    StateStarting                            // process spawned, health not yet passing
    StateRunning                             // health check passing
    StateStopped                             // explicitly stopped
    StateError                               // process exited non-zero or health failed
)

type Manager struct {
    // unexported
}

func NewManager(cfg config.RunnerConfig) *Manager
func (m *Manager) Configure(cfg config.RunnerConfig)   // update config; does not restart
func (m *Manager) SetBinaryPath(p string)
func (m *Manager) State() ManagerState
func (m *Manager) Platform() Platform
func (m *Manager) BaseURL() string                     // http://127.0.0.1:{port}/v1
func (m *Manager) LogLines(n int) []string
func (m *Manager) IsRunning() bool

// tea.Cmd helpers
func (m *Manager) StartCmd() tea.Cmd                          // spawns process; returns RunnerStartedMsg
func (m *Manager) StopCmd() tea.Cmd                           // stops process; returns RunnerStoppedMsg
func (m *Manager) HealthCheckCmd() tea.Cmd                    // single GET /health; returns HealthCheckMsg
func (m *Manager) PollUntilReadyCmd(timeout time.Duration) tea.Cmd
```

`PollUntilReadyCmd` repeatedly calls `HealthCheckCmd` (every 500ms) until the server responds 200 or the timeout expires. It returns a final `RunnerStartedMsg` with `Err` set on timeout.

---

### 5.6 `runner/msgs.go`

All Bubbletea message types exported by the runner package. The TUI imports only this file's types — not the internal business logic.

```go
package runner

// RunnerStartedMsg is returned by Manager.StartCmd() and Manager.PollUntilReadyCmd().
type RunnerStartedMsg struct{ Err error }

// RunnerStoppedMsg is returned by Manager.StopCmd().
type RunnerStoppedMsg struct{ Err error }

// HealthCheckMsg is returned by Manager.HealthCheckCmd().
type HealthCheckMsg struct {
    OK  bool
    Err error
}

// DownloadProgressMsg is returned by DownloadProgressCmd() on each channel read.
type DownloadProgressMsg = ProgressUpdate

// DetectCompleteMsg is returned by the setup screen's detectCmd().
type DetectCompleteMsg struct {
    Found bool
    Path  string
}
```

---

## 6. Setup Screen State Machine

The setup screen is a strict left-to-right state machine. Each step owns its rendering and knows exactly which step follows on each user action.

```
setupStep (int)

  stepBoot            → stepDetecting                      (auto, 400ms tick)
  stepDetecting       → stepModeSelect    (binary found)
                      → stepInstallPrompt (binary not found)
  stepInstallPrompt   → stepGPUSelect     (user: install)
                      → stepModeSelect    (user: skip)
  stepGPUSelect       → stepInstalling    (user: confirm choice)
  stepInstalling      → stepModeSelect    (done, no error)
                      → stepInstallError  (done, error)
  stepInstallError    → stepGPUSelect     (user: retry)
                      → stepModeSelect    (user: skip)
  stepModeSelect      → stepConfigLocal   (choice: local)
                      → stepConfigRemote  (choice: remote)
  stepConfigLocal     → stepStartingRunner (user: continue)
  stepConfigRemote    → stepDone           (user: continue — no runner to start)
  stepStartingRunner  → stepDone           (health OK or timeout with error notice)
  stepDone            → TransitionToChat   (auto, 800ms tick)
```

### SetupModel fields

```go
type SetupModel struct {
    width, height int
    step          setupStep
    cfg           *config.Config
    runner        *runner.Manager

    // Detection
    detectedBinPath string

    // GPU selection (stepGPUSelect)
    availableBackends []runner.GPUBackend
    gpuChoice         int  // index into availableBackends

    // Download (stepInstalling)
    downloadCh      <-chan runner.ProgressUpdate  // nil when not active
    downloadedBytes int64
    downloadTotal   int64
    downloadErr     error

    // Mode selection (stepModeSelect)
    modeChoice int   // 0 = local, 1 = remote

    // Local config form (stepConfigLocal)
    // Fields: model path, GPU layers, context size, port
    localInputs [4]textinput.Model
    localFocus  int

    // Remote config form (stepConfigRemote)
    // Fields: base URL, API key, model name
    remoteInputs [3]textinput.Model
    remoteFocus  int

    // Spinner state
    spinnerFrame int

    // Error message for display
    errMsg string
}
```

### Step responsibilities

| Step | Entry Cmd | Keys consumed | Output |
|------|-----------|---------------|--------|
| `stepBoot` | `tea.Tick(400ms)` → advance | — | Livie logo + tagline |
| `stepDetecting` | `detectCmd()` → `DetectCompleteMsg` | — | Spinner |
| `stepInstallPrompt` | — | `↑↓`, `enter`, `esc`→skip | Not-found notice + choices |
| `stepGPUSelect` | `detectAvailableCmd()` → populates list | `↑↓`, `enter`, `esc`→back | GPU backend radio list |
| `stepInstalling` | `startDownloadCmd()` + `DownloadProgressCmd` | `ctrl+c`→cancel | Progress bar |
| `stepInstallError` | — | `↑↓`, `enter` | Error text + retry/skip |
| `stepModeSelect` | — | `↑↓`, `enter` | Radio: local / remote |
| `stepConfigLocal` | init inputs with cfg defaults | `tab`/`shift+tab`, text, `enter` | 4-field form |
| `stepConfigRemote` | init inputs with cfg defaults | `tab`/`shift+tab`, text, `enter` | 3-field form |
| `stepStartingRunner` | `runner.PollUntilReadyCmd(30s)` | — | Spinner + log tail |
| `stepDone` | `tea.Tick(800ms)` → `TransitionToChat` | — | ✓ Ready summary |

`detectCmd` is a synchronous `tea.Cmd` (binary detection is a PATH lookup + `os.Stat` — sub-millisecond). `detectAvailableCmd` is similarly synchronous.

`startDownloadCmd` calls `runner.StartDownload(ctx, platform, destDir)` to obtain the channel, stores it on `downloadCh`, then immediately returns the initial `DownloadProgressCmd(ch)`. Each received `DownloadProgressMsg` updates the progress bar and re-issues `DownloadProgressCmd` until `Done == true`.

The `textinput` models are initialised when entering `stepConfigLocal` / `stepConfigRemote`, with values pre-populated from `cfg`. This means returning via `/setup` respects already-saved config values.

---

## 7. Setup Screen UX Wireframes

All steps run in AltScreen. Content is centred vertically and horizontally. A small persistent header appears at every step. The existing colour palette is used throughout — no new colours are introduced.

### Header (all steps)

```
  ◆ LIVIE                                  setup
```

`◆ LIVIE` in `ColAccentCyan · bold`. `setup` is `ColTextMuted`. A full-width `ColBorder` rule below.

---

### `stepBoot`

```



          ◆  L I V I E

          a local AI assistant
          that lives in your terminal.



```

`◆` cycles between `ColAccentCyan` and `ColSurfaceHi` on each 100ms tick, creating a pulse. Auto-advances to detecting after 400ms.

---

### `stepDetecting`

```

          ⠸  Scanning for llama-server...

```

Braille spinner `⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏` cycling at 80ms. Colour `ColAccentCyan`.

---

### `stepInstallPrompt`

```

          ✗  llama-server not found

          Livie uses llama-server to run GGUF models locally.
          It will be downloaded from the official llama.cpp
          GitHub releases (~80–200 MB depending on platform).

          ▶  Install llama-server
             Skip — configure a remote endpoint instead

          enter to confirm · esc to skip

```

Selected row: `ColAccentCyan`. Unselected: `ColTextSecondary`. `✗` in `ColAccentRose`.

---

### `stepGPUSelect`

```

          Choose a GPU backend

          This determines which llama-server binary is downloaded.
          Select CPU if you are unsure or have no discrete GPU.

          ▶  CPU only          (always available)
             NVIDIA  · CUDA    (detected ✓)
             AMD     · Vulkan  (not detected)

          ↑ ↓ to select · enter to download · esc to go back

```

Detected backends show `(detected ✓)` in `ColAccentGreen`. Undetected show `(not detected)` in `ColTextMuted`. Both are selectable — the user may know more than the detector. Selected row prefix `▶` in `ColAccentCyan`.

---

### `stepInstalling`

```

          ↓  Downloading llama-server  (linux/amd64 · CUDA)

          ████████████████░░░░░░░░░░░░░░░░  52%
          78.4 MB / 152.3 MB

```

After download completes and extraction begins:

```

          ↓  Downloading llama-server  (linux/amd64 · CUDA)

          ████████████████████████████████  100%
          152.3 MB / 152.3 MB

          → Extracting to ~/.local/share/livie/bin/...

```

Progress bar: `█` in `ColAccentCyan`, `░` in `ColSurfaceHi`. Percentage and byte counts in `ColTextSecondary`.

---

### `stepInstallError`

```

          ✗  Download failed

          connection refused: could not reach api.github.com

          ▶  Retry
             Skip — configure a remote endpoint instead

          enter to confirm · esc to go back

```

---

### `stepModeSelect`

```

          How would you like to connect?

          ●  Local runner  (llama-server)
             Run GGUF models directly on this machine.
             Best for privacy, offline use, and uncensored models.

          ○  Remote endpoint
             Connect to OpenAI, Groq, Ollama, LM Studio, or any
             OpenAI-compatible API.

          ↑ ↓ to select · enter to confirm

```

`●` / `○` in `ColAccentCyan` for selected, `ColTextMuted` for unselected.

---

### `stepConfigLocal`

```

          Configure local runner

          Model file
          ╭────────────────────────────────────────────────╮
          │ /home/kez/projects/livie/model/gemma-4-E2B-… │
          ╰────────────────────────────────────────────────╯

          GPU layers       Context size       Port
          ╭────────────╮   ╭──────────────╮   ╭────────╮
          │ -1         │   │ 16384        │   │ 8080   │
          ╰────────────╯   ╰──────────────╯   ╰────────╯

          GPU layers: -1 = offload all (recommended)
          Model can be changed later with /model <path>

          tab · shift+tab to move between fields · enter to continue

```

Focused field border: `ColAccentCyan`. Unfocused: `ColBorder`. Hint text: `ColTextMuted`. The model path field spans full width. The three numeric fields sit on one row with equal spacing.

---

### `stepConfigRemote`

```

          Configure remote endpoint

          Base URL
          ╭────────────────────────────────────────────────╮
          │ https://api.openai.com/v1                      │
          ╰────────────────────────────────────────────────╯

          API Key
          ╭────────────────────────────────────────────────╮
          │ ••••••••••••••••••••••••••••••••               │
          ╰────────────────────────────────────────────────╯

          Model
          ╭────────────────────────────────────────────────╮
          │ gpt-4o                                         │
          ╰────────────────────────────────────────────────╯

          tab · shift+tab to move between fields · enter to continue

```

API Key field uses `textinput.EchoPassword` mode. All three fields are identical width. Empty Base URL prevents advancing (inline validation: `✗ Base URL is required`).

---

### `stepStartingRunner`

```

          ⠸  Starting llama-server...

          gemma-4-E2B-it-uncensored-Q4_K_M.gguf
          Context: 16,384 tokens · GPU layers: all · Port: 8080

          ── server log ────────────────────────────────────
          llm_load_tensors: ggml_cuda_init...
          llama_new_context_with_model: n_ctx = 16384
          llama server listening at http://127.0.0.1:8080

```

Log tail shows last 4 lines of the ring buffer, `ColTextMuted`. Spinner in `ColAccentAmber` (loading, not yet healthy). Transitions to `stepDone` on first successful health check.

On timeout (30s):

```

          ✗  Server did not start in time

          Check /run log for details, then try /run start.

```

This is non-fatal — the user proceeds to chat and can diagnose via `/run log`.

---

### `stepDone`

```

          ✓  Ready

          Model     gemma-4 E2B (Q4_K_M)
          Endpoint  http://127.0.0.1:8080/v1
          Context   16,384 tokens

```

`✓` in `ColAccentGreen`. Auto-transitions to chat after 800ms.

---

## 8. HUD Integration

### HUDState additions

```go
// Added to components.HUDState:
RunnerStatus RunnerStatus  // none | stopped | starting | running | error
RunnerLabel  string        // e.g. "llama-server" | "openai" | "groq"
```

```go
type RunnerStatus int

const (
    RunnerStatusNone     RunnerStatus = iota  // not configured
    RunnerStatusStopped                        // configured, not running
    RunnerStatusStarting                       // process started, not yet healthy
    RunnerStatusRunning                        // health check passing
    RunnerStatusError                          // process exited or health failed
)
```

### HUD Row 2 update

Row 2 currently: `tokens · skills  (endpoint) model`

Updated: `◉ runner-label  tokens · skills  (endpoint) model`

The `◉` indicator and label sit flush left:

| Status | Colour | Label example |
|--------|--------|---------------|
| `None` / `Stopped` | `ColTextMuted` | `◌ stopped` |
| `Starting` | `ColAccentAmber` | `◎ starting` |
| `Running` | `ColAccentGreen` | `◉ llama-server` |
| `Error` | `ColAccentRose` | `◌ error` |

The runner chip is omitted entirely when `RunnerStatus == RunnerStatusNone` and the active endpoint is a named remote endpoint (no local runner involved).

### HUD update flow

`ChatModel` holds a `*runner.Manager`. A 1-second `tea.Tick` (a private `hudTickMsg`) fires in `ChatModel.Init()` and is perpetually re-issued. On each tick, `ChatModel.Update` reads `m.runner.State()` and maps it to a `RunnerStatus`, updates `m.hud`, then re-issues the tick.

The tick is lightweight — `Manager.State()` is a mutex-guarded field read, not a syscall.

---

## 9. App-Level Integration

### `app.Model` changes

```go
type Model struct {
    current screen
    setup   screens.SetupModel
    chat    screens.ChatModel
    cfg     *config.Config
    runner  *runner.Manager    // NEW — shared between setup and chat
    width   int
    height  int
}
```

### `app.New()` updated signature

```go
func New(cfg *config.Config, mgr *runner.Manager) Model
```

### `main.go` updated flow

```go
func main() {
    cfgPath := config.DefaultPath()
    cfg, err := config.Load(cfgPath)
    if err != nil {
        // Load returns DefaultConfig() when file is absent — only hard errors surface
        fmt.Fprintf(os.Stderr, "livie: config: %v\n", err)
        os.Exit(1)
    }

    mgr := runner.NewManager(cfg.Runner)

    p := tea.NewProgram(app.New(cfg, mgr))
    if _, err := p.Run(); err != nil {
        fmt.Fprintf(os.Stderr, "livie: %v\n", err)
        os.Exit(1)
    }
}
```

### First-run gate

`app.New()` starts at `screenSetup` when either:
- `cfg.IsFirstRun == true` (no config file existed), or
- `cfg.Endpoint.Active == "local"` AND `cfg.Runner.ModelPath == ""` (local endpoint configured but no model)

Otherwise starts directly at `screenChat`.

### `TransitionToChat` updated

```go
type TransitionToChat struct {
    Config *config.Config
}
```

When `app.Model.Update` receives `TransitionToChat`, it:
1. Merges the returned config into `m.cfg`
2. Calls `config.Save(m.cfg, m.cfg.ConfigPath)` — persists to TOML
3. Re-initialises `m.runner` with the final `RunnerConfig` via `m.runner.Configure()`
4. Transitions to `screenChat`

### `/setup` re-entry

When `ActionOpenSetup` is returned by the `/setup` command handler, `app.Model.Update` transitions back to `screenSetup`, calling `screens.NewSetupModel(m.cfg, m.runner, m.width, m.height)`. The setup model is initialised with the existing config, so all forms are pre-populated with current values. The user can re-run detection, change GPU choice, re-download, or simply adjust the config form and hit enter.

---

## 10. Command Implementations

### `/setup`

```
/setup   — re-open the setup wizard
```

Returns `ActionOpenSetup`. Handled in `app.Model.Update` by switching to `screenSetup`.

### `/run [start|stop|restart|status|log]`

```
/run           — alias for /run status
/run start     — start llama-server with current config
/run stop      — stop llama-server
/run restart   — stop, then start
/run status    — show state, PID if running, port, uptime
/run log       — show last 20 lines from the server log ring buffer
```

New `AppAction` constants: `ActionRunnerStart`, `ActionRunnerStop`, `ActionRunnerRestart`.

Handled in `ChatModel.handleAction` by calling the relevant `manager` method and issuing the corresponding `tea.Cmd`. Status and log output are rendered as `MsgSystem` messages in the viewport.

### `/model <path>`

```
/model            — show the current model name and full path
/model <path>     — switch to a different model file
```

`<path>` accepts:
- An absolute or `~`-expanded path to a `.gguf` file → used directly
- A path to a directory → the first `.gguf` file found in that directory (non-recursive) is used

On change:
1. Validates the path exists and has a `.gguf` extension
2. Updates `cfg.Runner.ModelPath`
3. Saves config
4. If the runner is currently running: restarts it via `ActionRunnerRestart` with the new model
5. Returns a confirmation message: `"model set to gemma-4-E2B-it-uncensored-Q4_K_M.gguf — runner restarting"`

### `/endpoint [name | list]`

```
/endpoint          — show active endpoint name and base URL
/endpoint list     — list all configured endpoints with their URLs
/endpoint local    — switch active endpoint to local runner
/endpoint <name>   — switch to a named remote endpoint
```

On switch:
1. Updates `cfg.Endpoint.Active`
2. Saves config
3. If switching to `"local"` and runner is not running, emits: `"local endpoint selected — use /run start to start the runner"`

---

## 11. Bubbletea Message Catalogue

A complete reference of all new `tea.Msg` types introduced — which package owns them, and where they are produced and consumed.

| Message | Owner | Produced by | Consumed in |
|---------|-------|-------------|-------------|
| `runner.DetectCompleteMsg` | `runner` | `detectCmd()` in setup | `SetupModel.Update` |
| `runner.DownloadProgressMsg` | `runner` | `DownloadProgressCmd(ch)` | `SetupModel.Update` |
| `runner.RunnerStartedMsg` | `runner` | `Manager.StartCmd()` / `PollUntilReadyCmd()` | `SetupModel.Update`, `ChatModel.Update` |
| `runner.RunnerStoppedMsg` | `runner` | `Manager.StopCmd()` | `ChatModel.Update` |
| `runner.HealthCheckMsg` | `runner` | `Manager.HealthCheckCmd()` | `SetupModel.Update`, `ChatModel.Update` |
| `screens.TransitionToChat` | `screens` | setup `stepDone` tick | `app.Model.Update` |
| `hudTickMsg` *(private)* | `tui/screens` | `hudTickCmd()` tick | `ChatModel.Update` |
| `setupSpinnerTickMsg` *(private)* | `tui/screens` | `tea.Tick` 80ms | `SetupModel.Update` |
| `setupAdvanceMsg` *(private)* | `tui/screens` | `tea.Tick` for boot/done delays | `SetupModel.Update` |
| `tui.CommandActionMsg` | `tui` | `CommandRegistry.Dispatch` | `ChatModel.Update` |

No message type is defined in more than one package. Private (unexported) message types are defined in the file that uses them.

---

## 12. Implementation Phases

Each phase produces code that compiles and the app runs cleanly before the next phase begins.

### Phase 1 — Config foundation
**Files:** `config/config.go` (rewrite), `config/toml.go` (new), `main.go` (update)

- Define all config structs with TOML tags
- `Load()` / `Save()` with atomic write
- `DefaultConfig()` scans `./model/` for a `.gguf` and pre-populates `Runner.ModelPath`
- Context size default: `16384`
- `main.go` uses `config.Load()` instead of `config.DefaultConfig()`
- Add `github.com/BurntSushi/toml` to `go.mod`
- App still auto-transitions past setup (no behaviour change yet)

### Phase 2 — Runner package
**Files:** `runner/platform.go`, `runner/detect.go`, `runner/download.go`, `runner/process.go`, `runner/manager.go`, `runner/msgs.go`

- Full runner package — all types and logic
- `runner/runner_test.go` covers: `DetectAvailable()`, `ParseBackend()`/round-trip, `Platform.ReleaseAssetSuffix()` for all combinations, `Detect()` (mock PATH)
- No TUI integration yet

### Phase 3 — Setup screen redesign
**Files:** `tui/screens/setup.go` (full rewrite), `app/app.go` (updated)

- All steps wired per §6 state machine
- `app.Model` holds `*runner.Manager`
- `app.New()` takes `(cfg, mgr)` signature
- `TransitionToChat` carries config; `app.Model` saves to TOML on transition
- First-run gate logic

### Phase 4 — HUD + chat integration
**Files:** `tui/components/hud.go`, `tui/screens/chat.go`

- `RunnerStatus` + `RunnerLabel` added to `HUDState`
- HUD Row 2 updated with runner chip
- `ChatModel` receives `*runner.Manager`
- `hudTickCmd` polls runner state each second

### Phase 5 — Command implementations
**Files:** `tui/commands.go`

- `/setup`, `/run`, `/model`, `/endpoint` fully wired
- New `AppAction` constants
- `ChatModel.handleAction` handles all runner-related actions
- `app.Model.Update` handles `ActionOpenSetup`

---

## 13. Decisions & Rationale

| # | Decision | Rationale |
|---|----------|-----------|
| D1 | GPU backend is user-chosen in setup; `DetectAvailable()` informs but does not decide | Users know their hardware. Auto-detection can miss custom driver setups (e.g. CUDA without `nvidia-smi` on PATH). The detector marks backends as "detected ✓" to guide the user, but never overrides them. |
| D2 | `runner/` has zero TUI imports | Independently testable; lower packages must not import upper ones; follows the existing codebase convention |
| D3 | All `tea.Msg` types for runner live in `runner/msgs.go` | Single source of truth; avoids circular-ish imports if any file in `runner/` needed to reference them |
| D4 | Download progress uses a blocking channel drain (one `tea.Cmd` per update) | Avoids double-poll races; update rate is naturally throttled by goroutine write pace; no unnecessary re-renders; matches patterns already in the codebase |
| D5 | `detectCmd` is synchronous, not a goroutine | Binary detection is a PATH lookup + `os.Stat` — sub-millisecond. A goroutine round-trip adds latency with no benefit. |
| D6 | `config.Load` returns `DefaultConfig()` on missing file, not an error | First-run is expected and normal; `IsFirstRun = true` distinguishes the case; `main.go` stays clean |
| D7 | Context size defaults to `16384` | Reasonable balance between context usefulness and memory usage on typical hardware. User-configurable in setup and via `/setup`. |
| D8 | `stepConfigLocal` pre-populates from `cfg.Runner.ModelPath` | Gemma 4 E2B is already at `./model/` — zero friction for the primary development machine |
| D9 | `/model <path>` accepts both file and directory | Both are natural inputs: a user may type `/model ~/models/` or `/model ~/models/gemma.gguf` — the directory form scans for the first `.gguf` |
| D10 | API Key masked with `textinput.EchoPassword` | Standard practice for secrets in TUI forms |
| D11 | `config.Save` uses write-to-temp + rename | Atomic write prevents a corrupt config file on crash or kill mid-write |
| D12 | Runner process log is a 500-line ring buffer | Bounded memory; provides enough context for debugging startup failures; accessible via `/run log` at any time |
| D13 | Health check is a `tea.Cmd`, not a timer inside `process.go` | Keeps `runner/` free of Bubbletea; TUI controls polling frequency; consistent with the rest of the async pattern |
| D14 | `stepDone` auto-transitions after 800ms | Gives the user a moment to read the "Ready" summary without requiring a keypress; consistent with the original boot animation delay |
| D15 | `/setup` re-initialises `SetupModel` with existing config | Re-entry respects current config values; user sees their settings pre-filled and can change only what they need |
