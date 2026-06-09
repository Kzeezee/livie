package skills

import (
	"os"
	"path/filepath"
	"testing"
)

// ── parseFrontmatter ─────────────────────────────────────────────────────────

func TestParseFrontmatter_WithFrontmatter(t *testing.T) {
	content := "---\nname: test-skill\ndescription: A test\n---\n\n# Body\nSome content."
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "test-skill" {
		t.Errorf("expected name %q, got %q", "test-skill", fm.Name)
	}
	if fm.Description != "A test" {
		t.Errorf("expected description %q, got %q", "A test", fm.Description)
	}
	if body != "# Body\nSome content." {
		t.Errorf("unexpected body: %q", body)
	}
}

func TestParseFrontmatter_NoFrontmatter(t *testing.T) {
	content := "# Just a body\nNo frontmatter here."
	fm, body, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "" {
		t.Errorf("expected empty name, got %q", fm.Name)
	}
	if body != content {
		t.Errorf("body should equal content when no frontmatter, got %q", body)
	}
}

func TestParseFrontmatter_WithTools(t *testing.T) {
	content := `---
name: hello-skill
description: Example script-based skill
tools:
  - name: greet
    description: Greets a person by name
    handler: handlers/greet.sh
    parameters: '{"type":"object","properties":{"name":{"type":"string"}},"required":["name"]}'
---

# Hello Skill
Use greet to say hello.`

	fm, _, err := parseFrontmatter(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if fm.Name != "hello-skill" {
		t.Errorf("expected name %q, got %q", "hello-skill", fm.Name)
	}
	if len(fm.Tools) != 1 {
		t.Fatalf("expected 1 tool, got %d", len(fm.Tools))
	}
	if fm.Tools[0].Name != "greet" {
		t.Errorf("expected tool name %q, got %q", "greet", fm.Tools[0].Name)
	}
	if fm.Tools[0].Handler != "handlers/greet.sh" {
		t.Errorf("expected handler %q, got %q", "handlers/greet.sh", fm.Tools[0].Handler)
	}
}

// ── StripFrontmatter ─────────────────────────────────────────────────────────

func TestStripFrontmatter(t *testing.T) {
	content := "---\nname: x\n---\n\n# Title\nBody text."
	got := StripFrontmatter(content)
	want := "# Title\nBody text."
	if got != want {
		t.Errorf("StripFrontmatter: got %q, want %q", got, want)
	}
}

// ── SkillLoader discovery ────────────────────────────────────────────────────

func TestDiscoverExternal_EmptyDir(t *testing.T) {
	dir := t.TempDir()
	l := NewLoader(dir, dir)
	if err := l.DiscoverExternal(); err != nil {
		t.Fatalf("unexpected error on empty dir: %v", err)
	}
	if l.Count() != 0 {
		t.Errorf("expected 0 skills, got %d", l.Count())
	}
}

func TestDiscoverExternal_MissingDir(t *testing.T) {
	l := NewLoader("/nonexistent/path/abc123", ".")
	// Should not error — missing dir is non-fatal
	if err := l.DiscoverExternal(); err != nil {
		t.Fatalf("expected nil error for missing dir, got: %v", err)
	}
}

func TestDiscoverExternal_ValidSkill(t *testing.T) {
	skillsDir := t.TempDir()
	cwd := t.TempDir()

	// Create a minimal valid skill directory
	skillDir := filepath.Join(skillsDir, "test-skill")
	if err := os.MkdirAll(skillDir, 0o755); err != nil {
		t.Fatal(err)
	}
	skillMD := `---
name: test-skill
description: A test skill for unit testing
---

# Test Skill
This is a test.`
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o644); err != nil {
		t.Fatal(err)
	}

	l := NewLoader(skillsDir, cwd)
	if err := l.DiscoverExternal(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if l.Count() != 1 {
		t.Fatalf("expected 1 skill, got %d", l.Count())
	}
	names := l.Names()
	if names[0] != "test-skill" {
		t.Errorf("expected skill name %q, got %q", "test-skill", names[0])
	}
}

func TestDiscoverExternal_Deduplication(t *testing.T) {
	skillsDir := t.TempDir()
	cwd := t.TempDir()

	skillDir := filepath.Join(skillsDir, "my-skill")
	_ = os.MkdirAll(skillDir, 0o755)
	_ = os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(
		"---\nname: my-skill\ndescription: A skill\n---\n# Body",
	), 0o644)

	l := NewLoader(skillsDir, cwd)
	_ = l.DiscoverExternal()
	_ = l.DiscoverExternal() // call twice
	if l.Count() != 1 {
		t.Errorf("expected 1 skill after double discovery, got %d", l.Count())
	}
}
