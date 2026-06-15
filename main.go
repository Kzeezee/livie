package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/agent"
	"github.com/kez/livie/app"
	"github.com/kez/livie/config"
	"github.com/kez/livie/index"
	"github.com/kez/livie/memory"
	"github.com/kez/livie/runner"
)

func main() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "livie: config: %v\n", err)
		os.Exit(1)
	}

	if err := memory.Init(cfg.Paths.Vault); err != nil {
		// Non-fatal. Agent will fall back to defaultSystemPrompt.
		fmt.Fprintf(os.Stderr, "livie: vault init warning: %v\n", err)
	}

	mgr := runner.NewManager(cfg.Runner)

	// Open the persistent vector index. Only available when the local endpoint
	// is active — remote endpoints cannot serve our GGUF embeddings.
	var (
		ix    *index.Indexer
		store *index.Store
	)
	if cfg.Endpoint.Active == "local" {
		ep := cfg.ActiveEndpoint()
		embedFn := index.EmbeddingFunc(ep.BaseURL, ep.APIKey)

		s, err := index.Open(cfg.Paths.Index, embedFn)
		if err != nil {
			fmt.Fprintf(os.Stderr, "livie: index open warning: %v\n", err)
		} else {
			manifest, err := index.LoadManifest(cfg.Paths.Index)
			if err != nil {
				fmt.Fprintf(os.Stderr, "livie: manifest warning: %v\n", err)
			} else {
				// Vision client uses the same local endpoint.
				modelName := cfg.ModelName()
				vision := index.NewVisionClient(ep.BaseURL, ep.APIKey, modelName)
				store = s
				ix = index.NewIndexer(store, manifest, vision, cfg.Paths.Index)
			}
		}
	}

	agt := agent.New(cfg, ix, store)

	p := tea.NewProgram(app.New(cfg, mgr, agt, ix, store))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "livie: %v\n", err)
		os.Exit(1)
	}

	// Shut down the local runner on exit so llama-server doesn't outlive the app.
	_ = mgr.Stop()
}
