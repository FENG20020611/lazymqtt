package panel

import (
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/store"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

// Messages lists the recent messages on the selected topic.
//
// Showing a list rather than only the latest payload is what makes the app
// useful for anything time-varying, which is most of MQTT.
type Messages struct {
	cursor int
	offset int
	height int

	// anchor is the Seq of the selected message. Tracking the message rather
	// than its index is what keeps the selection still: index 0 means "the
	// newest", so every arrival silently reassigns every index and the
	// selection slides onto its neighbour. Same reasoning as the tree panel,
	// where the anchor is the topic string.
	anchor uint64

	// Autoscroll sticks the view to the newest entry. It switches itself off
	// when the user scrolls up, like a chat client, and back on at G.
	Autoscroll bool
	topic      string
}

// NewMessages returns a message panel with autoscroll on.
func NewMessages() Messages { return Messages{Autoscroll: true} }

// SetHeight records the body height.
func (p Messages) SetHeight(h int) Messages {
	p.height = h
	return p
}

// SetTopic switches to another topic, resetting the cursor to the newest
// message.
func (p Messages) SetTopic(topic string) Messages {
	if p.topic == topic {
		return p
	}
	p.topic = topic
	p.cursor, p.offset, p.anchor = 0, 0, 0
	p.Autoscroll = true
	return p
}

// Topic returns the topic currently displayed.
func (p Messages) Topic() string { return p.topic }

// Cursor returns the cursor index, counting from the newest message.
func (p Messages) Cursor() int { return p.cursor }

func ring(ctx Context, topic string) *store.Ring[*mqtt.Message] {
	if topic == "" {
		return ctx.Store.Stream()
	}
	n := ctx.Store.Node(topic)
	if n == nil || n.History == nil {
		return nil
	}
	return n.History
}

// Len returns how many messages are available for the current topic.
func (p Messages) Len(ctx Context) int {
	r := ring(ctx, p.topic)
	if r == nil {
		return 0
	}
	return r.Len()
}

// Move shifts the cursor. Scrolling away from the newest entry turns
// autoscroll off.
func (p Messages) Move(ctx Context, delta int) Messages {
	n := p.Len(ctx)
	if n == 0 {
		return p
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= n {
		p.cursor = n - 1
	}
	if p.cursor > 0 {
		p.Autoscroll = false
	}
	return p.anchorHere(ctx).clampOffset(n)
}

// anchorHere records the Seq of the message now under the cursor.
func (p Messages) anchorHere(ctx Context) Messages {
	if m := p.Current(ctx); m != nil {
		p.anchor = m.Seq
	}
	return p
}

// Resync re-resolves the cursor from the anchored message after the ring has
// changed underneath it. Called on every ingest.
func (p Messages) Resync(ctx Context) Messages {
	r := ring(ctx, p.topic)
	if r == nil || r.Len() == 0 {
		p.cursor, p.offset = 0, 0
		return p
	}
	if p.Autoscroll {
		// Following the tail: the newest message is always the selection.
		p.cursor, p.offset = 0, 0
		return p.anchorHere(ctx)
	}
	if p.anchor == 0 {
		return p.clampOffset(r.Len())
	}
	// Seq is monotonic and the ring is oldest-first, so scanning back from the
	// newest finds a recent selection immediately.
	for i := r.Len() - 1; i >= 0; i-- {
		if m := r.At(i); m != nil && m.Seq == p.anchor {
			p.cursor = r.Len() - 1 - i
			return p.clampOffset(r.Len())
		}
	}
	// The anchored message aged out of the ring; hold at the oldest one left.
	p.cursor = r.Len() - 1
	return p.anchorHere(ctx).clampOffset(r.Len())
}

// Top jumps to the newest message and re-enables autoscroll.
func (p Messages) Top(ctx Context) Messages {
	p.cursor, p.offset = 0, 0
	p.Autoscroll = true
	return p.anchorHere(ctx)
}

// Bottom jumps to the oldest retained message.
func (p Messages) Bottom(ctx Context) Messages {
	n := p.Len(ctx)
	if n == 0 {
		return p
	}
	p.cursor = n - 1
	p.Autoscroll = false
	return p.anchorHere(ctx).clampOffset(n)
}

func (p Messages) clampOffset(total int) Messages {
	h := p.height
	if h < 1 {
		h = 1
	}
	if p.cursor < p.offset {
		p.offset = p.cursor
	}
	if p.cursor >= p.offset+h {
		p.offset = p.cursor - h + 1
	}
	if p.offset > total-h {
		p.offset = total - h
	}
	if p.offset < 0 {
		p.offset = 0
	}
	return p
}

// Current returns the message under the cursor.
func (p Messages) Current(ctx Context) *mqtt.Message {
	r := ring(ctx, p.topic)
	if r == nil || r.Len() == 0 {
		return nil
	}
	idx := r.Len() - 1 - p.cursor // cursor 0 is the newest
	if idx < 0 || idx >= r.Len() {
		return nil
	}
	return r.At(idx)
}

// View renders newest-first, touching only the visible window. Rendering the
// whole buffer every frame is the difference between O(visible) and
// O(stream_history) per frame.
func (p Messages) View(ctx Context, w, h int) string {
	r := ring(ctx, p.topic)
	if r == nil || r.Len() == 0 {
		return ctx.Theme.Dim.Render("  no messages")
	}
	total := r.Len()

	p.height = h
	if p.Autoscroll {
		p.cursor, p.offset = 0, 0
	}
	p = p.clampOffset(total)

	// Newest first: window [offset, offset+h) counted from the newest.
	from := total - min(p.offset+h, total)
	to := total - p.offset
	window := r.Slice(from, to)

	var b strings.Builder
	b.Grow(w * h)
	for i := len(window) - 1; i >= 0; i-- {
		if i < len(window)-1 {
			b.WriteByte('\n')
		}
		idx := p.offset + (len(window) - 1 - i)
		b.WriteString(p.row(ctx, window[i], idx == p.cursor, w))
	}
	return b.String()
}

func (p Messages) row(ctx Context, m *mqtt.Message, selected bool, w int) string {
	t := ctx.Theme
	ts := m.ReceivedAt.Format(timestampFormat(ctx))

	flags := "q" + string(rune('0'+m.QoS))
	if m.Retained {
		flags += " R"
	} else {
		flags += " -"
	}
	if m.Duplicate {
		flags += "d"
	}

	prefix := ts + "  " + flags + "  "
	// The topic column only earns its place in the global firehose view.
	if p.topic == "" {
		prefix += Truncate(sanitize.Topic(m.Topic), max(w/3, 8)) + "  "
	}
	rest := max(w-lipgloss.Width(prefix), 4)
	payload := sanitize.Line(m.Payload, rest)
	if m.Truncated {
		payload += " ⋯"
	}

	line := Truncate(prefix+payload, w)
	if selected {
		return t.Selected.Render(Pad(line, w))
	}
	if m.Retained {
		return t.Retained.Render(line)
	}
	return t.Base.Render(line)
}

func timestampFormat(ctx Context) string {
	if ctx.Config.UI.TimestampFormat != "" {
		return ctx.Config.UI.TimestampFormat
	}
	return "15:04:05.000"
}
