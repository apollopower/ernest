package tui

import "github.com/charmbracelet/lipgloss"

var (
	// Colors — adaptive, works on light and dark terminals
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	muted     = lipgloss.AdaptiveColor{Light: "#969B86", Dark: "#626262"}

	// Status bar
	statusBarStyle = lipgloss.NewStyle().
			Background(lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}).
			Foreground(lipgloss.AdaptiveColor{Light: "#FFFDF5", Dark: "#FAFAFA"}).
			Padding(0, 1)

	statusProviderStyle = lipgloss.NewStyle().
				Background(lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}).
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#1a1a1a"}).
				Padding(0, 1).
				Bold(true)

	// Chat messages
	userMsgStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#FAFAFA"}).
			Bold(true)

	userLabelStyle = lipgloss.NewStyle().
			Foreground(highlight).
			Bold(true)

	assistantLabelStyle = lipgloss.NewStyle().
				Foreground(special).
				Bold(true)

	assistantMsgStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#DDDDDD"})

	// Input box
	inputFocusedStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(highlight).
				Padding(0, 1)

	inputBlurredStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(muted).
				Padding(0, 1)

	// Tool events
	toolLabelStyle = lipgloss.NewStyle().
			Foreground(lipgloss.AdaptiveColor{Light: "#D4A017", Dark: "#FFD700"}).
			Bold(true)

	toolContentStyle = lipgloss.NewStyle().
				Foreground(muted)

	// Tool confirmation dialog
	toolConfirmTitleStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#D4A017", Dark: "#FFD700"}).
				Bold(true)

	toolConfirmNameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#FAFAFA"}).
				Bold(true)

	toolConfirmInputStyle = lipgloss.NewStyle().
				Foreground(muted)

	toolConfirmPromptStyle = lipgloss.NewStyle().
				Foreground(lipgloss.AdaptiveColor{Light: "#1a1a1a", Dark: "#DDDDDD"})

	// Help text
	helpStyle = lipgloss.NewStyle().
			Foreground(muted)
)
