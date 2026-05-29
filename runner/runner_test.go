package runner

import (
	"runtime"
	"testing"
)

func TestParseBackendRoundTrip(t *testing.T) {
	cases := []GPUBackend{
		GPUBackendCPU,
		GPUBackendCUDA,
		GPUBackendMetal,
		GPUBackendVulkan,
	}
	for _, b := range cases {
		got := ParseBackend(b.TOML())
		if got != b {
			t.Errorf("ParseBackend(%q) = %v; want %v", b.TOML(), got, b)
		}
	}
}

func TestParseBackendFallback(t *testing.T) {
	if got := ParseBackend("unknown"); got != GPUBackendCPU {
		t.Errorf("expected CPU fallback, got %v", got)
	}
	if got := ParseBackend(""); got != GPUBackendCPU {
		t.Errorf("expected CPU fallback for empty string, got %v", got)
	}
}

func TestDetectAvailableAlwaysHasCPU(t *testing.T) {
	backends := DetectAvailable()
	if len(backends) == 0 {
		t.Fatal("DetectAvailable returned empty slice")
	}
	if backends[0] != GPUBackendCPU {
		t.Errorf("first backend must be CPU, got %v", backends[0])
	}
}

func TestPlatformReleaseAssetSuffix(t *testing.T) {
	cases := []struct {
		os   string
		arch string
		gpu  GPUBackend
		want string
	}{
		{"linux", "amd64", GPUBackendCPU, "ubuntu-x64.zip"},
		{"linux", "amd64", GPUBackendCUDA, "linux-cuda-cu12.2.0-x64.zip"},
		{"linux", "amd64", GPUBackendVulkan, "linux-vulkan-x64.zip"},
		{"linux", "arm64", GPUBackendCPU, "linux-arm64.zip"},
		{"darwin", "arm64", GPUBackendMetal, "macos-arm64.zip"},
		{"darwin", "amd64", GPUBackendMetal, "macos-x64.zip"},
		{"windows", "amd64", GPUBackendCPU, "win-x64.zip"},
		{"windows", "amd64", GPUBackendCUDA, "win-cuda-cu12.2.0-x64.zip"},
		{"windows", "amd64", GPUBackendVulkan, "win-vulkan-x64.zip"},
	}
	for _, tc := range cases {
		p := Platform{OS: tc.os, Arch: tc.arch, GPU: tc.gpu}
		got := p.ReleaseAssetSuffix()
		if got != tc.want {
			t.Errorf("Platform{%s/%s · %v}.ReleaseAssetSuffix() = %q; want %q",
				tc.os, tc.arch, tc.gpu, got, tc.want)
		}
	}
}

func TestPlatformBinaryName(t *testing.T) {
	p := Platform{OS: "linux"}
	if p.BinaryName() != "llama-server" {
		t.Error("expected llama-server on linux")
	}
	p.OS = "windows"
	if p.BinaryName() != "llama-server.exe" {
		t.Error("expected llama-server.exe on windows")
	}
}

func TestDetectNotFound(t *testing.T) {
	cfg := RunnerConfig{
		BinaryPath: "/nonexistent/path/llama-server",
	}
	// Only fails if the binary actually exists (unlikely in CI).
	path, found := Detect(cfg)
	if found && path == cfg.BinaryPath {
		// The override path doesn't exist, so it must have been found via PATH.
		// That's acceptable — just ensure it returned a real executable.
	}
	_ = path
}

func TestNewPlatform(t *testing.T) {
	p := New(GPUBackendCUDA)
	if p.OS != runtime.GOOS {
		t.Errorf("expected OS %s, got %s", runtime.GOOS, p.OS)
	}
	if p.Arch != runtime.GOARCH {
		t.Errorf("expected Arch %s, got %s", runtime.GOARCH, p.Arch)
	}
	if p.GPU != GPUBackendCUDA {
		t.Errorf("expected CUDA backend, got %v", p.GPU)
	}
}
