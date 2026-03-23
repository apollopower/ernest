package tui

import (
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

// PickerResult is sent when the user makes a selection.
type PickerResult struct {
	ID    string // identifier for the selected item
	Label string // display label
}

// PickerCancelMsg is sent when the user cancels the picker.
type PickerCancelMsg struct{}

// PickerItem is a selectable option in the picker.
type PickerItem struct {
	ID    string
	Label string
}

// PickerModel renders a centered modal with selectable options.
type PickerModel struct {
	title    string
	items    []PickerItem
	cursor   int
	width    int
}

func NewPickerModel(title string, items []PickerItem, width int) PickerModel {
	return PickerModel{
		title: title,
		items: items,
		width: width,
	}
}

func (m PickerModel) Init() tea.Cmd {
	return nil
}

func (m PickerModel) Update(msg tea.Msg) (PickerModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "j", "down":
			if m.cursor < len(m.items)-1 {
				m.cursor++
			}
		case "k", "up":
			if m.cursor > 0 {
				m.cursor--
			}
		case "enter":
			if m.cursor < len(m.items) {
				item := m.items[m.cursor]
				return m, func() tea.Msg {
					return PickerResult{ID: item.ID, Label: item.Label}
				}
			}
		case "esc", "q":
			return m, func() tea.Msg { return PickerCancelMsg{} }
		default:
			// Number keys for quick selection (1-9)
			if len(msg.String()) == 1 && msg.String()[0] >= '1' && msg.String()[0] <= '9' {
				idx := int(msg.String()[0]-'1')
				if idx < len(m.items) {
					item := m.items[idx]
					return m, func() tea.Msg {
						return PickerResult{ID: item.ID, Label: item.Label}
					}
				}
			}
		}
	}
	return m, nil
}

func (m PickerModel) View() string {
	width := m.width - 4
	if width < 30 {
		width = 30
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(highlight).
		Padding(1, 2).
		Width(width)

	title := lipgloss.NewStyle().Bold(true).Foreground(highlight).Render(m.title)

	var lines []string
	for i, item := range m.items {
		cursor := "  "
		style := lipgloss.NewStyle().Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#DDDDDD"})
		if i == m.cursor {
			cursor = "> "
			style = style.Bold(true).Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#FAFAFA"})
		}
		lines = append(lines, style.Render(fmt.Sprintf("%s%d. %s", cursor, i+1, item.Label)))
	}

	help := helpStyle.Render("j/k: navigate  1-9: quick select  enter: select  esc: cancel")

	content := title + "\n\n" + strings.Join(lines, "\n") + "\n\n" + help
	return border.Render(content)
}
