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
	Role        string // "user", "assistant", "tool_call", "tool_result"
	Content     string
	ToolName    string // for tool_call and tool_result messages
	streaming   bool   // true while message is being streamed
	rendered    string // cached glamour output
	renderedLen int    // content length when rendered was computed
	// Incremental rendering: cache rendered prefix up to a block boundary
	stableRendered string // glamour output for stable (completed) blocks
	stableLen      int    // content length covered by stableRendered
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

// streamRenderTickMsg fires periodically to batch-render streaming content.
type streamRenderTickMsg struct{}

// dotTickMsg drives the animated "..." indicator while waiting for a response.
type dotTickMsg struct{}

type ChatModel struct {
	viewport    viewport.Model
	messages    []ChatMessage
	renderer    *glamour.TermRenderer
	dotCount    int  // 0-2, produces 1-3 dots for animated indicator
	width       int
	height      int
	ready       bool
	noProviders bool // show setup hints on home screen
	renderDirty bool // content changed, needs re-render on next tick
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
	case streamRenderTickMsg:
		if m.renderDirty {
			m.renderDirty = false
			m.renderMessages()
			m.viewport.GotoBottom()
		}
		// Keep ticking as long as streaming is active
		if m.isStreaming() {
			return m, m.tickStreamRender()
		}
		return m, nil
	default:
		m.viewport, cmd = m.viewport.Update(msg)
	}

	return m, cmd
}

// tickStreamRender returns a command that fires a streamRenderTickMsg after 30ms (~33fps).
func (m ChatModel) tickStreamRender() tea.Cmd {
	return tea.Tick(30*time.Millisecond, func(time.Time) tea.Msg {
		return streamRenderTickMsg{}
	})
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
// Returns tea.Cmds for dot animation and debounced markdown rendering.
func (m *ChatModel) StartStreamingMessage() tea.Cmd {
	m.dotCount = 0
	m.renderDirty = false
	m.messages = append(m.messages, ChatMessage{Role: "assistant", streaming: true})
	m.renderMessages()
	m.viewport.GotoBottom()
	return tea.Batch(m.tickDots(), m.tickStreamRender())
}

// AppendToMessage appends text to the last message (used during streaming).
// Does not render immediately — sets dirty flag for debounced rendering.
func (m *ChatModel) AppendToMessage(text string) {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	last.Content += text
	m.renderDirty = true
}

// FinalizeMessage marks the last message as no longer streaming and re-renders
// with markdown. Invalidates cache to get a clean final render without sanitization.
func (m *ChatModel) FinalizeMessage() {
	if len(m.messages) == 0 {
		return
	}
	last := &m.messages[len(m.messages)-1]
	last.streaming = false
	last.rendered = ""  // invalidate cache for clean final render
	last.renderedLen = 0
	last.stableRendered = ""
	last.stableLen = 0
	m.renderDirty = false
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
		msg := &m.messages[len(m.messages)-1]
		msg.streaming = false
		msg.rendered = ""
		msg.renderedLen = 0
		msg.stableRendered = ""
		msg.stableLen = 0
	}
	m.renderDirty = false
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

	// Invalidate all render caches — word-wrap width changed
	for i := range m.messages {
		m.messages[i].rendered = ""
		m.messages[i].renderedLen = 0
		m.messages[i].stableRendered = ""
		m.messages[i].stableLen = 0
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

	for i := range m.messages {
		msg := &m.messages[i]
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

	var commands []struct{ cmd, desc string }
	if m.noProviders {
		commands = []struct{ cmd, desc string }{
			{"/provider add", "<type> <key>"},
			{"", ""},
			{"Types:", "anthropic, openai"},
			{"", "siliconflow, gemini"},
			{"", "ollama (no key)"},
		}
	} else {
		commands = []struct{ cmd, desc string }{
			{"/model", "Switch provider"},
			{"/resume", "Continue a session"},
			{"/providers", "Show connections"},
			{"/status", "Session info"},
		}
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

// renderAssistantContent renders assistant messages through glamour for markdown.
// During streaming, uses incremental rendering: caches rendered output for completed
// blocks and only runs glamour on the trailing active block.
func (m *ChatModel) renderAssistantContent(msg *ChatMessage) string {
	if msg.Content == "" {
		dots := strings.Repeat(".", m.dotCount+1)
		return assistantMsgStyle.Render(dots)
	}

	if m.renderer == nil {
		return assistantMsgStyle.Render(msg.Content)
	}

	// Use full cache if content hasn't changed
	if msg.rendered != "" && msg.renderedLen == len(msg.Content) {
		return msg.rendered
	}

	var result string
	if msg.streaming {
		result = m.renderIncremental(msg)
	} else {
		rendered, err := m.renderer.Render(msg.Content)
		if err != nil {
			return assistantMsgStyle.Render(msg.Content)
		}
		result = strings.TrimSpace(rendered)
	}

	// Cache the full result
	msg.rendered = result
	msg.renderedLen = len(msg.Content)

	return result
}

// renderIncremental renders streaming content by splitting at the last stable
// block boundary. Completed blocks are cached; only the active tail is rendered.
// Note: cross-block constructs (numbered lists, nested blockquotes) may render
// with visual breaks during streaming since prefix and tail are rendered
// independently. These are corrected on finalization when the full message
// is rendered as one document.
func (m *ChatModel) renderIncremental(msg *ChatMessage) string {
	content := msg.Content

	// Find the last stable block boundary (double newline not inside a code fence)
	splitAt := findStableBlockBoundary(content)

	// If we have new stable content beyond what's cached, render and cache it
	if splitAt > msg.stableLen {
		stableContent := content[:splitAt]
		rendered, err := m.renderer.Render(stableContent)
		if err == nil {
			msg.stableRendered = strings.TrimSpace(rendered)
			msg.stableLen = splitAt
		}
	}

	// Render the active tail (from stable boundary to end)
	tail := content[msg.stableLen:]
	if tail == "" {
		return msg.stableRendered
	}

	tail = sanitizePartialMarkdown(tail)
	tailRendered, err := m.renderer.Render(tail)
	if err != nil {
		tailRendered = assistantMsgStyle.Render(tail)
	} else {
		tailRendered = strings.TrimSpace(tailRendered)
	}

	if msg.stableRendered == "" {
		return tailRendered
	}
	return msg.stableRendered + "\n" + tailRendered
}

// findStableBlockBoundary returns the byte offset of the last blank line
// that is not inside an unclosed code fence. Everything before this point is
// "stable" — completed markdown blocks whose rendering won't change.
func findStableBlockBoundary(content string) int {
	lastBoundary := 0
	fenceOpen := false

	lines := strings.Split(content, "\n")
	offset := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)

		// Track code fence state
		if strings.HasPrefix(trimmed, "```") {
			fenceOpen = !fenceOpen
		}

		// A blank line outside a code fence marks a block boundary
		if trimmed == "" && !fenceOpen && offset > 0 {
			lastBoundary = offset
		}

		offset += len(line) + 1 // +1 for the newline
	}

	return lastBoundary
}

// sanitizePartialMarkdown closes unclosed code fences in partial markdown
// so glamour doesn't produce broken output during streaming.
// Uses line-aware fence detection (same logic as findStableBlockBoundary).
func sanitizePartialMarkdown(content string) string {
	fenceOpen := false
	for _, line := range strings.Split(content, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenceOpen = !fenceOpen
		}
	}
	if fenceOpen {
		content += "\n```"
	}
	return content
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
