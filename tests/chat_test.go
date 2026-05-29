package tests

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/config"
	"github.com/kez/livie/tui/components"
	"github.com/kez/livie/tui/screens"
)

func newChat() screens.ChatModel {
	return screens.NewChatModel(config.DefaultConfig(), 120, 36)
}

// send feeds a single tea.Msg into the chat model and returns the updated model.
func send(m screens.ChatModel, msg tea.Msg) screens.ChatModel {
	m, _ = m.Update(msg)
	return m
}

func keyPress(code rune) tea.KeyPressMsg              { return tea.KeyPressMsg{Code: code} }
func keyMod(code rune, mod tea.KeyMod) tea.KeyPressMsg { return tea.KeyPressMsg{Code: code, Mod: mod} }
func runeKey(r rune) tea.KeyPressMsg                  { return tea.KeyPressMsg{Code: r, Text: string(r)} }

// typeText feeds a string character by character into the model.
func typeText(m screens.ChatModel, s string) screens.ChatModel {
	for _, r := range s {
		m = send(m, runeKey(r))
	}
	return m
}

// ── ViewportH ────────────────────────────────────────────────────────────────

func TestViewportH_Normal(t *testing.T) {
	// 36 total - 1 HUD - 1 divider - 3 input = 31
	got := screens.ViewportH(36, 3)
	if got != 31 {
		t.Errorf("ViewportH(36, 3) = %d, want 31", got)
	}
}

func TestViewportH_MinimumIsOne(t *testing.T) {
	got := screens.ViewportH(1, 100)
	if got < 1 {
		t.Errorf("ViewportH should never return < 1, got %d", got)
	}
}

func TestViewportH_LargerInputReducesViewport(t *testing.T) {
	small := screens.ViewportH(36, 3)
	large := screens.ViewportH(36, 6)
	if large >= small {
		t.Errorf("larger inputH should reduce viewport: small=%d large=%d", small, large)
	}
}

// ── Initial state ─────────────────────────────────────────────────────────────

func TestChat_InitialModeIsChat(t *testing.T) {
	m := newChat()
	if m.Mode() != components.ModeChat {
		t.Errorf("expected ModeChat on init, got %v", m.Mode())
	}
}

func TestChat_InputIsEmptyOnInit(t *testing.T) {
	m := newChat()
	if got := m.Input().Value(); got != "" {
		t.Errorf("expected empty input on init, got %q", got)
	}
}

// ── Typing ───────────────────────────────────────────────────────────────────

func TestChat_TypingUpdatesInputValue(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello")
	if got := m.Input().Value(); got != "hello" {
		t.Errorf("expected input value 'hello', got %q", got)
	}
}

// ── Ctrl+J newline ────────────────────────────────────────────────────────────

func TestChat_CtrlJ_InsertsNewline(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello")
	m = send(m, keyMod(tea.KeyEnter, tea.ModShift))
	val := m.Input().Value()
	found := false
	for _, c := range val {
		if c == '\n' {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected newline in value after ctrl+j, got %q", val)
	}
}

func TestChat_CtrlJ_DoesNotSubmit(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello")
	m = send(m, keyMod(tea.KeyEnter, tea.ModShift))
	if got := m.Input().Value(); got == "" {
		t.Error("ctrl+j should not clear the input (not a submit)")
	}
}

func TestChat_CtrlJ_GrowsInputHeight(t *testing.T) {
	m := newChat()
	before := m.InputHeight()
	m = send(m, keyMod(tea.KeyEnter, tea.ModShift))
	after := m.InputHeight()
	if after <= before {
		t.Errorf("expected input to grow after ctrl+j: before=%d after=%d", before, after)
	}
}

func TestChat_Enter_DoesNotInsertNewline(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello")
	m = send(m, keyPress(tea.KeyEnter))
	// After submit, input is cleared — no newline should have been inserted.
	for _, c := range m.Input().Value() {
		if c == '\n' {
			t.Errorf("plain enter should not insert a newline, got value %q", m.Input().Value())
			break
		}
	}
}

// ── Submit (Enter) ────────────────────────────────────────────────────────────

func TestChat_Submit_ClearsInput(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello world")
	m = send(m, keyPress(tea.KeyEnter))
	if got := m.Input().Value(); got != "" {
		t.Errorf("expected empty input after submit, got %q", got)
	}
}

func TestChat_Submit_EmptyIsNoOp(t *testing.T) {
	m := newChat()
	m = send(m, keyPress(tea.KeyEnter))
	if got := m.Input().Value(); got != "" {
		t.Errorf("expected input to remain empty, got %q", got)
	}
}

// ── ClearInput (Ctrl+U) ───────────────────────────────────────────────────────

func TestChat_CtrlU_ClearsInput(t *testing.T) {
	m := newChat()
	m = typeText(m, "hello world")
	m = send(m, keyMod('u', tea.ModCtrl))
	if got := m.Input().Value(); got != "" {
		t.Errorf("expected empty input after ctrl+u, got %q", got)
	}
}

func TestChat_CtrlU_PreservesMode(t *testing.T) {
	m := newChat()
	m = send(m, keyMod('u', tea.ModCtrl))
	if m.Mode() != components.ModeChat {
		t.Errorf("ctrl+u should not change mode, got %v", m.Mode())
	}
}

// ── ToggleMode (Shift+Tab) ────────────────────────────────────────────────────

func TestChat_ShiftTab_ChatToBash(t *testing.T) {
	m := newChat()
	m = send(m, keyMod(tea.KeyTab, tea.ModShift))
	if m.Mode() != components.ModeBash {
		t.Errorf("expected ModeBash after shift+tab, got %v", m.Mode())
	}
}

func TestChat_ShiftTab_BashToChat(t *testing.T) {
	m := newChat()
	m = send(m, keyMod(tea.KeyTab, tea.ModShift))
	m = send(m, keyMod(tea.KeyTab, tea.ModShift))
	if m.Mode() != components.ModeChat {
		t.Errorf("expected ModeChat after two shift+tab presses, got %v", m.Mode())
	}
}

// ── Escape ────────────────────────────────────────────────────────────────────

func TestChat_Escape_FromBashReturnsToChat(t *testing.T) {
	m := newChat()
	m = send(m, keyMod(tea.KeyTab, tea.ModShift)) // → Bash
	m = send(m, keyPress(tea.KeyEsc))
	if m.Mode() != components.ModeChat {
		t.Errorf("expected ModeChat after esc from bash, got %v", m.Mode())
	}
}

func TestChat_Escape_InChatNoChange(t *testing.T) {
	m := newChat()
	m = send(m, keyPress(tea.KeyEsc))
	if m.Mode() != components.ModeChat {
		t.Errorf("esc in chat mode should stay ModeChat, got %v", m.Mode())
	}
}

// ── Quit (Ctrl+C double-press) ────────────────────────────────────────────────

func TestChat_SingleCtrlC_DoesNotQuit(t *testing.T) {
	m := newChat()
	_, cmd := m.Update(keyMod('c', tea.ModCtrl))
	// First ctrl+c schedules a tick (waiting for second press) — cmd must be non-nil.
	if cmd == nil {
		t.Error("expected non-nil cmd (tick) after first ctrl+c")
	}
}

// ── Window resize ─────────────────────────────────────────────────────────────

func TestChat_WindowResize_UpdatesDimensions(t *testing.T) {
	m := newChat()
	m = send(m, tea.WindowSizeMsg{Width: 200, Height: 50})
	if m.TermWidth() != 200 || m.TermHeight() != 50 {
		t.Errorf("expected 200x50 after resize, got %dx%d", m.TermWidth(), m.TermHeight())
	}
}

// ── syncInputHeight (via InputHeight) ────────────────────────────────────────

func TestChat_InputGrows_ViewportShrinks(t *testing.T) {
	m := newChat()
	vpBefore := screens.ViewportH(m.TermHeight(), m.InputHeight())
	m = send(m, keyMod(tea.KeyEnter, tea.ModShift)) // grow the input by one line
	vpAfter := screens.ViewportH(m.TermHeight(), m.InputHeight())
	if vpAfter >= vpBefore {
		t.Errorf("viewport should shrink when input grows: before=%d after=%d", vpBefore, vpAfter)
	}
}
