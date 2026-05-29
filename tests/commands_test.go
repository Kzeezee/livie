package tests

import (
	"strings"
	"testing"

	"github.com/kez/livie/tui"
)

func newRegistry() *tui.CommandRegistry {
	return tui.NewCommandRegistry()
}

// ── Dispatch ──────────────────────────────────────────────────────────────────

func TestDispatch_NoLeadingSlash_ReturnsEmpty(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("help")
	if resp != "" || action != tui.ActionNone {
		t.Errorf("expected no response/action for non-command input, got %q / %v", resp, action)
	}
}

func TestDispatch_EmptyInput_ReturnsEmpty(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("")
	if resp != "" || action != tui.ActionNone {
		t.Errorf("expected empty result for blank input, got %q / %v", resp, action)
	}
}

func TestDispatch_SlashOnly_ReturnsEmpty(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("/")
	if resp != "" || action != tui.ActionNone {
		t.Errorf("expected empty result for bare slash, got %q / %v", resp, action)
	}
}

func TestDispatch_UnknownCommand_ReturnsErrorMessage(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("/nonexistent")
	if !strings.Contains(resp, "Unknown command") {
		t.Errorf("expected 'Unknown command' in response, got %q", resp)
	}
	if action != tui.ActionNone {
		t.Errorf("expected ActionNone for unknown command, got %v", action)
	}
}

func TestDispatch_Help_ReturnsHelpText(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("/help")
	if !strings.Contains(resp, "Available Commands") {
		t.Errorf("expected help text to contain 'Available Commands', got %q", resp)
	}
	if action != tui.ActionNone {
		t.Errorf("expected ActionNone for /help, got %v", action)
	}
}

func TestDispatch_Help_AliasH(t *testing.T) {
	r := newRegistry()
	resp, _ := r.Dispatch("/h")
	if !strings.Contains(resp, "Available Commands") {
		t.Errorf("alias /h should return help text, got %q", resp)
	}
}

func TestDispatch_Help_AliasQuestion(t *testing.T) {
	r := newRegistry()
	resp, _ := r.Dispatch("/?")
	if !strings.Contains(resp, "Available Commands") {
		t.Errorf("alias /? should return help text, got %q", resp)
	}
}

func TestDispatch_Exit_ReturnsActionQuit(t *testing.T) {
	r := newRegistry()
	_, action := r.Dispatch("/exit")
	if action != tui.ActionQuit {
		t.Errorf("expected ActionQuit for /exit, got %v", action)
	}
}

func TestDispatch_Quit_AliasReturnsActionQuit(t *testing.T) {
	r := newRegistry()
	_, action := r.Dispatch("/quit")
	if action != tui.ActionQuit {
		t.Errorf("expected ActionQuit for /quit alias, got %v", action)
	}
}

func TestDispatch_Version_ReturnsVersionString(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("/version")
	if !strings.Contains(resp, "Livie") {
		t.Errorf("expected 'Livie' in /version response, got %q", resp)
	}
	if action != tui.ActionNone {
		t.Errorf("expected ActionNone for /version, got %v", action)
	}
}

func TestDispatch_CaseInsensitive(t *testing.T) {
	r := newRegistry()
	resp, _ := r.Dispatch("/HELP")
	if !strings.Contains(resp, "Available Commands") {
		t.Errorf("expected case-insensitive dispatch to work, got %q", resp)
	}
}

func TestDispatch_LeadingTrailingWhitespace(t *testing.T) {
	r := newRegistry()
	resp, _ := r.Dispatch("  /help  ")
	if !strings.Contains(resp, "Available Commands") {
		t.Errorf("expected whitespace-trimmed dispatch to work, got %q", resp)
	}
}

func TestDispatch_StubCommand_ReturnsComing(t *testing.T) {
	r := newRegistry()
	resp, action := r.Dispatch("/skills")
	if !strings.Contains(resp, "coming") {
		t.Errorf("expected stub response to mention 'coming', got %q", resp)
	}
	if action != tui.ActionNone {
		t.Errorf("expected ActionNone for stub command, got %v", action)
	}
}

// ── HelpText ──────────────────────────────────────────────────────────────────

func TestHelpText_ContainsKeyboardShortcuts(t *testing.T) {
	r := newRegistry()
	help := r.HelpText()
	for _, want := range []string{"shift+tab", "ctrl+j", "ctrl+u", "ctrl+c"} {
		if !strings.Contains(help, want) {
			t.Errorf("expected help text to contain shortcut %q", want)
		}
	}
}

func TestHelpText_DoesNotContainRemovedShortcuts(t *testing.T) {
	r := newRegistry()
	help := r.HelpText()
	// Check for exact backtick-quoted tokens as they appear in help output,
	// to avoid false positives (e.g. "/model" containing "/mode" as substring).
	removed := []string{"ctrl+l", "ctrl+b", "`/clear`", "`/mode`"}
	for _, bad := range removed {
		if strings.Contains(help, bad) {
			t.Errorf("help text should not contain removed item %q", bad)
		}
	}
}

func TestHelpText_ContainsAllBuiltinCommands(t *testing.T) {
	r := newRegistry()
	help := r.HelpText()
	for _, cmd := range []string{"/help", "/version", "/exit", "/skills", "/usage"} {
		if !strings.Contains(help, cmd) {
			t.Errorf("expected help text to contain %q", cmd)
		}
	}
}

// ── Register ──────────────────────────────────────────────────────────────────

func TestRegister_CustomCommand(t *testing.T) {
	r := newRegistry()
	r.Register(&tui.Command{
		Name:        "ping",
		Description: "returns pong",
		Handler: func(args []string) (string, tui.AppAction) {
			return "pong", tui.ActionNone
		},
	})
	resp, _ := r.Dispatch("/ping")
	if resp != "pong" {
		t.Errorf("expected 'pong', got %q", resp)
	}
}

func TestRegister_AliasDispatch(t *testing.T) {
	r := newRegistry()
	r.Register(&tui.Command{
		Name:    "ping",
		Aliases: []string{"p"},
		Handler: func(args []string) (string, tui.AppAction) {
			return "pong", tui.ActionNone
		},
	})
	resp, _ := r.Dispatch("/p")
	if resp != "pong" {
		t.Errorf("expected alias /p to return 'pong', got %q", resp)
	}
}
