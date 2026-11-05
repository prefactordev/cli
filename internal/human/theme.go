// Package human renders the CLI's output for people at a terminal: tables,
// cards, bar charts and timelines in colour. Output for scripts and coding
// agents stays JSON; the caller decides which applies.
package human

import (
	"image/color"
	"io"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/colorprofile"
)

// Palette, chosen to read on dark and light backgrounds alike.
var (
	violet = lipgloss.Color("#A78BFA")
	indigo = lipgloss.Color("#818CF8")
	sky    = lipgloss.Color("#38BDF8")
	green  = lipgloss.Color("#34D399")
	amber  = lipgloss.Color("#FBBF24")
	red    = lipgloss.Color("#F87171")
	grey   = lipgloss.Color("#8B8B9A")
)

// Theme holds the styles output is drawn with.
type Theme struct {
	Heading   lipgloss.Style
	Command   lipgloss.Style
	Option    lipgloss.Style
	Muted     lipgloss.Style
	Accent    lipgloss.Style
	Success   lipgloss.Style
	Warning   lipgloss.Style
	Danger    lipgloss.Style
	Title     lipgloss.Style
	Bar       lipgloss.Style
	BarTrack  lipgloss.Style
	TableEdge lipgloss.Style
}

// DefaultTheme is the CLI's theme.
func DefaultTheme() Theme {
	s := lipgloss.NewStyle
	return Theme{
		Heading:   s().Bold(true),
		Command:   s().Foreground(sky),
		Option:    s().Foreground(indigo),
		Muted:     s().Foreground(grey),
		Accent:    s().Foreground(violet).Bold(true),
		Success:   s().Foreground(green),
		Warning:   s().Foreground(amber),
		Danger:    s().Foreground(red),
		Title:     s().Bold(true).Foreground(violet),
		Bar:       s().Foreground(violet),
		BarTrack:  s().Foreground(grey).Faint(true),
		TableEdge: s().Foreground(grey),
	}
}

// Writer returns w adapted to the colours the environment supports: NO_COLOR
// removes colour, CLICOLOR_FORCE keeps it, TERM picks how many colours. The
// caller has already decided w is a terminal.
func Writer(w io.Writer, environ []string) io.Writer {
	return &colorprofile.Writer{Forward: w, Profile: colorprofile.Env(environ)}
}

// statusColour maps a status-like value to a colour, or nil to leave it.
func statusColour(value string) color.Color {
	switch strings.ToLower(value) {
	case "active", "success", "complete", "completed", "delivered", "present", "yes", "enabled", "healthy", "full_access":
		return green
	case "pending", "preparing", "warning", "paused", "suspended", "draft", "running", "in_progress", "read_only":
		return amber
	case "failed", "error", "critical", "terminated", "revoked", "raised", "missing", "no", "high", "expired":
		return red
	case "cancelled", "canceled", "retired", "cleared", "archived", "deleted", "info", "inactive", "low":
		return grey
	}
	return nil
}

// Status draws a status-like value in its colour.
func (t Theme) Status(value string) string {
	if c := statusColour(value); c != nil {
		return lipgloss.NewStyle().Foreground(c).Render(value)
	}
	return value
}
