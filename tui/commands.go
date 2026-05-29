package tui

import (
	"fmt"
	"strings"
)

// CommandHandler is a function executed when a /command is invoked.
// Returns a string response to display in the viewport (empty = silent),
// and optionally an AppAction to perform.
type CommandHandler func(args []string) (response string, action AppAction)

// AppAction is a side-effect the command wants to trigger on the app.
type AppAction int

const (
	ActionNone AppAction = iota
	ActionQuit
	ActionNew
	ActionSetModeChat
	ActionSetModeBash
)

// Command describes a registered /command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Handler     CommandHandler
}

// CommandRegistry maps command names to their definitions.
type CommandRegistry struct {
	commands map[string]*Command
	ordered  []*Command // for /help listing order
}

// NewCommandRegistry creates a registry with all built-in commands registered.
func NewCommandRegistry() *CommandRegistry {
	r := &CommandRegistry{
		commands: make(map[string]*Command),
	}
	r.registerBuiltins()
	return r
}

// Register adds a command. Also registers aliases.
func (r *CommandRegistry) Register(cmd *Command) {
	r.ordered = append(r.ordered, cmd)
	r.commands[cmd.Name] = cmd
	for _, alias := range cmd.Aliases {
		r.commands[alias] = cmd
	}
}

// Dispatch parses input and runs the matching command.
// Returns the response string and action, or an error message.
func (r *CommandRegistry) Dispatch(input string) (string, AppAction) {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") {
		return "", ActionNone
	}
	parts := strings.Fields(input[1:]) // strip leading /
	if len(parts) == 0 {
		return "", ActionNone
	}
	name := strings.ToLower(parts[0])
	args := parts[1:]

	cmd, ok := r.commands[name]
	if !ok {
		return fmt.Sprintf("Unknown command: /%s\nType /help for a list of commands.", name), ActionNone
	}
	return cmd.Handler(args)
}

// HelpText returns a formatted list of all commands.
func (r *CommandRegistry) HelpText() string {
	var sb strings.Builder
	sb.WriteString("**Available Commands**\n\n")
	for _, cmd := range r.ordered {
		aliases := ""
		if len(cmd.Aliases) > 0 {
			aliases = fmt.Sprintf(" _(also: /%s)_", strings.Join(cmd.Aliases, ", /"))
		}
		sb.WriteString(fmt.Sprintf("- `/%s`%s — %s\n", cmd.Name, aliases, cmd.Description))
	}
	sb.WriteString("\n**Keyboard Shortcuts**\n\n")
	sb.WriteString("- `shift+tab` — toggle Chat / Bash mode\n")
	sb.WriteString("- `ctrl+j` / `shift+enter` — insert new line (shift+enter requires Kitty terminal)\n")
	sb.WriteString("- `ctrl+u` — clear input\n")
	sb.WriteString("- `ctrl+c` × 2 — quit\n")
	return sb.String()
}

// registerBuiltins registers all Phase 1 commands.
func (r *CommandRegistry) registerBuiltins() {
	// /help
	r.Register(&Command{
		Name:        "help",
		Aliases:     []string{"h", "?"},
		Description: "Show all commands and keyboard shortcuts",
		Handler: func(args []string) (string, AppAction) {
			return r.HelpText(), ActionNone
		},
	})

	// /version
	r.Register(&Command{
		Name:        "version",
		Description: "Show Livie version and build info",
		Handler: func(args []string) (string, AppAction) {
			return fmt.Sprintf("**Livie** `%s`\nBuilt with Go · Bubbletea · Lipgloss", Version), ActionNone
		},
	})

	// /exit  /quit
	r.Register(&Command{
		Name:        "exit",
		Aliases:     []string{"quit", "q"},
		Description: "Quit Livie",
		Handler: func(args []string) (string, AppAction) {
			return "", ActionQuit
		},
	})

	// ── Phase 2 stubs ──────────────────────────────────────────────

	stub := func(name, desc string) *Command {
		return &Command{
			Name:        name,
			Description: desc + " _(coming soon)_",
			Handler: func(args []string) (string, AppAction) {
				return fmt.Sprintf("`/%s` is coming in a future update. Stay tuned.", name), ActionNone
			},
		}
	}

	r.Register(stub("skills", "List, install, enable or disable skills"))
	r.Register(stub("usage", "Show token usage and cost estimate for this session"))
	r.Register(stub("resume", "Resume a previous conversation session"))
	r.Register(stub("model", "Switch the active model"))
	r.Register(stub("endpoint", "Switch the active API endpoint"))
	r.Register(stub("memory", "View or edit Livie's memory files"))
	r.Register(stub("index", "Manage the local media index"))
	r.Register(stub("run", "Start or stop the local llama-server runner"))
	r.Register(stub("config", "Open the config file in your editor"))
}

// CommandActionMsg carries the result of a dispatched command back to the chat model.
type CommandActionMsg struct {
	Response string
	Action   AppAction
}

// Version is set at build time via -ldflags.
var Version = "dev"
