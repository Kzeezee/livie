package tui

import "charm.land/bubbles/v2/key"

// KeyMap holds all keybindings for the application.
type KeyMap struct {
	ToggleMode key.Binding
	Submit     key.Binding
	ClearInput key.Binding
	Quit       key.Binding
	Escape     key.Binding
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
		Escape: key.NewBinding(
			key.WithKeys("esc"),
			key.WithHelp("esc", "cancel / chat mode"),
		),
	}
}
