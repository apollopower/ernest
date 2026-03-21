package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
)

type ChatMessage struct {
	Role      string // "user" or "assistant"
	Content   string
	streaming bool // true while message is being streamed
}

// dotTickMsg drives the animated "..." indicator while waiting for a response.
type dotTickMsg struct{}

type ChatModel struct {
	viewport viewport.Model
	messages []ChatMessage
	renderer *glamour.TermRenderer
	dotCount int // 0-2, produces 1-3 dots for animated indicator
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
	case dotTickMsg:
		if m.isStreaming() && m.lastMessageEmpty() {
			m.dotCount = (m.dotCount + 1) % 3
			m.renderMessages()
			return m, m.tickDots()
		}
		return m, nil
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

// tickDots returns a command that sends a dotTickMsg after a short delay.
func (m ChatModel) tickDots() tea.Cmd {
	return tea.Tick(400*time.Millisecond, func(time.Time) tea.Msg {
		return dotTickMsg{}
	})
}

func (m *ChatModel) AddMessage(role, content string) {
	m.messages = append(m.messages, ChatMessage{Role: role, Content: content})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// StartStreamingMessage adds an empty assistant message and marks it as streaming.
// Returns a tea.Cmd that starts the dot animation.
func (m *ChatModel) StartStreamingMessage() tea.Cmd {
	m.dotCount = 0
	m.messages = append(m.messages, ChatMessage{Role: "assistant", streaming: true})
	m.renderMessages()
	m.viewport.GotoBottom()
	return m.tickDots()
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
	wrapWidth := width - 4
	if wrapWidth < 1 {
		wrapWidth = 1
	}
	r, _ := glamour.NewTermRenderer(
		glamour.WithStandardStyle("dark"),
		glamour.WithWordWrap(wrapWidth),
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
			label := userLabelStyle.Render(">")
			content := userMsgStyle.Render(msg.Content)
			lines = append(lines, label+" "+content)
		case "assistant":
			label := assistantLabelStyle.Render("E")
			content := m.renderAssistantContent(msg)
			lines = append(lines, label+" "+content)
		}
	}

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

// renderAssistantContent renders assistant messages. Streaming messages are
// rendered as plain text; finalized messages go through Glamour for markdown.
func (m *ChatModel) renderAssistantContent(msg ChatMessage) string {
	if msg.Content == "" {
		dots := strings.Repeat(".", m.dotCount+1)
		return assistantMsgStyle.Render(dots)
	}

	if msg.streaming || m.renderer == nil {
		return assistantMsgStyle.Render(msg.Content)
	}

	rendered, err := m.renderer.Render(msg.Content)
	if err != nil {
		return assistantMsgStyle.Render(msg.Content)
	}
	return strings.TrimSpace(rendered)
}

// isStreaming returns true if the last message is currently streaming.
func (m *ChatModel) isStreaming() bool {
	if len(m.messages) == 0 {
		return false
	}
	return m.messages[len(m.messages)-1].streaming
}

// lastMessageEmpty returns true if the last message has no content yet.
func (m *ChatModel) lastMessageEmpty() bool {
	if len(m.messages) == 0 {
		return false
	}
	return m.messages[len(m.messages)-1].Content == ""
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
