package rag

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	chromem "github.com/philippgille/chromem-go"

	"github.com/kez/livie/index"
	"github.com/kez/livie/skills"
)

const (
	defaultNResults = 5
	maxNResults     = 20
)

// RegisterTools registers the search_index tool onto r.
func RegisterTools(r skills.Registrar, store *index.Store) {
	r.Register(searchIndexTool(store))
}

func searchIndexTool(store *index.Store) *skills.Tool {
	return &skills.Tool{
		Name:        "search_index",
		Description: "Search the local document index using semantic similarity. Returns the top matching chunks with their source file and relevance score. Requires the local llama-server runner to be active.",
		Parameters: json.RawMessage(`{
			"type": "object",
			"properties": {
				"query": {
					"type": "string",
					"description": "Natural language search query describing what you are looking for"
				},
				"n_results": {
					"type": "integer",
					"description": "Number of results to return (default 5, max 20)",
					"default": 5
				}
			},
			"required": ["query"]
		}`),
		Handler: func(args string) (string, error) {
			if store == nil {
				return "error: index search requires the local runner (llama-server) to be running. Start it with /run start.", nil
			}

			var params struct {
				Query    string `json:"query"`
				NResults int    `json:"n_results"`
			}
			if err := json.Unmarshal([]byte(args), &params); err != nil {
				return "", fmt.Errorf("invalid args: %w", err)
			}
			if params.Query == "" {
				return "error: query must not be empty", nil
			}
			if params.NResults <= 0 {
				params.NResults = defaultNResults
			}
			if params.NResults > maxNResults {
				params.NResults = maxNResults
			}

			if store.Count() == 0 {
				return "The index is empty. Use `/index add <path>` to index files first.", nil
			}

			results, err := store.Query(context.Background(), params.Query, params.NResults)
			if err != nil {
				return fmt.Sprintf("error: search failed: %v", err), nil
			}
			if len(results) == 0 {
				return "No matching documents found in the index.", nil
			}

			return formatResults(results), nil
		},
	}
}

// formatResults formats chromem results as a readable Markdown block.
func formatResults(results []chromem.Result) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Found %d result(s):\n\n", len(results)))

	for i, r := range results {
		source := r.Metadata["source"]
		docType := r.Metadata["type"]
		lang := r.Metadata["lang"]

		// Shorten path to basename for readability; keep full path as reference.
		short := filepath.Base(source)
		if short == "" {
			short = source
		}

		sb.WriteString(fmt.Sprintf("### %d. %s\n", i+1, short))
		sb.WriteString(fmt.Sprintf("**Source:** `%s`  \n", source))
		sb.WriteString(fmt.Sprintf("**Type:** %s", docType))
		if lang != "" {
			sb.WriteString(fmt.Sprintf(" (%s)", lang))
		}
		sb.WriteString(fmt.Sprintf("  \n**Relevance:** %.3f\n\n", r.Similarity))

		content := strings.TrimSpace(r.Content)
		if len(content) > 800 {
			content = content[:800] + "…"
		}

		if docType == "code" && lang != "" {
			sb.WriteString(fmt.Sprintf("```%s\n%s\n```\n\n", lang, content))
		} else {
			sb.WriteString(content)
			sb.WriteString("\n\n")
		}
	}

	return strings.TrimRight(sb.String(), "\n")
}
