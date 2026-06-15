package tui

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
)

// QuitCmd returns a tea.Cmd that quits the program.
// Centralised here so all quit paths go through one place, making it
// easy to add cleanup (e.g. state persistence) in the future.
func QuitCmd() tea.Cmd {
	return tea.Quit
}

// KeyMap holds all keybindings for the application.
type KeyMap struct {
	ToggleMode         key.Binding
	Submit             key.Binding
	ClearInput         key.Binding
	Quit               key.Binding
	QuitAlt            key.Binding
	Escape             key.Binding
	AutocompleteAccept key.Binding
	AutocompleteUp     key.Binding
	AutocompleteDown   key.Binding
	ScrollUp           key.Binding
	ScrollDown         key.Binding
	ScrollTop          key.Binding
	ScrollBot          key.Binding
	CopyResponse       key.Binding
	HistoryPrev        key.Binding
	HistoryNext        key.Binding
}

// DefaultKeyMap returns the default keybindings.
func DefaultKeyMap() KeyMap {
	return KeyMap{
		ToggleMode: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "toggle bash/chat mode"),
		),
		Submit: key.NewBinding(
			key.WithKeys("enter"),
			key.WithHelp("enter", "submit"),
		),
		ClearInput: key.NewBinding(
			key.WithKeys("ctrl+u"),
			key.WithHelp("ctrl+u", "clear input"),
		),
		Quit: key.NewBinding(
			key.WithKeys("ctrl+c"),
			key.WithHelp("ctrl+c", "quit"),
		),
		QuitAlt: key.NewBinding(
			key.WithKeys("ctrl+q"),
			key.WithHelp("ctrl+q", "quit"),
		),
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel / chat mode"),
		),
		AutocompleteAccept: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "accept suggestion"),
		),
		AutocompleteUp: key.NewBinding(
			key.WithKeys("shift+tab"),
			key.WithHelp("shift+tab", "suggestion up"),
		),
		AutocompleteDown: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "cycle suggestions"),
		),
		ScrollUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("pgup", "scroll up"),
		),
		ScrollDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("pgdn", "scroll down"),
		),
		ScrollTop: key.NewBinding(
			key.WithKeys("ctrl+home"),
			key.WithHelp("ctrl+home", "scroll to top"),
		),
		ScrollBot: key.NewBinding(
			key.WithKeys("ctrl+end"),
			key.WithHelp("ctrl+end", "scroll to bottom"),
		),
		CopyResponse: key.NewBinding(
			key.WithKeys("ctrl+y"),
			key.WithHelp("ctrl+y", "copy last response"),
		),
		HistoryPrev: key.NewBinding(
			key.WithKeys("up"),
			key.WithHelp("↑", "previous message"),
		),
		HistoryNext: key.NewBinding(
			key.WithKeys("down"),
			key.WithHelp("↓", "next message"),
		),
	}
}
