package runner

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// SetupState records the setup wizard's progress between app launches so the
// user can continue where they left off after a restart or crash.
//
// It is written atomically to ~/.local/share/livie/setup-state.json at every
// meaningful step transition and deleted when setup completes.
type SetupState struct {
	// Step is the name of the furthest step reached, e.g. "gpu_select".
	// Valid values: "install_prompt", "gpu_select", "downloading",
	//               "mode_select", "config_local", "config_remote".
	Step string `json:"step"`

	// Detection results carried forward to skip re-scanning on resume.
	LlamaInstalled  bool   `json:"llama_installed"`
	DetectedBinPath string `json:"detected_bin_path,omitempty"`

	// GPU backend chosen at stepGPUSelect, e.g. "CUDA", "Metal", "CPU".
	GPUBackend string `json:"gpu_backend,omitempty"`

	// Connection mode chosen at stepModeSelect: "local" or "remote".
	Mode string `json:"mode,omitempty"`

	// Local runner config form values (stepConfigLocal).
	ModelPath   string `json:"model_path,omitempty"`
	GPULayers   int    `json:"gpu_layers,omitempty"`
	ContextSize int    `json:"context_size,omitempty"`
	Port        int    `json:"port,omitempty"`

	// Remote endpoint config form values (stepConfigRemote).
	BaseURL string `json:"base_url,omitempty"`
	APIKey  string `json:"api_key,omitempty"`
	Model   string `json:"model,omitempty"`
}

// setupStatePath returns the absolute path to the setup state file.
func setupStatePath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".local", "share", "livie", "setup-state.json")
}

// LoadSetupState reads a previously saved setup state from disk.
// Returns (nil, false) when no state file exists or it cannot be parsed.
func LoadSetupState() (*SetupState, bool) {
	data, err := os.ReadFile(setupStatePath())
	if err != nil {
		return nil, false
	}
	var s SetupState
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, false
	}
	if s.Step == "" {
		return nil, false
	}
	return &s, true
}

// SaveSetupState persists the current wizard progress to disk atomically.
// Errors are silently ignored — failure to save state is non-fatal.
func SaveSetupState(s *SetupState) {
	path := setupStatePath()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return
	}
	data, err := json.Marshal(s)
	if err != nil {
		return
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return
	}
	_ = os.Rename(tmp, path)
}

// DeleteSetupState removes the setup state file once setup has completed
// or when the user explicitly re-opens setup (fresh start).
// Errors are silently ignored.
func DeleteSetupState() {
	_ = os.Remove(setupStatePath())
}
