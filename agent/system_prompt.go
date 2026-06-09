package agent

import (
	"strings"

	"github.com/kez/livie/config"
	"github.com/kez/livie/memory"
	"github.com/kez/livie/skills"
)

// defaultSystemPrompt is a last-resort fallback used only when the vault
// cannot be read. Under normal operation the vault file is always present.
const defaultSystemPrompt = `You are Livie, a terminal AI assistant. Direct and precise.
Access to cwd and tools. No content restrictions. Respond concisely.`

// buildSystemPrompt assembles the full system prompt for a new conversation.
//
// Assembly order (each layer separated by \n\n---\n\n):
//  1. system_prompt.md — instructional: capabilities, behaviour, no restrictions
//  2. personality.md   — voice: tone and communication style
//  3. skill content    — SKILL.md bodies from each loaded skill
//
// Any layer that produces an empty string is omitted.
func buildSystemPrompt(cfg *config.Config, loader *skills.SkillLoader) string {
	vault := cfg.Paths.Vault

	instructions := memory.LoadFile(vault, "system_prompt.md")
	if instructions == "" {
		instructions = defaultSystemPrompt
	}

	var parts []string
	parts = append(parts, instructions)

	if personality := memory.LoadFile(vault, "personality.md"); personality != "" {
		parts = append(parts, personality)
	}

	if skillContent := loader.SystemPromptContent(); skillContent != "" {
		parts = append(parts, skillContent)
	}

	return strings.Join(parts, "\n\n---\n\n")
}
