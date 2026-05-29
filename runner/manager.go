package runner

import (
	"context"
	"fmt"
	"net/http"
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

// RunnerConfig is a type alias so external callers don't need to import config/.
// The manager accepts config.RunnerConfig directly.
type RunnerConfig = config.RunnerConfig

// Manager is the high-level API for the llama-server subprocess.
// It holds the resolved binary path, active config, platform, and process.
type Manager struct {
	mu         sync.Mutex
	cfg        RunnerConfig
	platform   Platform
	binPath    string        // resolved executable path
	proc       *Process
	manState   ManagerState
}

// NewManager creates a Manager. The binary is not started until StartCmd() is called.
func NewManager(cfg config.RunnerConfig) *Manager {
	m := &Manager{
		cfg:      cfg,
		platform: New(ParseBackend(cfg.GPUBackend)),
	}
	m.manState = m.computeState()
	return m
}

// Configure updates the manager's config without restarting the process.
func (m *Manager) Configure(cfg config.RunnerConfig) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg = cfg
	m.platform = New(ParseBackend(cfg.GPUBackend))
	if m.manState == StateUnconfigured || m.manState == StateReady {
		m.manState = m.computeStateLocked()
	}
}

// SetBinaryPath overrides the resolved binary path.
func (m *Manager) SetBinaryPath(p string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.binPath = p
	if m.manState == StateUnconfigured {
		m.manState = m.computeStateLocked()
	}
}

// State returns the current manager state.
func (m *Manager) State() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Sync manager state with process state when a process exists.
	if m.proc != nil {
		switch m.proc.State() {
		case ProcessStopped:
			if m.manState == StateStarting || m.manState == StateRunning {
				m.manState = StateStopped
			}
		case ProcessError:
			m.manState = StateError
		}
	}
	return m.manState
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

// ── tea.Cmd helpers ────────────────────────────────────────────────────────

// StartCmd spawns the llama-server process and returns a tea.Cmd.
// The cmd returns RunnerStartedMsg when the process has been launched
// (not yet health-checked — use PollUntilReadyCmd for that).
func (m *Manager) StartCmd() tea.Cmd {
	return func() tea.Msg {
		if err := m.start(); err != nil {
			return RunnerStartedMsg{Err: err}
		}
		return RunnerStartedMsg{}
	}
}

// StopCmd stops the running process and returns a tea.Cmd.
func (m *Manager) StopCmd() tea.Cmd {
	return func() tea.Msg {
		err := m.stop()
		return RunnerStoppedMsg{Err: err}
	}
}

// HealthCheckCmd performs a single GET /health and returns a tea.Cmd.
func (m *Manager) HealthCheckCmd() tea.Cmd {
	return func() tea.Msg {
		ok, err := m.healthCheck()
		if ok {
			m.mu.Lock()
			if m.proc != nil {
				m.proc.MarkRunning()
			}
			m.manState = StateRunning
			m.mu.Unlock()
		}
		return HealthCheckMsg{OK: ok, Err: err}
	}
}

// PollUntilReadyCmd polls GET /health every 500ms until the server responds 200
// or the timeout elapses. Returns RunnerStartedMsg with Err set on timeout.
func (m *Manager) PollUntilReadyCmd(timeout time.Duration) tea.Cmd {
	return func() tea.Msg {
		deadline := time.Now().Add(timeout)
		for time.Now().Before(deadline) {
			ok, _ := m.healthCheck()
			if ok {
				m.mu.Lock()
				if m.proc != nil {
					m.proc.MarkRunning()
				}
				m.manState = StateRunning
				m.mu.Unlock()
				return RunnerStartedMsg{}
			}
			time.Sleep(500 * time.Millisecond)
		}
		return RunnerStartedMsg{Err: fmt.Errorf("server did not become healthy within %s", timeout)}
	}
}

// ── internals ──────────────────────────────────────────────────────────────

func (m *Manager) start() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.binPath == "" {
		// Try to auto-detect.
		p, found := Detect(m.cfg)
		if !found {
			return fmt.Errorf("llama-server not found; run setup or set binary_path in config")
		}
		m.binPath = p
	}
	if m.cfg.ModelPath == "" {
		return fmt.Errorf("model_path is not configured")
	}

	args := buildArgs(m.cfg)
	m.proc = NewProcess(m.binPath, args)
	m.manState = StateStarting

	if err := m.proc.Start(); err != nil {
		m.manState = StateError
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
	m.manState = StateStopped
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

// computeState returns the initial ManagerState based on cfg and resolved path.
// Must be called without holding m.mu (uses computeStateLocked internally).
func (m *Manager) computeState() ManagerState {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.computeStateLocked()
}

// computeStateLocked computes state; caller must hold m.mu.
func (m *Manager) computeStateLocked() ManagerState {
	hasBin := m.binPath != "" || m.cfg.BinaryPath != ""
	if !hasBin {
		// Check PATH and data dir.
		_, found := Detect(m.cfg)
		hasBin = found
	}
	if !hasBin || m.cfg.ModelPath == "" {
		return StateUnconfigured
	}
	return StateReady
}

// buildArgs constructs the llama-server command-line arguments from cfg.
func buildArgs(cfg RunnerConfig) []string {
	args := []string{
		"--model", cfg.ModelPath,
		"--port", fmt.Sprintf("%d", cfg.Port),
		"--ctx-size", fmt.Sprintf("%d", cfg.ContextSize),
		"--host", "127.0.0.1",
	}
	if cfg.GPULayers != 0 {
		args = append(args, "--n-gpu-layers", fmt.Sprintf("%d", cfg.GPULayers))
	}
	if cfg.Threads != 0 {
		args = append(args, "--threads", fmt.Sprintf("%d", cfg.Threads))
	}
	if cfg.FlashAttn {
		args = append(args, "--flash-attn")
	}
	if !cfg.Verbose {
		args = append(args, "--log-disable")
	}
	return args
}
