package tui

import (
	"github.com/charmbracelet/lipgloss"
)

// Styles contains all the styling for the TUI.
type Styles struct {
	// Main containers
	App          lipgloss.Style
	Title        lipgloss.Style
	HostList     lipgloss.Style
	Help         lipgloss.Style
	Error        lipgloss.Style
	SearchPrompt lipgloss.Style

	// Host items
	HostItem       lipgloss.Style
	HostItemCursor lipgloss.Style
	HostItemDim    lipgloss.Style

	// Host details
	HostName lipgloss.Style
	HostAddr lipgloss.Style
	HostInfo lipgloss.Style

	// Mode selector
	ModePrompt   lipgloss.Style
	ModeOption   lipgloss.Style
	ModeSelected lipgloss.Style
}

// DefaultStyles returns the default styling.
func DefaultStyles() Styles {
	var styles Styles

	// Color palette
	primaryColor := lipgloss.Color("86")   // Cyan
	secondaryColor := lipgloss.Color("98") // Purple
	errorColor := lipgloss.Color("196")    // Red
	dimColor := lipgloss.Color("241")      // Gray

	// Main containers
	styles.App = lipgloss.NewStyle().
		Padding(1, 2).
		Border(lipgloss.RoundedBorder()).
		BorderForeground(primaryColor)

	styles.Title = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true)

	styles.HostList = lipgloss.NewStyle().
		MarginTop(1).
		MarginBottom(1)

	styles.Help = lipgloss.NewStyle().
		Foreground(dimColor).
		MarginTop(1)

	styles.Error = lipgloss.NewStyle().
		Foreground(errorColor).
		Bold(true)

	styles.SearchPrompt = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true)

	// Host items
	styles.HostItem = lipgloss.NewStyle().
		PaddingLeft(1)

	styles.HostItemCursor = lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(lipgloss.Color("black")).
		Background(primaryColor).
		Bold(true)

	styles.HostItemDim = lipgloss.NewStyle().
		PaddingLeft(1).
		Foreground(dimColor)

	// Host details
	styles.HostName = lipgloss.NewStyle().
		Foreground(secondaryColor).
		Bold(true)

	styles.HostAddr = lipgloss.NewStyle().
		Foreground(dimColor)

	styles.HostInfo = lipgloss.NewStyle().
		Foreground(lipgloss.Color("242"))

	// Mode selector
	styles.ModePrompt = lipgloss.NewStyle().
		Foreground(primaryColor).
		Bold(true).
		MarginTop(1)

	styles.ModeOption = lipgloss.NewStyle().
		PaddingLeft(1).
		PaddingRight(1)

	styles.ModeSelected = lipgloss.NewStyle().
		PaddingLeft(1).
		PaddingRight(1).
		Foreground(primaryColor).
		Bold(true)

	return styles
}

// WithWidth updates styles to use the specified width.
func (s Styles) WithWidth(width int) Styles {
	// Use full terminal width (bubbletea handles terminal width automatically)
	// We don't need to subtract padding as lipgloss handles that

	s.HostList = s.HostList.Width(width)
	s.HostItem = s.HostItem.Width(width)
	s.HostItemCursor = s.HostItemCursor.Width(width)
	s.HostItemDim = s.HostItemDim.Width(width)

	return s
}
