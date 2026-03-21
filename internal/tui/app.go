package tui

import (
	"context"
	"ernest/internal/agent"
	"ernest/internal/config"

	tea "github.com/charmbracelet/bubbletea"
)

// AgentEventMsg wraps an agent event for the BubbleTea update loop.
type AgentEventMsg struct {
	Event agent.AgentEvent
}

// StreamDoneMsg signals the agent event channel has closed.
type StreamDoneMsg struct{}

type AppModel struct {
	chat         ChatModel
	input        InputModel
	statusBar    StatusModel
	agent        *agent.Agent
	focused      bool   // true = input focused, false = vim nav mode
	streaming    bool   // true while agent is streaming a response
	pendingG     bool   // waiting for second 'g' in "gg" sequence
	width        int
	height       int
	pendingCmd   string // for ":" command accumulation
	cmdMode      bool   // in ":" command mode
	cancelStream context.CancelFunc
	agentCh      <-chan agent.AgentEvent
}

func NewAppModel(cfg config.Config, claudeCfg *config.ClaudeConfig, a *agent.Agent) AppModel {
	primary := cfg.PrimaryProvider()

	return AppModel{
		chat:      NewChatModel(),
		input:     NewInputModel(),
		statusBar: NewStatusModel(primary.Name, primary.Model, cfg.MaxContextTokens),
		agent:     a,
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
		return m, nil

	case SubmitMsg:
		if m.streaming {
			return m, nil // ignore input while streaming
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

	case dotTickMsg:
		var cmd tea.Cmd
		m.chat, cmd = m.chat.Update(msg)
		return m, cmd

	case StreamDoneMsg:
		m.chat.FinalizeMessage()
		m.streaming = false
		if m.cancelStream != nil {
			m.cancelStream()
			m.cancelStream = nil
		}
		return m, nil

	case tea.KeyMsg:
		// Ctrl+C: cancel streaming or quit
		if msg.Type == tea.KeyCtrlC {
			if m.streaming {
				if m.cancelStream != nil {
					m.cancelStream()
					m.cancelStream = nil
				}
				m.chat.FinalizeMessage()
				m.streaming = false
				return m, nil
			}
			return m, tea.Quit
		}

		if msg.Type == tea.KeyEsc {
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
		cmd := m.pendingCmd
		m.cmdMode = false
		m.pendingCmd = ""
		return m.executeCmd(cmd)
	case tea.KeyBackspace:
		if len(m.pendingCmd) > 0 {
			m.pendingCmd = m.pendingCmd[:len(m.pendingCmd)-1]
		}
		if len(m.pendingCmd) == 0 {
			m.cmdMode = false
		}
	default:
		m.pendingCmd += msg.String()
	}
	return m, nil
}

func (m AppModel) executeCmd(cmd string) (tea.Model, tea.Cmd) {
	switch cmd {
	case "q", "quit":
		return m, tea.Quit
	}
	return m, nil
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
