package screens

// setup.go — multi-step first-run wizard.
//
// State machine:
//   stepBoot → stepDetecting → stepInstallPrompt | stepModeSelect
//   stepInstallPrompt → stepGPUSelect | stepModeSelect
//   stepGPUSelect → stepInstalling
//   stepInstalling → stepModeSelect | stepInstallError
//   stepInstallError → stepGPUSelect | stepModeSelect
//   stepModeSelect → stepConfigLocal | stepConfigRemote
//   stepConfigLocal → stepStartingRunner
//   stepConfigRemote → stepDone
//   stepStartingRunner → stepDone
//   stepDone → TransitionToChat (auto, 800ms)

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/kez/livie/config"
	"github.com/kez/livie/runner"
	tui "github.com/kez/livie/tui"
)

// TransitionToChat signals the root app to switch from the setup screen to chat.
type TransitionToChat struct {
	Config *config.Config
}

// ── private message types ─────────────────────────────────────────────────

type setupSpinnerTickMsg struct{}
type setupAdvanceMsg struct{}
type detectAvailableMsg struct{ backends []runner.GPUBackend }

// ── step constants ────────────────────────────────────────────────────────

type setupStep int

const (
	stepBoot setupStep = iota
	stepDetecting
	stepInstallPrompt
	stepGPUSelect
	stepInstalling
	stepInstallError
	stepModeSelect
	stepConfigLocal
	stepConfigRemote
	stepStartingRunner
	stepDone
)

// braille spinner frames, cycled at 80ms.
var spinnerFrames = []string{"⠋", "⠙", "⠹", "⠸", "⠼", "⠴", "⠦", "⠧", "⠇", "⠏"}

// ── model ─────────────────────────────────────────────────────────────────

// SetupModel implements the multi-step setup wizard.
type SetupModel struct {
	width, height int
	step          setupStep
	cfg           *config.Config
	runner        *runner.Manager

	// Detection result
	detectedBinPath string

	// GPU selection (stepGPUSelect)
	availableBackends []runner.GPUBackend
	gpuChoice         int // index into availableBackends

	// Download (stepInstalling)
	downloadCtx     context.Context    //nolint:containedctx
	downloadCancel  context.CancelFunc
	downloadCh      <-chan runner.ProgressUpdate
	downloadedBytes int64
	downloadTotal   int64
	downloadErr     error

	// Mode selection (stepModeSelect)
	modeChoice int // 0 = local, 1 = remote

	// Local config form (stepConfigLocal)
	localInputs [4]textinput.Model // model path, gpu layers, ctx size, port
	localFocus  int

	// Remote config form (stepConfigRemote)
	remoteInputs [3]textinput.Model // base URL, api key, model name
	remoteFocus  int

	// Spinner animation
	spinnerFrame int

	// Error display
	errMsg string
}

// NewSetupModel creates the setup wizard pre-populated with existing config values.
func NewSetupModel(cfg *config.Config, mgr *runner.Manager, width, height int) SetupModel {
	m := SetupModel{
		width:  width,
		height: height,
		cfg:    cfg,
		runner: mgr,
		step:   stepBoot,
	}
	return m
}

func (m SetupModel) Init() tea.Cmd {
	return tea.Batch(
		spinnerTickCmd(),
		tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
			return setupAdvanceMsg{}
		}),
	)
}

// ── update ────────────────────────────────────────────────────────────────

func (m SetupModel) Update(msg tea.Msg) (SetupModel, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case setupSpinnerTickMsg:
		m.spinnerFrame = (m.spinnerFrame + 1) % len(spinnerFrames)
		cmds = append(cmds, spinnerTickCmd())

	case setupAdvanceMsg:
		switch m.step {
		case stepBoot:
			m.step = stepDetecting
			cmds = append(cmds, detectCmd(m.cfg.Runner))
		case stepDone:
			return m, func() tea.Msg {
				return TransitionToChat{Config: m.cfg}
			}
		}

	case runner.DetectCompleteMsg:
		if msg.Found {
			m.detectedBinPath = msg.Path
			m.runner.SetBinaryPath(msg.Path)
			m.step, cmds = m.enterModeSelect(cmds)
		} else {
			m.step = stepInstallPrompt
		}

	case detectAvailableMsg:
		m.availableBackends = msg.backends

	case runner.DownloadProgressMsg:
		m.downloadedBytes = msg.Downloaded
		m.downloadTotal = msg.Total
		if msg.Done {
			if msg.Err != nil {
				m.downloadErr = msg.Err
				m.step = stepInstallError
				m.errMsg = msg.Err.Error()
			} else {
				// Binary downloaded successfully.
				m.runner.SetBinaryPath(msg.BinaryPath)
				m.detectedBinPath = msg.BinaryPath
				m.step, cmds = m.enterModeSelect(cmds)
			}
		} else {
			// Re-issue the progress cmd for the next chunk.
			cmds = append(cmds, runner.DownloadProgressCmd(m.downloadCh))
		}

	case runner.RunnerStartedMsg:
		m.step = stepDone
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		}
		// Auto-advance to chat after 800ms.
		cmds = append(cmds, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
			return setupAdvanceMsg{}
		}))

	case tea.KeyPressMsg:
		var cmd tea.Cmd
		m, cmd = m.handleKey(msg)
		if cmd != nil {
			cmds = append(cmds, cmd)
		}
	}

	// Forward keyboard events to focused text inputs.
	m, inputCmds := m.updateInputs(msg)
	cmds = append(cmds, inputCmds...)

	return m, tea.Batch(cmds...)
}

func (m SetupModel) handleKey(msg tea.KeyPressMsg) (SetupModel, tea.Cmd) {
	k := msg.String()

	switch m.step {
	case stepInstallPrompt:
		switch k {
		case "up", "k":
			if m.modeChoice > 0 {
				m.modeChoice--
			}
		case "down", "j":
			if m.modeChoice < 1 {
				m.modeChoice++
			}
		case "enter":
			if m.modeChoice == 0 {
				// Install llama-server.
				return m, m.enterGPUSelect()
			}
			// Skip — go straight to mode selection (remote).
			m.modeChoice = 1
			var cmds []tea.Cmd
			m.step, cmds = m.enterModeSelect(nil)
			return m, tea.Batch(cmds...)
		case "esc":
			m.modeChoice = 1
			var cmds []tea.Cmd
			m.step, cmds = m.enterModeSelect(nil)
			return m, tea.Batch(cmds...)
		}

	case stepGPUSelect:
		switch k {
		case "up", "k":
			if m.gpuChoice > 0 {
				m.gpuChoice--
			}
		case "down", "j":
			if m.gpuChoice < len(m.availableBackends)-1 {
				m.gpuChoice++
			}
		case "enter":
			return m, m.startDownload()
		case "esc":
			m.step = stepInstallPrompt
			m.modeChoice = 0
		}

	case stepInstallError:
		switch k {
		case "up", "k":
			if m.modeChoice > 0 {
				m.modeChoice--
			}
		case "down", "j":
			if m.modeChoice < 1 {
				m.modeChoice++
			}
		case "enter":
			if m.modeChoice == 0 {
				// Retry.
				m.step = stepGPUSelect
				return m, m.startDownload()
			}
			// Skip to mode select.
			m.modeChoice = 1
			var cmds []tea.Cmd
			m.step, cmds = m.enterModeSelect(nil)
			return m, tea.Batch(cmds...)
		case "esc":
			m.step = stepGPUSelect
		}

	case stepModeSelect:
		switch k {
		case "up", "k":
			if m.modeChoice > 0 {
				m.modeChoice--
			}
		case "down", "j":
			if m.modeChoice < 1 {
				m.modeChoice++
			}
		case "enter":
			if m.modeChoice == 0 {
				m.step = stepConfigLocal
				m = m.initLocalInputs()
				return m, m.localInputs[0].Focus()
			}
			m.step = stepConfigRemote
			m = m.initRemoteInputs()
			return m, m.remoteInputs[0].Focus()
		}

	case stepConfigLocal:
		switch k {
		case "tab":
			m.localFocus = (m.localFocus + 1) % 4
			return m, m.refocusLocal()
		case "shift+tab":
			m.localFocus = (m.localFocus + 3) % 4
			return m, m.refocusLocal()
		case "enter":
			if m.localFocus < 3 {
				m.localFocus++
				return m, m.refocusLocal()
			}
			// Last field — save and proceed.
			m = m.saveLocalConfig()
			m.step = stepStartingRunner
			return m, m.runner.PollUntilReadyCmd(30 * time.Second)
		case "esc":
			m.step, _ = m.enterModeSelect(nil)
		}

	case stepConfigRemote:
		switch k {
		case "tab":
			m.remoteFocus = (m.remoteFocus + 1) % 3
			return m, m.refocusRemote()
		case "shift+tab":
			m.remoteFocus = (m.remoteFocus + 2) % 3
			return m, m.refocusRemote()
		case "enter":
			if m.remoteFocus < 2 {
				m.remoteFocus++
				return m, m.refocusRemote()
			}
			// Save and proceed — no runner to start.
			m = m.saveRemoteConfig()
			if m.remoteInputs[0].Value() == "" {
				m.errMsg = "Base URL is required"
				return m, nil
			}
			m.errMsg = ""
			m.step = stepDone
			return m, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
				return setupAdvanceMsg{}
			})
		case "esc":
			m.step, _ = m.enterModeSelect(nil)
		}
	}

	return m, nil
}

// updateInputs forwards messages to the active text input set.
func (m SetupModel) updateInputs(msg tea.Msg) (SetupModel, []tea.Cmd) {
	var cmds []tea.Cmd

	switch m.step {
	case stepConfigLocal:
		for i := range m.localInputs {
			var cmd tea.Cmd
			m.localInputs[i], cmd = m.localInputs[i].Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	case stepConfigRemote:
		for i := range m.remoteInputs {
			var cmd tea.Cmd
			m.remoteInputs[i], cmd = m.remoteInputs[i].Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
	}
	return m, cmds
}

// ── step entry helpers ────────────────────────────────────────────────────

func (m SetupModel) enterModeSelect(cmds []tea.Cmd) (setupStep, []tea.Cmd) {
	m.step = stepModeSelect
	m.modeChoice = 0
	return stepModeSelect, cmds
}

func (m SetupModel) enterGPUSelect() tea.Cmd {
	m.step = stepGPUSelect
	m.gpuChoice = 0
	return detectAvailableCmd()
}

func (m SetupModel) startDownload() tea.Cmd {
	var gpu runner.GPUBackend
	if len(m.availableBackends) > m.gpuChoice {
		gpu = m.availableBackends[m.gpuChoice]
	}
	platform := runner.New(gpu)
	destDir := runner.DataDirBinDir()

	ctx, cancel := context.Background(), func() {}
	ctx, cancel = context.WithCancel(ctx)
	m.downloadCtx = ctx
	m.downloadCancel = cancel
	m.downloadedBytes = 0
	m.downloadTotal = 0
	m.downloadErr = nil
	m.step = stepInstalling

	ch := runner.StartDownload(ctx, platform, destDir)
	m.downloadCh = ch
	return runner.DownloadProgressCmd(ch)
}

// initLocalInputs prepopulates the local config text inputs from cfg.
func (m SetupModel) initLocalInputs() SetupModel {
	port := 8080
	if m.cfg.Runner.Port > 0 {
		port = m.cfg.Runner.Port
	}
	gpuLayers := -1
	if m.cfg.Runner.GPULayers != 0 {
		gpuLayers = m.cfg.Runner.GPULayers
	}
	ctxSize := 16384
	if m.cfg.Runner.ContextSize > 0 {
		ctxSize = m.cfg.Runner.ContextSize
	}

	m.localInputs[0] = newInput("", 60)
	m.localInputs[0].SetValue(m.cfg.Runner.ModelPath)
	m.localInputs[0].Placeholder = "/path/to/model.gguf"

	m.localInputs[1] = newInput("", 12)
	m.localInputs[1].SetValue(fmt.Sprintf("%d", gpuLayers))
	m.localInputs[1].Placeholder = "-1"

	m.localInputs[2] = newInput("", 14)
	m.localInputs[2].SetValue(fmt.Sprintf("%d", ctxSize))
	m.localInputs[2].Placeholder = "16384"

	m.localInputs[3] = newInput("", 8)
	m.localInputs[3].SetValue(fmt.Sprintf("%d", port))
	m.localInputs[3].Placeholder = "8080"

	m.localFocus = 0
	return m
}

// initRemoteInputs prepopulates the remote config text inputs from cfg.
func (m SetupModel) initRemoteInputs() SetupModel {
	active := m.cfg.ActiveEndpoint()

	m.remoteInputs[0] = newInput("", 60)
	m.remoteInputs[0].SetValue(active.BaseURL)
	m.remoteInputs[0].Placeholder = "https://api.openai.com/v1"

	m.remoteInputs[1] = newInput("", 60)
	m.remoteInputs[1].EchoMode = textinput.EchoPassword
	m.remoteInputs[1].SetValue(active.APIKey)
	m.remoteInputs[1].Placeholder = "sk-..."

	m.remoteInputs[2] = newInput("", 60)
	m.remoteInputs[2].SetValue(active.Model)
	m.remoteInputs[2].Placeholder = "gpt-4o"

	m.remoteFocus = 0
	return m
}

func (m SetupModel) saveLocalConfig() SetupModel {
	m.cfg.Runner.ModelPath = strings.TrimSpace(m.localInputs[0].Value())
	gpuLayers := 0
	fmt.Sscanf(m.localInputs[1].Value(), "%d", &gpuLayers)
	m.cfg.Runner.GPULayers = gpuLayers
	ctxSize := 16384
	fmt.Sscanf(m.localInputs[2].Value(), "%d", &ctxSize)
	m.cfg.Runner.ContextSize = ctxSize
	port := 8080
	fmt.Sscanf(m.localInputs[3].Value(), "%d", &port)
	m.cfg.Runner.Port = port

	// Update runner with new config.
	m.runner.Configure(m.cfg.Runner)
	return m
}

func (m SetupModel) saveRemoteConfig() SetupModel {
	baseURL := strings.TrimSpace(m.remoteInputs[0].Value())
	apiKey := strings.TrimSpace(m.remoteInputs[1].Value())
	model := strings.TrimSpace(m.remoteInputs[2].Value())

	// Upsert the "remote" endpoint.
	found := false
	for i, ep := range m.cfg.Endpoints {
		if ep.Name == "remote" {
			m.cfg.Endpoints[i].BaseURL = baseURL
			m.cfg.Endpoints[i].APIKey = apiKey
			m.cfg.Endpoints[i].Model = model
			found = true
			break
		}
	}
	if !found {
		m.cfg.Endpoints = append(m.cfg.Endpoints, config.EndpointConfig{
			Name:    "remote",
			BaseURL: baseURL,
			APIKey:  apiKey,
			Model:   model,
		})
	}
	m.cfg.Endpoint.Active = "remote"
	return m
}

func (m SetupModel) refocusLocal() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.localInputs {
		if i == m.localFocus {
			cmds = append(cmds, m.localInputs[i].Focus())
		} else {
			m.localInputs[i].Blur()
		}
	}
	return tea.Batch(cmds...)
}

func (m SetupModel) refocusRemote() tea.Cmd {
	var cmds []tea.Cmd
	for i := range m.remoteInputs {
		if i == m.remoteFocus {
			cmds = append(cmds, m.remoteInputs[i].Focus())
		} else {
			m.remoteInputs[i].Blur()
		}
	}
	return tea.Batch(cmds...)
}

// ── view ──────────────────────────────────────────────────────────────────

func (m SetupModel) View() tea.View {
	content := m.renderStep()
	v := tea.NewView(content)
	v.AltScreen = true
	return v
}

func (m SetupModel) renderStep() string {
	header := m.renderHeader()
	body := m.renderBody()

	full := lipgloss.JoinVertical(lipgloss.Left, header, body)

	return lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(full)
}

func (m SetupModel) renderHeader() string {
	logo := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Bold(true).
		Render("◆ LIVIE")
	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("setup")

	inner := m.width - 2
	gap := inner - lipgloss.Width(logo) - lipgloss.Width(label)
	if gap < 1 {
		gap = 1
	}
	row := lipgloss.NewStyle().Padding(0, 1).Render(
		logo + strings.Repeat(" ", gap) + label,
	)
	rule := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColBorder)).
		Render(strings.Repeat("─", m.width))

	return lipgloss.JoinVertical(lipgloss.Left, row, rule)
}

func (m SetupModel) renderBody() string {
	padStyle := lipgloss.NewStyle().
		Width(m.width).
		PaddingLeft(10).
		PaddingTop(2)

	switch m.step {
	case stepBoot:
		return padStyle.Render(m.renderBoot())
	case stepDetecting:
		return padStyle.Render(m.renderDetecting())
	case stepInstallPrompt:
		return padStyle.Render(m.renderInstallPrompt())
	case stepGPUSelect:
		return padStyle.Render(m.renderGPUSelect())
	case stepInstalling:
		return padStyle.Render(m.renderInstalling())
	case stepInstallError:
		return padStyle.Render(m.renderInstallError())
	case stepModeSelect:
		return padStyle.Render(m.renderModeSelect())
	case stepConfigLocal:
		return padStyle.Render(m.renderConfigLocal())
	case stepConfigRemote:
		return padStyle.Render(m.renderConfigRemote())
	case stepStartingRunner:
		return padStyle.Render(m.renderStartingRunner())
	case stepDone:
		return padStyle.Render(m.renderDone())
	}
	return ""
}

// ── step renderers ────────────────────────────────────────────────────────

func (m SetupModel) renderBoot() string {
	// Pulse the ◆ symbol between ColAccentCyan and ColSurfaceHi.
	var diamondCol string
	if m.spinnerFrame%2 == 0 {
		diamondCol = tui.ColAccentCyan
	} else {
		diamondCol = tui.ColSurfaceHi
	}
	diamond := lipgloss.NewStyle().
		Foreground(lipgloss.Color(diamondCol)).
		Render("◆")

	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Bold(true).
		Render("  L I V I E")

	tagline := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("a local AI assistant\nthat lives in your terminal.")

	return lipgloss.JoinVertical(lipgloss.Left,
		"",
		"",
		diamond+title,
		"",
		tagline,
	)
}

func (m SetupModel) renderDetecting() string {
	sp := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Render(spinnerFrames[m.spinnerFrame])

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Render("  Scanning for llama-server...")

	return sp + label
}

func (m SetupModel) renderInstallPrompt() string {
	cross := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentRose)).
		Render("✗")

	heading := cross + lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("  llama-server not found")

	body := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(`
Livie uses llama-server to run GGUF models locally.
It will be downloaded from the official llama.cpp
GitHub releases (~80–200 MB depending on platform).
`)

	choices := []string{"Install llama-server", "Skip — configure a remote endpoint instead"}
	menu := renderMenu(choices, m.modeChoice)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("enter to confirm · esc to skip")

	return lipgloss.JoinVertical(lipgloss.Left, heading, body, menu, "", hint)
}

func (m SetupModel) renderGPUSelect() string {
	heading := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("Choose a GPU backend")

	sub := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(`
This determines which llama-server binary is downloaded.
Select CPU if you are unsure or have no discrete GPU.
`)

	var rows []string
	detected := runner.DetectAvailable()
	detectedSet := make(map[runner.GPUBackend]bool)
	for _, b := range detected {
		detectedSet[b] = true
	}

	backends := m.availableBackends
	if len(backends) == 0 {
		backends = []runner.GPUBackend{runner.GPUBackendCPU}
	}

	for i, b := range backends {
		prefix := "  "
		if i == m.gpuChoice {
			prefix = lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentCyan)).
				Render("▶ ")
		}

		label := b.String()
		var detTag string
		if detectedSet[b] {
			detTag = "  " + lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentGreen)).
				Render("(detected ✓)")
		} else {
			detTag = "  " + lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColTextMuted)).
				Render("(not detected)")
		}

		row := prefix + lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextPrimary)).
			Render(label) + detTag
		rows = append(rows, row)
	}

	menu := strings.Join(rows, "\n")
	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("↑ ↓ to select · enter to download · esc to go back")

	return lipgloss.JoinVertical(lipgloss.Left, heading, sub, menu, "", hint)
}

func (m SetupModel) renderInstalling() string {
	var gpu runner.GPUBackend
	if len(m.availableBackends) > m.gpuChoice {
		gpu = m.availableBackends[m.gpuChoice]
	}
	platform := runner.New(gpu)

	arrow := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Render("↓")

	heading := arrow + lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render(fmt.Sprintf("  Downloading llama-server  (%s)", platform.Description()))

	bar := renderProgressBar(m.downloadedBytes, m.downloadTotal, 34)
	var pct string
	if m.downloadTotal > 0 {
		pct = fmt.Sprintf("  %d%%", int(float64(m.downloadedBytes)/float64(m.downloadTotal)*100))
	} else {
		pct = ""
	}

	byteInfo := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(fmt.Sprintf("%s / %s", formatBytes(m.downloadedBytes), formatBytes(m.downloadTotal)))

	parts := []string{heading, "", bar + pct, byteInfo}

	if m.downloadedBytes > 0 && m.downloadedBytes >= m.downloadTotal && m.downloadTotal > 0 {
		extractMsg := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextSecondary)).
			Render(fmt.Sprintf("→ Extracting to %s...", runner.DataDirBinDir()))
		parts = append(parts, "", extractMsg)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderInstallError() string {
	cross := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentRose)).
		Render("✗")

	heading := cross + lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("  Download failed")

	errText := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render("\n" + m.errMsg + "\n")

	choices := []string{"Retry", "Skip — configure a remote endpoint instead"}
	menu := renderMenu(choices, m.modeChoice)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("enter to confirm · esc to go back")

	return lipgloss.JoinVertical(lipgloss.Left, heading, errText, menu, "", hint)
}

func (m SetupModel) renderModeSelect() string {
	heading := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("How would you like to connect?")

	type option struct {
		radio string
		title string
		desc  string
	}
	options := []option{
		{
			title: "Local runner  (llama-server)",
			desc:  "Run GGUF models directly on this machine.\nBest for privacy, offline use, and uncensored models.",
		},
		{
			title: "Remote endpoint",
			desc:  "Connect to OpenAI, Groq, Ollama, LM Studio, or any\nOpenAI-compatible API.",
		},
	}

	var rows []string
	for i, o := range options {
		var radio string
		if i == m.modeChoice {
			radio = lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentCyan)).
				Render("●")
		} else {
			radio = lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColTextMuted)).
				Render("○")
		}

		titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary))
		if i == m.modeChoice {
			titleStyle = titleStyle.Bold(true)
		}

		title := radio + "  " + titleStyle.Render(o.title)
		desc := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextSecondary)).
			PaddingLeft(5).
			Render(o.desc)

		rows = append(rows, title, desc, "")
	}

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("↑ ↓ to select · enter to confirm")

	return lipgloss.JoinVertical(lipgloss.Left, heading, "", strings.Join(rows, "\n"), hint)
}

func (m SetupModel) renderConfigLocal() string {
	heading := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("Configure local runner")

	modelLabel := labelStyle().Render("Model file")
	modelField := styledInput(m.localInputs[0], m.localFocus == 0)

	gpuLabel := labelStyle().Render("GPU layers")
	ctxLabel := labelStyle().Render("Context size")
	portLabel := labelStyle().Render("Port")

	gpuField := styledInput(m.localInputs[1], m.localFocus == 1)
	ctxField := styledInput(m.localInputs[2], m.localFocus == 2)
	portField := styledInput(m.localInputs[3], m.localFocus == 3)

	numRow := lipgloss.JoinHorizontal(lipgloss.Top,
		gpuLabel+"\n"+gpuField+"   ",
		ctxLabel+"\n"+ctxField+"   ",
		portLabel+"\n"+portField,
	)

	hint1 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("GPU layers: -1 = offload all (recommended)")
	hint2 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("Model can be changed later with /model <path>")
	hint3 := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("tab · shift+tab to move between fields · enter to continue")

	errLine := ""
	if m.errMsg != "" {
		errLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColAccentRose)).
			Render("✗ " + m.errMsg)
	}

	parts := []string{heading, "", modelLabel, modelField, "", numRow, "", hint1, hint2, "", hint3}
	if errLine != "" {
		parts = append(parts, errLine)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderConfigRemote() string {
	heading := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("Configure remote endpoint")

	urlLabel := labelStyle().Render("Base URL")
	urlField := styledInput(m.remoteInputs[0], m.remoteFocus == 0)

	keyLabel := labelStyle().Render("API Key")
	keyField := styledInput(m.remoteInputs[1], m.remoteFocus == 1)

	modelLabel := labelStyle().Render("Model")
	modelField := styledInput(m.remoteInputs[2], m.remoteFocus == 2)

	hint := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("tab · shift+tab to move between fields · enter to continue")

	errLine := ""
	if m.errMsg != "" {
		errLine = lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColAccentRose)).
			Render("✗ " + m.errMsg)
	}

	parts := []string{heading, "", urlLabel, urlField, "", keyLabel, keyField, "", modelLabel, modelField, "", hint}
	if errLine != "" {
		parts = append(parts, errLine)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderStartingRunner() string {
	sp := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentAmber)).
		Render(spinnerFrames[m.spinnerFrame])

	label := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Render("  Starting llama-server...")

	modelName := filepath.Base(m.cfg.Runner.ModelPath)
	if modelName == "" || modelName == "." {
		modelName = "(no model)"
	}

	meta := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(fmt.Sprintf("%s\nContext: %s tokens · GPU layers: %s · Port: %d",
			modelName,
			formatInt(m.cfg.Runner.ContextSize),
			gpuLayersLabel(m.cfg.Runner.GPULayers),
			m.cfg.Runner.Port,
		))

	rule := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColBorder)).
		Render("── server log " + strings.Repeat("─", 36))

	logLines := m.runner.LogLines(4)
	logStr := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render(strings.Join(logLines, "\n"))

	parts := []string{sp + label, "", meta, "", rule, logStr}

	if m.errMsg != "" {
		warn := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColAccentRose)).
			Render("\n✗  " + m.errMsg + "\nCheck /run log for details, then try /run start.")
		parts = append(parts, warn)
	}

	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderDone() string {
	check := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentGreen)).
		Bold(true).
		Render("✓")

	heading := check + lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Bold(true).
		Render("  Ready")

	active := m.cfg.ActiveEndpoint()
	modelName := filepath.Base(m.cfg.Runner.ModelPath)
	if modelName == "" || modelName == "." {
		modelName = active.Model
	}

	kv := func(label, value string) string {
		l := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextSecondary)).
			Width(10).
			Align(lipgloss.Right).
			Render(label)
		v := lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColTextPrimary)).
			Bold(true).
			Render(value)
		return l + "  " + v
	}

	rows := []string{
		kv("Model", modelName),
		kv("Endpoint", active.BaseURL),
	}
	if m.cfg.Endpoint.Active == "local" {
		rows = append(rows, kv("Context", formatInt(m.cfg.Runner.ContextSize)+" tokens"))
	}

	return lipgloss.JoinVertical(lipgloss.Left,
		append([]string{heading, ""}, rows...)...,
	)
}

// ── tea.Cmd factories ─────────────────────────────────────────────────────

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg {
		return setupSpinnerTickMsg{}
	})
}

func detectCmd(cfg config.RunnerConfig) tea.Cmd {
	return func() tea.Msg {
		path, found := runner.Detect(cfg)
		return runner.DetectCompleteMsg{Found: found, Path: path}
	}
}

func detectAvailableCmd() tea.Cmd {
	return func() tea.Msg {
		return detectAvailableMsg{backends: runner.DetectAvailable()}
	}
}

// ── render helpers ────────────────────────────────────────────────────────

func renderMenu(choices []string, selected int) string {
	var rows []string
	for i, c := range choices {
		if i == selected {
			prefix := lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColAccentCyan)).
				Render("▶  ")
			text := lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColTextPrimary)).
				Render(c)
			rows = append(rows, prefix+text)
		} else {
			prefix := "   "
			text := lipgloss.NewStyle().
				Foreground(lipgloss.Color(tui.ColTextSecondary)).
				Render(c)
			rows = append(rows, prefix+text)
		}
	}
	return strings.Join(rows, "\n")
}

func renderProgressBar(downloaded, total int64, width int) string {
	var filled int
	if total > 0 {
		filled = int(float64(downloaded) / float64(total) * float64(width))
	}
	if filled > width {
		filled = width
	}
	empty := width - filled

	bar := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Render(strings.Repeat("█", filled))
	bar += lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColSurfaceHi)).
		Render(strings.Repeat("░", empty))
	return bar
}

func styledInput(ti textinput.Model, focused bool) string {
	borderCol := tui.ColBorder
	if focused {
		borderCol = tui.ColAccentCyan
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(borderCol)).
		Padding(0, 1).
		Render(ti.View())
}

func newInput(placeholder string, width int) textinput.Model {
	ti := textinput.New()
	ti.Placeholder = placeholder
	ti.SetWidth(width)
	return ti
}

func labelStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextSecondary))
}

func formatBytes(b int64) string {
	if b == 0 {
		return "—"
	}
	const mb = 1024 * 1024
	return fmt.Sprintf("%.1f MB", float64(b)/float64(mb))
}

func formatInt(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	return fmt.Sprintf("%d,%03d", n/1000, n%1000)
}

func gpuLayersLabel(n int) string {
	if n == -1 {
		return "all"
	}
	return fmt.Sprintf("%d", n)
}
