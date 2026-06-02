package runner

import (
	"bufio"
	"io"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"
)

// ProcessState represents the lifecycle stage of the managed subprocess.
type ProcessState int

const (
	ProcessIdle     ProcessState = iota // not yet started
	ProcessStarting                     // Start() called, health not yet confirmed
	ProcessRunning                      // health check passing
	ProcessStopped                      // cleanly stopped (or stopped by user)
	ProcessError                        // exited with non-zero code or failed to start
)

const ringBufCap = 500

// ── ring buffer ───────────────────────────────────────────────────────────

// ringBuf is a fixed-capacity circular line buffer. Safe for concurrent use.
type ringBuf struct {
	mu    sync.Mutex
	lines []string
	cap   int
	head  int // index of the next write slot
	size  int // number of lines currently stored
}

func newRingBuf(capacity int) *ringBuf {
	return &ringBuf{lines: make([]string, capacity), cap: capacity}
}

func (r *ringBuf) push(line string) {
	r.mu.Lock()
	r.lines[r.head] = line
	r.head = (r.head + 1) % r.cap
	if r.size < r.cap {
		r.size++
	}
	r.mu.Unlock()
}

// last returns the last n lines in chronological order.
func (r *ringBuf) last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.size == 0 {
		return nil
	}
	if n > r.size {
		n = r.size
	}
	out := make([]string, n)
	start := (r.head - n + r.cap) % r.cap
	for i := 0; i < n; i++ {
		out[i] = r.lines[(start+i)%r.cap]
	}
	return out
}

// ── Process ───────────────────────────────────────────────────────────────

// Process manages a single llama-server subprocess.
type Process struct {
	mu            sync.Mutex
	cmd           *exec.Cmd
	state         ProcessState
	log           *ringBuf
	done          chan struct{} // closed by the wait goroutine once the process exits
	stoppedByUser bool         // set by Stop() before sending SIGTERM
}

// NewProcess creates a Process ready to run binPath with args.
// Call Start() to launch it.
func NewProcess(binPath string, args []string) *Process {
	return &Process{
		state: ProcessIdle,
		log:   newRingBuf(ringBufCap),
		cmd:   exec.Command(binPath, args...),
	}
}

// Start launches the subprocess and begins capturing its output into the ring buffer.
// Safe to call only once; subsequent calls while running are no-ops.
func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == ProcessStarting || p.state == ProcessRunning {
		return nil
	}

	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		return err
	}

	// Allocate the done channel before Start() so Stop() can safely read it.
	p.done = make(chan struct{})

	if err := p.cmd.Start(); err != nil {
		p.state = ProcessError
		close(p.done) // unblock any concurrent Stop() call
		return err
	}
	p.state = ProcessStarting

	go p.captureLines(stdout)
	go p.captureLines(stderr)

	// Wait goroutine: sets final state and signals done.
	go func() {
		_ = p.cmd.Wait()

		p.mu.Lock()
		if p.stoppedByUser || (p.cmd.ProcessState != nil && p.cmd.ProcessState.Success()) {
			p.state = ProcessStopped
		} else {
			p.state = ProcessError
		}
		p.mu.Unlock()

		close(p.done)
	}()

	return nil
}

// MarkRunning transitions the state from ProcessStarting to ProcessRunning.
// Called by the Manager once the health check passes.
func (p *Process) MarkRunning() {
	p.mu.Lock()
	if p.state == ProcessStarting {
		p.state = ProcessRunning
	}
	p.mu.Unlock()
}

// Stop sends SIGTERM to the subprocess. If the process has not exited within
// 5 seconds, SIGKILL is sent. Blocks until the process has fully exited.
//
// The wait goroutine — not Stop — owns the final state transition, so there
// is no double-Wait race.
func (p *Process) Stop() error {
	p.mu.Lock()
	proc := p.cmd.Process
	state := p.state
	done := p.done
	if proc != nil && state != ProcessIdle && state != ProcessStopped {
		p.stoppedByUser = true
	}
	p.mu.Unlock()

	// Nothing to stop.
	if proc == nil || state == ProcessIdle || state == ProcessStopped || done == nil {
		return nil
	}

	// Graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process already exited — wait for the done signal anyway.
		<-done
		return nil
	}

	select {
	case <-done:
		// Process exited cleanly within the grace period.
	case <-time.After(5 * time.Second):
		// Force-kill and wait for the goroutine to close done.
		_ = proc.Signal(syscall.SIGKILL)
		<-done
	}
	return nil
}

// State returns the current ProcessState.
func (p *Process) State() ProcessState {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.state
}

// LogLines returns the last n lines captured from stdout/stderr.
func (p *Process) LogLines(n int) []string {
	return p.log.last(n)
}

// Pid returns the OS process ID, or 0 if the process has not been started.
func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// Env sets additional environment variables on the subprocess.
// Must be called before Start().
func (p *Process) Env(extra []string) {
	p.cmd.Env = append(os.Environ(), extra...)
}

// captureLines reads lines from r and pushes them into the ring buffer.
func (p *Process) captureLines(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		p.log.push(scanner.Text())
	}
}
