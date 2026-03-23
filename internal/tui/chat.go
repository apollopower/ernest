package tui

import (
	"encoding/json"
	"ernest/internal/provider"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

type ChatMessage struct {
	Role      string // "user", "assistant", "tool_call", "tool_result"
	Content   string
	ToolName  string // for tool_call and tool_result messages
	streaming bool   // true while message is being streamed
}

// MessagesToChat converts provider Messages (agent history) into ChatMessages
// for displaying a resumed session in the TUI. Multi-block messages produce
// multiple ChatMessages.
func MessagesToChat(msgs []provider.Message) []ChatMessage {
	var result []ChatMessage
	// Track tool_use ID → name for labeling tool_result messages
	toolNames := make(map[string]string)

	for _, msg := range msgs {
		for _, block := range msg.Content {
			switch block.Type {
			case "text":
				role := "user"
				if msg.Role == provider.RoleAssistant {
					role = "assistant"
				}
				result = append(result, ChatMessage{Role: role, Content: block.Text})

			case "tool_use":
				toolNames[block.ToolUseID] = block.ToolName
				// Handle ToolInput type variants to avoid double-encoding
				var display string
				switch v := block.ToolInput.(type) {
				case nil:
					display = "{}"
				case string:
					display = v
				case json.RawMessage:
					display = string(v)
				default:
					b, _ := json.Marshal(v)
					display = string(b)
				}
				if len(display) > 200 {
					display = display[:200] + "..."
				}
				result = append(result, ChatMessage{
					Role:     "tool_call",
					ToolName: block.ToolName,
					Content:  display,
				})

			case "tool_result":
				content := block.Content
				lines := strings.Split(content, "\n")
				if len(lines) > 50 {
					content = strings.Join(lines[:50], "\n") + "\n... (truncated)"
				}
				toolName := toolNames[block.ToolUseID]
				if toolName == "" {
					toolName = "tool"
				}
				result = append(result, ChatMessage{
					Role:     "tool_result",
					ToolName: toolName,
					Content:  content,
				})
			}
		}
	}
	return result
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

// LoadMessages replaces the chat view with messages from a resumed session.
func (m *ChatModel) LoadMessages(msgs []ChatMessage) {
	m.messages = msgs
	m.renderMessages()
	m.viewport.GotoBottom()
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

// AddSystemMessage adds a system message to the chat. System messages are
// for command output and are NOT saved to session history.
func (m *ChatModel) AddSystemMessage(content string) {
	m.messages = append(m.messages, ChatMessage{Role: "system", Content: content})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// FinalizeOrRemoveEmpty finalizes the last message if it has content,
// or removes it if it's an empty streaming placeholder. This prevents
// blank lines between consecutive tool calls.
func (m *ChatModel) FinalizeOrRemoveEmpty() {
	if len(m.messages) == 0 {
		return
	}
	last := m.messages[len(m.messages)-1]
	if last.streaming && last.Content == "" {
		m.messages = m.messages[:len(m.messages)-1]
	} else {
		m.messages[len(m.messages)-1].streaming = false
	}
	m.renderMessages()
	m.viewport.GotoBottom()
}

// AddToolCall adds a tool call message to the chat.
func (m *ChatModel) AddToolCall(toolName, toolInput string) {
	// Truncate long tool input for display
	display := toolInput
	if len(display) > 200 {
		display = display[:200] + "..."
	}
	m.messages = append(m.messages, ChatMessage{
		Role:     "tool_call",
		ToolName: toolName,
		Content:  display,
	})
	m.renderMessages()
	m.viewport.GotoBottom()
}

// AddToolResult adds a tool result message to the chat.
func (m *ChatModel) AddToolResult(toolName, toolResult string) {
	// Truncate long results for display
	lines := strings.Split(toolResult, "\n")
	display := toolResult
	if len(lines) > 50 {
		display = strings.Join(lines[:50], "\n") + "\n... (truncated)"
	}
	m.messages = append(m.messages, ChatMessage{
		Role:     "tool_result",
		ToolName: toolName,
		Content:  display,
	})
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

	if !m.hasUserMessages() {
		lines = append(lines, m.renderHomeScreen()...)
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
		case "tool_call":
			label := toolLabelStyle.Render("[" + formatToolName(msg.ToolName) + "]")
			content := toolContentStyle.Render(msg.Content)
			lines = append(lines, label+" "+content)
		case "tool_result":
			label := toolLabelStyle.Render("[" + formatToolName(msg.ToolName) + " result]")
			content := toolContentStyle.Render(msg.Content)
			lines = append(lines, label+"\n"+content)
		case "system":
			content := helpStyle.Render(msg.Content)
			lines = append(lines, content)
		}
	}

	m.viewport.SetContent(strings.Join(lines, "\n"))
}

// renderHomeScreen returns the centered home screen for an empty chat.
func (m *ChatModel) renderHomeScreen() []string {
	titleStyle := lipgloss.NewStyle().
		Foreground(highlight).
		Bold(true)

	taglineStyle := lipgloss.NewStyle().
		Foreground(lipgloss.AdaptiveColor{Light: "#969B86", Dark: "#AAAAAA"}).
		Italic(true)

	cmdStyle := lipgloss.NewStyle().
		Foreground(special).
		Bold(true)

	descStyle := lipgloss.NewStyle().
		Foreground(muted)

	// ASCII art title
	asciiTitle := []string{
		"┌─┐┌─┐┌┐┌┌─┐┌─┐┌┬┐",
		"├┤ ├┬┘│││├┤ └─┐ │ ",
		"└─┘┴└─┘└┘└─┘└─┘ ┴ ",
	}

	var titleLines []string
	for _, line := range asciiTitle {
		titleLines = append(titleLines, "     "+titleStyle.Render(line))
	}
	title := strings.Join(titleLines, "\n")
	tagline := taglineStyle.Render("Write code. Cut the rest.")

	commands := []struct{ cmd, desc string }{
		{"/model", "Switch provider"},
		{"/resume", "Continue a session"},
		{"/providers", "Show connections"},
		{"/status", "Session info"},
	}

	var cmdLines []string
	for _, c := range commands {
		cmdLines = append(cmdLines,
			"     "+cmdStyle.Render(padRight(c.cmd, 14))+descStyle.Render(c.desc))
	}

	// Assemble with spacing
	var content []string
	content = append(content, "")
	content = append(content, title)
	content = append(content, "")
	content = append(content, "     "+tagline)
	content = append(content, "")
	content = append(content, cmdLines...)
	content = append(content, "")

	// Draw border
	boxWidth := 42
	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtle).
		Width(boxWidth).
		Padding(0, 1)

	boxContent := strings.Join(content, "\n")
	box := border.Render(boxContent)

	// Center vertically in the viewport
	boxLines := strings.Split(box, "\n")
	topPad := (m.height - len(boxLines)) / 3
	if topPad < 1 {
		topPad = 1
	}

	var result []string
	for i := 0; i < topPad; i++ {
		result = append(result, "")
	}

	// Center horizontally
	for _, line := range boxLines {
		lineWidth := lipgloss.Width(line)
		leftPad := (m.width - lineWidth) / 2
		if leftPad < 0 {
			leftPad = 0
		}
		result = append(result, strings.Repeat(" ", leftPad)+line)
	}

	return result
}

// hasUserMessages returns true if there are any non-system messages.
func (m *ChatModel) hasUserMessages() bool {
	for _, msg := range m.messages {
		if msg.Role != "system" {
			return true
		}
	}
	return false
}

// padRight pads a string to the given width with spaces.
func padRight(s string, width int) string {
	if len(s) >= width {
		return s
	}
	return s + strings.Repeat(" ", width-len(s))
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

// formatToolName converts MCP tool names to a friendly display format.
// "mcp__sentry__search_issues" → "sentry: search_issues"
// Built-in tools pass through unchanged.
func formatToolName(name string) string {
	if !strings.HasPrefix(name, "mcp__") {
		return name
	}
	rest := name[5:] // strip "mcp__"
	if idx := strings.Index(rest, "__"); idx > 0 {
		return rest[:idx] + ": " + rest[idx+2:]
	}
	return name
}
