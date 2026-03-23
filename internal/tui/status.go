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
	mode      string // "plan" or "" (build is default, not shown)
	tokens    int
	maxTokens int
	width     int
}

type StatusUpdateMsg struct {
	Provider  string
	Model     string
	Mode      string // "plan" or "" to clear
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
		if msg.Tokens >= 0 {
			m.tokens = msg.Tokens
		}
		if msg.MaxTokens >= 0 {
			m.maxTokens = msg.MaxTokens
		}
		// Mode: "plan" sets it, "build"/"clear" explicitly clear it.
		// Empty string is a no-op (doesn't accidentally clear on unrelated updates).
		if msg.Mode == "plan" {
			m.mode = "plan"
		} else if msg.Mode == "build" || msg.Mode == "clear" {
			m.mode = ""
		}
	}
	return m, nil
}

func (m StatusModel) View() string {
	if m.width == 0 {
		return ""
	}

	// Mode indicator (only shown in plan mode)
	modeIndicator := ""
	if m.mode == "plan" {
		modeStyle := lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#D4A017", Dark: "#FFD700"}).
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"}).
			Padding(0, 1).
			Bold(true)
		modeIndicator = modeStyle.Render("PLAN")
	}

	provider := statusProviderStyle.Render(m.provider)

	// Color-code token display based on usage percentage
	var tokenText string
	if m.maxTokens > 0 {
		tokenText = fmt.Sprintf(" %s │ tokens: %d/%d ", m.model, m.tokens, m.maxTokens)
	} else {
		tokenText = fmt.Sprintf(" %s │ tokens: %d ", m.model, m.tokens)
	}
	tokenStyle := m.tokenStyle()
	info := tokenStyle.Render(tokenText)

	leftParts := modeIndicator + provider
	gap := m.width - lipgloss.Width(leftParts) - lipgloss.Width(info)
	if gap < 0 {
		gap = 0
	}
	filler := statusBarStyle.Render(strings.Repeat(" ", gap))

	return leftParts + filler + info
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
