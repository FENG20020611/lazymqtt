// Package panel holds the individual UI panels. Each is a plain struct
// following a local convention rather than an interface: Bubble Tea's value
// semantics make interfaces awkward here and you would spend your life type
// asserting.
package panel

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/store"
	"github.com/Onizuka893/lazymqtt/internal/ui/keys"
	"github.com/Onizuka893/lazymqtt/internal/ui/theme"
)

// Context carries the shared read-only handles so panels do not each keep a
// copy of them.
type Context struct {
	Store  *store.Store
	Theme  *theme.Palette
	Keys   *keys.Map
	Config config.Config

	// Paused freezes the view without touching the connection.
	Paused bool
	// SelectedTopic is the topic the tree cursor sits on.
	SelectedTopic string
}

// Box draws a bordered panel with a title, sized to exactly w by h cells
// including the border.
func Box(t *theme.Palette, focused bool, title, body string, w, h int) string {
	if w < 4 || h < 3 {
		// Degenerate terminal: render the body unadorned rather than panic.
		return truncateBlock(body, max(w, 0), max(h, 0))
	}
	border := t.PanelBorder
	titleStyle := t.Title
	if focused {
		border = t.PanelBorderFocused
		titleStyle = t.TitleFocused
	}
	// The border consumes two rows and the title one, so the body gets h-3 —
	// the same arithmetic BoxInner reports to the panels.
	inner := w - 2
	body = fitBlock(body, inner, h-3)

	head := truncate(title, inner)
	// lipgloss sizes a bordered block by its outer dimensions, so Width and
	// Height take w and h while the content is built at w-2 by h-3.
	return border.Width(w).Height(h).Render(
		titleStyle.Render(head) + "\n" + body,
	)
}

// BoxInner returns the content area a Box of the given outer size provides.
// The title consumes one of those rows.
func BoxInner(w, h int) (int, int) {
	return max(w-2, 0), max(h-3, 0)
}

// fitBlock pads or truncates a block to exactly w by h cells.
func fitBlock(s string, w, h int) string {
	lines := strings.Split(s, "\n")
	if len(lines) > h {
		lines = lines[:h]
	}
	var b strings.Builder
	b.Grow(w * h)
	for i := 0; i < h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i < len(lines) {
			b.WriteString(pad(truncate(lines[i], w), w))
		} else {
			b.WriteString(strings.Repeat(" ", w))
		}
	}
	return b.String()
}

func truncateBlock(s string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	return fitBlock(s, w, h)
}

// truncate cuts a string to w display cells, appending an ellipsis when it
// had to cut. It is width-aware, so wide characters and emoji do not overflow
// the panel.
func truncate(s string, w int) string {
	if w <= 0 {
		return ""
	}
	if lipgloss.Width(s) <= w {
		return s
	}
	if w == 1 {
		return "…"
	}
	var b strings.Builder
	used := 0
	for _, r := range s {
		rw := lipgloss.Width(string(r))
		if used+rw > w-1 {
			break
		}
		b.WriteRune(r)
		used += rw
	}
	b.WriteString("…")
	return b.String()
}

// Truncate is the exported form, used by panels building their own rows.
func Truncate(s string, w int) string { return truncate(s, w) }

// pad right-pads a string to w display cells.
func pad(s string, w int) string {
	d := w - lipgloss.Width(s)
	if d <= 0 {
		return s
	}
	return s + strings.Repeat(" ", d)
}

// Pad is the exported form.
func Pad(s string, w int) string { return pad(s, w) }

// Indents is a precomputed table of indent prefixes, so the tree renderer
// never calls strings.Repeat per row per frame.
var Indents = func() []string {
	out := make([]string, 32)
	for i := range out {
		out[i] = strings.Repeat("  ", i)
	}
	return out
}()

// Indent returns the prefix for a depth, clamped to the table.
func Indent(depth int) string {
	if depth < 0 {
		return ""
	}
	if depth >= len(Indents) {
		return Indents[len(Indents)-1]
	}
	return Indents[depth]
}

func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
