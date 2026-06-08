package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// helper: fresh dispatcher with builtins registered against a temp dir
func newTestDispatcher(t *testing.T) (*ToolDispatcher, string) {
	t.Helper()
	dir := t.TempDir()
	d := NewToolDispatcher()
	RegisterBuiltins(d, dir)
	return d, dir
}

// ── 4: bash tool ─────────────────────────────────────────────────────────────

func TestBash_SimpleEcho(t *testing.T) {
	d, _ := newTestDispatcher(t)
	out, err := d.Dispatch("bash", `{"cmd":"echo hello"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != "hello" {
		t.Fatalf("expected 'hello', got %q", out)
	}
}

func TestBash_NonZeroExitIsNotAnError(t *testing.T) {
	d, _ := newTestDispatcher(t)
	// 'false' exits with code 1 — the tool should return output+[exit 1], not an error
	out, err := d.Dispatch("bash", `{"cmd":"echo oops && exit 1"}`)
	if err != nil {
		t.Fatalf("non-zero exit should not return an error, got: %v", err)
	}
	if !strings.Contains(out, "[exit 1]") {
		t.Fatalf("expected [exit 1] suffix, got %q", out)
	}
}

func TestBash_Timeout(t *testing.T) {
	d, _ := newTestDispatcher(t)
	// 0.1 s timeout against a 10 s sleep
	out, err := d.Dispatch("bash", `{"cmd":"sleep 10","timeout":0.1}`)
	if err != nil {
		t.Fatalf("timeout should not return an error, got: %v", err)
	}
	if !strings.Contains(out, "timed out") {
		t.Fatalf("expected 'timed out' message, got %q", out)
	}
}

func TestBash_CWDIsRespected(t *testing.T) {
	d, dir := newTestDispatcher(t)
	// pwd should print the temp dir, not wherever the test binary runs
	out, err := d.Dispatch("bash", `{"cmd":"pwd"}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if strings.TrimSpace(out) != dir {
		t.Fatalf("expected cwd %q, got %q", dir, strings.TrimSpace(out))
	}
}

// ── 5: edit_file uniqueness guard ────────────────────────────────────────────

func TestEditFile_Success(t *testing.T) {
	d, dir := newTestDispatcher(t)

	// Write a test file
	path := filepath.Join(dir, "hello.txt")
	_ = os.WriteFile(path, []byte("hello world\n"), 0o644)

	out, err := d.Dispatch("edit_file", `{
		"path": "hello.txt",
		"edits": [{"old_text": "hello", "new_text": "goodbye"}]
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "applied 1 edit") {
		t.Fatalf("expected success message, got %q", out)
	}

	data, _ := os.ReadFile(path)
	if string(data) != "goodbye world\n" {
		t.Fatalf("file content wrong: %q", string(data))
	}
}

func TestEditFile_NotFound(t *testing.T) {
	d, _ := newTestDispatcher(t)

	out, err := d.Dispatch("edit_file", `{
		"path": "hello.txt",
		"edits": [{"old_text": "hello", "new_text": "goodbye"}]
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	// Should report old_text not found, not crash
	if !strings.Contains(out, "not found") && !strings.Contains(out, "error") {
		t.Fatalf("expected not-found message, got %q", out)
	}
}

func TestEditFile_NotUnique(t *testing.T) {
	d, dir := newTestDispatcher(t)

	// "foo" appears twice — edit should be rejected
	path := filepath.Join(dir, "dupe.txt")
	_ = os.WriteFile(path, []byte("foo bar foo\n"), 0o644)

	out, err := d.Dispatch("edit_file", `{
		"path": "dupe.txt",
		"edits": [{"old_text": "foo", "new_text": "baz"}]
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "not unique") {
		t.Fatalf("expected 'not unique' error, got %q", out)
	}

	// File must be unchanged
	data, _ := os.ReadFile(path)
	if string(data) != "foo bar foo\n" {
		t.Fatalf("file was modified despite uniqueness error: %q", string(data))
	}
}

func TestEditFile_MultipleEditsAtomic(t *testing.T) {
	d, dir := newTestDispatcher(t)

	path := filepath.Join(dir, "atomic.txt")
	_ = os.WriteFile(path, []byte("alpha beta gamma\n"), 0o644)

	// Second edit references a string that doesn't exist — whole operation fails
	out, err := d.Dispatch("edit_file", `{
		"path": "atomic.txt",
		"edits": [
			{"old_text": "alpha", "new_text": "ALPHA"},
			{"old_text": "NOPE",  "new_text": "anything"}
		]
	}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(out, "not found") {
		t.Fatalf("expected 'not found' for edit 2, got %q", out)
	}

	// File must be unchanged — first edit should NOT have been applied
	data, _ := os.ReadFile(path)
	if string(data) != "alpha beta gamma\n" {
		t.Fatalf("file was partially modified: %q", string(data))
	}
}
