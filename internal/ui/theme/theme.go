// Package theme holds every lipgloss.Style used by the app.
//
// Styles are built once at package level. Constructing a style inside View()
// allocates on every row of every frame, which at 20 fps over a few thousand
// rows is enormous GC pressure for no benefit.
package theme

import "charm.land/lipgloss/v2"

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
}

var (
	colFg        = lipgloss.Color("#c8d0e0")
	colDim       = lipgloss.Color("#6b7280")
	colAccent    = lipgloss.Color("#7aa2f7")
	colSuccess   = lipgloss.Color("#9ece6a")
	colWarn      = lipgloss.Color("#e0af68")
	colError     = lipgloss.Color("#f7768e")
	colHighlight = lipgloss.Color("#bb9af7")
	colSelBG     = lipgloss.Color("#2a3050")
)

// Default is the single palette instance the UI renders through.
//
// It is handed around as a pointer. A Palette is roughly 19 KB of
// lipgloss.Style values, and the root model is copied on every Update and
// carried into a panel.Context on every render — passing it by value costs
// tens of kilobytes of copying per keystroke for a set of styles that never
// changes (§21, pitfall 15).
var Default = func() *Palette { p := build(); return &p }()

func build() Palette {
	base := lipgloss.NewStyle().Foreground(colFg)
	dim := lipgloss.NewStyle().Foreground(colDim)

	p := Palette{
		Base:      base,
		Dim:       dim,
		Accent:    lipgloss.NewStyle().Foreground(colAccent),
		Success:   lipgloss.NewStyle().Foreground(colSuccess),
		Warn:      lipgloss.NewStyle().Foreground(colWarn),
		Error:     lipgloss.NewStyle().Foreground(colError),
		Highlight: lipgloss.NewStyle().Foreground(colHighlight),

		PanelBorder:        lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colDim),
		PanelBorderFocused: lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colAccent),
		Title:              lipgloss.NewStyle().Foreground(colDim).Bold(true),
		TitleFocused:       lipgloss.NewStyle().Foreground(colAccent).Bold(true),

		Header:    lipgloss.NewStyle().Foreground(colFg),
		Footer:    lipgloss.NewStyle().Foreground(colDim),
		KeyCap:    lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		KeyDesc:   dim,
		Banner:    lipgloss.NewStyle().Foreground(lipgloss.Color("#1a1b26")).Background(colError).Bold(true),
		Selected:  lipgloss.NewStyle().Foreground(colFg).Background(colSelBG),
		Cursor:    lipgloss.NewStyle().Foreground(colAccent).Bold(true),
		Count:     dim,
		Timestamp: dim,
		Retained:  lipgloss.NewStyle().Foreground(colHighlight).Bold(true),
		QoS:       lipgloss.NewStyle().Foreground(colAccent),
		Label:     dim,
		Value:     base,
		Modal:     lipgloss.NewStyle().Border(lipgloss.RoundedBorder()).BorderForeground(colHighlight).Padding(0, 1),
	}
	p.Toast = [4]lipgloss.Style{
		lipgloss.NewStyle().Foreground(colFg).Background(colSelBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(colSuccess).Background(colSelBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(colWarn).Background(colSelBG).Padding(0, 1),
		lipgloss.NewStyle().Foreground(colError).Background(colSelBG).Padding(0, 1),
	}
	return p
}
