package session

import (
	"encoding/json"
	"os"
	"path/filepath"
)

const (
	inputHistoryFile = "input_history.json"
	// MaxInputHistory is the maximum number of entries kept in the persisted
	// history file. Older entries are trimmed when the cap is exceeded.
	MaxInputHistory = 1000
)

// inputHistoryPath returns the path to the input history file.
// Creates the parent sessions directory if it does not yet exist.
func inputHistoryPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, inputHistoryFile), nil
}

// LoadInputHistory reads the persisted input history from disk.
// Returns an empty slice (and no error) if the file does not yet exist.
func LoadInputHistory() ([]string, error) {
	path, err := inputHistoryPath()
	if err != nil {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return []string{}, nil
	}
	if err != nil {
		return nil, err
	}

	var entries []string
	if err := json.Unmarshal(data, &entries); err != nil {
		// Corrupted file — start fresh rather than crashing.
		return []string{}, nil
	}
	return entries, nil
}

// SaveInputHistory writes entries to disk atomically (temp → rename).
// Trims to the most recent MaxInputHistory entries before writing.
func SaveInputHistory(entries []string) error {
	if len(entries) > MaxInputHistory {
		entries = entries[len(entries)-MaxInputHistory:]
	}

	path, err := inputHistoryPath()
	if err != nil {
		return err
	}

	data, err := json.Marshal(entries)
	if err != nil {
		return err
	}

	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
