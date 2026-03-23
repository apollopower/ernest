package tui

import (
	"context"
	"ernest/internal/agent"
	"ernest/internal/config"
	"ernest/internal/provider"
	"ernest/internal/session"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentEventMsg wraps an agent event for the BubbleTea update loop.
type AgentEventMsg struct {
	Event agent.AgentEvent
}

// StreamDoneMsg signals the agent event channel has closed.
type StreamDoneMsg struct{}

// CompactionDoneMsg signals compaction completed.
type CompactionDoneMsg struct {
	Before int
	After  int
	Err    error
}

// MCPReconnectDoneMsg signals MCP reconnect completed.
type MCPReconnectDoneMsg struct {
	Results []mcpReconnectResult
}

type mcpReconnectResult struct {
	Name string
	Err  error
}

type AppModel struct {
	chat           ChatModel
	input          InputModel
	statusBar      StatusModel
	agent          *agent.Agent
	session        *session.Session
	cfg            config.Config
	creds          *config.Credentials
	confirmDialog  *ToolConfirmModel
	picker           *PickerModel
	pickerAction     string // "switch_provider" or "resume_session" — what to do with the result
	planSaveFilename string // non-empty when /plan save is streaming a consolidation
	focused        bool   // true = input focused, false = vim nav mode
	streaming      bool   // true while agent is streaming a response
	confirming     bool   // true while tool confirmation dialog is visible
	compacting     bool   // true while context compaction is running
	reconnecting   bool   // true while MCP reconnect is running
	cancelReconnect context.CancelFunc
	initialized    bool   // true after first WindowSizeMsg (auto-resume check)
	pendingG       bool   // waiting for second 'g' in "gg" sequence
	width          int
	height         int
	pendingCmd     string // for ":" command accumulation
	cmdMode        bool   // in ":" command mode
	cancelStream   context.CancelFunc
	agentCh        <-chan agent.AgentEvent
}

func NewAppModel(cfg config.Config, claudeCfg *config.ClaudeConfig, a *agent.Agent, sess *session.Session, creds *config.Credentials) AppModel {
	primary := cfg.PrimaryProvider()

	return AppModel{
		chat:      NewChatModel(),
		input:     NewInputModel(),
		statusBar: NewStatusModel(primary.Name, primary.Model, cfg.MaxContextTokens),
		agent:     a,
		session:   sess,
		cfg:       cfg,
		creds:     creds,
		focused:   true, // start with input focused
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.input.Init()
}

// waitForAgentEvent returns a tea.Cmd that performs a single blocking read
// from the agent event channel. Each read returns one message and the TUI
// schedules the next read after processing it. This avoids blocking the
// BubbleTea update loop.
func waitForAgentEvent(ch <-chan agent.AgentEvent) tea.Cmd {
	if ch == nil {
		return func() tea.Msg { return StreamDoneMsg{} }
	}
	return func() tea.Msg {
		event, ok := <-ch
		if !ok {
			return StreamDoneMsg{}
		}
		return AgentEventMsg{Event: event}
	}
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		if m.confirmDialog != nil {
			m.confirmDialog.width = msg.Width
		}
		// Check for auto-resume on first render
		if !m.initialized {
			m.initialized = true
			m.checkAutoResume()
		}
		return m, nil

	case SubmitMsg:
		if m.streaming || m.confirming || m.compacting {
			return m, nil // ignore input while streaming, confirming, or compacting
		}

		// Slash command detection: /command args
		// Only treat as command if the name matches a known command,
		// so messages like "/tmp/foo" pass through to the agent.
		if strings.HasPrefix(msg.Text, "/") {
			parts := strings.SplitN(msg.Text[1:], " ", 2)
			name := parts[0]
			if isKnownCommand(name) {
				args := ""
				if len(parts) > 1 {
					args = parts[1]
				}
				return m.executeCmd(name, args)
			}
		}

		if m.agent == nil {
			m.chat.AddMessage("user", msg.Text)
			m.chat.AddMessage("assistant", "[error: no provider configured]")
			return m, nil
		}
		m.chat.AddMessage("user", msg.Text)

		// Create a fresh context for this streaming turn
		ctx, cancel := context.WithCancel(context.Background())
		m.cancelStream = cancel
		m.streaming = true
		m.agentCh = m.agent.Run(ctx, msg.Text)
		dotCmd := m.chat.StartStreamingMessage()

		return m, tea.Batch(waitForAgentEvent(m.agentCh), dotCmd)

	case AgentEventMsg:
		return m.handleAgentEvent(msg.Event)

	case PickerResult:
		return m.handlePickerResult(msg)

	case PickerCancelMsg:
		m.picker = nil
		m.pickerAction = ""
		return m, nil

	case CompactionDoneMsg:
		m.compacting = false
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}
		if msg.Err != nil {
			m.chat.AddSystemMessage("Compaction failed: " + msg.Err.Error())
		} else if msg.Before == msg.After {
			m.chat.AddSystemMessage("Nothing to compact.")
		} else {
			m.chat.AddSystemMessage(fmt.Sprintf("Compacted: %d → %d tokens", msg.Before, msg.After))
			// Use EstimateCurrentTokens for consistent display (includes system prompt)
			if m.agent != nil {
				tokens := m.agent.EstimateCurrentTokens()
				m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Tokens: tokens})
			}
		}
		return m, nil

	case MCPReconnectDoneMsg:
		m.reconnecting = false
		if m.cancelReconnect != nil {
			m.cancelReconnect()
			m.cancelReconnect = nil
		}
		succeeded, failed := 0, 0
		for _, r := range msg.Results {
			if r.Err != nil {
				failed++
				m.chat.AddSystemMessage(fmt.Sprintf("Reconnect %s failed: %v", r.Name, r.Err))
			} else {
				succeeded++
			}
		}
		if succeeded > 0 {
			m.chat.AddSystemMessage(fmt.Sprintf("Reconnected %d server(s).", succeeded))
		} else if failed == 0 {
			m.chat.AddSystemMessage("All servers already connected.")
		}
		return m, nil

	case ToolApproveMsg:
		m.confirming = false
		m.confirmDialog = nil
		m.agent.ResolveTool(msg.ToolUseID, true)
		return m, waitForAgentEvent(m.agentCh)

	case ToolDenyMsg:
		m.confirming = false
		m.confirmDialog = nil
		m.agent.ResolveTool(msg.ToolUseID, false)
		return m, waitForAgentEvent(m.agentCh)

	case ToolAlwaysMsg:
		m.confirming = false
		m.confirmDialog = nil
		if err := m.agent.AllowToolAlways(msg.ToolUseID, msg.ToolName, msg.ToolInput); err != nil {
			log.Printf("[tui] warning: failed to save tool permission: %v", err)
		}
		return m, waitForAgentEvent(m.agentCh)

	case dotTickMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		return m, cmd

	case StreamDoneMsg:
		m.chat.FinalizeMessage()
		m.streaming = false
		m.confirming = false
		m.confirmDialog = nil
		if m.planSaveFilename != "" {
			m.chat.AddSystemMessage("Plan save cancelled.")
			m.planSaveFilename = ""
		}
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}
		return m, nil

	case tea.KeyMsg:
		// Ctrl+C always takes priority — even during confirmation dialog
		if msg.Type == tea.KeyCtrlC {
			if m.reconnecting {
				if m.cancelReconnect != nil {
					m.cancelReconnect()
					m.cancelReconnect = nil
				}
				m.reconnecting = false
				m.chat.AddSystemMessage("Reconnect cancelled.")
				return m, nil
			}
			if m.streaming || m.compacting {
				if m.cancelStream != nil {
					m.cancelStream()
					m.cancelStream = nil
				}
				// Drain remaining events to prevent agent goroutine leak
				if m.agentCh != nil {
					ch := m.agentCh
					go func() { for range ch {} }()
					m.agentCh = nil
				}
				m.chat.FinalizeMessage()
				m.streaming = false
				m.compacting = false
				m.confirming = false
				m.confirmDialog = nil
				m.planSaveFilename = ""
				return m, nil
			}
			return m, tea.Quit
		}

		// Shift+Tab cycles between plan and build modes
		if msg.Type == tea.KeyShiftTab {
			if m.agent != nil && !m.streaming && !m.confirming && !m.compacting {
				if m.agent.Mode() == agent.ModePlan {
					m.agent.SetMode(agent.ModeBuild)
					m.agent.InjectModeChange(agent.ModeBuild)
					m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "build"})
					m.chat.AddSystemMessage("Entered build mode. All tools available.")
				} else {
					m.agent.SetMode(agent.ModePlan)
					m.agent.InjectModeChange(agent.ModePlan)
					m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "plan"})
					m.chat.AddSystemMessage("Entered plan mode. Read-only tools only.")
				}
			}
			return m, nil
		}

		if msg.Type == tea.KeyEsc {
			// Dismiss picker
			if m.picker != nil {
				m.picker = nil
				m.pickerAction = ""
				return m, nil
			}
			// During confirmation, Esc is a no-op — don't leak into focus management
			if m.confirming {
				return m, nil
			}
			if m.cmdMode {
				m.cmdMode = false
				m.pendingCmd = ""
				return m, nil
			}
			if m.focused {
				m.focused = false
				m.input.Blur()
				return m, nil
			}
			return m, nil
		}

		// Confirmation dialog captures remaining keys (after Ctrl+C/Esc)
		if m.confirming && m.confirmDialog != nil {
			var cmd tea.Cmd
			dialog := *m.confirmDialog
			dialog, cmd = dialog.Update(msg)
			m.confirmDialog = &dialog
			return m, cmd
		}

		// Picker captures keys when active
		if m.picker != nil {
			var cmd tea.Cmd
			picker := *m.picker
			picker, cmd = picker.Update(msg)
			m.picker = &picker
			return m, cmd
		}

		// Command mode (after pressing ":")
		if m.cmdMode {
			return m.handleCmdMode(msg)
		}

		// Input-focused mode: pass keys to input
		if m.focused {
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			return m, cmd
		}

		// Vim navigation mode
		return m.handleVimNav(msg)
	}

	return m, nil
}

func (m AppModel) handleAgentEvent(evt agent.AgentEvent) (tea.Model, tea.Cmd) {
	switch evt.Type {
	case "text":
		m.chat.AppendToMessage(evt.Text)
		return m, waitForAgentEvent(m.agentCh)

	case "provider_switch":
		m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Provider: evt.ProviderName})
		return m, waitForAgentEvent(m.agentCh)

	case "usage":
		// Token count is updated from the estimate in the "done" handler,
		// not from per-turn API usage, to show context window usage consistently.
		return m, waitForAgentEvent(m.agentCh)

	case "tool_call":
		m.chat.FinalizeOrRemoveEmpty()
		m.chat.AddToolCall(evt.ToolName, evt.ToolInput)
		return m, waitForAgentEvent(m.agentCh)

	case "tool_confirm":
		// Show confirmation dialog — agent loop is blocked waiting for ResolveTool
		dialog := NewToolConfirmModel(evt.ToolName, evt.ToolInput, evt.ToolUseID, m.width)
		m.confirmDialog = &dialog
		m.confirming = true
		// Don't read next agent event — the agent is blocked on confirmCh.
		// The next read happens after ToolApproveMsg or ToolDenyMsg.
		return m, nil

	case "tool_result":
		m.chat.AddToolResult(evt.ToolName, evt.ToolResult)
		dotCmd := m.chat.StartStreamingMessage()
		return m, tea.Batch(dotCmd, waitForAgentEvent(m.agentCh))

	case "error":
		errText := "Error"
		if evt.Error != nil {
			errText = evt.Error.Error()
		}
		m.chat.AppendToMessage("\n\n[error: " + errText + "]")
		m.chat.FinalizeMessage()
		m.streaming = false
		if m.planSaveFilename != "" {
			m.chat.AddSystemMessage("Plan save cancelled due to error.")
			m.planSaveFilename = ""
		}
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}
		return m, nil

	case "done":
		m.chat.FinalizeMessage()
		m.streaming = false
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}

		// Handle /plan save — write the consolidated plan to disk
		if m.planSaveFilename != "" {
			filename := m.planSaveFilename
			m.planSaveFilename = ""
			m.writePlanFile(filename)
		}

		// Update token estimate in status bar
		if m.agent != nil {
			tokens := m.agent.EstimateCurrentTokens()
			maxTokens := m.agent.MaxContextTokens()
			m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{
				Tokens:    tokens,
				MaxTokens: maxTokens,
			})
			// Auto-compact if needed (reuse computed tokens to avoid re-estimating)
			if maxTokens > 0 && tokens > (maxTokens*90/100) {
				m.compacting = true
				m.chat.AddSystemMessage("Auto-compacting conversation...")
				return m, m.runCompaction()
			}
		}
		return m, nil
	}

	// Unknown event type — keep reading
	return m, waitForAgentEvent(m.agentCh)
}

func (m AppModel) handleVimNav(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	key := msg.String()

	// Handle "gg" second keypress
	if m.pendingG {
		m.pendingG = false
		if key == "g" {
			m.chat.GotoTop()
			return m, nil
		}
		// Not "gg", ignore the pending g
	}

	switch key {
	case "j", "down":
		m.chat.ScrollDown()
	case "k", "up":
		m.chat.ScrollUp()
	case "g":
		m.pendingG = true
	case "G":
		m.chat.GotoBottom()
	case "i", "enter":
		m.focused = true
		return m, m.input.Focus()
	case ":":
		m.cmdMode = true
		m.pendingCmd = ""
	}

	return m, nil
}

func (m AppModel) handleCmdMode(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch msg.Type {
	case tea.KeyEnter:
		cmdStr := m.pendingCmd
		m.cmdMode = false
		m.pendingCmd = ""
		parts := strings.SplitN(cmdStr, " ", 2)
		name := parts[0]
		args := ""
		if len(parts) > 1 {
			args = parts[1]
		}
		return m.executeCmd(name, args)
	case tea.KeyBackspace:
		if len(m.pendingCmd) > 0 {
			m.pendingCmd = m.pendingCmd[:len(m.pendingCmd)-1]
		}
		if len(m.pendingCmd) == 0 {
			m.cmdMode = false
		}
	default:
		if msg.Type == tea.KeyRunes {
			m.pendingCmd += msg.String()
		}
	}
	return m, nil
}

func (m AppModel) executeCmd(name, args string) (tea.Model, tea.Cmd) {
	switch name {
	case "q", "quit":
		// Session is auto-saved in main.go on exit — no need to save here
		return m, tea.Quit

	case "status":
		primary := m.cfg.PrimaryProvider()
		sessionID := "none"
		if m.session != nil {
			sessionID = m.session.ID
		}
		status := fmt.Sprintf("Provider: %s | Model: %s | Max tokens: %d | Session: %s",
			primary.Name, primary.Model, m.cfg.MaxContextTokens, sessionID)
		m.chat.AddSystemMessage(status)
		return m, nil

	case "save":
		if err := m.saveSession(); err != nil {
			m.chat.AddSystemMessage("Error saving session: " + err.Error())
		} else {
			m.chat.AddSystemMessage("Session saved.")
		}
		return m, nil

	case "clear":
		// Save current session before clearing — abort if save fails
		if err := m.saveSession(); err != nil {
			m.chat.AddSystemMessage("Error saving session before clearing: " + err.Error())
			return m, nil
		}
		m.chat = NewChatModel()
		m.chat.SetSize(m.width, m.height-6) // reapply layout
		if m.session != nil {
			// Reset in place to preserve shared pointer with main.go auto-save
			*m.session = *session.New(m.session.ProjectDir)
		}
		if m.agent != nil {
			m.agent.ClearHistory()
		}
		m.chat.AddSystemMessage("Conversation cleared.")
		return m, nil

	case "compact":
		if m.agent == nil {
			m.chat.AddSystemMessage("No agent configured.")
			return m, nil
		}
		m.compacting = true
		m.chat.AddSystemMessage("Compacting conversation...")
		return m, m.runCompaction()

	case "resume":
		return m.handleResume(args)

	case "providers":
		return m.handleProviders()

	case "provider":
		return m.handleProvider(args)

	case "model":
		return m.handleModel(args)

	case "plan":
		return m.handlePlan(args)

	case "build":
		return m.handleBuild()

	case "mcp":
		return m.handleMCP(args)

	}

	m.chat.AddSystemMessage("Unknown command: " + name)
	return m, nil
}

// handleProviders lists all configured providers with status.
func (m AppModel) handleProviders() (tea.Model, tea.Cmd) {
	sorted := m.cfg.SortedProviders()
	if len(sorted) == 0 {
		m.chat.AddSystemMessage("No providers configured. Use /provider add <type> <key> to add one.")
		return m, nil
	}

	var lines []string
	lines = append(lines, "Providers:")
	for _, p := range sorted {
		status := "no key"
		if p.HasAPIKeyWithCredentials(m.creds) {
			status = "connected"
		} else if p.BaseURL != "" {
			status = "configured" // local provider, no key needed
		}
		model := p.Model
		if model == "" {
			model = "(default)"
		}
		line := fmt.Sprintf("  %d. %s (%s) — %s", p.Priority, p.Name, model, status)
		if p.BaseURL != "" {
			line += fmt.Sprintf(" [%s]", p.BaseURL)
		}
		lines = append(lines, line)
	}

	primary := m.cfg.PrimaryProvider()
	lines = append(lines, "")
	lines = append(lines, "Active: "+primary.Name)

	m.chat.AddSystemMessage(strings.Join(lines, "\n"))
	return m, nil
}

// handleProvider handles /provider add and /provider remove.
func (m AppModel) handleProvider(args string) (tea.Model, tea.Cmd) {
	parts := strings.SplitN(args, " ", 2)
	if len(parts) == 0 || parts[0] == "" {
		m.chat.AddSystemMessage("Usage: /provider add <type> <key> or /provider remove <name>")
		return m, nil
	}

	subCmd := parts[0]
	subArgs := ""
	if len(parts) > 1 {
		subArgs = parts[1]
	}

	switch subCmd {
	case "add":
		return m.handleProviderAdd(subArgs)
	case "remove":
		return m.handleProviderRemove(subArgs)
	default:
		m.chat.AddSystemMessage("Unknown provider command: " + subCmd + ". Use 'add' or 'remove'.")
		return m, nil
	}
}

// handleProviderAdd handles /provider add <type> <key> [--model X] [--base-url Y]
func (m AppModel) handleProviderAdd(args string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(args)
	if len(parts) < 2 {
		m.chat.AddSystemMessage("Usage: /provider add <type> <key> [--model <model>] [--base-url <url>]")
		return m, nil
	}

	providerName := strings.ToLower(parts[0])
	apiKey := parts[1]

	// Parse optional flags
	model := ""
	baseURL := ""
	for i := 2; i < len(parts); i++ {
		switch parts[i] {
		case "--model":
			if i+1 >= len(parts) {
				m.chat.AddSystemMessage("--model requires a value")
				return m, nil
			}
			model = parts[i+1]
			i++
		case "--base-url":
			if i+1 >= len(parts) {
				m.chat.AddSystemMessage("--base-url requires a value")
				return m, nil
			}
			baseURL = parts[i+1]
			i++
		default:
			m.chat.AddSystemMessage("Unknown flag: " + parts[i])
			return m, nil
		}
	}

	// Default models per provider
	if model == "" {
		switch providerName {
		case "anthropic":
			model = "claude-opus-4-6"
		case "openai":
			model = "gpt-4.1"
		case "gemini":
			model = "gemini-2.5-pro"
		case "siliconflow":
			model = "deepseek-ai/DeepSeek-R1"
			if baseURL == "" {
				baseURL = "https://api.siliconflow.com/v1"
			}
		default:
			model = "default"
		}
	}

	// Save config first (if this fails, no orphaned credentials)
	m.cfg.AddProvider(config.ProviderConfig{
		Name:    providerName,
		Model:   model,
		BaseURL: baseURL,
	})
	if err := config.SaveConfig(m.cfg); err != nil {
		m.chat.AddSystemMessage("Error saving config: " + err.Error())
		return m, nil
	}

	// Save credentials
	if m.creds == nil {
		m.creds = &config.Credentials{}
	}
	m.creds.SetKey(providerName, apiKey)
	if err := config.SaveCredentials(m.creds); err != nil {
		m.chat.AddSystemMessage("Error saving credentials: " + err.Error())
		return m, nil
	}

	// Rebuild router
	m.rebuildRouter()

	msg := fmt.Sprintf("Added provider: %s (model: %s)", providerName, model)
	if baseURL != "" {
		msg += fmt.Sprintf(" [%s]", baseURL)
	}
	m.chat.AddSystemMessage(msg)
	return m, nil
}

// handleProviderRemove handles /provider remove <name>
func (m AppModel) handleProviderRemove(args string) (tea.Model, tea.Cmd) {
	name := strings.TrimSpace(args)
	if name == "" {
		m.chat.AddSystemMessage("Usage: /provider remove <name>")
		return m, nil
	}

	m.cfg.RemoveProvider(name)
	if err := config.SaveConfig(m.cfg); err != nil {
		m.chat.AddSystemMessage("Error saving config: " + err.Error())
		return m, nil
	}

	if m.creds != nil {
		m.creds.Remove(name)
		if err := config.SaveCredentials(m.creds); err != nil {
			m.chat.AddSystemMessage("Warning: failed to save credentials: " + err.Error())
		}
	}

	m.rebuildRouter()
	m.chat.AddSystemMessage("Removed provider: " + name)
	return m, nil
}

// handleModel handles /model — opens a picker to switch active provider, or /model <provider> <model> to change model string.
func (m AppModel) handleModel(args string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(args)

	if len(parts) == 0 {
		// Open picker to switch active provider
		sorted := m.cfg.SortedProviders()
		if len(sorted) == 0 {
			m.chat.AddSystemMessage("No providers configured.")
			return m, nil
		}
		var items []PickerItem
		for _, p := range sorted {
			label := fmt.Sprintf("%s — %s", p.Name, p.Model)
			if p.BaseURL != "" {
				label += fmt.Sprintf(" [%s]", p.BaseURL)
			}
			items = append(items, PickerItem{
				ID:    p.Name,
				Label: label,
			})
		}
		picker := NewPickerModel("Switch active provider", items, m.width)
		m.picker = &picker
		m.pickerAction = "switch_provider"
		return m, nil
	}

	if len(parts) != 2 {
		m.chat.AddSystemMessage("Usage: /model (opens picker) or /model <provider> <model>")
		return m, nil
	}

	providerName := parts[0]
	modelName := parts[1]

	return m.applyModelChange(providerName, modelName)
}

func (m AppModel) applyModelChange(providerName, modelName string) (tea.Model, tea.Cmd) {
	if !m.cfg.SetModel(providerName, modelName) {
		m.chat.AddSystemMessage("Provider not found: " + providerName)
		return m, nil
	}

	if err := config.SaveConfig(m.cfg); err != nil {
		m.chat.AddSystemMessage("Error saving config: " + err.Error())
		return m, nil
	}

	m.rebuildRouter()
	m.chat.AddSystemMessage(fmt.Sprintf("Model for %s set to %s", providerName, modelName))
	return m, nil
}

// handlePickerResult processes the user's selection from a picker modal.
func (m AppModel) handlePickerResult(result PickerResult) (tea.Model, tea.Cmd) {
	m.picker = nil
	action := m.pickerAction
	m.pickerAction = ""

	switch action {
	case "switch_provider":
		m.makeProviderPrimary(result.ID)
		if err := config.SaveConfig(m.cfg); err != nil {
			m.chat.AddSystemMessage("Error saving config: " + err.Error())
			return m, nil
		}
		m.rebuildRouter()
		m.chat.AddSystemMessage(fmt.Sprintf("Switched to %s", result.ID))
		return m, nil

	case "resume_session":
		return m.loadSessionByID(result.ID)
	}

	return m, nil
}

// makeProviderPrimary sets the named provider to priority 1 and renumbers others.
func (m *AppModel) makeProviderPrimary(name string) {
	// Set selected to priority 0 (will sort first), then renumber sequentially
	for i, p := range m.cfg.Providers {
		if strings.EqualFold(p.Name, name) {
			m.cfg.Providers[i].Priority = 0
		}
	}
	sorted := m.cfg.SortedProviders()
	for i, p := range sorted {
		for j := range m.cfg.Providers {
			if m.cfg.Providers[j].Name == p.Name {
				m.cfg.Providers[j].Priority = i + 1
			}
		}
	}
}

// rebuildRouter creates a new router from the current config and credentials,
// then hot-swaps it on the agent and updates the status bar.
func (m *AppModel) rebuildRouter() {
	var providers []provider.Provider
	for _, pc := range m.cfg.SortedProviders() {
		apiKey := pc.ResolveAPIKeyWithCredentials(m.creds)
		if apiKey == "" && pc.BaseURL == "" {
			continue
		}
		name := strings.ToLower(pc.Name)
		switch {
		case name == "anthropic":
			providers = append(providers, provider.NewAnthropic(apiKey, pc.Model))
		default:
			providers = append(providers, provider.NewOpenAICompat(pc.Name, apiKey, pc.Model, pc.BaseURL))
		}
	}

	if len(providers) > 0 && m.agent != nil {
		cooldown := time.Duration(m.cfg.CooldownSeconds) * time.Second
		router := provider.NewRouter(providers, cooldown)
		m.agent.SetRouter(router)
	}

	// Update status bar with the active (priority 1) provider
	primary := m.cfg.PrimaryProvider()
	m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{
		Provider: primary.Name,
		Model:    primary.Model,
	})
}

// handlePlan enters plan mode or saves a plan.
func (m AppModel) handlePlan(args string) (tea.Model, tea.Cmd) {
	parts := strings.Fields(args)

	// /plan save <filename>
	if len(parts) >= 1 && parts[0] == "save" {
		if len(parts) < 2 {
			m.chat.AddSystemMessage("Usage: /plan save <filename>")
			return m, nil
		}
		return m.handlePlanSave(parts[1])
	}

	// /plan (enter plan mode)
	if m.agent == nil {
		m.chat.AddSystemMessage("No agent configured.")
		return m, nil
	}

	if m.agent.Mode() == agent.ModePlan {
		m.chat.AddSystemMessage("Already in plan mode.")
		return m, nil
	}

	m.agent.SetMode(agent.ModePlan)
	m.agent.InjectModeChange(agent.ModePlan)
	m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "plan"})
	m.chat.AddSystemMessage("Entered plan mode. Read-only tools only. Use /build to return.")
	return m, nil
}

// handlePlanSave consolidates and saves the plan to docs/plans/<filename>.md.
func (m AppModel) handlePlanSave(filename string) (tea.Model, tea.Cmd) {
	// Validate and sanitize filename
	filename = strings.TrimSpace(filename)
	if filename == "" {
		m.chat.AddSystemMessage("Invalid filename.")
		return m, nil
	}
	filename = filepath.Base(filename) // prevent path traversal
	if filename == "." || filename == ".." {
		m.chat.AddSystemMessage("Invalid filename.")
		return m, nil
	}
	// Strip .md suffix if user included it (we add it)
	if strings.HasSuffix(strings.ToLower(filename), ".md") {
		filename = filename[:len(filename)-3]
	}
	if filename == "" {
		m.chat.AddSystemMessage("Invalid filename.")
		return m, nil
	}
	if m.agent == nil {
		m.chat.AddSystemMessage("No agent configured.")
		return m, nil
	}
	if m.agent.Mode() != agent.ModePlan {
		m.chat.AddSystemMessage("Use /plan to enter plan mode first.")
		return m, nil
	}

	// Send a consolidation prompt to the agent
	m.chat.AddSystemMessage("Saving plan...")
	m.streaming = true

	ctx, cancel := context.WithCancel(context.Background())
	m.cancelStream = cancel

	// The filename is captured for use after streaming completes
	m.planSaveFilename = filename
	m.agentCh = m.agent.Run(ctx, "Output the complete plan document in a single markdown message, following the required plan format. Include all sections we discussed.")
	dotCmd := m.chat.StartStreamingMessage()

	return m, tea.Batch(waitForAgentEvent(m.agentCh), dotCmd)
}

// writePlanFile extracts the last assistant message and writes it to docs/plans/.
func (m *AppModel) writePlanFile(filename string) {
	// Find the last assistant message text
	history := m.agent.History()
	var planText string
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == provider.RoleAssistant {
			for _, block := range history[i].Content {
				if block.Type == "text" {
					planText = block.Text
					break
				}
			}
			if planText != "" {
				break
			}
		}
	}

	if planText == "" {
		m.chat.AddSystemMessage("No plan text found to save.")
		return
	}

	baseDir := "."
	if m.session != nil && m.session.ProjectDir != "" {
		baseDir = m.session.ProjectDir
	}
	path := filepath.Join(baseDir, "docs", "plans", filename+".md")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		m.chat.AddSystemMessage("Error creating directory: " + err.Error())
		return
	}
	if err := os.WriteFile(path, []byte(planText), 0o644); err != nil {
		m.chat.AddSystemMessage("Error writing plan: " + err.Error())
		return
	}
	m.chat.AddSystemMessage("Plan saved to " + path)
}

// handleBuild exits plan mode.
func (m AppModel) handleBuild() (tea.Model, tea.Cmd) {
	if m.agent == nil {
		m.chat.AddSystemMessage("No agent configured.")
		return m, nil
	}

	if m.agent.Mode() == agent.ModeBuild {
		m.chat.AddSystemMessage("Already in build mode.")
		return m, nil
	}

	m.agent.SetMode(agent.ModeBuild)
	m.agent.InjectModeChange(agent.ModeBuild)
	m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "build"})
	m.chat.AddSystemMessage("Entered build mode. All tools available.")
	return m, nil
}

// handleMCP handles /mcp [reconnect [name]].
func (m AppModel) handleMCP(args string) (tea.Model, tea.Cmd) {
	if m.agent == nil {
		m.chat.AddSystemMessage("No agent configured.")
		return m, nil
	}
	mgr := m.agent.MCPManager()
	if mgr == nil {
		m.chat.AddSystemMessage("No MCP servers configured.")
		return m, nil
	}

	parts := strings.Fields(args)
	subCmd := ""
	if len(parts) > 0 {
		subCmd = parts[0]
	}

	switch subCmd {
	case "", "status":
		// Show MCP server status
		statuses := mgr.Status()
		if len(statuses) == 0 {
			m.chat.AddSystemMessage("No MCP servers configured.")
			return m, nil
		}

		var sb strings.Builder
		sb.WriteString("MCP Servers:\n")
		totalTools := 0
		for _, s := range statuses {
			switch s.Status {
			case "connected":
				sb.WriteString(fmt.Sprintf("  %s — connected (%d tools)\n", s.Name, s.ToolCount))
				totalTools += s.ToolCount
			case "error":
				sb.WriteString(fmt.Sprintf("  %s — error: %s\n", s.Name, s.Error))
			default:
				sb.WriteString(fmt.Sprintf("  %s — %s\n", s.Name, s.Status))
			}
		}
		sb.WriteString(fmt.Sprintf("\nTotal: %d MCP tools available", totalTools))
		m.chat.AddSystemMessage(sb.String())
		return m, nil

	case "reconnect":
		name := ""
		if len(parts) > 1 {
			name = parts[1]
		}

		// Collect servers to reconnect
		var targets []string
		if name != "" {
			targets = []string{name}
		} else {
			for _, s := range mgr.Status() {
				if s.Status != "connected" {
					targets = append(targets, s.Name)
				}
			}
		}

		if len(targets) == 0 {
			m.chat.AddSystemMessage("All servers already connected.")
			return m, nil
		}

		m.chat.AddSystemMessage("Reconnecting...")
		ctx, cancel := context.WithCancel(context.Background())
		m.reconnecting = true
		m.cancelReconnect = cancel
		return m, func() tea.Msg {
			var results []mcpReconnectResult
			for _, t := range targets {
				err := mgr.Reconnect(ctx, t)
				results = append(results, mcpReconnectResult{Name: t, Err: err})
			}
			return MCPReconnectDoneMsg{Results: results}
		}

	default:
		m.chat.AddSystemMessage("Usage: /mcp [status|reconnect [name]]")
		return m, nil
	}
}

// knownCommands is the set of recognized slash/colon commands.
var knownCommands = map[string]bool{
	"q": true, "quit": true,
	"status": true, "save": true, "clear": true,
	"compact": true, "resume": true,
	"providers": true, "provider": true, "model": true,
	"plan": true, "build": true, "mcp": true,
}

func isKnownCommand(name string) bool {
	return knownCommands[name]
}

// isValidSessionID checks that the ID is 8 hex characters (no path traversal).
var validSessionID = regexp.MustCompile(`^[0-9a-f]{8}$`)

func isValidSessionID(id string) bool {
	return validSessionID.MatchString(id)
}

// runCompaction returns a tea.Cmd that runs compaction in a goroutine
// and sends a CompactionDoneMsg when complete. Uses cancelStream context
// so Ctrl+C can abort compaction.
func (m *AppModel) runCompaction() tea.Cmd {
	a := m.agent
	ctx, cancel := context.WithCancel(context.Background())
	m.cancelStream = cancel
	return func() tea.Msg {
		before, after, err := a.Compact(ctx)
		return CompactionDoneMsg{Before: before, After: after, Err: err}
	}
}

// checkAutoResume is a no-op — removed in favor of the home screen
// which shows /resume as a discoverable command.
func (m *AppModel) checkAutoResume() {}

// handleResume implements the /resume command.
// With no args: lists recent sessions. With an ID: loads that session.
func (m AppModel) handleResume(args string) (tea.Model, tea.Cmd) {
	args = strings.TrimSpace(args)
	if args == "" {
		// Open picker with recent sessions
		sessions, err := session.ListSessions()
		if err != nil {
			m.chat.AddSystemMessage("Error listing sessions: " + err.Error())
			return m, nil
		}
		if len(sessions) == 0 {
			m.chat.AddSystemMessage("No saved sessions found.")
			return m, nil
		}

		limit := 10
		if len(sessions) < limit {
			limit = len(sessions)
		}

		var items []PickerItem
		for _, s := range sessions[:limit] {
			summary := s.Summary
			if len(summary) > 50 {
				summary = summary[:50] + "..."
			}
			label := fmt.Sprintf("%s  %s  %s",
				s.UpdatedAt.Format("2006-01-02 15:04"), s.ProjectDir, summary)
			items = append(items, PickerItem{ID: s.ID, Label: label})
		}

		picker := NewPickerModel("Resume session", items, m.width)
		m.picker = &picker
		m.pickerAction = "resume_session"
		return m, nil
	}

	// Direct ID provided
	return m.loadSessionByID(args)
}

// loadSessionByID loads a session by its ID, replacing the current conversation.
func (m AppModel) loadSessionByID(id string) (tea.Model, tea.Cmd) {
	if !isValidSessionID(id) {
		m.chat.AddSystemMessage("Invalid session ID: " + id)
		return m, nil
	}

	sessionPath := filepath.Join(session.SessionDir(), id+".json")
	sess, err := session.Load(sessionPath)
	if err != nil {
		m.chat.AddSystemMessage("Error loading session: " + err.Error())
		return m, nil
	}

	// Save current session before switching — abort if save fails
	if err := m.saveSession(); err != nil {
		m.chat.AddSystemMessage("Error saving current session: " + err.Error())
		return m, nil
	}

	// Replace session and restore history
	*m.session = *sess
	if m.agent != nil {
		m.agent.LoadSession(sess.Messages)
	}

	// Rebuild chat view from loaded messages
	m.chat = NewChatModel()
	m.layout()
	chatMsgs := MessagesToChat(sess.Messages)
	m.chat.LoadMessages(chatMsgs)
	m.chat.AddSystemMessage(fmt.Sprintf("Resumed session %s", sess.ID))

	// Restore mode from session
	if m.agent != nil {
		restoredMode := agent.AgentMode(sess.Mode)
		if restoredMode == agent.ModePlan {
			m.agent.SetMode(agent.ModePlan)
			m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "plan"})
		} else {
			m.agent.SetMode(agent.ModeBuild)
			m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Mode: "build"})
		}
		tokens := m.agent.EstimateCurrentTokens()
		m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{Tokens: tokens})
	}

	return m, nil
}

// saveSession persists the current session to disk.
func (m *AppModel) saveSession() error {
	if m.session == nil {
		return nil
	}
	if m.agent != nil {
		m.session.SetMessages(m.agent.History())
		m.session.Mode = string(m.agent.Mode())
	}
	return m.session.Save()
}

func (m *AppModel) layout() {
	// Status bar: 1 line
	// Input: ~5 lines (3 textarea + 2 border)
	// Chat: everything else
	statusHeight := 1
	inputHeight := 5
	chatHeight := m.height - statusHeight - inputHeight

	if chatHeight < 3 {
		chatHeight = 3
	}

	m.chat.SetSize(m.width, chatHeight)
	m.input.SetWidth(m.width)
	m.statusBar.width = m.width
}

func (m AppModel) View() string {
	if m.width == 0 {
		return "Starting Ernest..."
	}

	// Show confirmation dialog overlay when active
	if m.confirming && m.confirmDialog != nil {
		chatView := m.chat.View()
		dialogView := m.confirmDialog.View()
		statusView := m.statusBar.View()
		return chatView + "\n" + dialogView + "\n" + statusView
	}

	// Show picker overlay when active
	if m.picker != nil {
		chatView := m.chat.View()
		pickerView := m.picker.View()
		statusView := m.statusBar.View()
		return chatView + "\n" + pickerView + "\n" + statusView
	}

	help := ""
	if m.cmdMode {
		help = helpStyle.Render(":" + m.pendingCmd)
	} else if !m.focused {
		help = helpStyle.Render("i: input  j/k: scroll  gg/G: top/bottom  :: command  ctrl+c: quit")
	}

	chatView := m.chat.View()
	inputView := m.input.View()
	statusView := m.statusBar.View()

	if help != "" {
		return chatView + "\n" + inputView + "\n" + help + "\n" + statusView
	}
	return chatView + "\n" + inputView + "\n" + statusView
}
