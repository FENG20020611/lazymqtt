package panel

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/store"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

// Topics is the topic tree. Bubbles ships no tree component, so this is the
// one bespoke UI widget in the project.
//
// The panel holds only a cursor and a scroll offset; the node graph lives in
// the store and the flattened view is cached there.
type Topics struct {
	cursor int
	offset int
	height int // rows the panel body has, set on resize

	// selected is the full topic of the node under the cursor. Tracking the
	// topic rather than the index is what keeps the selection stable: without
	// it, every new topic that sorts before the current one shifts the
	// flattened slice and the cursor appears to move on its own.
	selected string

	// Follow makes the cursor jump to whichever topic last received a
	// message. Excellent for discovery, unusable at high rates, hence a
	// toggle.
	Follow bool
}

// SetHeight records the body height so scrolling can be computed when the
// cursor moves rather than recomputed from scratch on every frame.
func (p Topics) SetHeight(h int) Topics {
	p.height = h
	return p
}

// clampOffset scrolls the window so the cursor stays inside it.
func (p Topics) clampOffset(total int) Topics {
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

// Selected returns the topic under the cursor.
func (p Topics) Selected() string { return p.selected }

// Cursor returns the cursor index, for tests.
func (p Topics) Cursor() int { return p.cursor }

// Resync re-resolves the cursor index from the remembered topic after the
// flattened slice has changed underneath it.
func (p Topics) Resync(flat []*store.TopicNode) Topics {
	if len(flat) == 0 {
		p.cursor, p.offset, p.selected = 0, 0, ""
		return p
	}
	if p.selected != "" {
		for i, n := range flat {
			if n.Full == p.selected {
				p.cursor = i
				return p.clampOffset(len(flat))
			}
		}
	}
	// The selected node is gone (collapsed, filtered or evicted): keep the
	// cursor where it is and adopt whatever now sits there.
	if p.cursor >= len(flat) {
		p.cursor = len(flat) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}
	p.selected = flat[p.cursor].Full
	return p.clampOffset(len(flat))
}

// FollowTopic moves the cursor onto a topic, expanding its ancestors.
func (p Topics) FollowTopic(s *store.Store, topic string) Topics {
	n := s.Node(topic)
	if n == nil {
		return p
	}
	n.ExpandAncestors()
	s.Invalidate()
	p.selected = topic
	return p.Resync(s.Flatten())
}

// Move shifts the cursor by delta rows and clamps it.
func (p Topics) Move(flat []*store.TopicNode, delta int) Topics {
	if len(flat) == 0 {
		return p
	}
	p.cursor += delta
	if p.cursor < 0 {
		p.cursor = 0
	}
	if p.cursor >= len(flat) {
		p.cursor = len(flat) - 1
	}
	p.selected = flat[p.cursor].Full
	return p.clampOffset(len(flat))
}

// GoTo jumps to an absolute row.
func (p Topics) GoTo(flat []*store.TopicNode, idx int) Topics {
	p.cursor = 0
	return p.Move(flat, idx)
}

// Expand opens the node under the cursor, or steps into its first child when
// it is already open.
func (p Topics) Expand(s *store.Store) Topics {
	flat := s.Flatten()
	if p.cursor >= len(flat) {
		return p
	}
	n := flat[p.cursor]
	if n.HasChildren() && !n.Expanded {
		n.SetExpanded(true)
		s.Invalidate()
		return p.Resync(s.Flatten())
	}
	return p.Move(s.Flatten(), 1)
}

// Collapse closes the node under the cursor, or steps out to its parent when
// it is already closed.
func (p Topics) Collapse(s *store.Store) Topics {
	flat := s.Flatten()
	if p.cursor >= len(flat) {
		return p
	}
	n := flat[p.cursor]
	if n.HasChildren() && n.Expanded {
		n.SetExpanded(false)
		s.Invalidate()
		return p.Resync(s.Flatten())
	}
	if n.Parent != nil && n.Parent.Full != "" {
		p.selected = n.Parent.Full
		return p.Resync(s.Flatten())
	}
	return p
}

// Toggle flips the expansion state of the node under the cursor.
func (p Topics) Toggle(s *store.Store) Topics {
	flat := s.Flatten()
	if p.cursor >= len(flat) {
		return p
	}
	n := flat[p.cursor]
	if !n.HasChildren() {
		return p
	}
	n.SetExpanded(!n.Expanded)
	s.Invalidate()
	return p.Resync(s.Flatten())
}

// View renders the visible window only. The flattened slice may hold
// thousands of nodes; the loop below touches at most h of them.
func (p Topics) View(ctx Context, w, h int) string {
	flat := ctx.Store.Flatten()
	if len(flat) == 0 {
		return ctx.Theme.Dim.Render("  no topics yet")
	}

	// The offset is maintained as the cursor moves; re-clamp here only so a
	// resize between updates cannot scroll the cursor off screen.
	p.height = h
	p = p.clampOffset(len(flat))
	offset := p.offset
	end := min(offset+h, len(flat))

	var b strings.Builder
	b.Grow(w * h)
	for i := offset; i < end; i++ {
		if i > offset {
			b.WriteByte('\n')
		}
		b.WriteString(p.row(ctx, flat[i], i == p.cursor, w))
	}
	return b.String()
}

func (p Topics) row(ctx Context, n *store.TopicNode, selected bool, w int) string {
	t := ctx.Theme

	marker := " "
	switch {
	case n.HasChildren() && n.Expanded:
		marker = "▾"
	case n.HasChildren():
		marker = "▸"
	case n.IsTopic():
		marker = "●"
	}

	count := ""
	if n.Total > 0 {
		count = formatCount(n.Total)
	}

	label := sanitize.Topic(n.Segment)
	if label == "" {
		label = "(empty)"
	}

	left := Indent(n.Depth) + marker + " " + label
	gap := w - lipgloss.Width(left) - lipgloss.Width(count) - 1
	if gap < 1 {
		left = Truncate(left, max(w-lipgloss.Width(count)-2, 1))
		gap = w - lipgloss.Width(left) - lipgloss.Width(count) - 1
		if gap < 0 {
			gap = 0
		}
	}
	line := left + strings.Repeat(" ", gap) + count + " "

	if selected {
		return t.Selected.Render(Pad(Truncate(line, w), w))
	}
	if n.Retained {
		return t.Retained.Render(Truncate(line, w))
	}
	if n.IsTopic() {
		return t.Base.Render(Truncate(line, w))
	}
	return t.Dim.Render(Truncate(line, w))
}

// formatCount renders large counts compactly so the column stays narrow.
func formatCount(n uint64) string {
	switch {
	case n < 10000:
		return fmt.Sprintf("%d", n)
	case n < 1_000_000:
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	default:
		return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
	}
}
