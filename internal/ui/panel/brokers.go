package panel

import (
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/config"
)

// Brokers is the profile picker opened with `c`.
type Brokers struct {
	cursor int
	names  []string
}

// NewBrokers builds a picker over the configured profiles.
func NewBrokers(cfg config.Config, current string) Brokers {
	b := Brokers{names: cfg.Names()}
	for i, n := range b.names {
		if n == current {
			b.cursor = i
		}
	}
	return b
}

// Empty reports whether there is nothing to pick from.
func (p Brokers) Empty() bool { return len(p.names) == 0 }

// Move shifts the cursor.
func (p Brokers) Move(delta int) Brokers {
	if len(p.names) == 0 {
		return p
	}
	p.cursor = (p.cursor + delta + len(p.names)) % len(p.names)
	return p
}

// Selected returns the highlighted profile name.
func (p Brokers) Selected() string {
	if p.cursor < 0 || p.cursor >= len(p.names) {
		return ""
	}
	return p.names[p.cursor]
}

// View renders the picker.
func (p Brokers) View(ctx Context, cfg config.Config, w int) string {
	t := ctx.Theme
	if len(p.names) == 0 {
		return t.Dim.Render("no broker profiles configured\nrun: lazymqtt config init")
	}
	var b strings.Builder
	for i, name := range p.names {
		if i > 0 {
			b.WriteByte('\n')
		}
		br := cfg.Brokers[name]
		addr, err := br.ServerURL()
		if err != nil {
			addr = "?"
		}
		line := " " + Pad(name, 16) + " " + addr
		if i == p.cursor {
			b.WriteString(t.Selected.Render(Pad(Truncate(line, w), w)))
		} else {
			b.WriteString(t.Base.Render(Truncate(line, w)))
		}
	}
	b.WriteString("\n" + t.Dim.Render("↵ connect  esc cancel"))
	return b.String()
}
