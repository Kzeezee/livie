package livieself

import (
	_ "embed"

	"github.com/kez/livie/skills"
)

//go:embed SKILL.md
var skillMD string

// Skill is the compiled-in livie-self meta-skill.
// It has no tools — its only purpose is to inject Livie's self-description
// into the system prompt so the AI understands the application it runs inside.
type Skill struct{}

// New returns a new livie-self Skill.
func New() *Skill { return &Skill{} }

func (s *Skill) Name() string        { return "livie-self" }
func (s *Skill) Description() string { return "Livie self-description: modes, keys, commands, config, vault" }
func (s *Skill) SkillMD() string     { return skills.StripFrontmatter(skillMD) }

// Register is a no-op — this skill contributes only system prompt content.
func (s *Skill) Register(_ skills.Registrar) {}
