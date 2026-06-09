package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

// SkillLoader discovers and holds all active skills.
type SkillLoader struct {
	skills []Skill
	dir    string          // external skills directory (cfg.Paths.Skills)
	cwd    string          // Livie's launch cwd, passed to script subprocesses
	names  map[string]bool // deduplication: name → already registered
}

// NewLoader creates a SkillLoader that will scan skillsDir for external skills.
// cwd is the working directory fixed at Livie's launch time.
func NewLoader(skillsDir, cwd string) *SkillLoader {
	return &SkillLoader{
		dir:   skillsDir,
		cwd:   cwd,
		names: make(map[string]bool),
	}
}

// RegisterBuiltin adds a compiled-in skill. Duplicate names are silently skipped.
func (l *SkillLoader) RegisterBuiltin(s Skill) {
	if l.names[s.Name()] {
		return
	}
	l.names[s.Name()] = true
	l.skills = append(l.skills, s)
}

// addSkill is the internal deduplicating append used during discovery.
func (l *SkillLoader) addSkill(s Skill) {
	if l.names[s.Name()] {
		return
	}
	l.names[s.Name()] = true
	l.skills = append(l.skills, s)
}

// DiscoverExternal walks l.dir and loads any valid skill subdirectories.
// Missing or unreadable directories are silently ignored (non-fatal).
// Already-registered skill names are skipped so re-discovery is safe to call
// multiple times (e.g. after /skills install).
func (l *SkillLoader) DiscoverExternal() error {
	if l.dir == "" {
		return nil
	}
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // skills dir not created yet — fine
		}
		return fmt.Errorf("read skills dir %s: %w", l.dir, err)
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		skillDir := filepath.Join(l.dir, e.Name())
		s, err := loadScriptSkill(skillDir, l.cwd)
		if err != nil {
			// Skip invalid skills — don't abort the whole discovery
			continue
		}
		l.addSkill(s)
	}
	return nil
}

// LoadAll calls Register on every loaded skill, passing r as the tool registrar.
// Calling LoadAll multiple times is safe: ToolDispatcher.Register overwrites on duplicate names.
func (l *SkillLoader) LoadAll(r Registrar) {
	for _, s := range l.skills {
		s.Register(r)
	}
}

// SystemPromptContent joins all loaded skills' SkillMD bodies with a separator.
// Returns "" when no skills are loaded or all return empty bodies.
func (l *SkillLoader) SystemPromptContent() string {
	var parts []string
	for _, s := range l.skills {
		if md := s.SkillMD(); md != "" {
			parts = append(parts, md)
		}
	}
	if len(parts) == 0 {
		return ""
	}
	return strings.Join(parts, "\n\n---\n\n")
}

// Count returns the number of loaded skills.
func (l *SkillLoader) Count() int { return len(l.skills) }

// Names returns the names of all loaded skills, in registration order.
func (l *SkillLoader) Names() []string {
	names := make([]string, len(l.skills))
	for i, s := range l.skills {
		names[i] = s.Name()
	}
	return names
}

// Skills returns the loaded Skill slice (read-only by convention).
func (l *SkillLoader) Skills() []Skill { return l.skills }

// ── External (script-based) skill ────────────────────────────────────────────

// ScriptSkill is a skill loaded from a directory on disk. Its tools are
// implemented as executable shell scripts invoked as subprocesses.
type ScriptSkill struct {
	name        string
	description string
	skillMD     string       // Markdown body with frontmatter stripped
	tools       []scriptTool
	cwd         string // Livie's launch cwd — subprocess working directory
}

type scriptTool struct {
	name        string
	description string
	handler     string          // absolute path to the handler script
	parameters  json.RawMessage // JSON Schema for the tool's input
	timeoutSecs float64
}

func (s *ScriptSkill) Name() string        { return s.name }
func (s *ScriptSkill) Description() string { return s.description }
func (s *ScriptSkill) SkillMD() string     { return s.skillMD }

// Register creates a *Tool per scriptTool and registers each on r.
func (s *ScriptSkill) Register(r Registrar) {
	for _, t := range s.tools {
		t := t   // capture loop variable
		cwd := s.cwd
		r.Register(&Tool{
			Name:        t.name,
			Description: t.description,
			Parameters:  t.parameters,
			Handler:     scriptHandler(t, cwd),
		})
	}
}

// scriptHandler returns the Handler func for a single scriptTool.
// Extracted so it can be tested in isolation.
func scriptHandler(t scriptTool, cwd string) func(string) (string, error) {
	return func(args string) (string, error) {
		timeout := t.timeoutSecs
		if timeout <= 0 {
			timeout = 30
		}

		ctx, cancel := context.WithTimeout(
			context.Background(),
			time.Duration(timeout*float64(time.Second)),
		)
		defer cancel()

		cmd := exec.CommandContext(ctx, t.handler, args)
		cmd.Dir = cwd
		out, err := cmd.CombinedOutput()

		output := string(out)
		const maxOutput = 8000
		if len(output) > maxOutput {
			output = output[:maxOutput] + "\n[... truncated]"
		}

		if ctx.Err() == context.DeadlineExceeded {
			return fmt.Sprintf("error: tool timed out after %.0fs", timeout), nil
		}

		if err != nil {
			if exitErr, ok := err.(*exec.ExitError); ok {
				return output + fmt.Sprintf("\n[exit %d]", exitErr.ExitCode()), nil
			}
			// OS-level failure (e.g. exec not found)
			return "", err
		}
		return output, nil
	}
}

// ── SKILL.md parsing ─────────────────────────────────────────────────────────

// skillFrontmatter is the YAML structure parsed from the --- block.
type skillFrontmatter struct {
	Name        string    `yaml:"name"`
	Version     string    `yaml:"version"`
	Description string    `yaml:"description"`
	TimeoutSecs float64   `yaml:"timeout_seconds"`
	Tools       []toolDef `yaml:"tools"`
}

// toolDef is one entry in the tools array of a skill's frontmatter.
// Parameters may be:
//   - a JSON string literal: parameters: '{"type":"object",...}'
//   - an inline YAML mapping:  parameters: {type: object, ...}
type toolDef struct {
	Name        string      `yaml:"name"`
	Description string      `yaml:"description"`
	Handler     string      `yaml:"handler"`
	Parameters  interface{} `yaml:"parameters"`
	TimeoutSecs float64     `yaml:"timeout_seconds"`
}

// loadScriptSkill parses dir/SKILL.md and returns a *ScriptSkill.
// Returns an error if the directory has no SKILL.md, the frontmatter is
// invalid, or required fields (name, description) are absent.
func loadScriptSkill(dir, cwd string) (*ScriptSkill, error) {
	mdPath := filepath.Join(dir, "SKILL.md")
	data, err := os.ReadFile(mdPath)
	if err != nil {
		return nil, fmt.Errorf("read SKILL.md: %w", err)
	}

	fm, body, err := parseFrontmatter(string(data))
	if err != nil {
		return nil, fmt.Errorf("parse frontmatter in %s: %w", mdPath, err)
	}
	if fm.Name == "" {
		return nil, fmt.Errorf("%s: frontmatter missing required field 'name'", mdPath)
	}
	if fm.Description == "" {
		return nil, fmt.Errorf("%s: frontmatter missing required field 'description'", mdPath)
	}

	skill := &ScriptSkill{
		name:        fm.Name,
		description: fm.Description,
		skillMD:     body,
		cwd:         cwd,
	}

	for _, td := range fm.Tools {
		if td.Name == "" || td.Handler == "" {
			continue
		}
		handlerPath := filepath.Join(dir, td.Handler)

		params := toolParams(td.Parameters)

		ts := fm.TimeoutSecs
		if td.TimeoutSecs > 0 {
			ts = td.TimeoutSecs
		}

		skill.tools = append(skill.tools, scriptTool{
			name:        td.Name,
			description: td.Description,
			handler:     handlerPath,
			parameters:  params,
			timeoutSecs: ts,
		})
	}

	return skill, nil
}

// toolParams converts the raw TOML-decoded parameters value into a JSON Schema
// byte slice. Accepts:
//   - string: treated as a raw JSON string
//   - map[string]interface{}: marshalled to JSON
//   - nil: returns a minimal empty-object schema
func toolParams(raw interface{}) json.RawMessage {
	switch v := raw.(type) {
	case string:
		if v != "" {
			return json.RawMessage(v)
		}
	case map[string]interface{}:
		b, err := json.Marshal(v)
		if err == nil {
			return b
		}
	}
	return json.RawMessage(`{"type":"object","properties":{}}`)
}

// StripFrontmatter returns only the Markdown body of a SKILL.md, discarding
// the TOML frontmatter between the --- delimiters. If the file has no
// frontmatter the full content is returned unchanged. Used by compiled-in
// skills whose //go:embed includes the frontmatter.
func StripFrontmatter(content string) string {
	_, body, err := parseFrontmatter(content)
	if err != nil {
		return content
	}
	return body
}

// parseFrontmatter splits a SKILL.md file into its TOML frontmatter (between
// the --- delimiters) and the plain Markdown body that follows.
//
// If the file does not start with "---\n" it is treated as body-only with an
// empty frontmatter (no error).
func parseFrontmatter(content string) (skillFrontmatter, string, error) {
	var fm skillFrontmatter

	const delim = "---"

	if !strings.HasPrefix(content, delim+"\n") {
		// No frontmatter section — the entire content is the body
		return fm, content, nil
	}

	// Advance past opening "---\n"
	rest := content[len(delim)+1:]

	// Find the closing "---" on its own line
	closingIdx := -1
	for _, sep := range []string{"\n" + delim + "\n", "\n" + delim} {
		if idx := strings.Index(rest, sep); idx >= 0 {
			closingIdx = idx
			break
		}
	}
	if closingIdx < 0 {
		return fm, content, fmt.Errorf("unclosed SKILL.md frontmatter (no closing ---)")
	}

	fmContent := rest[:closingIdx]
	bodyRaw := rest[closingIdx:]
	// Strip the closing delimiter and any leading newlines from the body
	bodyRaw = strings.TrimPrefix(bodyRaw, "\n"+delim+"\n")
	bodyRaw = strings.TrimPrefix(bodyRaw, "\n"+delim)
	body := strings.TrimLeft(bodyRaw, "\n")

	if err := yaml.Unmarshal([]byte(fmContent), &fm); err != nil {
		return fm, content, fmt.Errorf("invalid YAML frontmatter: %w", err)
	}

	return fm, body, nil
}
