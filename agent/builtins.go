package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// RegisterBuiltins registers the 5 built-in tools on the dispatcher.
// cwd is the working directory fixed at agent launch time; all relative paths
// are resolved against it.
func RegisterBuiltins(d *ToolDispatcher, cwd string) {
	d.Register(bashTool(cwd))
	d.Register(readFileTool(cwd))
	d.Register(writeFileTool(cwd))
	d.Register(findFilesTool(cwd))
	d.Register(editFileTool(cwd))
}

// ── bash ─────────────────────────────────────────────────────────────────────

func bashTool(cwd string) *Tool {
	return &Tool{
		Name:        "bash",
		Description: "Run a shell command. Non-zero exit returns output+[exit N], not an error.",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"cmd":     {"type": "string"},
				"timeout": {"type": "number", "description": "Seconds (default 30)"}
			},
			"required": ["cmd"]
		}`),
		Handler: func(args string) (string, error) {
			var params struct {
				Cmd     string  `json:"cmd"`
				Timeout float64 `json:"timeout"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
			if params.Timeout <= 0 {
				params.Timeout = 30
			}

			timeout := time.Duration(params.Timeout * float64(time.Second))
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", params.Cmd)
			cmd.Dir = cwd

			var out bytes.Buffer
			cmd.Stdout = &out
			cmd.Stderr = &out

			err := cmd.Run()

			output := out.String()

			// Truncate to 8,000 chars to protect context window.
			const maxOutput = 8000
			if len(output) > maxOutput {
				output = output[:maxOutput] + "\n[... truncated]"
			}

			if ctx.Err() == context.DeadlineExceeded {
				return fmt.Sprintf("error: command timed out after %.0fs", params.Timeout), nil
			}

			// Non-zero exit is not an error — the AI sees the output + exit code.
			if err != nil {
				if exitErr, ok := err.(*exec.ExitError); ok {
					return output + fmt.Sprintf("\n[exit %d]", exitErr.ExitCode()), nil
				}
				// exec failure (e.g. /bin/sh not found) — treat as error
				return "", err
			}

			return output, nil
		},
	}
}

// ── read_file ─────────────────────────────────────────────────────────────────

func readFileTool(cwd string) *Tool {
	return &Tool{
		Name:        "read_file",
		Description: "Read a file. Use offset/limit to window large files.",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"path":   {"type": "string"},
				"offset": {"type": "number", "description": "Start line, 1-indexed (default 1)"},
				"limit":  {"type": "number", "description": "Max lines (default 2000)"}
			},
			"required": ["path"]
		}`),
		Handler: func(args string) (string, error) {
			var params struct {
				Path   string `json:"path"`
				Offset int    `json:"offset"`
				Limit  int    `json:"limit"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
			if params.Offset <= 0 {
				params.Offset = 1
			}
			if params.Limit <= 0 {
				params.Limit = 2000
			}

			path := resolvePath(cwd, params.Path)
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Sprintf("error: %s", err), nil
			}

			lines := strings.Split(string(data), "\n")
			total := len(lines)

			// Apply offset/limit window (offset is 1-indexed).
			start := params.Offset - 1
			if start < 0 {
				start = 0
			}
			if start >= total {
				return fmt.Sprintf("[file has %d lines, offset %d is out of range]", total, params.Offset), nil
			}

			end := start + params.Limit
			if end > total {
				end = total
			}

			windowed := lines[start:end]
			content := strings.Join(windowed, "\n")

			// Prefix with a header when we're not returning the whole file.
			windowing := start > 0 || end < total
			if windowing {
				header := fmt.Sprintf("[lines %d-%d of %d]\n", start+1, end, total)
				content = header + content
			}

			return content, nil
		},
	}
}

// ── write_file ────────────────────────────────────────────────────────────────

func writeFileTool(cwd string) *Tool {
	return &Tool{
		Name:        "write_file",
		Description: "Write a file. Creates parent dirs. Overwrites existing.",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"path":    {"type": "string"},
				"content": {"type": "string"}
			},
			"required": ["path", "content"]
		}`),
		Handler: func(args string) (string, error) {
			var params struct {
				Path    string `json:"path"`
				Content string `json:"content"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}

			path := resolvePath(cwd, params.Path)

			if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
				return fmt.Sprintf("error creating directories: %s", err), nil
			}

			if err := os.WriteFile(path, []byte(params.Content), 0o644); err != nil {
				return fmt.Sprintf("error writing file: %s", err), nil
			}

			return fmt.Sprintf("wrote %d bytes to %s", len(params.Content), params.Path), nil
		},
	}
}

// ── find_files ────────────────────────────────────────────────────────────────

func findFilesTool(cwd string) *Tool {
	return &Tool{
		Name:        "find_files",
		Description: "Find files by filename glob. Use bash+find for recursive patterns.",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"pattern": {"type": "string", "description": "Filename glob, e.g. *.go"},
				"dir":     {"type": "string", "description": "Search root (default: cwd)"}
			},
			"required": ["pattern"]
		}`),
		Handler: func(args string) (string, error) {
			var params struct {
				Pattern string `json:"pattern"`
				Dir     string `json:"dir"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}

			searchDir := cwd
			if params.Dir != "" {
				searchDir = resolvePath(cwd, params.Dir)
			}

			const maxResults = 200
			var results []string

			err := filepath.Walk(searchDir, func(path string, info os.FileInfo, err error) error {
				if err != nil {
					return nil // skip unreadable entries
				}
				if info.IsDir() {
					return nil
				}

				matched, matchErr := filepath.Match(params.Pattern, filepath.Base(path))
				if matchErr != nil {
					return matchErr
				}
				if matched {
					// Return paths relative to cwd.
					rel, relErr := filepath.Rel(cwd, path)
					if relErr != nil {
						rel = path
					}
					results = append(results, rel)
					if len(results) >= maxResults {
						return filepath.SkipAll
					}
				}
				return nil
			})
			if err != nil {
				return fmt.Sprintf("error: %s", err), nil
			}

			if len(results) == 0 {
				return fmt.Sprintf("no files matching %q found in %s", params.Pattern, searchDir), nil
			}

			output := strings.Join(results, "\n")
			// Note: we walked up to maxResults+1 entries but capped at maxResults,
			// so we don't know the exact total of remaining entries. A second walk
			// would be expensive; just note the cap was hit.
			if len(results) == maxResults {
				output += fmt.Sprintf("\n[results capped at %d — use a narrower pattern or dir]", maxResults)
			}

			return output, nil
		},
	}
}

// ── edit_file ─────────────────────────────────────────────────────────────────

func editFileTool(cwd string) *Tool {
	return &Tool{
		Name:        "edit_file",
		Description: "Apply exact text substitutions to a file. Each old_text must appear exactly once (atomic — all edits validated before any write).",
		Parameters: []byte(`{
			"type": "object",
			"properties": {
				"path": {"type": "string"},
				"edits": {
					"type": "array",
					"items": {
						"type": "object",
						"properties": {
							"old_text": {"type": "string", "description": "Must be unique in file"},
							"new_text": {"type": "string"}
						},
						"required": ["old_text", "new_text"]
					}
				}
			},
			"required": ["path", "edits"]
		}`),
		Handler: func(args string) (string, error) {
			var params struct {
				Path  string `json:"path"`
				Edits []struct {
					OldText string `json:"old_text"`
					NewText string `json:"new_text"`
				} `json:"edits"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}

			path := resolvePath(cwd, params.Path)
			data, err := os.ReadFile(path)
			if err != nil {
				return fmt.Sprintf("error reading file: %s", err), nil
			}

			content := string(data)

			// Validate all edits before applying any (all-or-nothing).
			for i, e := range params.Edits {
				count := strings.Count(content, e.OldText)
				switch count {
				case 0:
					return fmt.Sprintf("edit %d: old_text not found", i+1), nil
				case 1:
					// OK — exactly one occurrence.
				default:
					return fmt.Sprintf("edit %d: old_text not unique (found %d occurrences)", i+1, count), nil
				}
			}

			// Apply edits sequentially against the running buffer.
			for _, e := range params.Edits {
				content = strings.Replace(content, e.OldText, e.NewText, 1)
			}

			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				return fmt.Sprintf("error writing file: %s", err), nil
			}

			return fmt.Sprintf("applied %d edit(s) to %s", len(params.Edits), params.Path), nil
		},
	}
}

// ── helpers ───────────────────────────────────────────────────────────────────

// resolvePath returns path as-is if absolute, otherwise joins it to cwd.
func resolvePath(cwd, path string) string {
	if filepath.IsAbs(path) {
		return path
	}
	return filepath.Join(cwd, path)
}
