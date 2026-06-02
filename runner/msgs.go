package runner

// msgs.go — all Bubbletea message types exported by the runner package.
// The TUI imports only these types; it does not reach into runner internals.

// RunnerStartedMsg is returned by Manager.StartCmd() and PollUntilReadyCmd().
// Err is non-nil when the process failed to start or timed out during health polling.
type RunnerStartedMsg struct{ Err error }

// RunnerStoppedMsg is returned by Manager.StopCmd().
type RunnerStoppedMsg struct{ Err error }

// HealthCheckMsg is returned by Manager.HealthCheckCmd() after one GET /health call.
type HealthCheckMsg struct {
	OK  bool
	Err error
}

// DownloadProgressMsg is returned by DownloadProgressCmd() on each channel read.
// It is a type alias for ProgressUpdate so setup.go can use runner.DownloadProgressMsg.
type DownloadProgressMsg = ProgressUpdate

// DetectCompleteMsg is returned by the setup screen's detectCmd().
type DetectCompleteMsg struct {
	Found bool
	Path  string
}
