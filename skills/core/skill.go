package core

import (
	_ "embed"

	"github.com/kez/livie/skills"
)

//go:embed SKILL.md
var skillMD string

// Skill is the compiled-in core-tools skill.
type Skill struct{ cwd string }

// New returns a new core Skill bound to the given working directory.
func New(cwd string) *Skill { return &Skill{cwd: cwd} }

func (s *Skill) Name() string        { return "core-tools" }
func (s *Skill) Description() string { return "Built-in file, shell, and search tools" }
func (s *Skill) SkillMD() string     { return skills.StripFrontmatter(skillMD) }

// Register adds the 5 built-in tools to r.
func (s *Skill) Register(r skills.Registrar) {
	RegisterTools(r, s.cwd)
}
