package runner

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"

	"github.com/kez/livie/config"
)

// Detect locates a usable llama-server binary. It searches in order:
//  1. cfg.BinaryPath — if non-empty, exists, and is executable
//  2. System PATH via exec.LookPath
//  3. Livie's own managed binary at DataDirBinaryPath()
//
// Returns the resolved path and true, or ("", false) if none is found.
func Detect(cfg config.RunnerConfig) (path string, found bool) {
	if cfg.BinaryPath != "" {
		if isExecutable(cfg.BinaryPath) {
			return cfg.BinaryPath, true
		}
	}

	if p, err := exec.LookPath("llama-server"); err == nil {
		return p, true
	}

	managed := DataDirBinaryPath()
	if isExecutable(managed) {
		return managed, true
	}

	return "", false
}

// DataDirBinaryPath returns the path where Livie stores its managed llama-server binary.
func DataDirBinaryPath() string {
	home, _ := os.UserHomeDir()
	name := "llama-server"
	if runtime.GOOS == "windows" {
		name = "llama-server.exe"
	}
	return filepath.Join(home, ".local", "share", "livie", "bin", name)
}

// DataDirBinDir returns the directory that contains the managed binary.
func DataDirBinDir() string {
	return filepath.Dir(DataDirBinaryPath())
}

// isExecutable returns true when path exists and is executable.
// On Windows, existence of any non-directory file is sufficient.
func isExecutable(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		return !info.IsDir()
	}
	return info.Mode()&0o111 != 0
}

// fileExists returns true when path exists (file or directory).
func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
