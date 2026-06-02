package session

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"

	tea "charm.land/bubbletea/v2"
)

// Dir returns the sessions directory path: ~/.local/share/livie/sessions/
// Creates the directory if it does not exist.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dir := filepath.Join(home, ".local", "share", "livie", "sessions")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	return dir, nil
}

// Save writes s to disk atomically (temp file → rename).
// Sets s.Preview from the first RoleUser message (≤72 chars) before writing.
// Creates the sessions directory if needed.
func Save(s *Session) error {
	// Build preview from the first user message.
	for _, m := range s.Messages {
		if m.Role == RoleUser && m.Content != "" {
			preview := m.Content
			if len([]rune(preview)) > 72 {
				runes := []rune(preview)
				preview = string(runes[:69]) + "…"
			}
			s.Preview = preview
			break
		}
	}

	dir, err := Dir()
	if err != nil {
		return err
	}

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}

	// Write to temp file then rename for atomicity.
	tmp := filepath.Join(dir, s.ID+".tmp")
	dst := filepath.Join(dir, s.ID+".json")

	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, dst)
}

// Load reads and deserialises the session with the given ID.
func Load(id string) (*Session, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}
	path := filepath.Join(dir, id+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var s Session
	if err := json.Unmarshal(data, &s); err != nil {
		return nil, err
	}
	return &s, nil
}

// ListSummaries reads all *.json files in Dir(), builds a Summary for each,
// and returns the list sorted newest-first by UpdatedAt.
// Files that fail to parse are silently skipped.
func ListSummaries() ([]Summary, error) {
	dir, err := Dir()
	if err != nil {
		return nil, err
	}

	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var summaries []Summary
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		path := filepath.Join(dir, e.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var s Session
		if err := json.Unmarshal(data, &s); err != nil {
			continue
		}
		summaries = append(summaries, Summary{
			ID:           s.ID,
			CreatedAt:    s.CreatedAt,
			UpdatedAt:    s.UpdatedAt,
			MessageCount: len(s.Messages),
			Preview:      s.Preview,
			ModelName:    s.ModelName,
			EndpointName: s.EndpointName,
		})
	}

	// Sort newest-first by UpdatedAt.
	sort.Slice(summaries, func(i, j int) bool {
		return summaries[i].UpdatedAt.After(summaries[j].UpdatedAt)
	})

	return summaries, nil
}

// ── tea.Cmd wrappers ─────────────────────────────────────────────────────────

// SummariesLoadedMsg is returned by ListSummariesCmd.
type SummariesLoadedMsg struct {
	Summaries []Summary
	Err       error
}

// SessionLoadedMsg is returned by LoadCmd.
type SessionLoadedMsg struct {
	Session *Session
	Err     error
}

// ListSummariesCmd wraps ListSummaries as a tea.Cmd.
func ListSummariesCmd() tea.Cmd {
	return func() tea.Msg {
		summaries, err := ListSummaries()
		return SummariesLoadedMsg{Summaries: summaries, Err: err}
	}
}

// LoadCmd wraps Load as a tea.Cmd.
func LoadCmd(id string) tea.Cmd {
	return func() tea.Msg {
		s, err := Load(id)
		return SessionLoadedMsg{Session: s, Err: err}
	}
}
