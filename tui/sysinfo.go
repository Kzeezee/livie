package tui

import (
	"os"
	"os/exec"
	"runtime"
	"strings"
)

// SysInfo holds system information for the welcome screen.
type SysInfo struct {
	Username string
	Hostname string
	OS       string
	Shell    string
	Terminal string
	GoVersion string
}

// GatherSysInfo collects system information at startup.
func GatherSysInfo() SysInfo {
	hostname, _ := os.Hostname()
	username := os.Getenv("USER")
	if username == "" {
		username = os.Getenv("USERNAME") // Windows
	}
	if username == "" {
		username = "user"
	}

	return SysInfo{
		Username:  username,
		Hostname:  hostname,
		OS:        detectOS(),
		Shell:     detectShell(),
		Terminal:  detectTerminal(),
		GoVersion: runtime.Version(),
	}
}

func detectOS() string {
	switch runtime.GOOS {
	case "linux":
		return detectLinuxDistro()
	case "darwin":
		return detectMacOS()
	case "windows":
		return "Windows"
	default:
		return runtime.GOOS
	}
}

func detectLinuxDistro() string {
	data, err := os.ReadFile("/etc/os-release")
	if err != nil {
		return "Linux"
	}
	for _, line := range strings.Split(string(data), "\n") {
		if strings.HasPrefix(line, "PRETTY_NAME=") {
			name := strings.TrimPrefix(line, "PRETTY_NAME=")
			name = strings.Trim(name, `"`)
			return name
		}
	}
	return "Linux"
}

func detectMacOS() string {
	out, err := exec.Command("sw_vers", "-productVersion").Output()
	if err != nil {
		return "macOS"
	}
	return "macOS " + strings.TrimSpace(string(out))
}

func detectShell() string {
	shell := os.Getenv("SHELL")
	if shell == "" {
		return "unknown"
	}
	parts := strings.Split(shell, "/")
	name := parts[len(parts)-1]

	// Try to get version
	out, err := exec.Command(shell, "--version").Output()
	if err != nil {
		return name
	}
	first := strings.SplitN(string(out), "\n", 2)[0]
	// Extract version number heuristically
	for _, word := range strings.Fields(first) {
		if len(word) > 0 && (word[0] >= '0' && word[0] <= '9') {
			return name + " " + word
		}
	}
	return name
}

func detectTerminal() string {
	if t := os.Getenv("TERM_PROGRAM"); t != "" {
		return t
	}
	if os.Getenv("KITTY_WINDOW_ID") != "" {
		return "kitty"
	}
	if t := os.Getenv("TERM"); t != "" {
		return t
	}
	return "unknown"
}
