// Package ui holds presentational primitives shared by all commands.
package ui

import "github.com/charmbracelet/lipgloss"

// Palette. Tuned for red-team / blue-team / synthesis brand.
var (
	ColorRed    = lipgloss.Color("#FF3B3B")
	ColorBlue   = lipgloss.Color("#2D8CFF")
	ColorPurple = lipgloss.Color("#B14AED")
	ColorGreen  = lipgloss.Color("#3DD68C")
	ColorAmber  = lipgloss.Color("#F2A93B")
	ColorGrey   = lipgloss.Color("#6E7681")
)

// Re-usable styles.
var (
	BrandStyle = lipgloss.NewStyle().Foreground(ColorPurple).Bold(true)
	OKStyle    = lipgloss.NewStyle().Foreground(ColorGreen).Bold(true)
	WarnStyle  = lipgloss.NewStyle().Foreground(ColorAmber).Bold(true)
	ErrStyle   = lipgloss.NewStyle().Foreground(ColorRed).Bold(true)
	InfoStyle  = lipgloss.NewStyle().Foreground(ColorBlue)
	DimStyle   = lipgloss.NewStyle().Foreground(ColorGrey)
	BoxStyle   = lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).Padding(0, 1)
)

// StateStyle picks a style by scan state string ("RUNNING", "COMPLETED", …).
// Unknown states fall back to InfoStyle.
func StateStyle(state string) lipgloss.Style {
	switch state {
	case "COMPLETED":
		return OKStyle
	case "FAILED", "CANCELLED":
		return ErrStyle
	case "PARTIAL":
		return WarnStyle
	case "RUNNING", "PENDING":
		return InfoStyle
	}
	return InfoStyle
}
