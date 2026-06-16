package tui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/kez/livie/config"
	"github.com/kez/livie/memory"
	"github.com/kez/livie/runner"
)

// CommandHandler is a function executed when a /command is invoked.
// Returns a string response to display in the viewport (empty = silent),
// and optionally an AppAction to perform.
type CommandHandler func(args []string) (response string, action AppAction)

// AppAction is a side-effect the command wants to trigger on the app.
type AppAction int

const (
	ActionNone        AppAction = iota
	ActionQuit                  // handled by ChatModel.handleAction
	ActionNew                   // handled by ChatModel.handleAction
	ActionSetModeChat           // handled by ChatModel.handleAction
	ActionSetModeBash           // handled by ChatModel.handleAction

	// Phase 5 additions:
	ActionOpenSetup     // intercepted by app.Model.Update — triggers screen switch
	ActionRunnerStart   // handled by ChatModel.handleAction
	ActionRunnerStop    // handled by ChatModel.handleAction
	ActionRunnerRestart // handled by ChatModel.handleAction

	// Phase 6–8 additions:
	ActionOpenResume    // handled by ChatModel — fires session.ListSummariesCmd
	ActionResumeSession // handled by ChatModel — loads the selected session

	// Phase 10 additions:
	ActionSkillsUpdated // handled by ChatModel — refreshes HUD skill count after install

	// Phase 7 addendum:
	ActionMemoryChanged // handled by ChatModel — rebuilds system prompt after memory config toggle

	// Phase 9 — RAG index actions:
	ActionIndexAdd    // start background indexing of a path
	ActionIndexStatus // show index status
	ActionIndexClear  // wipe the entire index
	ActionIndexStop   // cancel any in-progress background indexing
)

// SubArg describes a named sub-argument for a /command (e.g. "start" for /run).
// SubArgs may nest arbitrarily: each SubArg can itself carry further SubArgs,
// enabling multi-level completions (e.g. /run start --gpu).
type SubArg struct {
	Name        string
	Description string
	SubArgs     []SubArg // next-level completions, if any
}

// Command describes a registered /command.
type Command struct {
	Name        string
	Aliases     []string
	Description string
	Subcommands []SubArg // optional sub-arguments shown in autocomplete
	Handler     CommandHandler
}

// CommandRegistry maps command names to their definitions.
type CommandRegistry struct {
	commands map[string]*Command
	ordered  []*Command // for /help listing order
}

// NewCommandRegistry creates a registry with all built-in commands registered.
// cfg and mgr are captured by the runner-aware command handlers via closures.
func NewCommandRegistry(cfg *config.Config, mgr *runner.Manager) *CommandRegistry {
	r := &CommandRegistry{
		commands: make(map[string]*Command),
	}
	r.registerBuiltins(cfg, mgr)
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

// FindCommand returns the Command registered under name (or nil).
func (r *CommandRegistry) FindCommand(name string) *Command {
	return r.commands[strings.ToLower(name)]
}

// Suggest returns all commands (in registration order) whose Name or any
// Alias has prefix as a case-insensitive prefix. No cap — callers window.
func (r *CommandRegistry) Suggest(prefix string) []*Command {
	prefix = strings.ToLower(prefix)
	var out []*Command
	for _, cmd := range r.ordered {
		if prefix == "" || strings.HasPrefix(cmd.Name, prefix) {
			out = append(out, cmd)
			continue
		}
		for _, alias := range cmd.Aliases {
			if strings.HasPrefix(alias, prefix) {
				out = append(out, cmd)
				break
			}
		}
	}
	return out
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

// registerBuiltins registers all built-in commands.
// cfg and mgr are captured by the runner-aware handlers via closures.
func (r *CommandRegistry) registerBuiltins(cfg *config.Config, mgr *runner.Manager) {
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

	// /setup — re-opens the setup wizard
	r.Register(&Command{
		Name:        "setup",
		Description: "Re-open the setup wizard",
		Handler: func(args []string) (string, AppAction) {
			return "", ActionOpenSetup
		},
	})

	// /run — manage the local llama-server runner
	r.Register(&Command{
		Name:        "run",
		Description: "Manage the local llama-server runner",
		Subcommands: []SubArg{
			{Name: "status", Description: "Show runner state, binary path, and uptime (default)"},
			{Name: "start", Description: "Start the llama-server process"},
			{Name: "stop", Description: "Stop the llama-server process"},
			{Name: "restart", Description: "Restart the llama-server process"},
			{Name: "log", Description: "Show the last 20 lines of runner output"},
		},
		Handler: func(args []string) (string, AppAction) {
			sub := "status"
			if len(args) > 0 {
				sub = strings.ToLower(args[0])
			}
			switch sub {
			case "start":
				return "", ActionRunnerStart
			case "stop":
				return "", ActionRunnerStop
			case "restart":
				return "", ActionRunnerRestart
			case "status":
				return runStatus(cfg, mgr), ActionNone
			case "log":
				return runLog(mgr), ActionNone
			default:
				return fmt.Sprintf(
					"unknown subcommand: %q\n\nUsage: `/run [start|stop|restart|status|log]`",
					sub,
				), ActionNone
			}
		},
	})

	// /model — show or switch the active model file
	r.Register(&Command{
		Name:        "model",
		Description: "Show or switch the active model file",
		Subcommands: []SubArg{
			{Name: "<path>", Description: "Path to a .gguf file or directory containing one"},
		},
		Handler: func(args []string) (string, AppAction) {
			if len(args) == 0 {
				return modelStatus(cfg), ActionNone
			}
			path, err := resolveModelPath(args[0])
			if err != nil {
				return "✗ " + err.Error(), ActionNone
			}
			cfg.Runner.ModelPath = path
			_ = config.Save(cfg, cfg.ConfigPath)
			mgr.Configure(cfg.Runner)
			name := filepath.Base(path)
			if mgr.IsRunning() {
				return fmt.Sprintf("model set to **%s** — restarting runner…", name), ActionRunnerRestart
			}
			return fmt.Sprintf("model set to **%s**", name), ActionNone
		},
	})

	// /endpoint — show or switch the active API endpoint
	r.Register(&Command{
		Name:        "endpoint",
		Description: "Show or switch the active API endpoint",
		Subcommands: []SubArg{
			{Name: "list", Description: "List all configured endpoints"},
			{Name: "<name>", Description: "Switch to a named endpoint (e.g. local, openai)"},
		},
		Handler: func(args []string) (string, AppAction) {
			if len(args) == 0 {
				return endpointStatus(cfg), ActionNone
			}
			sub := strings.ToLower(args[0])
			if sub == "list" {
				return endpointList(cfg), ActionNone
			}
			return switchEndpoint(cfg, mgr, sub)
		},
	})

	// ── Stubs for future phases ──────────────────────────────────────────────

	stub := func(name, desc string) *Command {
		return &Command{
			Name:        name,
			Description: desc + " _(coming soon)_",
			Handler: func(args []string) (string, AppAction) {
				return fmt.Sprintf("`/%s` is coming in a future update. Stay tuned.", name), ActionNone
			},
		}
	}

	r.Register(stub("usage", "Show token usage and cost estimate for this session"))
	r.Register(&Command{
		Name:        "resume",
		Description: "Resume a previous conversation",
		Handler: func(args []string) (string, AppAction) {
			return "", ActionOpenResume
		},
	})
	r.Register(&Command{
		Name:        "memory",
		Description: "Show vault memory, check status, or toggle memory layers on/off",
		Subcommands: []SubArg{
			{Name: "status", Description: "Show enabled/disabled state of profile and memory"},
			{Name: "on", Description: "Enable both user-profile and memory.md"},
			{Name: "off", Description: "Disable both user-profile and memory.md"},
			{Name: "profile", Description: "Toggle user-profile injection", SubArgs: []SubArg{
				{Name: "on", Description: "Enable user-profile.md in system prompt"},
				{Name: "off", Description: "Disable user-profile.md from system prompt"},
			}},
			{Name: "memory", Description: "Toggle memory.md on-demand hint and write tool", SubArgs: []SubArg{
				{Name: "on", Description: "Enable memory.md on-demand + write_vault_file tool"},
				{Name: "off", Description: "Disable memory.md hint and hard-remove write_vault_file tool"},
			}},
		},
		Handler: func(args []string) (string, AppAction) {
			// /memory — show vault file contents
			if len(args) == 0 {
				vault := cfg.Paths.Vault
				userProfile := memory.LoadFile(vault, "user-profile.md")
				mem := memory.LoadFile(vault, "memory.md")
				if userProfile == "" && mem == "" {
					return "Vault memory is empty — nothing written yet.", ActionNone
				}
				var sb strings.Builder
				if userProfile != "" {
					sb.WriteString("## User Profile\n\n")
					sb.WriteString(userProfile)
				}
				if userProfile != "" && mem != "" {
					sb.WriteString("\n\n---\n\n")
				}
				if mem != "" {
					sb.WriteString("## Memory\n\n")
					sb.WriteString(mem)
				}
				return sb.String(), ActionNone
			}

			sub := strings.ToLower(args[0])

			// /memory status
			if sub == "status" {
				profileState := onOff(cfg.Memory.Profile)
				memState := onOff(cfg.Memory.Enabled)
				return fmt.Sprintf("profile: %s\nmemory:  %s", profileState, memState), ActionNone
			}

			// /memory on | off
			if sub == "on" || sub == "off" {
				enabled := sub == "on"
				cfg.Memory.Profile = enabled
				cfg.Memory.Enabled = enabled
				_ = config.Save(cfg, cfg.ConfigPath)
				return fmt.Sprintf("memory %s (profile: %s, memory.md: %s)", sub, onOff(enabled), onOff(enabled)), ActionMemoryChanged
			}

			// /memory profile on|off
			if sub == "profile" && len(args) >= 2 {
				val := strings.ToLower(args[1])
				if val != "on" && val != "off" {
					return "usage: /memory profile on|off", ActionNone
				}
				cfg.Memory.Profile = val == "on"
				_ = config.Save(cfg, cfg.ConfigPath)
				return fmt.Sprintf("user-profile injection: %s", onOff(cfg.Memory.Profile)), ActionMemoryChanged
			}

			// /memory memory on|off
			if sub == "memory" && len(args) >= 2 {
				val := strings.ToLower(args[1])
				if val != "on" && val != "off" {
					return "usage: /memory memory on|off", ActionNone
				}
				cfg.Memory.Enabled = val == "on"
				_ = config.Save(cfg, cfg.ConfigPath)
				return fmt.Sprintf("memory.md + write_vault_file: %s", onOff(cfg.Memory.Enabled)), ActionMemoryChanged
			}

			return fmt.Sprintf("unknown subcommand: %q\n\nUsage: `/memory [status|on|off|profile on|off|memory on|off]`", args[0]), ActionNone
		},
	})
	// /index — manage the local document index
	r.Register(&Command{
		Name:        "index",
		Description: "Manage the local document and media index",
		Subcommands: []SubArg{
			{Name: "add", Description: "Index a file or directory recursively (runs in background)",
				SubArgs: []SubArg{{Name: "<path>", Description: "File or directory to index"}}},
			{Name: "status", Description: "Show file count, chunk count, store size, and index path"},
			{Name: "stop", Description: "Cancel any in-progress background indexing"},
			{Name: "clear", Description: "Wipe the entire index and manifest"},
		},
		Handler: func(args []string) (string, AppAction) {
			if len(args) == 0 {
				return "Usage: `/index [add <path>|status|stop|clear]`", ActionNone
			}
			switch strings.ToLower(args[0]) {
			case "add":
				if len(args) < 2 {
					return "Usage: `/index add <path>`", ActionNone
				}
				IndexPendingPath = strings.Join(args[1:], " ")
				return "", ActionIndexAdd
			case "status":
				return "", ActionIndexStatus
			case "stop":
				return "", ActionIndexStop
			case "clear":
				return "", ActionIndexClear
			default:
				return fmt.Sprintf("unknown subcommand: %q\n\nUsage: `/index [add <path>|status|stop|clear]`", args[0]), ActionNone
			}
		},
	})
	r.Register(stub("config", "Open the config file in your editor"))
}

// ── /run helpers ──────────────────────────────────────────────────────────

// runStatus returns a formatted status block for the local runner.
func runStatus(cfg *config.Config, mgr *runner.Manager) string {
	state := mgr.State()

	// State line — include PID when running.
	stateLabel := stateString(state)
	if state == runner.StateRunning {
		if pid := mgr.PID(); pid != 0 {
			stateLabel = fmt.Sprintf("running  (PID %d)", pid)
		}
	}

	// Binary path — shorten home dir to ~.
	binPath := shortenHome(mgr.ResolvedBinPath())
	if binPath == "" {
		binPath = "(not found)"
	}

	// Model name — basename only.
	modelName := filepath.Base(cfg.Runner.ModelPath)
	if cfg.Runner.ModelPath == "" {
		modelName = "(not configured)"
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("runner: %s\n", stateLabel))
	sb.WriteString(fmt.Sprintf("binary: %s\n", binPath))
	sb.WriteString(fmt.Sprintf("model:  %s\n", modelName))
	sb.WriteString(fmt.Sprintf("port:   %d", cfg.Runner.Port))

	if state == runner.StateRunning {
		if up := mgr.Uptime(); up > 0 {
			sb.WriteString("\n")
			sb.WriteString(fmt.Sprintf("uptime: %s", formatUptime(up)))
		}
	}

	return sb.String()
}

// runLog returns the last 20 lines of the ring buffer wrapped in a code fence.
func runLog(mgr *runner.Manager) string {
	lines := mgr.LogLines(20)
	if len(lines) == 0 {
		return "_No log output captured yet._"
	}
	return "```\n" + strings.Join(lines, "\n") + "\n```"
}

// ── /model helpers ────────────────────────────────────────────────────────

// modelStatus returns a formatted info block for the current model.
func modelStatus(cfg *config.Config) string {
	if cfg.Runner.ModelPath == "" {
		return "_No model configured. Use `/model <path>` to set one._"
	}
	name := filepath.Base(cfg.Runner.ModelPath)
	path := shortenHome(cfg.Runner.ModelPath)
	ctx := formatContextSize(cfg.Runner.ContextSize)
	backend := cfg.Runner.GPUBackend
	if backend == "" {
		backend = "cpu"
	}
	return fmt.Sprintf(
		"model:    %s\npath:     %s\ncontext:  %s tokens\nbackend:  %s",
		name, path, ctx, backend,
	)
}

// resolveModelPath expands ~ and resolves a user-supplied path to an absolute
// .gguf file path. Returns an absolute path or an error.
//
//  1. File with .gguf extension that exists → use directly
//  2. Directory → scan (non-recursive) for the first .gguf file
//  3. Anything else → error
func resolveModelPath(raw string) (string, error) {
	// Tilde expansion.
	if strings.HasPrefix(raw, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("cannot determine home directory: %w", err)
		}
		raw = filepath.Join(home, raw[2:])
	}

	// Absolute-ify relative paths.
	abs, err := filepath.Abs(raw)
	if err != nil {
		return "", fmt.Errorf("invalid path: %w", err)
	}

	info, err := os.Stat(abs)
	if err != nil {
		return "", fmt.Errorf("path not found: %s", abs)
	}

	// Direct .gguf file.
	if !info.IsDir() {
		if filepath.Ext(abs) != ".gguf" {
			return "", fmt.Errorf("not a .gguf file: %s", abs)
		}
		return abs, nil
	}

	// Directory — scan for the first .gguf.
	entries, err := os.ReadDir(abs)
	if err != nil {
		return "", fmt.Errorf("cannot read directory %s: %w", abs, err)
	}
	for _, e := range entries {
		if !e.IsDir() && filepath.Ext(e.Name()) == ".gguf" {
			return filepath.Join(abs, e.Name()), nil
		}
	}
	return "", fmt.Errorf("no .gguf file found in %s", abs)
}

// ── /endpoint helpers ─────────────────────────────────────────────────────

// endpointStatus returns a one-liner showing the active endpoint.
func endpointStatus(cfg *config.Config) string {
	ep := cfg.ActiveEndpoint()
	if ep.Name == "" {
		return fmt.Sprintf("active endpoint: **%s** _(not found in config)_", cfg.Endpoint.Active)
	}
	return fmt.Sprintf("active endpoint: **%s** → %s", ep.Name, ep.BaseURL)
}

// endpointList returns a table of all configured endpoints.
func endpointList(cfg *config.Config) string {
	if len(cfg.Endpoints) == 0 {
		return "_No endpoints configured._"
	}

	// Find the longest name for alignment.
	maxName := 0
	for _, ep := range cfg.Endpoints {
		if len(ep.Name) > maxName {
			maxName = len(ep.Name)
		}
	}

	var sb strings.Builder
	for _, ep := range cfg.Endpoints {
		padding := strings.Repeat(" ", maxName-len(ep.Name))
		active := ""
		if ep.Name == cfg.Endpoint.Active {
			active = "  **(active)**"
		}
		sb.WriteString(fmt.Sprintf("  %s%s  →  %s%s\n", ep.Name, padding, ep.BaseURL, active))
	}
	return strings.TrimRight(sb.String(), "\n")
}

// switchEndpoint changes the active endpoint, saves config, and returns a
// user-facing message describing any follow-up action needed.
func switchEndpoint(cfg *config.Config, mgr *runner.Manager, name string) (string, AppAction) {
	// Validate the endpoint name.
	found := false
	for _, ep := range cfg.Endpoints {
		if ep.Name == name {
			found = true
			break
		}
	}
	if !found {
		return fmt.Sprintf(
			"✗ endpoint %q not found\n\nAvailable: %s\n\nUse `/endpoint list` to see all endpoints.",
			name, endpointNames(cfg),
		), ActionNone
	}

	prev := cfg.Endpoint.Active
	cfg.Endpoint.Active = name
	_ = config.Save(cfg, cfg.ConfigPath)

	switch {
	case name == "local" && !mgr.IsRunning():
		return fmt.Sprintf(
			"switched to **%s** endpoint — use `/run start` to start the runner",
			name,
		), ActionNone

	case prev == "local" && name != "local" && mgr.IsRunning():
		return fmt.Sprintf(
			"switched to **%s** endpoint — runner left running (use `/run stop` to stop it)",
			name,
		), ActionNone

	default:
		return fmt.Sprintf("switched to **%s** endpoint", name), ActionNone
	}
}

// ── small utilities ───────────────────────────────────────────────────────

// stateString maps a runner.ManagerState to a human-readable label.
func stateString(s runner.ManagerState) string {
	switch s {
	case runner.StateUnconfigured:
		return "unconfigured"
	case runner.StateReady:
		return "ready (not started)"
	case runner.StateStarting:
		return "starting"
	case runner.StateRunning:
		return "running"
	case runner.StateStopped:
		return "stopped"
	case runner.StateError:
		return "error"
	default:
		return "unknown"
	}
}

// shortenHome replaces the user's home directory prefix with ~.
func shortenHome(p string) string {
	if p == "" {
		return ""
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return p
	}
	if strings.HasPrefix(p, home) {
		return "~" + p[len(home):]
	}
	return p
}

// formatUptime formats a duration as "Xm Ys" (e.g. "4m 32s").
func formatUptime(d time.Duration) string {
	total := int(d.Seconds())
	if total < 60 {
		return fmt.Sprintf("%ds", total)
	}
	m := total / 60
	s := total % 60
	return fmt.Sprintf("%dm %ds", m, s)
}

// formatContextSize formats a token count with a comma separator (e.g. 16,384).
func formatContextSize(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

// endpointNames returns a comma-joined list of endpoint names for error messages.
func endpointNames(cfg *config.Config) string {
	names := make([]string, len(cfg.Endpoints))
	for i, ep := range cfg.Endpoints {
		names[i] = ep.Name
	}
	return strings.Join(names, ", ")
}

// onOff returns "on" or "off" for a boolean — used in /memory status output.
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}

// CommandActionMsg carries the result of a dispatched command back to the chat model.
type CommandActionMsg struct {
	Response string
	Action   AppAction
}

// Version is set at build time via -ldflags.
var Version = "dev"

// IndexPendingPath holds the path argument for the most recent /index add command.
// Access is safe because the TUI processes one message at a time (single-threaded).
var IndexPendingPath string
