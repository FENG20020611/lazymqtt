package panel

import (
	"fmt"
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

// Subscriptions lists what was asked for and what the broker granted.
//
// It gets its own panel rather than being implicit because a QoS downgrade is
// common and otherwise silent.
type Subscriptions struct {
	cursor int
	offset int
	height int
}

// SetHeight records the body height.
func (p Subscriptions) SetHeight(h int) Subscriptions {
	p.height = h
	return p
}

// Move shifts the cursor.
func (p Subscriptions) Move(subs []mqtt.Subscription, delta int) Subscriptions {
	if len(subs) == 0 {
		p.cursor = 0
		return p
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(subs) {
		p.cursor = len(subs) - 1
	}
	h := max(p.height, 1)
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h {
		p.offset = p.cursor - h + 1
	}
	return p
}

// Selected returns the subscription under the cursor, or nil.
func (p Subscriptions) Selected(subs []mqtt.Subscription) *mqtt.Subscription {
	if p.cursor < 0 || p.cursor >= len(subs) {
		return nil
	}
	return &subs[p.cursor]
}

// View renders the subscription list.
func (p Subscriptions) View(ctx Context, subs []mqtt.Subscription, w, h int) string {
	if len(subs) == 0 {
		return ctx.Theme.Dim.Render("  none — press s to subscribe")
	}
	t := ctx.Theme
	end := min(p.offset+h, len(subs))

	var b strings.Builder
	b.Grow(w * h)
	for i := p.offset; i < end; i++ {
		if i > p.offset {
			b.WriteByte('\n')
		}
		s := subs[i]

		status := "○"
		style := t.Dim
		switch {
		case s.Err != nil:
			status, style = "✗", t.Error
		case s.Active && s.GrantedQoS < s.QoS:
			// The silent downgrade, made loud.
			status, style = "!", t.Warn
		case s.Active:
			status, style = "●", t.Success
		}

		qos := fmt.Sprintf("q%d", s.QoS)
		if s.Active && s.GrantedQoS != s.QoS {
			qos = fmt.Sprintf("q%d→%d", s.QoS, s.GrantedQoS)
		}

		count := ""
		if s.Count > 0 {
			count = formatCount(s.Count)
		}

		left := " " + status + " " + sanitize.Topic(s.Filter)
		right := qos + " " + count
		line := Pad(Truncate(left, max(w-len(right)-1, 1)), max(w-len(right)-1, 1)) + " " + right

		if i == p.cursor {
			b.WriteString(t.Selected.Render(Pad(Truncate(line, w), w)))
		} else {
			b.WriteString(style.Render(Truncate(line, w)))
		}
	}
	return b.String()
}
