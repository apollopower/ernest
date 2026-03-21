package tui

import (
	"ernest/internal/config"
	"fmt"

	tea "github.com/charmbracelet/bubbletea"
)

type AppModel struct {
	chat       ChatModel
	input      InputModel
	statusBar  StatusModel
	focused    bool // true = input focused, false = vim nav mode
	pendingG   bool // waiting for second 'g' in "gg" sequence
	width      int
	height     int
	pendingCmd string // for ":" command accumulation
	cmdMode    bool   // in ":" command mode
}

func NewAppModel(cfg config.Config, claudeCfg *config.ClaudeConfig) AppModel {
	primary := cfg.PrimaryProvider()

	return AppModel{
		chat:      NewChatModel(),
		input:     NewInputModel(),
		statusBar: NewStatusModel(primary.Name, primary.Model, cfg.MaxContextTokens),
		focused:   true, // start with input focused
	}
}

func (m AppModel) Init() tea.Cmd {
	return m.input.Init()
}

func (m AppModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.layout()
		return m, nil

	case SubmitMsg:
		m.chat.AddMessage("user", msg.Text)
		// Echo placeholder until provider is wired in
		m.chat.AddMessage("assistant", fmt.Sprintf("Echo: %s", msg.Text))
		return m, nil

	case tea.KeyMsg:
		// Global keys
		switch msg.Type {
		case tea.KeyCtrlC:
			return m, tea.Quit
		case tea.KeyEsc:
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
