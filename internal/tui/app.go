package tui

import (
	"context"
	"ernest/internal/agent"
	"ernest/internal/config"
	"ernest/internal/session"
	"fmt"
	"log"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentEventMsg wraps an agent event for the BubbleTea update loop.
type AgentEventMsg struct {
	Event agent.AgentEvent
}

// StreamDoneMsg signals the agent event channel has closed.
type StreamDoneMsg struct{}

type AppModel struct {
	chat           ChatModel
	input          InputModel
	statusBar      StatusModel
	agent          *agent.Agent
	session        *session.Session
	cfg            config.Config
	confirmDialog  *ToolConfirmModel
	focused        bool   // true = input focused, false = vim nav mode
	streaming      bool   // true while agent is streaming a response
	confirming     bool   // true while tool confirmation dialog is visible
	pendingG       bool   // waiting for second 'g' in "gg" sequence
	width          int
	height         int
	pendingCmd     string // for ":" command accumulation
	cmdMode        bool   // in ":" command mode
	cancelStream   context.CancelFunc
	agentCh        <-chan agent.AgentEvent
}

func NewAppModel(cfg config.Config, claudeCfg *config.ClaudeConfig, a *agent.Agent, sess *session.Session) AppModel {
	primary := cfg.PrimaryProvider()

	return AppModel{
		chat:      NewChatModel(),
		input:     NewInputModel(),
		statusBar: NewStatusModel(primary.Name, primary.Model, cfg.MaxContextTokens),
		agent:     a,
		session:   sess,
		cfg:       cfg,
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
		return m, nil

	case SubmitMsg:
		if m.streaming || m.confirming {
			return m, nil // ignore input while streaming or confirming
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
		if err := m.agent.AllowToolAlways(msg.ToolUseID, msg.ToolName); err != nil {
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
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}
		return m, nil

	case tea.KeyMsg:
		// Ctrl+C always takes priority — even during confirmation dialog
		if msg.Type == tea.KeyCtrlC {
			if m.streaming {
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
				m.confirming = false
				m.confirmDialog = nil
				return m, nil
			}
			return m, tea.Quit
		}

		if msg.Type == tea.KeyEsc {
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
		if evt.Usage != nil {
			m.statusBar, _ = m.statusBar.Update(StatusUpdateMsg{
				Tokens: evt.Usage.InputTokens + evt.Usage.OutputTokens,
			})
		}
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
	}

	m.chat.AddSystemMessage("Unknown command: " + name)
	return m, nil
}

// knownCommands is the set of recognized slash/colon commands.
var knownCommands = map[string]bool{
	"q": true, "quit": true,
	"status": true, "save": true, "clear": true,
	"compact": true, "resume": true,
}

func isKnownCommand(name string) bool {
	return knownCommands[name]
}

// saveSession persists the current session to disk.
func (m *AppModel) saveSession() error {
	if m.session == nil {
		return nil
	}
	if m.agent != nil {
		m.session.SetMessages(m.agent.History())
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
