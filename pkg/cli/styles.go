package cli

import "github.com/charmbracelet/lipgloss"

// Colors
var (
	subtle    = lipgloss.AdaptiveColor{Light: "#D9DCCF", Dark: "#383838"}
	highlight = lipgloss.AdaptiveColor{Light: "#874BFD", Dark: "#7D56F4"}
	special   = lipgloss.AdaptiveColor{Light: "#43BF6D", Dark: "#73F59F"}
	warning   = lipgloss.AdaptiveColor{Light: "#FFA500", Dark: "#FFB347"}
)

// Styles
var (
	titleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			MarginLeft(2)

	itemStyle = lipgloss.NewStyle().
			PaddingLeft(4)

	selectedItemStyle = lipgloss.NewStyle().
				PaddingLeft(2).
				Foreground(highlight).
				Bold(true)

	helpStyle = lipgloss.NewStyle().
			Foreground(subtle).
			PaddingLeft(4).
			PaddingTop(1)

	statusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FFFDF5")).
			Background(lipgloss.Color("#6124DF")).
			Padding(0, 1)

	modalStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(highlight).
			Padding(1, 2).
			Width(80)

	modalTitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlight).
			MarginBottom(1)

	labelStyle = lipgloss.NewStyle().
			Foreground(subtle).
			Width(20)

	valueStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("#FAFAFA"))

	phaseDetectedStyle = lipgloss.NewStyle().
				Foreground(warning).
				Bold(true)

	phaseResolvedStyle = lipgloss.NewStyle().
				Foreground(special).
				Bold(true)
)
