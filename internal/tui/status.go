package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

var (
	tokenGreenStyle = lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}).
		Padding(0, 1)

	tokenYellowStyle = lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#D4A017", Dark: "#FFD700"}).
		Padding(0, 1)

	tokenRedStyle = lipgloss.NewStyle().
		Background(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
		Foreground(lipgloss.AdaptiveColor{Light: "#FF0000", Dark: "#FF4444"}).
		Padding(0, 1).
		Bold(true)
)

type StatusModel struct {
	provider  string
	model     string
	tokens    int
	maxTokens int
	width     int
}

type StatusUpdateMsg struct {
	Provider  string
	Model     string
	Tokens    int
	MaxTokens int
}

func NewStatusModel(provider, model string, maxTokens int) StatusModel {
	return StatusModel{
		provider:  provider,
		model:     model,
		maxTokens: maxTokens,
	}
}

func (m StatusModel) Init() tea.Cmd {
	return nil
}

func (m StatusModel) Update(msg tea.Msg) (StatusModel, tea.Cmd) {
	switch msg := msg.(type) {
	case StatusUpdateMsg:
		if msg.Provider != "" {
			m.provider = msg.Provider
		}
		if msg.Model != "" {
			m.model = msg.Model
		}
		if msg.Tokens > 0 {
			m.tokens = msg.Tokens
		}
		if msg.MaxTokens > 0 {
			m.maxTokens = msg.MaxTokens
		}
	}
	return m, nil
}

func (m StatusModel) View() string {
	if m.width == 0 {
		return ""
	}

	provider := statusProviderStyle.Render(m.provider)

	// Color-code token display based on usage percentage
	tokenText := fmt.Sprintf(" %s │ tokens: %d/%d ", m.model, m.tokens, m.maxTokens)
	tokenStyle := m.tokenStyle()
	info := tokenStyle.Render(tokenText)

	gap := m.width - lipgloss.Width(provider) - lipgloss.Width(info)
	if gap < 0 {
		gap = 0
	}
	filler := statusBarStyle.Render(strings.Repeat(" ", gap))

	return provider + filler + info
}

// tokenStyle returns the appropriate style based on token usage percentage.
func (m StatusModel) tokenStyle() lipgloss.Style {
	if m.maxTokens <= 0 {
		return statusBarStyle // no color coding when maxTokens is not set
	}

	pct := m.tokens * 100 / m.maxTokens
	switch {
	case pct >= 80:
		return tokenRedStyle
	case pct >= 50:
		return tokenYellowStyle
	default:
		return tokenGreenStyle
	}
}
