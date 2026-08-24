package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

// Detail shows the metadata and payload of one message.
type Detail struct {
	offset int
	height int

	// Wrap soft-wraps the payload instead of truncating each line.
	Wrap bool
	// Pretty holds an asynchronously formatted payload, keyed by sequence
	// number so a stale result is never shown against a newer message.
	Pretty     string
	PrettySeq  uint64
	Formatting bool
}

// SetHeight records the body height.
func (p Detail) SetHeight(h int) Detail {
	p.height = h
	return p
}

// Scroll moves the payload view.
func (p Detail) Scroll(delta int) Detail {
	p.offset += delta
	if p.offset < 0 {
		p.offset = 0
	}
	return p
}

// Reset returns to the top of the payload.
func (p Detail) Reset() Detail {
	p.offset = 0
	return p
}

// View renders the metadata header and the payload body.
func (p Detail) View(ctx Context, m *mqtt.Message, w, h int) string {
	t := ctx.Theme
	if m == nil {
		return t.Dim.Render("  select a topic to see its messages")
	}

	label := func(k, v string) string {
		return t.Label.Render(Pad(k, 10)) + t.Value.Render(Truncate(v, max(w-10, 1)))
	}

	var head []string
	head = append(head, label("topic", sanitize.Topic(m.Topic)))

	size := humanBytes(len(m.Payload))
	if m.Truncated {
		size = fmt.Sprintf("%s of %s (truncated)", humanBytes(len(m.Payload)), humanBytes(m.OrigSize))
	}
	head = append(head, label("meta",
		fmt.Sprintf("qos %d   retain %v   dup %v   size %s", m.QoS, m.Retained, m.Duplicate, size)))

	// Named "received", not "sent": MQTT carries no publish timestamp, and
	// users of GUI clients are routinely confused by this.
	head = append(head, label("received", m.ReceivedAt.Format(timestampFormat(ctx))+" (local)"))

	if m.Props != nil {
		if m.Props.ContentType != "" {
			head = append(head, label("type", m.Props.ContentType))
		}
		if m.Props.ResponseTopic != "" {
			head = append(head, label("resp-to", sanitize.Topic(m.Props.ResponseTopic)))
		}
		if m.Props.MessageExpiry != nil {
			head = append(head, label("expiry", fmt.Sprintf("%ds", *m.Props.MessageExpiry)))
		}
		for _, u := range m.Props.User {
			head = append(head, label("user", sanitize.Topic(u.Key)+"="+sanitize.Topic(u.Value)))
		}
	}
	head = append(head, t.Dim.Render(strings.Repeat("─", max(w, 0))))

	body := p.payloadLines(ctx, m, w, max(h-len(head), 1))
	return strings.Join(append(head, body...), "\n")
}

func (p Detail) payloadLines(ctx Context, m *mqtt.Message, w, h int) []string {
	if p.Formatting {
		return []string{ctx.Theme.Dim.Render("  formatting…")}
	}
	text := ""
	if p.Pretty != "" && p.PrettySeq == m.Seq {
		text = p.Pretty
	} else {
		text = sanitize.Block(m.Payload)
	}
	if text == "" {
		return []string{ctx.Theme.Dim.Render("  (empty payload)")}
	}

	lines := strings.Split(text, "\n")
	if p.Wrap {
		lines = strings.Split(lipgloss.Wrap(text, max(w, 1), " -"), "\n")
	}

	// Render only the visible window; a 1 MiB payload is 30,000 lines and
	// none but h of them can be seen.
	from := min(p.offset, max(len(lines)-1, 0))
	to := min(from+h, len(lines))
	out := make([]string, 0, to-from)
	for _, l := range lines[from:to] {
		out = append(out, ctx.Theme.Value.Render(Truncate(l, w)))
	}
	return out
}

func humanBytes(n int) string {
	switch {
	case n < 1024:
		return fmt.Sprintf("%d B", n)
	case n < 1024*1024:
		return fmt.Sprintf("%.1f KiB", float64(n)/1024)
	default:
		return fmt.Sprintf("%.1f MiB", float64(n)/(1024*1024))
	}
}
