package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// ToolApproveMsg is sent when the user approves a tool use.
type ToolApproveMsg struct{ ToolUseID string }

// ToolDenyMsg is sent when the user denies a tool use.
type ToolDenyMsg struct{ ToolUseID string }

// ToolAlwaysMsg is sent when the user approves and wants to remember the choice.
type ToolAlwaysMsg struct {
	ToolUseID string
	ToolName  string
	ToolInput string
}

// ToolConfirmModel renders a modal confirmation dialog for tool use.
type ToolConfirmModel struct {
	toolName  string
	toolInput string
	toolUseID string
	width     int
}

func NewToolConfirmModel(toolName, toolInput, toolUseID string, width int) ToolConfirmModel {
	return ToolConfirmModel{
		toolName:  toolName,
		toolInput: toolInput,
		toolUseID: toolUseID,
		width:     width,
	}
}

func (m ToolConfirmModel) Init() tea.Cmd {
	return nil
}

func (m ToolConfirmModel) Update(msg tea.Msg) (ToolConfirmModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "y":
			return m, func() tea.Msg { return ToolApproveMsg{ToolUseID: m.toolUseID} }
		case "n":
			return m, func() tea.Msg { return ToolDenyMsg{ToolUseID: m.toolUseID} }
		case "a":
			name := m.toolName
			id := m.toolUseID
			input := m.toolInput
			return m, func() tea.Msg { return ToolAlwaysMsg{ToolUseID: id, ToolName: name, ToolInput: input} }
		}
	}
	return m, nil
}

func (m ToolConfirmModel) View() string {
	width := m.width - 4
	if width < 20 {
		width = 20
	}

	summary := m.formatInput()

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.AdaptiveColor{Light: "#D4A017", Dark: "#FFD700"}).
		Padding(1, 2).
		Width(width)

	title := toolConfirmTitleStyle.Render("Tool Use")
	name := toolConfirmNameStyle.Render(m.toolName)
	input := toolConfirmInputStyle.Render(summary)
	prompt := toolConfirmPromptStyle.Render("Allow? (y)es / (n)o / (a)lways")

	content := fmt.Sprintf("%s\n\n%s\n%s\n\n%s", title, name, input, prompt)

	return border.Render(content)
}

// formatInput returns a human-readable summary of the tool input.
func (m ToolConfirmModel) formatInput() string {
	var parsed map[string]any
	if err := json.Unmarshal([]byte(m.toolInput), &parsed); err != nil {
		s := m.toolInput
		if len(s) > 200 {
			s = s[:200] + "..."
		}
		return s
	}

	// Show the most relevant field based on tool type
	switch m.toolName {
	case "bash":
		if cmd, ok := parsed["command"].(string); ok {
			return "> " + cmd
		}
	case "write_file":
		if path, ok := parsed["file_path"].(string); ok {
			lines := 0
			if content, ok := parsed["content"].(string); ok {
				lines = strings.Count(content, "\n") + 1
			}
			return fmt.Sprintf("%s (%d lines)", path, lines)
		}
	case "str_replace":
		if path, ok := parsed["file_path"].(string); ok {
			return path
		}
	}

	// Default: show truncated JSON
	s := m.toolInput
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
