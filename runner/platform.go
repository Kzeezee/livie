// Package runner manages the llama-server subprocess lifecycle and related
// helpers for detection, download, and process management.
package runner

import (
	"os/exec"
	"runtime"
	"strings"
)

// GPUBackend identifies the compute backend used to accelerate inference.
type GPUBackend int

const (
	GPUBackendCPU    GPUBackend = iota // always available
	GPUBackendCUDA                     // NVIDIA CUDA
	GPUBackendMetal                    // Apple Metal (macOS)
	GPUBackendVulkan                   // AMD / generic Vulkan
)

// String returns the human-readable backend label.
func (g GPUBackend) String() string {
	switch g {
	case GPUBackendCUDA:
		return "CUDA"
	case GPUBackendMetal:
		return "Metal"
	case GPUBackendVulkan:
		return "Vulkan"
	default:
		return "CPU"
	}
}

// TOML returns the lowercase config file token for this backend.
func (g GPUBackend) TOML() string {
	return strings.ToLower(g.String())
}

// ParseBackend converts a TOML string back to a GPUBackend.
// Unknown values fall back to GPUBackendCPU.
func ParseBackend(s string) GPUBackend {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "cuda":
		return GPUBackendCUDA
	case "metal":
		return GPUBackendMetal
	case "vulkan":
		return GPUBackendVulkan
	default:
		return GPUBackendCPU
	}
}

// Platform describes the target OS/arch/GPU combination used for binary
// selection and download asset naming.
type Platform struct {
	OS   string     // runtime.GOOS
	Arch string     // runtime.GOARCH
	GPU  GPUBackend // user-chosen
}

// New constructs a Platform for the current host with the user-chosen GPU backend.
func New(gpu GPUBackend) Platform {
	return Platform{
		OS:   runtime.GOOS,
		Arch: runtime.GOARCH,
		GPU:  gpu,
	}
}

// DetectAvailable returns which backends are likely usable on this machine.
// The first element is always GPUBackendCPU (always available).
// The list is used by the setup screen to show "detected ✓" indicators.
func DetectAvailable() []GPUBackend {
	backends := []GPUBackend{GPUBackendCPU}

	switch runtime.GOOS {
	case "darwin":
		backends = append(backends, GPUBackendMetal)
	default:
		// Check for NVIDIA CUDA via nvidia-smi on PATH.
		if _, err := exec.LookPath("nvidia-smi"); err == nil {
			backends = append(backends, GPUBackendCUDA)
		}
		// Check for AMD / Vulkan via /dev/kfd or rocm-smi.
		if fileExists("/dev/kfd") {
			backends = append(backends, GPUBackendVulkan)
		} else if _, err := exec.LookPath("rocm-smi"); err == nil {
			backends = append(backends, GPUBackendVulkan)
		}
	}

	return backends
}

// ReleaseAssetSuffix returns the filename suffix used to select the correct
// llama.cpp GitHub release archive for this platform.
//
// Asset naming as of b9452+:
//   - Linux/macOS: llama-<tag>-bin-<os>-<variant>-<arch>.tar.gz
//   - Windows:     llama-<tag>-bin-win-<variant>-<arch>.zip
func (p Platform) ReleaseAssetSuffix() string {
	switch p.OS {
	case "darwin":
		if p.Arch == "arm64" {
			return "bin-macos-arm64.tar.gz"
		}
		return "bin-macos-x64.tar.gz"
	case "windows":
		switch p.GPU {
		case GPUBackendCUDA:
			// Prefer CUDA 12 build; falls back gracefully if not found.
			return "bin-win-cuda-12.4-x64.zip"
		case GPUBackendVulkan:
			return "bin-win-vulkan-x64.zip"
		default:
			if p.Arch == "arm64" {
				return "bin-win-cpu-arm64.zip"
			}
			return "bin-win-cpu-x64.zip"
		}
	default: // linux
		if p.Arch == "arm64" {
			return "bin-ubuntu-arm64.tar.gz"
		}
		switch p.GPU {
		case GPUBackendVulkan:
			return "bin-ubuntu-vulkan-x64.tar.gz"
		default:
			// No separate CUDA linux build is published; the standard x64
			// build supports CPU inference and is the best available fallback.
			return "bin-ubuntu-x64.tar.gz"
		}
	}
}

// BinaryName returns the platform-appropriate executable name.
func (p Platform) BinaryName() string {
	if p.OS == "windows" {
		return "llama-server.exe"
	}
	return "llama-server"
}

// Description returns a short human-readable label, e.g. "linux/amd64 · CUDA".
func (p Platform) Description() string {
	return p.OS + "/" + p.Arch + " · " + p.GPU.String()
}
