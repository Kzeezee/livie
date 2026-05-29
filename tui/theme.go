package tui

import "charm.land/lipgloss/v2"

// Colour palette — anchored to #2B2D42
const (
	ColBase         = "#1A1B2E"
	ColSurface      = "#2B2D42"
	ColSurfaceHi    = "#3D3F5C"
	ColBorder       = "#4A4D6A"
	ColTextPrimary  = "#E2E8F0"
	ColTextSecondary = "#8D99AE"
	ColTextMuted    = "#4A5568"
	ColAccentCyan   = "#4CC9F0"
	ColAccentRose   = "#E94560"
	ColAccentAmber  = "#F6A623"
	ColAccentGreen  = "#68D391"
	ColAccentPurple = "#9B72CF"
)

// Styles — defined once, used everywhere
var (
	StyleBase = lipgloss.NewStyle().
			Background(lipgloss.Color(ColBase)).
			Foreground(lipgloss.Color(ColTextPrimary))

	StyleSurface = lipgloss.NewStyle().
			Background(lipgloss.Color(ColSurface)).
			Foreground(lipgloss.Color(ColTextPrimary))

	StyleBorder = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color(ColBorder)).
			Padding(0, 1)

	StyleBorderFocusQuery = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(ColAccentCyan)).
				Padding(0, 1)

	StyleBorderFocusBash = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color(ColAccentRose)).
				Padding(0, 1)

	StyleLabel = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColTextSecondary)).
			Bold(false)

	StyleValue = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColTextPrimary)).
			Bold(true)

	StyleDim = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColTextMuted)).
			Italic(true)

	StyleMuted = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColTextMuted))

	StyleAccentCyan = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentCyan)).
			Bold(true)

	StyleAccentRose = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentRose)).
			Bold(true)

	StyleAccentAmber = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentAmber)).
			Bold(true)

	StyleAccentGreen = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentGreen)).
			Bold(true)

	StyleAccentPurple = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentPurple)).
			Bold(true)

	StyleCommand = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentPurple)).
			Bold(true)

	StyleCode = lipgloss.NewStyle().
			Background(lipgloss.Color(ColSurface)).
			Foreground(lipgloss.Color(ColAccentCyan)).
			Padding(0, 1)

	StyleHUD = lipgloss.NewStyle().
			Background(lipgloss.Color(ColSurface)).
			Foreground(lipgloss.Color(ColTextSecondary)).
			Padding(0, 1)

	StyleDivider = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColBorder))

	// Mode badges
	StyleModeBadgeQuery = lipgloss.NewStyle().
				Background(lipgloss.Color(ColSurface)).
				Foreground(lipgloss.Color(ColAccentCyan)).
				Bold(true).
				Padding(0, 1)

	StyleModeBadgeBash = lipgloss.NewStyle().
				Background(lipgloss.Color(ColSurface)).
				Foreground(lipgloss.Color(ColAccentRose)).
				Bold(true).
				Padding(0, 1)

	// Message prefixes
	StyleMsgUser = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentCyan)).
			Bold(true)

	StyleMsgAssistant = lipgloss.NewStyle().
				Foreground(lipgloss.Color(ColAccentPurple)).
				Bold(true)

	StyleMsgSystem = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColTextMuted)).
			Italic(true)

	StyleMsgError = lipgloss.NewStyle().
			Foreground(lipgloss.Color(ColAccentRose)).
			Bold(true)
)
