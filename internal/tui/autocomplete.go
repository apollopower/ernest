package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const maxAutocompleteItems = 6

// AutocompleteModel shows filtered command suggestions above the input.
type AutocompleteModel struct {
	items   []CommandDef
	cursor  int
	visible bool
	width   int
}

// Update refreshes the autocomplete state based on current input text.
// Returns true if the autocomplete is visible after the update.
func (a *AutocompleteModel) Update(input string) bool {
	input = strings.TrimSpace(input)
	if !strings.HasPrefix(input, "/") || strings.Contains(input, " ") {
		a.visible = false
		a.items = nil
		a.cursor = 0
		return false
	}

	prefix := strings.ToLower(input[1:]) // strip "/"
	a.items = filterCommands(prefix)
	a.visible = len(a.items) > 0
	if a.cursor >= len(a.items) {
		a.cursor = max(0, len(a.items)-1)
	}
	return a.visible
}

// MoveUp moves the cursor up.
func (a *AutocompleteModel) MoveUp() {
	if a.cursor > 0 {
		a.cursor--
	}
}

// MoveDown moves the cursor down.
func (a *AutocompleteModel) MoveDown() {
	if a.cursor < len(a.items)-1 {
		a.cursor++
	}
}

// Selected returns the currently highlighted command, or empty string.
func (a *AutocompleteModel) Selected() string {
	if a.cursor < len(a.items) {
		return a.items[a.cursor].Name
	}
	return ""
}

// Dismiss hides the autocomplete.
func (a *AutocompleteModel) Dismiss() {
	a.visible = false
	a.items = nil
	a.cursor = 0
}

// View renders the autocomplete popup.
func (a *AutocompleteModel) View() string {
	if !a.visible || len(a.items) == 0 {
		return ""
	}

	width := a.width - 4
	if width < 30 {
		width = 30
	}

	nameStyle := lipgloss.NewStyle().Foreground(special).Bold(true)
	descStyle := lipgloss.NewStyle().Foreground(muted)
	selectedBg := lipgloss.NewStyle().Background(lipgloss.Color("237"))

	var lines []string
	shown := a.items
	if len(shown) > maxAutocompleteItems {
		shown = shown[:maxAutocompleteItems]
	}

	for i, item := range shown {
		name := nameStyle.Render("/" + padRight(item.Name, 12))
		desc := descStyle.Render(item.Desc)
		line := name + desc
		if i == a.cursor {
			line = selectedBg.Render(line)
		}
		lines = append(lines, " "+line)
	}

	border := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(subtle).
		Width(width)

	return border.Render(strings.Join(lines, "\n"))
}

// filterCommands returns commands whose name starts with the given prefix.
func filterCommands(prefix string) []CommandDef {
	if prefix == "" {
		// Show all commands (except "q" alias to avoid clutter)
		var all []CommandDef
		for _, c := range commands {
			if c.Name != "q" {
				all = append(all, c)
			}
		}
		return all
	}
	var matches []CommandDef
	for _, c := range commands {
		if strings.HasPrefix(c.Name, prefix) {
			matches = append(matches, c)
		}
	}
	return matches
}
