package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textarea"
	tea "github.com/charmbracelet/bubbletea"
)

// SubmitMsg is sent when the user submits their input.
type SubmitMsg struct {
	Text string
}

type InputModel struct {
	textarea textarea.Model
	focused  bool
}

func NewInputModel() InputModel {
	ta := textarea.New()
	ta.Placeholder = "Ask Ernest something..."
	ta.CharLimit = 0 // unlimited
	ta.MaxHeight = 6
	ta.ShowLineNumbers = false
	ta.SetHeight(3)
	ta.Focus()

	return InputModel{
		textarea: ta,
		focused:  true,
	}
}

func (m InputModel) Init() tea.Cmd {
	return textarea.Blink
}

func (m InputModel) Update(msg tea.Msg) (InputModel, tea.Cmd) {
	if !m.focused {
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyEnter:
			// Submit on Enter, newline on Alt+Enter/Shift+Enter
			if msg.Alt {
				break // fall through to textarea update for newline
			}
			text := strings.TrimSpace(m.textarea.Value())
			if text == "" {
				return m, nil
			}
			m.textarea.Reset()
			return m, func() tea.Msg { return SubmitMsg{Text: text} }
		}
	}

	var cmd tea.Cmd
	m.textarea, cmd = m.textarea.Update(msg)
	return m, cmd
}

func (m InputModel) View() string {
	style := inputBlurredStyle
	if m.focused {
		style = inputFocusedStyle
	}
	return style.Render(m.textarea.View())
}

func (m *InputModel) Focus() tea.Cmd {
	m.focused = true
	return m.textarea.Focus()
}

func (m *InputModel) Blur() {
	m.focused = false
	m.textarea.Blur()
}

func (m *InputModel) SetWidth(w int) {
	// Account for border and padding (2 border + 2 padding = 4)
	inner := w - 4
	if inner < 10 {
		inner = 10
	}
	m.textarea.SetWidth(inner)
}
