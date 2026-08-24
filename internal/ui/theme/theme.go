// Package theme holds every lipgloss.Style used by the app.
//
// Styles are built once at package level. Constructing a style inside View()
// allocates on every row of every frame, which at 20 fps over a few thousand
// rows is enormous GC pressure for no benefit.
package theme

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// Palette is the small set of colours the whole UI draws from.
type Palette struct {
	Base      lipgloss.Style
	Dim       lipgloss.Style
	Accent    lipgloss.Style
	Success   lipgloss.Style
	Warn      lipgloss.Style
	Error     lipgloss.Style
	Highlight lipgloss.Style

	PanelBorder        lipgloss.Style
	PanelBorderFocused lipgloss.Style
	Title              lipgloss.Style
	TitleFocused       lipgloss.Style

	Header    lipgloss.Style
	Footer    lipgloss.Style
	KeyCap    lipgloss.Style
	KeyDesc   lipgloss.Style
	Banner    lipgloss.Style
	Selected  lipgloss.Style
	Cursor    lipgloss.Style
	Count     lipgloss.Style
	Timestamp lipgloss.Style
	Retained  lipgloss.Style
	QoS       lipgloss.Style
	Label     lipgloss.Style
	Value     lipgloss.Style
	Modal     lipgloss.Style
	Toast     [4]lipgloss.Style // indexed by app.Level

	// JSON syntax highlighting for the detail pane. Structure is what makes a
	// nested payload readable, so keys and punctuation are as important to
	// colour as the values.
	JSONKey    lipgloss.Style
	JSONString lipgloss.Style
	JSONNumber lipgloss.Style
	JSONLiter  lipgloss.Style // true, false, null
	JSONPunct  lipgloss.Style
}

// colors is one theme's raw colour set. Everything else is derived, so a new
// theme is a dozen hex values rather than a copy of build().
type colors struct {
	fg        color.Color
	dim       color.Color
	accent    color.Color
	success   color.Color
	warn      color.Color
	err       color.Color
	highlight color.Color
	selBG     color.Color
	// bannerFG is the text colour on the error-coloured banner, which needs
	// contrast against `err` rather than against the terminal background.
	bannerFG color.Color
}

var darkColors = colors{
	fg:        lipgloss.Color("#c8d0e0"),
	dim:       lipgloss.Color("#6b7280"),
	accent:    lipgloss.Color("#7aa2f7"),
	success:   lipgloss.Color("#9ece6a"),
	warn:      lipgloss.Color("#e0af68"),
	err:       lipgloss.Color("#f7768e"),
	highlight: lipgloss.Color("#bb9af7"),
	selBG:     lipgloss.Color("#2a3050"),
	bannerFG:  lipgloss.Color("#1a1b26"),
}

// lightColors is not the dark set inverted. The pastels that read well on a
// near-black background turn to mush on white, so every hue is darkened until
// it carries at least ~4.5:1 against the light background.
var lightColors = colors{
	fg:        lipgloss.Color("#1f2430"),
	dim:       lipgloss.Color("#5c6470"),
	accent:    lipgloss.Color("#2d55c8"),
	success:   lipgloss.Color("#2c7a2c"),
	warn:      lipgloss.Color("#8a5a00"),
	err:       lipgloss.Color("#c02040"),
	highlight: lipgloss.Color("#6a35b8"),
	selBG:     lipgloss.Color("#dbe3f7"),
	bannerFG:  lipgloss.Color("#fdfdfd"),
}

// Dark and Light are the two built palettes. They are handed around as
// pointers: a Palette is roughly 19 KB of lipgloss.Style values, and the root
// model is copied on every Update and carried into a panel.Context on every
// render — passing it by value costs tens of kilobytes of copying per
// keystroke for a set of styles that never changes (§21, pitfall 15).
var (
	Dark  = func() *Palette { p := build(darkColors); return &p }()
	Light = func() *Palette { p := build(lightColors); return &p }()
)

// Default is the palette used before the terminal has reported anything.
var Default = Dark

// For picks a palette from the configured preference. `auto` defers to the
// terminal's reported background, which arrives asynchronously as a
// tea.BackgroundColorMsg — until it does, darkBG carries the assumption.
func For(pref string, darkBG bool) *Palette {
	switch pref {
	case "dark":
		return Dark
	case "light":
		return Light
	default: // "", "auto"
		if darkBG {
			return Dark
		}
		return Light
	}
}

func build(c colors) Palette {
	base := lipgloss.NewStyle().Foreground(c.fg)
	dim := lipgloss.NewStyle().Foreground(c.dim)

	p := Palette{
		Base:      base,
		Dim:       dim,
		Accent:    lipgloss.NewStyle().Foreground(c.accent),
		Success:   lipgloss.NewStyle().Foreground(c.success),
		Warn:      lipgloss.NewStyle().Foreground(c.warn),
		Error:     lipgloss.NewStyle().Foreground(c.err),
		Highlight: lipgloss.NewStyle().Foreground(c.highlight),

		PanelBorder:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.dim),
		PanelBorderFocused: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.accent),
		Title:              lipgloss.NewStyle().Foreground(c.dim).Bold(true),
		TitleFocused:       lipgloss.NewStyle().Foreground(c.accent).Bold(true),

		Header:    lipgloss.NewStyle().Foreground(c.fg),
		Footer:    lipgloss.NewStyle().Foreground(c.dim),
		KeyCap:    lipgloss.NewStyle().Foreground(c.accent).Bold(true),
		KeyDesc:   dim,
		Banner:    lipgloss.NewStyle().Foreground(c.bannerFG).Background(c.err).Bold(true),
		Selected:  lipgloss.NewStyle().Foreground(c.fg).Background(c.selBG),
		Cursor:    lipgloss.NewStyle().Foreground(c.accent).Bold(true),
		Count:     dim,
		Timestamp: dim,
		Retained:  lipgloss.NewStyle().Foreground(c.highlight).Bold(true),
		QoS:       lipgloss.NewStyle().Foreground(c.accent),
		Label:     dim,
		Value:     base,
		Modal:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(c.highlight).Padding(0, 1),

		JSONKey:    lipgloss.NewStyle().Foreground(c.accent),
		JSONString: lipgloss.NewStyle().Foreground(c.success),
		JSONNumber: lipgloss.NewStyle().Foreground(c.warn),
		JSONLiter:  lipgloss.NewStyle().Foreground(c.highlight),
		JSONPunct:  dim,
	}
	p.Toast = [4]lipgloss.Style{
		lipgloss.NewStyle().Foreground(c.fg).Background(c.selBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(c.success).Background(c.selBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(c.warn).Background(c.selBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(c.err).Background(c.selBG).Padding(0, 1),
	}
	return p
}
