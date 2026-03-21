package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
)

type ChatMessage struct {
	Role    string // "user" or "assistant"
	Content string
}

type ChatModel struct {
	viewport viewport.Model
	messages []ChatMessage
	width    int
	height   int
	ready    bool
}

func NewChatModel() ChatModel {
	return ChatModel{}
}

func (m ChatModel) Init() tea.Cmd {
	return nil
}

func (m ChatModel) Update(msg tea.Msg) (ChatModel, tea.Cmd) {
	var cmd tea.Cmd

	switch msg.(type) {
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

func (m *ChatModel) AddMessage(role, content string) {
	m.messages = append(m.messages, ChatMessage{Role: role, Content: content})
	m.renderMessages()
	m.viewport.GotoBottom()
}

func (m *ChatModel) SetSize(width, height int) {
	m.width = width
	m.height = height

	if !m.ready {
		m.viewport = viewport.New(width, height)
		m.viewport.KeyMap = viewport.KeyMap{} // we handle keys in app model
		m.ready = true
	} else {
		m.viewport.Width = width
		m.viewport.Height = height
	}

	m.renderMessages()
}

func (m *ChatModel) renderMessages() {
	if !m.ready {
		return
	}

	var lines []string

	if len(m.messages) == 0 {
		welcome := helpStyle.Render("Welcome to Ernest. Type a message to get started.")
		lines = append(lines, "", welcome, "")
	}

	for i, msg := range m.messages {
		if i > 0 {
			lines = append(lines, "")
		}

		switch msg.Role {
		case "user":
			label := userLabelStyle.Render("You")
			content := userMsgStyle.Render(msg.Content)
			lines = append(lines, fmt.Sprintf("%s  %s", label, content))
		case "assistant":
			label := assistantLabelStyle.Render("Ernest")
			content := assistantMsgStyle.Render(msg.Content)
			lines = append(lines, fmt.Sprintf("%s  %s", label, content))
		}
	}

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

func (m ChatModel) View() string {
	if !m.ready {
		return "Initializing..."
	}
	return m.viewport.View()
}

// ScrollUp scrolls the chat view up by one line.
func (m *ChatModel) ScrollUp() {
	m.viewport.LineUp(1)
}

// ScrollDown scrolls the chat view down by one line.
func (m *ChatModel) ScrollDown() {
	m.viewport.LineDown(1)
}

// GotoTop scrolls to the top of the chat.
func (m *ChatModel) GotoTop() {
	m.viewport.GotoTop()
}

// GotoBottom scrolls to the bottom of the chat.
func (m *ChatModel) GotoBottom() {
	m.viewport.GotoBottom()
}
