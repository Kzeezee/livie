// Package config defines Livie's configuration schema and helpers.
// Config is loaded from ~/.config/livie/config.toml on startup.
// When the file is absent, DefaultConfig() is returned with IsFirstRun=true.
package config

import (
	"os"
	"path/filepath"
)

// Config holds all Livie configuration. Populated from TOML via config/toml.go.
type Config struct {
	Runner    RunnerConfig     `toml:"runner"`
	Endpoint  EndpointSelector `toml:"endpoint"`
	Endpoints []EndpointConfig `toml:"endpoints"`
	Behaviour BehaviourConfig  `toml:"behaviour"`
	HUD       HUDConfig        `toml:"hud"`
	Paths     PathsConfig      `toml:"paths"`

	// Runtime-only — never written to TOML.
	IsFirstRun bool   `toml:"-"`
	ConfigPath string `toml:"-"`
}

// RunnerConfig holds settings for the local llama-server runner.
type RunnerConfig struct {
	BinaryPath  string `toml:"binary_path"`
	ModelPath   string `toml:"model_path"`
	GPUBackend  string `toml:"gpu_backend"`  // "cpu" | "cuda" | "metal" | "vulkan"
	Port        int    `toml:"port"`
	GPULayers   int    `toml:"gpu_layers"`
	ContextSize int    `toml:"context_size"`
	Threads     int    `toml:"threads"`
	FlashAttn   bool   `toml:"flash_attn"`
	Verbose     bool   `toml:"verbose"`
}

// EndpointSelector names the currently active endpoint.
type EndpointSelector struct {
	Active string `toml:"active"`
}

// EndpointConfig describes a single API endpoint (local or remote).
type EndpointConfig struct {
	Name    string `toml:"name"`
	BaseURL string `toml:"base_url"`
	APIKey  string `toml:"api_key"`
	Model   string `toml:"model"`
}

// BehaviourConfig controls tool-use and execution safety.
type BehaviourConfig struct {
	AutoExecuteBash  bool `toml:"auto_execute_bash"`
	ConfirmToolCalls bool `toml:"confirm_tool_calls"`
}

// HUDConfig controls the heads-up display.
type HUDConfig struct {
	Position string `toml:"position"` // "top" | "bottom"
}

// PathsConfig holds filesystem paths for Livie's data.
type PathsConfig struct {
	Vault  string `toml:"vault"`
	Skills string `toml:"skills"`
	Index  string `toml:"index"`
}

// DefaultPath returns the canonical config file path.
func DefaultPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".config", "livie", "config.toml")
}

// DefaultConfig returns a Config with sensible defaults.
// IsFirstRun is always false here; Load() is the sole authority on that flag.
// It scans ./model/ for any .gguf file and pre-populates Runner.ModelPath.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()

	return &Config{
		Runner: RunnerConfig{
			BinaryPath:  "",
			ModelPath:   scanForModel(),
			GPUBackend:  "cpu",
			Port:        8080,
			GPULayers:   -1,
			ContextSize: 16384,
			Threads:     0,
			FlashAttn:   true,
			Verbose:     false,
		},
		Endpoint: EndpointSelector{Active: "local"},
		Endpoints: []EndpointConfig{
			{
				Name:    "local",
				BaseURL: "http://localhost:8080/v1",
				APIKey:  "",
				Model:   "",
			},
		},
		Behaviour: BehaviourConfig{
			AutoExecuteBash:  false,
			ConfirmToolCalls: true,
		},
		HUD: HUDConfig{Position: "bottom"},
		Paths: PathsConfig{
			Vault:  filepath.Join(home, ".local", "share", "livie", "vault"),
			Skills: filepath.Join(home, ".local", "share", "livie", "skills"),
			Index:  filepath.Join(home, ".local", "share", "livie", "index"),
		},
		// IsFirstRun intentionally left false — Load() sets it.
		// ConfigPath intentionally left empty — Load() sets it.
	}
}

// ModelName returns the display name of the active model (basename without path).
// Returns "(no model)" when no model path is configured.
func (c *Config) ModelName() string {
	if c.Runner.ModelPath == "" {
		return "(no model)"
	}
	return filepath.Base(c.Runner.ModelPath)
}

// ActiveEndpoint returns the EndpointConfig for the currently active endpoint,
// or a zero-value config if none is found.
func (c *Config) ActiveEndpoint() EndpointConfig {
	for _, ep := range c.Endpoints {
		if ep.Name == c.Endpoint.Active {
			return ep
		}
	}
	return EndpointConfig{}
}

// scanForModel looks in ./model/ (relative to working directory) for the first
// .gguf file and returns its absolute path. Returns "" if none found.
func scanForModel() string {
	entries, err := os.ReadDir("model")
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".gguf" {
			abs, err := filepath.Abs(filepath.Join("model", e.Name()))
			if err != nil {
				return filepath.Join("model", e.Name())
			}
			return abs
		}
	}
	return ""
}
