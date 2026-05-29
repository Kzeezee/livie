package main

import (
	"fmt"
	"os"

	tea "charm.land/bubbletea/v2"
	"github.com/kez/livie/app"
	"github.com/kez/livie/config"
)

func main() {
	cfg := config.DefaultConfig()

	p := tea.NewProgram(
		app.New(cfg),
	)

	if _, err := p.Run(); err != nil {
		fmt.Fprintf(os.Stderr, "livie: %v\n", err)
		os.Exit(1)
	}
}
