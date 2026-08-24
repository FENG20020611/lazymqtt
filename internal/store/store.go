// Package store holds every piece of mutable application state: the topic
// tree, the message ring buffers, the caps and the counters.
//
// INVARIANT: Store has exactly one writer, the Bubble Tea update goroutine.
// It contains no mutexes and needs none. Do not call any Store method from a
// tea.Cmd goroutine or from a paho callback.
//
// This package must never import internal/ui or internal/app.
package store

import (
	"container/list"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Store is the facade over all application data.
type Store struct {
	root   *TopicNode
	index  map[string]*TopicNode // full topic -> node, for topics with data
	stream *Ring[*mqtt.Message]  // global firehose
	lru    *list.List            // *TopicNode, front = most recently updated

	limits Limits
	stats  Stats
	filter Filter

	// restore holds topics that were expanded in a previous session. Nodes
	// are created as messages arrive, so the expansion cannot be applied up
	// front; it is consulted once, when the node first appears.
	restore map[string]struct{}

	flat      []*TopicNode
	flatDirty bool
	treeDirty bool // set on any change the UI might need to redraw for

	// rate tracking, advanced by Tick
	lastTick     time.Time
	lastReceived uint64
}

// New returns an empty store with the given caps applied.
func New(limits Limits) *Store {
	limits = limits.Normalize()
	s := &Store{
		root:      newNode(nil, ""),
		index:     make(map[string]*TopicNode, 256),
		stream:    NewRing[*mqtt.Message](limits.StreamHistory),
		lru:       list.New(),
		limits:    limits,
		flatDirty: true,
	}
	s.root.Expanded = true
	return s
}

// Limits returns the active caps.
func (s *Store) Limits() Limits { return s.limits }

// Root returns the invisible root node whose children are the first topic
// level.
func (s *Store) Root() *TopicNode { return s.root }

// Stream returns the global firehose ring.
func (s *Store) Stream() *Ring[*mqtt.Message] { return s.stream }

// Stats returns a snapshot of the counters.
func (s *Store) Stats() Stats {
	st := s.stats
	st.Nodes = countNodes(s.root) - 1
	st.Topics = len(s.index)
	return st
}

// Dirty reports whether anything changed since the last ClearDirty. Panels
// use it to skip rebuilding their body strings on an idle frame.
func (s *Store) Dirty() bool { return s.treeDirty }

// ClearDirty resets the dirty flag; the renderer calls it once per frame.
func (s *Store) ClearDirty() { s.treeDirty = false }

// AddDropped records messages discarded before they reached the store.
func (s *Store) AddDropped(n uint64) {
	if n > 0 {
		s.stats.Dropped += n
		s.treeDirty = true
	}
}

// Ingest is the only write path for messages.
func (s *Store) Ingest(batch []*mqtt.Message) {
	if len(batch) == 0 {
		return
	}
	for _, m := range batch {
		s.ingestOne(m)
	}
	s.evictOverCap()
	s.flatDirty = true
	s.treeDirty = true
}

func (s *Store) ingestOne(m *mqtt.Message) {
	s.stats.Received++
	if m.Truncated {
		s.stats.Truncated++
	}

	n := s.ensureNode(m.Topic)
	if n.History == nil {
		n.History = NewRing[*mqtt.Message](s.limits.PerTopicHistory)
	}
	if old, evicted := n.History.Push(m); evicted && old != nil {
		s.stats.PayloadBytes -= int64(len(old.Payload))
	}
	s.stats.PayloadBytes += int64(len(m.Payload))

	now := m.ReceivedAt.UnixNano()
	if n.FirstSeen == 0 {
		n.FirstSeen = now
		s.index[m.Topic] = n
	}
	n.LastSeen = now
	n.Count++
	n.Last = m
	n.Retained = m.Retained
	for p := n.Parent; p != nil; p = p.Parent {
		p.Total++
	}
	n.Total++

	s.touchLRU(n)

	if evicted, didEvict := s.stream.Push(m); didEvict && evicted != nil {
		_ = evicted // payload bytes are accounted per-topic, not per-stream
	}
	if !s.filter.Empty() {
		// A newly created node inherits visibility from the live filter.
		n.visible = s.filter.MatchNode(n)
		if n.visible {
			for p := n.Parent; p != nil; p = p.Parent {
				p.visible = true
			}
		}
	}
}

// ensureNode walks or creates the path to a topic.
func (s *Store) ensureNode(topic string) *TopicNode {
	if n, ok := s.index[topic]; ok {
		return n
	}
	n := s.root
	for _, seg := range splitTopic(topic) {
		c, created := n.child(seg)
		if created {
			s.treeDirty = true
			if _, want := s.restore[c.Full]; want {
				c.Expanded = true
			}
		}
		n = c
	}
	return n
}

// RestoreExpanded records the topics that should open as soon as they appear.
// Call it once at startup, before any ingest.
func (s *Store) RestoreExpanded(topics []string) {
	if len(topics) == 0 {
		s.restore = nil
		return
	}
	s.restore = make(map[string]struct{}, len(topics))
	for _, t := range topics {
		s.restore[t] = struct{}{}
	}
	// Anything already in the tree is opened now; the rest is handled as the
	// nodes are created.
	for t := range s.restore {
		if n := s.Node(t); n != nil {
			n.Expanded = true
		}
	}
	s.Invalidate()
}

// ExpandedPaths returns the topics of every open node with children, in tree
// order. This is what gets persisted so a restart returns to the same view.
func (s *Store) ExpandedPaths(limit int) []string {
	out := make([]string, 0, 64)
	var walk func(*TopicNode)
	walk = func(n *TopicNode) {
		for _, c := range n.ordered {
			if len(out) >= limit {
				return
			}
			if c.Expanded && c.HasChildren() {
				out = append(out, c.Full)
			}
			walk(c)
		}
	}
	walk(s.root)
	return out
}

// Node returns the node for a topic, or nil.
func (s *Store) Node(topic string) *TopicNode {
	if n, ok := s.index[topic]; ok {
		return n
	}
	n := s.root
	for _, seg := range splitTopic(topic) {
		c, ok := n.children[seg]
		if !ok {
			return nil
		}
		n = c
	}
	if n == s.root {
		return nil
	}
	return n
}

func (s *Store) touchLRU(n *TopicNode) {
	if n.lru != nil {
		s.lru.MoveToFront(n.lru)
		return
	}
	n.lru = s.lru.PushFront(n)
}

// evictOverCap enforces MaxTopics by dropping the least recently updated
// topics and pruning any parents left empty behind them.
//
// This matters more than it sounds: namespaces containing UUIDs or session
// IDs generate unbounded cardinality, and a viewer subscribed to '#' on such
// a broker OOMs within minutes without it.
func (s *Store) evictOverCap() {
	for s.lru.Len() > s.limits.MaxTopics {
		el := s.lru.Back()
		if el == nil {
			return
		}
		n, _ := el.Value.(*TopicNode)
		s.lru.Remove(el)
		if n == nil {
			continue
		}
		n.lru = nil
		s.dropNode(n)
		s.stats.Evicted++
	}
}

func (s *Store) dropNode(n *TopicNode) {
	if n.History != nil {
		for i := 0; i < n.History.Len(); i++ {
			if m := n.History.At(i); m != nil {
				s.stats.PayloadBytes -= int64(len(m.Payload))
			}
		}
		n.History = nil
	}
	delete(s.index, n.Full)
	freed := n.Count
	n.Count, n.Last, n.Retained = 0, nil, false
	n.FirstSeen, n.LastSeen = 0, 0
	n.Total -= freed
	for p := n.Parent; p != nil; p = p.Parent {
		p.Total -= freed
	}
	// Prune structure that now holds nothing.
	for cur := n; cur != nil && cur != s.root; {
		if cur.HasChildren() || cur.IsTopic() {
			break
		}
		parent := cur.Parent
		parent.removeChild(cur.Segment)
		cur = parent
	}
	s.flatDirty = true
	s.treeDirty = true
}

// Flatten returns the visible, expanded nodes in render order. The result is
// cached and must not be mutated by the caller.
func (s *Store) Flatten() []*TopicNode {
	if !s.flatDirty && s.flat != nil {
		return s.flat
	}
	s.flat = flatten(s.root, s.flat[:0])
	s.flatDirty = false
	return s.flat
}

// Invalidate forces the next Flatten to rebuild. Call after expanding or
// collapsing a node.
func (s *Store) Invalidate() {
	s.flatDirty = true
	s.treeDirty = true
}

// SetFilter installs a new filter and recomputes node visibility.
func (s *Store) SetFilter(f Filter) {
	s.filter = f.Normalize()
	if s.filter.Empty() {
		markAllVisible(s.root)
	} else {
		markVisible(s.root, s.filter.MatchNode)
		s.root.visible = true
	}
	s.Invalidate()
}

// Filter returns the active filter.
func (s *Store) Filter() Filter { return s.filter }

// ClearTopic empties a topic's history but keeps the node, so the tree does
// not jump around underneath the cursor.
func (s *Store) ClearTopic(topic string) {
	n := s.Node(topic)
	if n == nil {
		return
	}
	if n.History != nil {
		for i := 0; i < n.History.Len(); i++ {
			if m := n.History.At(i); m != nil {
				s.stats.PayloadBytes -= int64(len(m.Payload))
			}
		}
		n.History.Reset()
	}
	n.Last = nil
	s.treeDirty = true
}

// ClearAll resets every message, node and counter. Caps and filter survive.
func (s *Store) ClearAll() {
	s.root = newNode(nil, "")
	s.root.Expanded = true
	s.index = make(map[string]*TopicNode, 256)
	s.stream.Reset()
	s.lru.Init()
	s.stats = Stats{Dropped: s.stats.Dropped}
	s.flat = nil
	s.Invalidate()
}

// Tick advances the rate EWMA. Call from the 1 Hz UI tick.
func (s *Store) Tick(now time.Time) {
	if s.lastTick.IsZero() {
		s.lastTick, s.lastReceived = now, s.stats.Received
		return
	}
	elapsed := now.Sub(s.lastTick).Seconds()
	if elapsed <= 0 {
		return
	}
	instant := float64(s.stats.Received-s.lastReceived) / elapsed
	const alpha = 0.4
	s.stats.RatePerSec = alpha*instant + (1-alpha)*s.stats.RatePerSec
	if s.stats.RatePerSec > s.stats.PeakRate {
		s.stats.PeakRate = s.stats.RatePerSec
	}
	s.lastTick, s.lastReceived = now, s.stats.Received
}

func countNodes(n *TopicNode) int {
	total := 1
	for _, c := range n.ordered {
		total += countNodes(c)
	}
	return total
}
