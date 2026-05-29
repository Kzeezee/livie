package tests

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/kez/livie/tui/components"
)

func newInput() components.InputModel {
	return components.NewInputModel(80)
}

// typeInto feeds a string character by character into an InputModel via Update.
func typeInto(m components.InputModel, s string) components.InputModel {
	for _, r := range s {
		m, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	return m
}

// ── Initial state ─────────────────────────────────────────────────────────────

func TestInput_InitialValue(t *testing.T) {
	m := newInput()
	if got := m.Value(); got != "" {
		t.Errorf("expected empty value, got %q", got)
	}
}

func TestInput_InitialHeight(t *testing.T) {
	m := newInput()
	want := components.InputMinLines // borderless: height == textarea line count
	if got := m.Height(); got != want {
		t.Errorf("expected initial height %d, got %d", want, got)
	}
}

func TestInput_InitialMode(t *testing.T) {
	m := newInput()
	if got := m.Mode(); got != components.ModeChat {
		t.Errorf("expected ModeChat, got %v", got)
	}
}

func TestInput_InitialNotDisabled(t *testing.T) {
	m := newInput()
	if m.IsDisabled() {
		t.Error("expected input to be enabled by default")
	}
}

// ── IsCommand ─────────────────────────────────────────────────────────────────

func TestIsCommand_True(t *testing.T) {
	m := newInput()
	m.SetValue("/help")
	if !m.IsCommand() {
		t.Error("expected IsCommand()=true for /help")
	}
}

func TestIsCommand_TrueWithLeadingSpace(t *testing.T) {
	m := newInput()
	m.SetValue("  /help")
	if !m.IsCommand() {
		t.Error("expected IsCommand()=true for '  /help'")
	}
}

func TestIsCommand_FalseForPlainText(t *testing.T) {
	m := newInput()
	m.SetValue("hello world")
	if m.IsCommand() {
		t.Error("expected IsCommand()=false for plain text")
	}
}

func TestIsCommand_FalseWhenEmpty(t *testing.T) {
	m := newInput()
	if m.IsCommand() {
		t.Error("expected IsCommand()=false when empty")
	}
}

// ── SetValue ──────────────────────────────────────────────────────────────────

func TestSetValue_SetsContent(t *testing.T) {
	m := newInput()
	m.SetValue("hello")
	if got := m.Value(); got != "hello" {
		t.Errorf("expected 'hello', got %q", got)
	}
}

func TestSetValue_TriggersAutoGrow(t *testing.T) {
	m := newInput()
	before := m.Height()
	m.SetValue("line1\nline2")
	if got := m.Height(); got <= before {
		t.Errorf("expected height to grow after SetValue with newline: before=%d after=%d", before, got)
	}
}

// ── Reset ─────────────────────────────────────────────────────────────────────

func TestReset_ClearsValue(t *testing.T) {
	m := newInput()
	m.SetValue("some text")
	m.Reset()
	if got := m.Value(); got != "" {
		t.Errorf("expected empty value after Reset, got %q", got)
	}
}

func TestReset_ShrinksHeightToMinimum(t *testing.T) {
	m := newInput()
	m.InsertNewline()
	m.InsertNewline()
	m.InsertNewline()
	m.Reset()
	want := components.InputMinLines // borderless: resets to min line count
	if got := m.Height(); got != want {
		t.Errorf("expected height %d after Reset, got %d", want, got)
	}
}

func TestReset_TextareaHeightShrinks(t *testing.T) {
	m := newInput()
	m.InsertNewline()
	m.InsertNewline()
	m.Reset()
	if got := m.TextareaHeight(); got != components.InputMinLines {
		t.Errorf("expected textarea height %d after Reset, got %d", components.InputMinLines, got)
	}
}

// ── InsertNewline ─────────────────────────────────────────────────────────────

func TestInsertNewline_ValueContainsNewline(t *testing.T) {
	m := newInput()
	m.SetValue("hello")
	m.InsertNewline()
	if !strings.Contains(m.Value(), "\n") {
		t.Errorf("expected newline in value, got %q", m.Value())
	}
}

func TestInsertNewline_IncreasesModelHeight(t *testing.T) {
	m := newInput()
	before := m.Height()
	m.InsertNewline()
	if got := m.Height(); got <= before {
		t.Errorf("expected Height() to increase: before=%d after=%d", before, got)
	}
}

func TestInsertNewline_UpdatesTextareaRenderHeight(t *testing.T) {
	// The textarea's own render height must grow so lines are actually visible.
	m := newInput()
	m.InsertNewline()
	if got := m.TextareaHeight(); got != 2 {
		t.Errorf("expected TextareaHeight()=2 after one InsertNewline, got %d", got)
	}
}

func TestInsertNewline_CapsAtMaxLines(t *testing.T) {
	m := newInput()
	for i := 0; i < components.InputMaxLines+5; i++ {
		m.InsertNewline()
	}
	wantModelH := components.InputMaxLines // borderless: height == textarea line count
	if got := m.Height(); got != wantModelH {
		t.Errorf("expected model height capped at %d, got %d", wantModelH, got)
	}
	if got := m.TextareaHeight(); got != components.InputMaxLines {
		t.Errorf("expected textarea height capped at %d, got %d", components.InputMaxLines, got)
	}
}

func TestInsertNewline_OnEmptyValue(t *testing.T) {
	m := newInput()
	m.InsertNewline()
	if got := m.Value(); got != "\n" {
		t.Errorf("expected value \"\\n\" after InsertNewline on empty, got %q", got)
	}
}

// ── Height ────────────────────────────────────────────────────────────────────

func TestHeight_MinimumIsOne(t *testing.T) {
	m := newInput()
	if got := m.Height(); got < components.InputMinLines {
		t.Errorf("height should be at least %d, got %d", components.InputMinLines, got)
	}
}

func TestHeight_TwoLinesGivesTwo(t *testing.T) {
	m := newInput()
	m.InsertNewline()
	// After one InsertNewline on empty input: 2 lines → Height() == 2
	if got := m.Height(); got != 2 {
		t.Errorf("expected height=2 for 2 lines, got %d", got)
	}
}

// ── SetMode ───────────────────────────────────────────────────────────────────

func TestSetMode_ToBash(t *testing.T) {
	m := newInput()
	m.SetMode(components.ModeBash)
	if got := m.Mode(); got != components.ModeBash {
		t.Errorf("expected ModeBash, got %v", got)
	}
}

func TestSetMode_BackToChat(t *testing.T) {
	m := newInput()
	m.SetMode(components.ModeBash)
	m.SetMode(components.ModeChat)
	if got := m.Mode(); got != components.ModeChat {
		t.Errorf("expected ModeChat after switching back, got %v", got)
	}
}

// ── SetDisabled ───────────────────────────────────────────────────────────────

func TestSetDisabled_SetsFlag(t *testing.T) {
	m := newInput()
	m.SetDisabled(true)
	if !m.IsDisabled() {
		t.Error("expected IsDisabled()=true")
	}
}

func TestSetDisabled_ClearsFlag(t *testing.T) {
	m := newInput()
	m.SetDisabled(true)
	m.SetDisabled(false)
	if m.IsDisabled() {
		t.Error("expected IsDisabled()=false after re-enabling")
	}
}

func TestSetDisabled_UpdateIsNoOpWhenDisabled(t *testing.T) {
	m := newInput()
	m.SetValue("existing")
	m.SetDisabled(true)
	m, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if got := m.Value(); got != "existing" {
		t.Errorf("disabled input should not change on keypress, got %q", got)
	}
}

// ── SetWidth ──────────────────────────────────────────────────────────────────

func TestSetWidth_UpdatesStoredWidth(t *testing.T) {
	m := newInput()
	m.SetWidth(120)
	if got := m.Width(); got != 120 {
		t.Errorf("expected Width()=120, got %d", got)
	}
}

// ── InputMode.String ──────────────────────────────────────────────────────────

func TestInputMode_String_Chat(t *testing.T) {
	if got := components.ModeChat.String(); got != "CHAT" {
		t.Errorf("expected CHAT, got %q", got)
	}
}

func TestInputMode_String_Bash(t *testing.T) {
	if got := components.ModeBash.String(); got != "BASH" {
		t.Errorf("expected BASH, got %q", got)
	}
}
