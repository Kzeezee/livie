package runner

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/config"
)

// ManagerState describes the high-level runner lifecycle stage.
type ManagerState int

const (
	StateUnconfigured ManagerState = iota // no binary or no model path
	StateReady                            // configured, not started
	StateStarting                         // process spawned, health not yet passing
	StateRunning                          // health check passing
	StateStopped                          // explicitly stopped
	StateError                            // process exited non-zero or health failed
)

// Manager is the high-level API for the llama-server subprocess.
type Manager struct {
	mu        sync.Mutex
	cfg       config.RunnerConfig
	platform  Platform
	binPath   string // resolved executable path (set via SetBinaryPath or detection)
	proc      *Process
	state     ManagerState
	startedAt time.Time // set when health first passes; zeroed on stop/error
}

// NewManager creates a Manager. The binary is not started until StartCmd() is called.
// Binary detection (PATH lookup + data-dir check) is performed eagerly here,
// outside any lock, so computeStateLocked stays I/O-free.
// If a llama-server is already healthy on the configured port it is adopted
// immediately so the HUD reflects the real state from the first frame.
func NewManager(cfg config.RunnerConfig) *Manager {
	m := &Manager{
		cfg:      cfg,
		platform: New(ParseBackend(cfg.GPUBackend)),
	}
	// Detect the binary up-front (I/O is fine here — m is not yet shared).
	if p, found := Detect(cfg); found {
		m.binPath = p
	}
	m.state = m.computeStateLocked()
	// Adopt an already-running server (e.g. left over from a previous session).
	if ok, _ := m.healthCheck(); ok {
		m.markRunningLocked()
	}
	return m
}

// Configure updates the manager's config without restarting the process.
// Binary detection for the new config is performed before taking the lock.
func (m *Manager) Configure(cfg config.RunnerConfig) {
	// Resolve the binary for the new config outside the lock.
	var newBin string
	if cfg.BinaryPath != "" && isExecutable(cfg.BinaryPath) {
		newBin = cfg.BinaryPath
	}

	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.platform = New(ParseBackend(cfg.GPUBackend))
	// Respect an explicitly-configured binary; don't overwrite a path that was
	// already resolved (e.g. set by SetBinaryPath after a download).
	if newBin != "" {
		m.binPath = newBin
	}
	if m.state == StateUnconfigured || m.state == StateReady {
		m.state = m.computeStateLocked()
	}
}

// SetBinaryPath overrides the resolved binary path (e.g. after a fresh download).
func (m *Manager) SetBinaryPath(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.binPath = p
	if m.state == StateUnconfigured {
		m.state = m.computeStateLocked()
	}
}

// State returns the current manager state, syncing against the underlying
// process state so that unexpected exits are reflected immediately.
func (m *Manager) State() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.syncProcessStateLocked()
	return m.state
}

// Platform returns the platform the manager is configured for.
func (m *Manager) Platform() Platform {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.platform
}

// BaseURL returns the HTTP base URL for the local llama-server API.
func (m *Manager) BaseURL() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return fmt.Sprintf("http://127.0.0.1:%d/v1", m.cfg.Port)
}

// LogLines returns the last n lines from the server log ring buffer.
func (m *Manager) LogLines(n int) []string {
	m.mu.Lock()
	proc := m.proc
	m.mu.Unlock()
	if proc == nil {
		return nil
	}
	return proc.LogLines(n)
}

// IsRunning returns true when the server is healthy and accepting requests.
func (m *Manager) IsRunning() bool {
	return m.State() == StateRunning
}

// ResolvedBinPath returns the binary path currently in use (may be empty).
func (m *Manager) ResolvedBinPath() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.binPath
}

// ── tea.Cmd helpers ───────────────────────────────────────────────────────

// StartCmd spawns the llama-server process and returns a RunnerStartedMsg.
// The process is spawned but not yet health-checked; use PollUntilReadyCmd
// (or StartAndPollCmd) to wait for readiness.
func (m *Manager) StartCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.start(); err != nil {
			return RunnerStartedMsg{Err: err}
		}
		return RunnerStartedMsg{}
	}
}

// Stop synchronously stops the running process. Intended for use during
// application shutdown where a tea.Cmd cannot be returned.
func (m *Manager) Stop() error {
	return m.stop()
}

// StopCmd stops the running process and returns a RunnerStoppedMsg.
func (m *Manager) StopCmd() tea.Cmd {
	return func() tea.Msg {
		return RunnerStoppedMsg{Err: m.stop()}
	}
}

// HealthCheckCmd performs a single GET /health and returns a HealthCheckMsg.
func (m *Manager) HealthCheckCmd() tea.Cmd {
	return func() tea.Msg {
		ok, err := m.healthCheck()
		if ok {
			m.mu.Lock()
			m.markRunningLocked()
			m.mu.Unlock()
		}
		return HealthCheckMsg{OK: ok, Err: err}
	}
}

// PollUntilReadyCmd polls GET /health every 500 ms until the server responds 200
// or the timeout elapses. Returns RunnerStartedMsg; Err is set on timeout.
//
// The process must already be running. To start and poll in one step use
// StartAndPollCmd.
func (m *Manager) PollUntilReadyCmd(timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if m.procExitedWithError() {
				return RunnerStartedMsg{Err: fmt.Errorf("process exited unexpectedly")}
			}
			if ok, _ := m.healthCheck(); ok {
				m.mu.Lock()
				m.markRunningLocked()
				m.mu.Unlock()
				return RunnerStartedMsg{}
			}
			time.Sleep(500 * time.Millisecond)
		}
		return RunnerStartedMsg{Err: fmt.Errorf("server did not become healthy within %s", timeout)}
	}
}

// Uptime returns how long the server has been healthy (since the first passing
// health check). Returns 0 when the server is not currently running.
func (m *Manager) Uptime() time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.startedAt.IsZero() {
		return 0
	}
	return time.Since(m.startedAt)
}

// PID returns the OS process ID of the running llama-server, or 0.
func (m *Manager) PID() int {
	m.mu.Lock()
	proc := m.proc
	m.mu.Unlock()
	if proc == nil {
		return 0
	}
	return proc.Pid()
}

// RestartCmd stops the current process, starts a fresh one, and polls until
// healthy or timeout elapses. Returns RunnerStartedMsg.
// This is the single cmd to use for /run restart and /model <path> switches.
func (m *Manager) RestartCmd(timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		_ = m.stop()
		if err := m.start(); err != nil {
			return RunnerStartedMsg{Err: err}
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if m.procExitedWithError() {
				return RunnerStartedMsg{Err: fmt.Errorf("process exited unexpectedly")}
			}
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

// StartAndPollCmd starts the process then polls until healthy or the timeout
// elapses. This is the single cmd to use from the setup screen's
// stepStartingRunner entry.
func (m *Manager) StartAndPollCmd(timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		// Adopt an already-healthy server rather than trying to bind the same port.
		if ok, _ := m.healthCheck(); ok {
			m.mu.Lock()
			m.markRunningLocked()
			m.mu.Unlock()
			return RunnerStartedMsg{}
		}
		if err := m.start(); err != nil {
			return RunnerStartedMsg{Err: err}
		}
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			if m.procExitedWithError() {
				return RunnerStartedMsg{Err: fmt.Errorf("process exited unexpectedly")}
			}
			if ok, _ := m.healthCheck(); ok {
				m.mu.Lock()
				m.markRunningLocked()
				m.mu.Unlock()
				return RunnerStartedMsg{}
			}
			time.Sleep(500 * time.Millisecond)
		}
		return RunnerStartedMsg{Err: fmt.Errorf("server did not become healthy within %s", timeout)}
	}
}

// ── internals ─────────────────────────────────────────────────────────────

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.binPath == "" {
		p, found := Detect(m.cfg)
		if !found {
			return fmt.Errorf("llama-server not found; run setup or set binary_path in config")
		}
		m.binPath = p
	}
	if m.cfg.ModelPath == "" {
		return fmt.Errorf("model_path is not configured")
	}

	m.proc = NewProcess(m.binPath, buildArgs(m.cfg))
	m.proc.Env(libPathEnv(m.binPath))
	m.state = StateStarting

	if err := m.proc.Start(); err != nil {
		m.state = StateError
		return fmt.Errorf("start llama-server: %w", err)
	}
	return nil
}

func (m *Manager) stop() error {
	m.mu.Lock()
	proc := m.proc
	m.mu.Unlock()

	if proc == nil {
		return nil
	}
	err := proc.Stop()
	m.mu.Lock()
	m.state = StateStopped
	m.startedAt = time.Time{}
	m.mu.Unlock()
	return err
}

func (m *Manager) healthCheck() (bool, error) {
	m.mu.Lock()
	port := m.cfg.Port
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	url := fmt.Sprintf("http://127.0.0.1:%d/health", port)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return false, err
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK, nil
}

// syncProcessStateLocked updates m.state to reflect the current process state.
// Must be called with m.mu held.
func (m *Manager) syncProcessStateLocked() {
	if m.proc == nil {
		return
	}
	switch m.proc.State() {
	case ProcessStopped:
		if m.state == StateStarting || m.state == StateRunning {
			m.state = StateStopped
			m.startedAt = time.Time{}
		}
	case ProcessError:
		m.state = StateError
		m.startedAt = time.Time{}
	}
}

// markRunningLocked transitions the manager and process to the Running state.
// Must be called with m.mu held.
func (m *Manager) markRunningLocked() {
	if m.proc != nil {
		m.proc.MarkRunning()
	}
	m.state = StateRunning
	// Record the moment the server became healthy (not when the process started).
	if m.startedAt.IsZero() {
		m.startedAt = time.Now()
	}
}

// procExitedWithError returns true if the managed process has exited with a
// non-zero status. Safe for concurrent use; does not require m.mu.
func (m *Manager) procExitedWithError() bool {
	m.mu.Lock()
	proc := m.proc
	m.mu.Unlock()
	if proc == nil {
		return false
	}
	return proc.State() == ProcessError
}

// computeStateLocked returns the appropriate initial state based on currently
// cached fields. Does not perform any I/O. Caller must hold m.mu.
func (m *Manager) computeStateLocked() ManagerState {
	if m.binPath == "" || m.cfg.ModelPath == "" {
		return StateUnconfigured
	}
	return StateReady
}

// libPathEnv returns environment variable overrides that prepend the directory
// containing binPath to the OS-specific shared-library search path.
// This allows companion .so / .dylib / .dll files sitting next to the binary
// (e.g. libllama-server-impl.so) to be found without any system-wide config.
func libPathEnv(binPath string) []string {
	dir := filepath.Dir(binPath)
	var vars []string
	if existing := os.Getenv("LD_LIBRARY_PATH"); existing != "" {
		vars = append(vars, "LD_LIBRARY_PATH="+dir+":"+existing)
	} else {
		vars = append(vars, "LD_LIBRARY_PATH="+dir)
	}
	// macOS uses a different variable; setting both is harmless on each platform.
	if existing := os.Getenv("DYLD_LIBRARY_PATH"); existing != "" {
		vars = append(vars, "DYLD_LIBRARY_PATH="+dir+":"+existing)
	} else {
		vars = append(vars, "DYLD_LIBRARY_PATH="+dir)
	}
	return vars
}

// buildArgs constructs the llama-server command-line arguments from cfg.
func buildArgs(cfg config.RunnerConfig) []string {
	args := []string{
		"--model", cfg.ModelPath,
		"--port", fmt.Sprintf("%d", cfg.Port),
		"--ctx-size", fmt.Sprintf("%d", cfg.ContextSize),
		"--host", "127.0.0.1",
	}
	if cfg.GPULayers > 0 {
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", cfg.GPULayers))
	}
	if cfg.Threads != 0 {
		args = append(args, "--threads", fmt.Sprintf("%d", cfg.Threads))
	}
	if cfg.FlashAttn {
		args = append(args, "--flash-attn", "on")
	}
	if !cfg.Verbose {
		args = append(args, "--log-disable")
	}
	return args
}
