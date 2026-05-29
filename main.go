package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/app"
	"github.com/kez/livie/config"
	"github.com/kez/livie/runner"
)

func main() {
	cfgPath := config.DefaultPath()
	cfg, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "livie: config: %v\n", err)
		os.Exit(1)
	}

	mgr := runner.NewManager(cfg.Runner)

	p := tea.NewProgram(app.New(cfg, mgr))
	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "livie: %v\n", err)
		os.Exit(1)
	}
}
