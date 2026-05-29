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
	ProcessStarting                     // exec.Cmd created, Start() called
	ProcessRunning                      // process is alive (health check is separate)
	ProcessStopped                      // cleanly stopped
	ProcessError                        // exited with non-zero code or failed to start
)

const ringBufCap = 500

// ringBuf is a fixed-capacity circular line buffer.
type ringBuf struct {
	mu    sync.Mutex
	lines []string
	cap   int
	head  int // index of next write position
	size  int // number of lines stored
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
	// oldest relevant index
	start := (r.head - n + r.cap) % r.cap
	for i := 0; i < n; i++ {
		out[i] = r.lines[(start+i)%r.cap]
	}
	return out
}

// Process manages a single llama-server subprocess.
type Process struct {
	mu     sync.Mutex
	cmd    *exec.Cmd
	state  ProcessState
	log    *ringBuf
	cancel func() // kills the process on demand
}

// NewProcess creates a Process configured to run binPath with args.
func NewProcess(binPath string, args []string) *Process {
	return &Process{
		state: ProcessIdle,
		log:   newRingBuf(ringBufCap),
		cmd:   exec.Command(binPath, args...),
	}
}

// Start launches the process and begins capturing its output.
func (p *Process) Start() error {
	p.mu.Lock()
	defer p.mu.Unlock()

	if p.state == ProcessStarting || p.state == ProcessRunning {
		return nil
	}

	// Fresh pipes for stdout and stderr.
	stdout, err := p.cmd.StdoutPipe()
	if err != nil {
		return err
	}
	stderr, err := p.cmd.StderrPipe()
	if err != nil {
		return err
	}

	if err := p.cmd.Start(); err != nil {
		p.state = ProcessError
		return err
	}
	p.state = ProcessStarting

	// Goroutines to drain stdout and stderr into the ring buffer.
	go p.captureLines(stdout)
	go p.captureLines(stderr)

	// Goroutine to update state when the process exits.
	go func() {
		_ = p.cmd.Wait()
		p.mu.Lock()
		p.state = ProcessStopped
		if p.cmd.ProcessState != nil && !p.cmd.ProcessState.Success() {
			p.state = ProcessError
		}
		p.mu.Unlock()
	}()

	return nil
}

// MarkRunning transitions the state from ProcessStarting to ProcessRunning.
// Called by the manager once the health check passes.
func (p *Process) MarkRunning() {
	p.mu.Lock()
	if p.state == ProcessStarting {
		p.state = ProcessRunning
	}
	p.mu.Unlock()
}

// Stop sends SIGTERM to the process. If it has not exited within 5 seconds,
// SIGKILL is sent.
func (p *Process) Stop() error {
	p.mu.Lock()
	proc := p.cmd.Process
	state := p.state
	p.mu.Unlock()

	if proc == nil || state == ProcessIdle || state == ProcessStopped {
		return nil
	}

	// Attempt graceful shutdown.
	if err := proc.Signal(syscall.SIGTERM); err != nil {
		// Process may already have exited.
		return nil
	}

	done := make(chan struct{})
	go func() {
		_ = p.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = proc.Signal(syscall.SIGKILL)
		<-done
	}

	p.mu.Lock()
	p.state = ProcessStopped
	p.mu.Unlock()
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

// Pid returns the OS process ID, or 0 if not running.
func (p *Process) Pid() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd.Process == nil {
		return 0
	}
	return p.cmd.Process.Pid
}

// captureLines reads lines from r and pushes them into the ring buffer.
func (p *Process) captureLines(r io.Reader) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		p.log.push(scanner.Text())
	}
}

// Env sets environment variables on the underlying command.
// Must be called before Start().
func (p *Process) Env(env []string) {
	p.cmd.Env = append(os.Environ(), env...)
}
