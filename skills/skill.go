// Package skills defines the Skill interface and the SkillLoader that
// discovers, loads, and manages both compiled-in and external script-based skills.
package skills

import "encoding/json"

// Tool describes a single callable tool exposed to the model.
// Defined here (rather than in agent/) so skills can construct tools without
// creating an import cycle with the agent package.
type Tool struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object describing the tool's input.
	Parameters json.RawMessage
	Handler    func(args string) (string, error)
}

// Registrar accepts tool registrations. *agent.ToolDispatcher satisfies this
// interface via its Register(*Tool) method.
type Registrar interface {
	Register(t *Tool)
}

// Skill is the interface every skill implements.
type Skill interface {
	// Name returns the unique skill identifier (e.g. "core-tools", "livie-self").
	Name() string

	// Description returns a single sentence shown in /skills list.
	Description() string

	// Register adds the skill's tools to the registrar.
	// Called once at agent construction. No-op for description-only skills.
	Register(r Registrar)

	// SkillMD returns the full SKILL.md body to inject into the system prompt.
	// Should be plain Markdown without TOML frontmatter.
	SkillMD() string
}
