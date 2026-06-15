// Package rag provides the search_index AI tool — semantic similarity search
// over locally indexed documents, images, and videos.
package rag

import (
	_ "embed"

	"github.com/kez/livie/index"
	"github.com/kez/livie/skills"
)

//go:embed SKILL.md
var skillMD string

// Skill is the compiled-in RAG skill.
type Skill struct {
	indexer *index.Indexer
	store   *index.Store
}

// New returns a RAG Skill backed by the given indexer and store.
func New(indexer *index.Indexer, store *index.Store) *Skill {
	return &Skill{indexer: indexer, store: store}
}

func (s *Skill) Name() string        { return "rag" }
func (s *Skill) Description() string { return "Semantic search over locally indexed files" }
func (s *Skill) SkillMD() string     { return skills.StripFrontmatter(skillMD) }

func (s *Skill) Register(r skills.Registrar) {
	RegisterTools(r, s.store)
}
