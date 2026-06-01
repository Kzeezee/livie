package config

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/BurntSushi/toml"
)

// Load reads the config file at path.
//
// When the file does not exist, DefaultConfig() is returned with IsFirstRun=true
// and ConfigPath set — this is not an error, it is the expected first-run case.
// Any other I/O or parse error is returned as-is.
func Load(path string) (*Config, error) {
	if _, err := os.Stat(path); os.IsNotExist(err) {
		cfg := DefaultConfig()
		cfg.IsFirstRun = true
		cfg.ConfigPath = path
		return cfg, nil
	}

	cfg := DefaultConfig()
	if _, err := toml.DecodeFile(path, cfg); err != nil {
		return nil, fmt.Errorf("config: parse %s: %w", path, err)
	}

	// Runtime-only fields are always set from the argument, not from the file.
	cfg.IsFirstRun = false
	cfg.ConfigPath = path
	return cfg, nil
}

// Save writes cfg to path atomically (temp file → rename).
// Creates any missing parent directories.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return fmt.Errorf("config: mkdir: %w", err)
	}

	// Write to a sibling temp file first.
	tmp := path + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return fmt.Errorf("config: create temp: %w", err)
	}

	if err := toml.NewEncoder(f).Encode(cfg); err != nil {
		f.Close()
		os.Remove(tmp)
		return fmt.Errorf("config: encode: %w", err)
	}
	if err := f.Close(); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: close temp: %w", err)
	}

	// Atomic rename.
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("config: rename: %w", err)
	}
	return nil
}
