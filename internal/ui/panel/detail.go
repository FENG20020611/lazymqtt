package panel

import (
	"bytes"
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

	// Format is the user's preference for pretty-printing JSON, toggled with
	// `F`. It is a session-wide preference rather than per-message state:
	// someone watching JSON telemetry wants every payload indented, not to
	// press a key on each one.
	Format bool
	// Pretty holds an asynchronously formatted payload, keyed by sequence
	// number so a stale result is never shown against a newer message. It is
	// empty when the payload was not JSON, and PrettySeq still records the
	// attempt so it is not made again.
	Pretty    string
	PrettySeq uint64
	// PrettyJSON reports that Pretty is JSON and should be highlighted.
	PrettyJSON bool
	// PendingSeq is the message a format is in flight for, so a repeated
	// selection does not spawn a goroutine per frame. Formatting drives the
	// on-screen indicator and is only set for an explicit `F`.
	PendingSeq uint64
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

// maxWrapBytes bounds the payload handed to the soft-wrapper.
//
// Wrapping has to see a whole logical line to decide where to break it, so
// unlike the truncating path it cannot be windowed by line. 128 KiB of text
// is far more than any terminal can display and takes well under a
// millisecond to reflow, which keeps the wrap toggle instant on a payload of
// any size.
const maxWrapBytes = 128 << 10

func (p Detail) payloadLines(ctx Context, m *mqtt.Message, w, h int) []string {
	if p.Formatting {
		return []string{ctx.Theme.Dim.Render("  formatting…")}
	}
	raw := m.Payload
	json := false
	if p.Format && p.Pretty != "" && p.PrettySeq == m.Seq {
		raw, json = []byte(p.Pretty), p.PrettyJSON
	}
	if len(raw) == 0 {
		return []string{ctx.Theme.Dim.Render("  (empty payload)")}
	}

	// Window the RAW bytes before sanitising them. Sanitising first and
	// slicing afterwards is the obvious order and it is the wrong one: it
	// decodes, validates and re-encodes every byte of the payload on every
	// frame to display at most h lines of it. On a 10 MB payload that is
	// seconds per frame, and the app appears to hang while scrolling.
	var text string
	if p.Wrap {
		clipped := raw
		if len(clipped) > maxWrapBytes {
			clipped = clipped[:maxWrapBytes]
		}
		text = lipgloss.Wrap(sanitize.Block(clipped), max(w, 1), " -")
	} else {
		// Each line is truncated to w columns anyway, so there is no reason
		// to carry more than a generous multiple of that. Four bytes per
		// column covers the widest UTF-8 rune.
		text = sanitize.Block(lineWindow(raw, p.offset, h, max(w, 1)*4+64))
	}

	lines := strings.Split(text, "\n")
	from := 0
	if p.Wrap {
		from = min(p.offset, max(len(lines)-1, 0))
	}
	to := min(from+h, len(lines))
	out := make([]string, 0, to-from)
	inString := false
	for _, l := range lines[from:to] {
		// Truncate before highlighting, never after: the styled string
		// carries escape sequences that Truncate would count as columns and
		// cut through.
		l = Truncate(l, w)
		if json {
			var styled string
			styled, inString = highlightJSON(ctx.Theme, l, inString)
			// A string only continues across rows when the soft-wrapper
			// split one. Without wrapping, an unterminated string means the
			// line was truncated, and carrying that state would paint every
			// following row as string-coloured.
			inString = inString && p.Wrap
			out = append(out, styled)
			continue
		}
		out = append(out, ctx.Theme.Value.Render(l))
	}
	return out
}

// lineWindow returns the bytes of at most count newline-separated lines
// starting at line `from`, clipping any single line to maxLine bytes.
//
// The scan is a plain memchr walk over the payload with no allocation beyond
// the window itself, so the cost is proportional to what is displayed rather
// than to the size of the message.
func lineWindow(raw []byte, from, count, maxLine int) []byte {
	if count < 1 {
		count = 1
	}
	out := make([]byte, 0, count*min(maxLine, 4096))
	line := 0
	for len(raw) > 0 && line < from+count {
		end := bytes.IndexByte(raw, '\n')
		var cur []byte
		if end < 0 {
			cur, raw = raw, nil
		} else {
			cur, raw = raw[:end], raw[end+1:]
		}
		if line >= from {
			if len(cur) > maxLine {
				cur = cur[:maxLine]
			}
			if len(out) > 0 {
				out = append(out, '\n')
			}
			out = append(out, cur...)
		}
		line++
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
