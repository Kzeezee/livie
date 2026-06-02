package agent

import (
	"encoding/json"
	"errors"

	openai "github.com/sashabaranov/go-openai"
)

// Tool describes a single callable tool exposed to the model.
type Tool struct {
	Name        string
	Description string
	// Parameters is a JSON Schema object.
	// Example: {"type":"object","properties":{"cmd":{"type":"string"}},"required":["cmd"]}
	Parameters json.RawMessage
	Handler    func(args string) (string, error)
}

// ToolDispatcher holds registered tools and dispatches calls by name.
type ToolDispatcher struct {
	tools map[string]*Tool
}

// NewToolDispatcher creates an empty ToolDispatcher.
func NewToolDispatcher() *ToolDispatcher {
	return &ToolDispatcher{
		tools: make(map[string]*Tool),
	}
}

// Register adds a tool. A duplicate name silently overwrites the prior entry.
func (d *ToolDispatcher) Register(t *Tool) {
	d.tools[t.Name] = t
}

// Definitions returns the []openai.Tool slice for inclusion in API requests.
// Returns nil (not an empty slice) when no tools are registered, which causes
// the go-openai library to omit the "tools" field from the request entirely —
// models that receive no tools don't attempt tool calls.
func (d *ToolDispatcher) Definitions() []openai.Tool {
	if len(d.tools) == 0 {
		return nil
	}
	defs := make([]openai.Tool, 0, len(d.tools))
	for _, t := range d.tools {
		params := t.Parameters
		if params == nil {
			params = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		var schema map[string]interface{}
		_ = json.Unmarshal(params, &schema)

		defs = append(defs, openai.Tool{
			Type: openai.ToolTypeFunction,
			Function: &openai.FunctionDefinition{
				Name:        t.Name,
				Description: t.Description,
				Parameters:  schema,
			},
		})
	}
	return defs
}

// Dispatch invokes the named tool with the raw JSON args string.
// Returns ErrToolNotFound when the name is not registered.
// Phase 6: always returns ErrToolNotFound — no tools are registered.
func (d *ToolDispatcher) Dispatch(name, args string) (string, error) {
	t, ok := d.tools[name]
	if !ok {
		return "", ErrToolNotFound
	}
	return t.Handler(args)
}

// ErrToolNotFound is returned by Dispatch when the tool name is not registered.
var ErrToolNotFound = errors.New("tool not found")
