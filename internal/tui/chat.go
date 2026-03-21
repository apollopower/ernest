package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

type ChatMessage struct {
	Role      string // "user" or "assistant"
	Content   string
	streaming bool // true while message is being streamed
}

type ChatModel struct {
	viewport viewport.Model
	messages []ChatMessage
	renderer *glamour.TermRenderer
	width    int
	height   int
	ready    bool
}

func NewChatModel() ChatModel {
	// Use dark style directly instead of WithAutoStyle() to avoid
	// terminal background color probing (OSC query), which can hang
	// on terminals that don't respond to the background color escape.
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(0),
	)
	return ChatModel{renderer: r}
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

// StartStreamingMessage adds an empty assistant message and marks it as streaming.
func (m *ChatModel) StartStreamingMessage() {
	m.messages = append(m.messages, ChatMessage{Role: "assistant", streaming: true})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// AppendToMessage appends text to the last message (used during streaming).
func (m *ChatModel) AppendToMessage(text string) {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	last.Content += text
	m.renderMessages()
	m.viewport.GotoBottom()
}

// FinalizeMessage marks the last message as no longer streaming and re-renders
// with markdown.
func (m *ChatModel) FinalizeMessage() {
	if len(m.messages) == 0 {
		return
	}
	m.messages[len(m.messages)-1].streaming = false
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

	// Recreate renderer with new width
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(width-4),
	)
	if r != nil {
		m.renderer = r
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
			content := m.renderAssistantContent(msg)
			lines = append(lines, fmt.Sprintf("%s\n%s", label, content))
		}
	}

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

// renderAssistantContent renders assistant messages. Streaming messages are
// rendered as plain text; finalized messages go through Glamour for markdown.
func (m *ChatModel) renderAssistantContent(msg ChatMessage) string {
	if msg.Content == "" {
		return assistantMsgStyle.Render("...")
	}

	if msg.streaming || m.renderer == nil {
		return assistantMsgStyle.Render(msg.Content)
	}

	rendered, err := m.renderer.Render(msg.Content)
	if err != nil {
		return assistantMsgStyle.Render(msg.Content)
	}
	return strings.TrimRight(rendered, "\n")
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
