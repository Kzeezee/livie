package agent

import (
	"os"
	"path/filepath"

	"github.com/kez/livie/config"
	"github.com/kez/livie/skills"
)

const defaultSystemPrompt = `You are Livie, a terminal-native AI assistant.
You are direct, technically precise, and helpful. You run inside the user's
terminal and have access to their working directory and tools.
Respond concisely unless detail is explicitly requested.`

// LoadSystemPrompt reads the system prompt from path.
// If the file does not exist or cannot be read, defaultSystemPrompt is
// returned silently. This makes the function forward-compatible with the
// future personality.md vault file without requiring it to exist yet.
func LoadSystemPrompt(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return defaultSystemPrompt
	}
	s := string(data)
	if s == "" {
		return defaultSystemPrompt
	}
	return s
}

// buildSystemPrompt constructs the full system prompt for a new conversation.
// The base prompt is read from the vault; each loaded skill's SkillMD body
// is appended after a --- separator so the model knows what tools exist and
// how to use them, and understands the Livie application it runs inside.
func buildSystemPrompt(cfg *config.Config, loader *skills.SkillLoader) string {
	base := LoadSystemPrompt(filepath.Join(cfg.Paths.Vault, "system_prompt.md"))
	if extra := loader.SystemPromptContent(); extra != "" {
		return base + "\n\n---\n\n" + extra
	}
	return base
}
