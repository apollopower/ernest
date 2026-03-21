package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type StatusModel struct {
	provider string
	model    string
	tokens   int
	maxTokens int
	width    int
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
	tokenInfo := fmt.Sprintf(" %s │ tokens: %d/%d ", m.model, m.tokens, m.maxTokens)
	info := statusBarStyle.Render(tokenInfo)

	gap := m.width - lipgloss.Width(provider) - lipgloss.Width(info)
	if gap < 0 {
		gap = 0
	}
	filler := statusBarStyle.Render(strings.Repeat(" ", gap))

	return provider + filler + info
}
