package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
)

// Detect locates a usable llama-server binary. It searches in order:
//  1. cfg.BinaryPath (if set, exists, and is executable)
//  2. system PATH via exec.LookPath
//  3. Livie's own managed binary directory (~/.local/share/livie/bin/)
//
// Returns the resolved path and true, or ("", false) if none found.
func Detect(cfg RunnerConfig) (path string, found bool) {
	// 1. Explicit override.
	if cfg.BinaryPath != "" {
		if isExecutable(cfg.BinaryPath) {
			return cfg.BinaryPath, true
		}
	}

	// 2. System PATH.
	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, true
	}

	// 3. Livie data-dir binary.
	managed := DataDirBinaryPath()
	if isExecutable(managed) {
		return managed, true
	}

	return "", false
}

// DataDirBinaryPath returns the path where Livie stores its own llama-server binary.
func DataDirBinaryPath() string {
	home, _ := os.UserHomeDir()
	p := New(GPUBackendCPU) // CPU variant just for BinaryName()
	p.OS = runtime.GOOS
	return filepath.Join(home, ".local", "share", "livie", "bin", p.BinaryName())
}

// DataDirBinDir returns the directory that contains the managed binary.
func DataDirBinDir() string {
	return filepath.Dir(DataDirBinaryPath())
}

// isExecutable returns true when the file exists and is executable by the
// current process.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	// On Windows the executable bit is not meaningful; existence suffices.
	if runtime.GOOS == "windows" {
		return !info.IsDir()
	}
	return info.Mode()&0o111 != 0
}

// fileExists returns true when path exists (any kind).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
