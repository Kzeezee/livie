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
	"strconv"
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

	// Detection result.
	detectedBinPath string

	// GPU selection (stepGPUSelect).
	availableBackends []runner.GPUBackend
	gpuChoice         int // index into availableBackends

	// Download progress (stepInstalling).
	// downloadCh is the only download field needed in the model; the context is
	// owned by the goroutine closure and does not need to be stored here.
	downloadCh      <-chan runner.ProgressUpdate
	downloadedBytes int64
	downloadTotal   int64
	downloadErr     error

	// stepInstallPrompt / stepInstallError choice: 0 = install/retry, 1 = skip.
	installChoice int

	// stepModeSelect choice: 0 = local, 1 = remote.
	modeChoice int

	// Local config form (stepConfigLocal).
	// Inputs: [0] model path, [1] gpu layers, [2] context size, [3] port.
	localInputs [4]textinput.Model
	localFocus  int

	// Remote config form (stepConfigRemote).
	// Inputs: [0] base URL, [1] api key, [2] model name.
	remoteInputs [3]textinput.Model
	remoteFocus  int

	// Spinner animation frame index.
	spinnerFrame int

	// Inline error message shown on config form steps.
	errMsg string
}

// NewSetupModel creates the setup wizard, pre-populated with existing config values.
func NewSetupModel(cfg *config.Config, mgr *runner.Manager, width, height int) SetupModel {
	return SetupModel{
		width:  width,
		height: height,
		cfg:    cfg,
		runner: mgr,
		step:   stepBoot,
	}
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
			return m, func() tea.Msg { return TransitionToChat{Config: m.cfg} }
		}

	case runner.DetectCompleteMsg:
		if msg.Found {
			m.detectedBinPath = msg.Path
			m.runner.SetBinaryPath(msg.Path)
			m = m.enterModeSelect(0)
		} else {
			m.step = stepInstallPrompt
			m.installChoice = 0
		}

	case detectAvailableMsg:
		m.availableBackends = msg.backends

	case runner.DownloadProgressMsg:
		m.downloadedBytes = msg.Downloaded
		m.downloadTotal = msg.Total
		if msg.Done {
			if msg.Err != nil {
				m.downloadErr = msg.Err
				m.errMsg = msg.Err.Error()
				m.installChoice = 0
				m.step = stepInstallError
			} else {
				m.runner.SetBinaryPath(msg.BinaryPath)
				m.detectedBinPath = msg.BinaryPath
				m = m.enterModeSelect(0)
			}
		} else {
			cmds = append(cmds, runner.DownloadProgressCmd(m.downloadCh))
		}

	case runner.RunnerStartedMsg:
		if msg.Err != nil {
			m.errMsg = msg.Err.Error()
		}
		m.step = stepDone
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

	// Forward messages to whichever input set is active.
	m, inputCmds := m.updateInputs(msg)
	cmds = append(cmds, inputCmds...)

	return m, tea.Batch(cmds...)
}

func (m SetupModel) handleKey(msg tea.KeyPressMsg) (SetupModel, tea.Cmd) {
	k := msg.String()

	switch m.step {

	// ── stepInstallPrompt ─────────────────────────────────────────────────
	case stepInstallPrompt:
		switch k {
		case "up", "k":
			if m.installChoice > 0 {
				m.installChoice--
			}
		case "down", "j":
			if m.installChoice < 1 {
				m.installChoice++
			}
		case "enter":
			if m.installChoice == 0 {
				return m.enterGPUSelect()
			}
			return m.enterModeSelect(1), nil
		case "esc":
			return m.enterModeSelect(1), nil
		}

	// ── stepGPUSelect ─────────────────────────────────────────────────────
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
			return m.startDownload()
		case "esc":
			m.step = stepInstallPrompt
			m.installChoice = 0
		}

	// ── stepInstallError ─────────────────────────────────────────────────
	case stepInstallError:
		switch k {
		case "up", "k":
			if m.installChoice > 0 {
				m.installChoice--
			}
		case "down", "j":
			if m.installChoice < 1 {
				m.installChoice++
			}
		case "enter":
			if m.installChoice == 0 {
				return m.startDownload()
			}
			return m.enterModeSelect(1), nil
		case "esc":
			m.step = stepGPUSelect
		}

	// ── stepModeSelect ────────────────────────────────────────────────────
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
				return m.refocusLocal()
			}
			m.step = stepConfigRemote
			m = m.initRemoteInputs()
			return m.refocusRemote()
		}

	// ── stepConfigLocal ───────────────────────────────────────────────────
	case stepConfigLocal:
		switch k {
		case "tab":
			m.localFocus = (m.localFocus + 1) % 4
			return m.refocusLocal()
		case "shift+tab":
			m.localFocus = (m.localFocus + 3) % 4
			return m.refocusLocal()
		case "enter":
			if m.localFocus < 3 {
				m.localFocus++
				return m.refocusLocal()
			}
			// Last field — validate, save, and start the runner.
			var ok bool
			m, ok = m.validateAndSaveLocalConfig()
			if !ok {
				return m, nil
			}
			m.step = stepStartingRunner
			return m, m.runner.StartAndPollCmd(30 * time.Second)
		case "esc":
			return m.enterModeSelect(0), nil
		}

	// ── stepConfigRemote ──────────────────────────────────────────────────
	case stepConfigRemote:
		switch k {
		case "tab":
			m.remoteFocus = (m.remoteFocus + 1) % 3
			return m.refocusRemote()
		case "shift+tab":
			m.remoteFocus = (m.remoteFocus + 2) % 3
			return m.refocusRemote()
		case "enter":
			if m.remoteFocus < 2 {
				m.remoteFocus++
				return m.refocusRemote()
			}
			// Last field — validate and proceed (no runner to start).
			if strings.TrimSpace(m.remoteInputs[0].Value()) == "" {
				m.errMsg = "Base URL is required"
				return m, nil
			}
			m.errMsg = ""
			m = m.saveRemoteConfig()
			m.step = stepDone
			return m, tea.Tick(800*time.Millisecond, func(time.Time) tea.Msg {
				return setupAdvanceMsg{}
			})
		case "esc":
			return m.enterModeSelect(0), nil
		}
	}

	return m, nil
}

// updateInputs forwards messages to whichever text input set is currently active.
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

// enterModeSelect transitions to stepModeSelect, resetting modeChoice to
// defaultChoice. No cmd is needed for this step.
func (m SetupModel) enterModeSelect(defaultChoice int) SetupModel {
	m.step = stepModeSelect
	m.modeChoice = defaultChoice
	return m
}

// enterGPUSelect transitions to stepGPUSelect and fires the backend detection cmd.
func (m SetupModel) enterGPUSelect() (SetupModel, tea.Cmd) {
	m.step = stepGPUSelect
	m.gpuChoice = 0
	return m, detectAvailableCmd()
}

// startDownload transitions to stepInstalling, resets progress counters, and
// fires the download pipeline. The context lives only inside the goroutine
// closure — it does not need to be stored on the model.
func (m SetupModel) startDownload() (SetupModel, tea.Cmd) {
	var gpu runner.GPUBackend
	if len(m.availableBackends) > m.gpuChoice {
		gpu = m.availableBackends[m.gpuChoice]
	}

	m.downloadedBytes = 0
	m.downloadTotal = 0
	m.downloadErr = nil
	m.errMsg = ""
	m.step = stepInstalling

	ch := runner.StartDownload(context.Background(), runner.New(gpu), runner.DataDirBinDir())
	m.downloadCh = ch
	return m, runner.DownloadProgressCmd(ch)
}

// refocusLocal applies focus/blur to localInputs according to m.localFocus
// and returns the updated model alongside the Focus cmd for the active input.
func (m SetupModel) refocusLocal() (SetupModel, tea.Cmd) {
	var focusCmd tea.Cmd
	for i := range m.localInputs {
		if i == m.localFocus {
			focusCmd = m.localInputs[i].Focus()
		} else {
			m.localInputs[i].Blur()
		}
	}
	return m, focusCmd
}

// refocusRemote applies focus/blur to remoteInputs according to m.remoteFocus.
func (m SetupModel) refocusRemote() (SetupModel, tea.Cmd) {
	var focusCmd tea.Cmd
	for i := range m.remoteInputs {
		if i == m.remoteFocus {
			focusCmd = m.remoteInputs[i].Focus()
		} else {
			m.remoteInputs[i].Blur()
		}
	}
	return m, focusCmd
}

// ── form helpers ──────────────────────────────────────────────────────────

// initLocalInputs prepopulates the local config text inputs from m.cfg.
func (m SetupModel) initLocalInputs() SetupModel {
	cfg := m.cfg.Runner

	port := 8080
	if cfg.Port > 0 {
		port = cfg.Port
	}
	gpuLayers := -1
	if cfg.GPULayers != 0 {
		gpuLayers = cfg.GPULayers
	}
	ctxSize := 16384
	if cfg.ContextSize > 0 {
		ctxSize = cfg.ContextSize
	}

	m.localInputs[0] = newInput(60)
	m.localInputs[0].SetValue(cfg.ModelPath)
	m.localInputs[0].Placeholder = "/path/to/model.gguf"

	m.localInputs[1] = newInput(12)
	m.localInputs[1].SetValue(strconv.Itoa(gpuLayers))
	m.localInputs[1].Placeholder = "-1"

	m.localInputs[2] = newInput(14)
	m.localInputs[2].SetValue(strconv.Itoa(ctxSize))
	m.localInputs[2].Placeholder = "16384"

	m.localInputs[3] = newInput(8)
	m.localInputs[3].SetValue(strconv.Itoa(port))
	m.localInputs[3].Placeholder = "8080"

	m.localFocus = 0
	return m
}

// initRemoteInputs prepopulates the remote config text inputs from m.cfg.
func (m SetupModel) initRemoteInputs() SetupModel {
	active := m.cfg.ActiveEndpoint()

	m.remoteInputs[0] = newInput(60)
	m.remoteInputs[0].SetValue(active.BaseURL)
	m.remoteInputs[0].Placeholder = "https://api.openai.com/v1"

	m.remoteInputs[1] = newInput(60)
	m.remoteInputs[1].EchoMode = textinput.EchoPassword
	m.remoteInputs[1].EchoCharacter = '•'
	m.remoteInputs[1].SetValue(active.APIKey)
	m.remoteInputs[1].Placeholder = "sk-..."

	m.remoteInputs[2] = newInput(60)
	m.remoteInputs[2].SetValue(active.Model)
	m.remoteInputs[2].Placeholder = "gpt-4o"

	m.remoteFocus = 0
	return m
}

// validateAndSaveLocalConfig validates the local config form, writes valid values
// into m.cfg, and returns (updatedModel, true) on success or (model with errMsg, false).
func (m SetupModel) validateAndSaveLocalConfig() (SetupModel, bool) {
	modelPath := strings.TrimSpace(m.localInputs[0].Value())
	if modelPath == "" {
		m.errMsg = "Model file path is required"
		return m, false
	}

	gpuLayers, err := strconv.Atoi(strings.TrimSpace(m.localInputs[1].Value()))
	if err != nil {
		m.errMsg = "GPU layers must be an integer (-1 = offload all)"
		return m, false
	}

	ctxSize, err := strconv.Atoi(strings.TrimSpace(m.localInputs[2].Value()))
	if err != nil || ctxSize < 512 {
		m.errMsg = "Context size must be an integer ≥ 512"
		return m, false
	}

	port, err := strconv.Atoi(strings.TrimSpace(m.localInputs[3].Value()))
	if err != nil || port < 1 || port > 65535 {
		m.errMsg = "Port must be an integer between 1 and 65535"
		return m, false
	}

	m.errMsg = ""
	m.cfg.Runner.ModelPath = modelPath
	m.cfg.Runner.GPULayers = gpuLayers
	m.cfg.Runner.ContextSize = ctxSize
	m.cfg.Runner.Port = port
	m.runner.Configure(m.cfg.Runner)
	return m, true
}

// saveRemoteConfig writes the remote config form values into m.cfg,
// upserting the "remote" endpoint entry.
func (m SetupModel) saveRemoteConfig() SetupModel {
	baseURL := strings.TrimSpace(m.remoteInputs[0].Value())
	apiKey := strings.TrimSpace(m.remoteInputs[1].Value())
	model := strings.TrimSpace(m.remoteInputs[2].Value())

	for i, ep := range m.cfg.Endpoints {
		if ep.Name == "remote" {
			m.cfg.Endpoints[i].BaseURL = baseURL
			m.cfg.Endpoints[i].APIKey = apiKey
			m.cfg.Endpoints[i].Model = model
			m.cfg.Endpoint.Active = "remote"
			return m
		}
	}
	m.cfg.Endpoints = append(m.cfg.Endpoints, config.EndpointConfig{
		Name: "remote", BaseURL: baseURL, APIKey: apiKey, Model: model,
	})
	m.cfg.Endpoint.Active = "remote"
	return m
}

// ── view ──────────────────────────────────────────────────────────────────

func (m SetupModel) View() tea.View {
	content := lipgloss.NewStyle().
		Width(m.width).
		Height(m.height).
		Render(lipgloss.JoinVertical(lipgloss.Left, m.renderHeader(), m.renderBody()))
	v := tea.NewView(content)
	v.AltScreen = true
	return v
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
	padStyle := lipgloss.NewStyle().Width(m.width).PaddingLeft(10).PaddingTop(2)
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
	// Pulse ◆ between ColAccentCyan and ColSurfaceHi.
	diamondCol := tui.ColSurfaceHi
	if m.spinnerFrame%2 == 0 {
		diamondCol = tui.ColAccentCyan
	}
	diamond := lipgloss.NewStyle().Foreground(lipgloss.Color(diamondCol)).Render("◆")
	title := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColAccentCyan)).Bold(true).
		Render("  L I V I E")
	tagline := lipgloss.NewStyle().
		Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("a local AI assistant\nthat lives in your terminal.")
	return lipgloss.JoinVertical(lipgloss.Left, "", "", diamond+title, "", tagline)
}

func (m SetupModel) renderDetecting() string {
	sp := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Render(spinnerFrames[m.spinnerFrame])
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Render("  Scanning for llama-server...")
	return sp + label
}

func (m SetupModel) renderInstallPrompt() string {
	cross := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentRose)).Render("✗")
	heading := cross + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("  llama-server not found")
	body := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).Render(`
Livie uses llama-server to run GGUF models locally.
It will be downloaded from the official llama.cpp
GitHub releases (~80–200 MB depending on platform).
`)
	choices := []string{"Install llama-server", "Skip — configure a remote endpoint instead"}
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("enter to confirm · esc to skip")
	return lipgloss.JoinVertical(lipgloss.Left,
		heading, body, renderMenu(choices, m.installChoice), "", hint)
}

func (m SetupModel) renderGPUSelect() string {
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("Choose a GPU backend")
	sub := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).Render(`
This determines which llama-server binary is downloaded.
Select CPU if you are unsure or have no discrete GPU.
`)

	// Use the already-populated availableBackends as the detected set —
	// no additional I/O on every render frame.
	detectedSet := make(map[runner.GPUBackend]bool, len(m.availableBackends))
	for _, b := range m.availableBackends {
		detectedSet[b] = true
	}

	backends := m.availableBackends
	if len(backends) == 0 {
		backends = []runner.GPUBackend{runner.GPUBackendCPU}
	}

	var rows []string
	for i, b := range backends {
		var prefix string
		if i == m.gpuChoice {
			prefix = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).Render("▶ ")
		} else {
			prefix = "  "
		}

		var detTag string
		if detectedSet[b] {
			detTag = "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentGreen)).
				Render("(detected ✓)")
		} else {
			detTag = "  " + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
				Render("(not detected)")
		}
		rows = append(rows, prefix+
			lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Render(b.String())+
			detTag)
	}

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("↑ ↓ to select · enter to download · esc to go back")
	return lipgloss.JoinVertical(lipgloss.Left, heading, sub, strings.Join(rows, "\n"), "", hint)
}

func (m SetupModel) renderInstalling() string {
	var gpu runner.GPUBackend
	if len(m.availableBackends) > m.gpuChoice {
		gpu = m.availableBackends[m.gpuChoice]
	}
	platform := runner.New(gpu)

	arrow := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).Render("↓")
	heading := arrow + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render(fmt.Sprintf("  Downloading llama-server  (%s)", platform.Description()))

	bar := renderProgressBar(m.downloadedBytes, m.downloadTotal, 34)
	pct := ""
	if m.downloadTotal > 0 {
		pct = fmt.Sprintf("  %d%%", int(float64(m.downloadedBytes)/float64(m.downloadTotal)*100))
	}
	byteInfo := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(fmt.Sprintf("%s / %s", formatBytes(m.downloadedBytes), formatBytes(m.downloadTotal)))

	parts := []string{heading, "", bar + pct, byteInfo}
	if m.downloadTotal > 0 && m.downloadedBytes >= m.downloadTotal {
		extractMsg := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
			Render(fmt.Sprintf("→ Extracting to %s...", runner.DataDirBinDir()))
		parts = append(parts, "", extractMsg)
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderInstallError() string {
	cross := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentRose)).Render("✗")
	heading := cross + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("  Download failed")
	errText := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render("\n" + m.errMsg + "\n")
	choices := []string{"Retry", "Skip — configure a remote endpoint instead"}
	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("enter to confirm · esc to go back")
	return lipgloss.JoinVertical(lipgloss.Left,
		heading, errText, renderMenu(choices, m.installChoice), "", hint, "",
		renderManualInstallNote())
}

// renderManualInstallNote renders a compact tip shown on the error screen,
// telling the user where to grab a binary and where to drop it.
func renderManualInstallNote() string {
	muted := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).Render(s)
	}
	accent := func(s string) string {
		return lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).Render(s)
	}

	labelW := 14
	urlLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Width(labelW).Render("download from")
	pathLabel := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Width(labelW).Render("drop binary at")

	urlVal := accent("github.com/ggerganov/llama.cpp/releases")
	pathVal := accent(runner.DataDirBinaryPath())

	rule := muted(strings.Repeat("┄", 52))
	title := muted("or install manually")

	return lipgloss.JoinVertical(lipgloss.Left,
		lipgloss.JoinHorizontal(lipgloss.Top, rule, "  ", title),
		urlLabel+"  "+urlVal,
		pathLabel+"  "+pathVal,
	)
}

func (m SetupModel) renderModeSelect() string {
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("How would you like to connect?")

	type option struct{ title, desc string }
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
			radio = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).Render("●")
		} else {
			radio = lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).Render("○")
		}
		titleStyle := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary))
		if i == m.modeChoice {
			titleStyle = titleStyle.Bold(true)
		}
		desc := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
			PaddingLeft(5).Render(o.desc)
		rows = append(rows, radio+"  "+titleStyle.Render(o.title), desc, "")
	}

	hint := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render("↑ ↓ to select · enter to confirm")
	return lipgloss.JoinVertical(lipgloss.Left, heading, "", strings.Join(rows, "\n"), hint)
}

func (m SetupModel) renderConfigLocal() string {
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("Configure local runner")

	numRow := lipgloss.JoinHorizontal(lipgloss.Top,
		labelStyle().Render("GPU layers")+"\n"+styledInput(m.localInputs[1], m.localFocus == 1)+"   ",
		labelStyle().Render("Context size")+"\n"+styledInput(m.localInputs[2], m.localFocus == 2)+"   ",
		labelStyle().Render("Port")+"\n"+styledInput(m.localInputs[3], m.localFocus == 3),
	)

	hint1 := mutedText("GPU layers: -1 = offload all (recommended)")
	hint2 := mutedText("Model can be changed later with /model <path>")
	hint3 := mutedText("tab · shift+tab to move between fields · enter to continue")

	parts := []string{
		heading, "",
		labelStyle().Render("Model file"), styledInput(m.localInputs[0], m.localFocus == 0),
		"", numRow, "", hint1, hint2, "", hint3,
	}
	if m.errMsg != "" {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColAccentRose)).Render("✗ "+m.errMsg))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderConfigRemote() string {
	heading := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("Configure remote endpoint")

	hint := mutedText("tab · shift+tab to move between fields · enter to continue")

	parts := []string{
		heading, "",
		labelStyle().Render("Base URL"), styledInput(m.remoteInputs[0], m.remoteFocus == 0), "",
		labelStyle().Render("API Key"), styledInput(m.remoteInputs[1], m.remoteFocus == 1), "",
		labelStyle().Render("Model"), styledInput(m.remoteInputs[2], m.remoteFocus == 2), "",
		hint,
	}
	if m.errMsg != "" {
		parts = append(parts, lipgloss.NewStyle().
			Foreground(lipgloss.Color(tui.ColAccentRose)).Render("✗ "+m.errMsg))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderStartingRunner() string {
	sp := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentAmber)).
		Render(spinnerFrames[m.spinnerFrame])
	label := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).
		Render("  Starting llama-server...")

	modelName := filepath.Base(m.cfg.Runner.ModelPath)
	if modelName == "" || modelName == "." {
		modelName = "(no model)"
	}
	meta := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
		Render(fmt.Sprintf("%s\nContext: %s tokens · GPU layers: %s · Port: %d",
			modelName,
			formatInt(m.cfg.Runner.ContextSize),
			gpuLayersLabel(m.cfg.Runner.GPULayers),
			m.cfg.Runner.Port,
		))

	rule := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColBorder)).
		Render("── server log " + strings.Repeat("─", 36))
	logStr := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).
		Render(strings.Join(m.runner.LogLines(4), "\n"))

	parts := []string{sp + label, "", meta, "", rule, logStr}
	if m.errMsg != "" {
		parts = append(parts,
			lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentRose)).
				Render("\n✗  "+m.errMsg+"\nCheck /run log for details, then try /run start."))
	}
	return lipgloss.JoinVertical(lipgloss.Left, parts...)
}

func (m SetupModel) renderDone() string {
	check := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentGreen)).Bold(true).Render("✓")
	heading := check + lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).
		Render("  Ready")

	active := m.cfg.ActiveEndpoint()
	modelName := filepath.Base(m.cfg.Runner.ModelPath)
	if modelName == "" || modelName == "." {
		modelName = active.Model
	}

	kv := func(label, value string) string {
		l := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).
			Width(10).Align(lipgloss.Right).Render(label)
		v := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Bold(true).Render(value)
		return l + "  " + v
	}

	rows := []string{kv("Model", modelName), kv("Endpoint", active.BaseURL)}
	if m.cfg.Endpoint.Active == "local" {
		rows = append(rows, kv("Context", formatInt(m.cfg.Runner.ContextSize)+" tokens"))
	}
	return lipgloss.JoinVertical(lipgloss.Left, append([]string{heading, ""}, rows...)...)
}

// ── tea.Cmd factories ─────────────────────────────────────────────────────

func spinnerTickCmd() tea.Cmd {
	return tea.Tick(80*time.Millisecond, func(time.Time) tea.Msg { return setupSpinnerTickMsg{} })
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
			prefix := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).Render("▶  ")
			rows = append(rows, prefix+lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextPrimary)).Render(c))
		} else {
			rows = append(rows, "   "+lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary)).Render(c))
		}
	}
	return strings.Join(rows, "\n")
}

func renderProgressBar(downloaded, total int64, width int) string {
	var filled int
	if total > 0 {
		filled = int(float64(downloaded) / float64(total) * float64(width))
		if filled > width {
			filled = width
		}
	}
	bar := lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColAccentCyan)).
		Render(strings.Repeat("█", filled))
	bar += lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColSurfaceHi)).
		Render(strings.Repeat("░", width-filled))
	return bar
}

func styledInput(ti textinput.Model, focused bool) string {
	col := tui.ColBorder
	if focused {
		col = tui.ColAccentCyan
	}
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color(col)).
		Padding(0, 1).
		Render(ti.View())
}

// newInput creates a textinput.Model with the given display width.
// The caller is responsible for setting Placeholder and initial value.
func newInput(width int) textinput.Model {
	ti := textinput.New()
	ti.SetWidth(width)
	return ti
}

func labelStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextSecondary))
}

func mutedText(s string) string {
	return lipgloss.NewStyle().Foreground(lipgloss.Color(tui.ColTextMuted)).Render(s)
}

func formatBytes(b int64) string {
	if b == 0 {
		return "—"
	}
	return fmt.Sprintf("%.1f MB", float64(b)/(1024*1024))
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
	return strconv.Itoa(n)
}
