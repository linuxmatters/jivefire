package cli

import (
	"fmt"
	"os"

	"github.com/charmbracelet/lipgloss"
)

// Color palette
var (
	primaryColor   = lipgloss.Color("#A40000") // Jivefire red
	accentColor    = lipgloss.Color("#FFA500") // Orange/gold
	mutedColor     = lipgloss.Color("#888888") // Gray
	highlightColor = lipgloss.Color("#FFFF00") // Yellow
	textColor      = lipgloss.Color("#FFFFFF") // White
)

// Styles
var (
	// Title style - bold red with fire emoji
	TitleStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor).
			MarginBottom(1)

	// Section header style
	HeaderStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(accentColor).
			MarginTop(1).
			MarginBottom(1)

	// Error message style
	ErrorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(primaryColor)

	// Highlight style for important values
	HighlightStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(highlightColor)

	// Key-value pair styles
	KeyStyle = lipgloss.NewStyle().
			Foreground(mutedColor)

	ValueStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(textColor)
)

// PrintVersion prints version information
func PrintVersion(version string) {
	fmt.Println(TitleStyle.Render("Jivefire 🔥"))
	fmt.Printf("%s %s\n", KeyStyle.Render("Version:"), ValueStyle.Render(version))
}

// EncoderInfo holds information about a hardware encoder for display
type EncoderInfo struct {
	Name        string
	Description string
	Available   bool
}

// PrintHardwareProbe prints a styled hardware encoder probe result
func PrintHardwareProbe(encoders []EncoderInfo) {
	fmt.Println(TitleStyle.Render("Jivefire 🔥"))
	fmt.Println(HeaderStyle.Render("Hardware Encoder Probe"))

	for _, enc := range encoders {
		var status string
		if enc.Available {
			status = HighlightStyle.Render("✓ available")
		} else {
			status = ErrorStyle.Render("✗ not available")
		}
		fmt.Printf("  %s (%s): %s\n",
			ValueStyle.Render(enc.Description),
			KeyStyle.Render(enc.Name),
			status)
	}
	fmt.Println()
}

// PrintError prints an error message
func PrintError(message string) {
	fmt.Fprintf(os.Stderr, "%s %s\n", ErrorStyle.Render("Error:"), message)
}
