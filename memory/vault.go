// Package memory handles vault I/O for Livie's persistent data directory.
// The vault holds user-editable files (system_prompt.md, personality.md, etc.)
// that are seeded on first run and owned by the user thereafter.
package memory

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"
)

//go:embed seeds/system_prompt.md
var defaultSystemPrompt string

//go:embed seeds/personality.md
var defaultPersonality string

// Init creates the vault directory and seeds any missing files.
// Safe to call on every startup — existing files are never overwritten.
// Non-fatal: returns an error but callers should warn and continue.
func Init(vaultPath string) error {
	if err := os.MkdirAll(vaultPath, 0o755); err != nil {
		return fmt.Errorf("create vault dir: %w", err)
	}
	if err := seedFile(vaultPath, "system_prompt.md", defaultSystemPrompt); err != nil {
		return err
	}
	if err := seedFile(vaultPath, "personality.md", defaultPersonality); err != nil {
		return err
	}
	// Memory files start empty — seeded as zero-byte files so they exist on disk
	// but contribute no content to the system prompt until the AI writes to them.
	if err := seedFile(vaultPath, "memory.md", ""); err != nil {
		return err
	}
	return seedFile(vaultPath, "user-profile.md", "")
}

// WriteFile writes content to vaultPath/filename, creating or overwriting the file.
// Unlike seedFile, this always overwrites. Callers are responsible for
// scoping filenames appropriately.
func WriteFile(vaultPath, filename, content string) error {
	return os.WriteFile(filepath.Join(vaultPath, filename), []byte(content), 0o644)
}

// LoadFile reads filename from vaultPath.
// Returns "" silently if the file does not exist or cannot be read.
func LoadFile(vaultPath, filename string) string {
	data, err := os.ReadFile(filepath.Join(vaultPath, filename))
	if err != nil {
		return ""
	}
	return string(data)
}

// seedFile writes content to vaultPath/filename only if the file does not
// already exist. Existing files are never modified.
func seedFile(vaultPath, filename, content string) error {
	path := filepath.Join(vaultPath, filename)
	if _, err := os.Stat(path); err == nil {
		return nil // already exists — leave it alone
	}
	return os.WriteFile(path, []byte(content), 0o644)
}
