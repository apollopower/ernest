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
	textarea    textarea.Model
	focused     bool
	masked      bool   // render input as dots (for API key entry)
	placeholder string // saved placeholder to restore after masked mode
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
			// Newline on Alt+Enter or when line ends with \
			if msg.Alt {
				break // fall through to textarea update for newline
			}
			value := m.textarea.Value()
			if strings.HasSuffix(strings.TrimRight(value, " \t"), "\\") {
				// Remove trailing backslash and insert a newline
				trimmed := strings.TrimRight(value, " \t")
				trimmed = trimmed[:len(trimmed)-1]
				m.textarea.SetValue(trimmed + "\n")
				// Move cursor to end
				m.textarea.CursorEnd()
				return m, nil
			}
			text := strings.TrimSpace(value)
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
	if m.masked {
		// Show fixed-length dots to avoid leaking key length
		val := m.textarea.Value()
		display := m.textarea.Placeholder
		if len(val) > 0 {
			display = strings.Repeat("•", 8)
		}
		return style.Render("┃ " + display)
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

// SetMasked enables or disables masked input mode.
// When masked, the view renders dots instead of the actual text.
func (m *InputModel) SetMasked(masked bool, placeholder string) {
	m.masked = masked
	if masked {
		m.placeholder = m.textarea.Placeholder
		m.textarea.Placeholder = placeholder
		m.textarea.Reset()
	} else {
		m.textarea.Placeholder = m.placeholder
		m.textarea.Reset()
	}
}

// IsMasked returns true if the input is in masked mode.
func (m *InputModel) IsMasked() bool {
	return m.masked
}

func (m *InputModel) SetWidth(w int) {
	// Account for border and padding (2 border + 2 padding = 4)
	inner := w - 4
	if inner < 10 {
		inner = 10
	}
	m.textarea.SetWidth(inner)
}
