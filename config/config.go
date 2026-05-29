package config

import (
	"os"
	"path/filepath"
)

// Config holds all Livie configuration. Populated from TOML in future phases.
type Config struct {
	// Endpoint
	EndpointURL  string
	EndpointName string
	ModelName    string
	APIKey       string

	// Paths
	VaultPath  string
	ConfigPath string
	SkillsPath string

	// Behaviour
	AutoExecuteBash  bool
	ConfirmToolCalls bool

	// HUD
	HUDPosition string // "top" | "bottom"

	// Internal
	IsFirstRun bool
}

// DefaultConfig returns a config with sensible defaults.
func DefaultConfig() *Config {
	home, _ := os.UserHomeDir()
	configPath := filepath.Join(home, ".config", "livie", "config.toml")
	vaultPath := filepath.Join(home, ".local", "share", "livie", "vault")

	return &Config{
		EndpointURL:      "http://localhost:8080",
		EndpointName:     "local",
		ModelName:        "(not configured)",
		VaultPath:        vaultPath,
		ConfigPath:       configPath,
		SkillsPath:       filepath.Join(home, ".local", "share", "livie", "skills"),
		AutoExecuteBash:  false,
		ConfirmToolCalls: true,
		HUDPosition:      "top",
		IsFirstRun:       !configExists(configPath),
	}
}

func configExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
