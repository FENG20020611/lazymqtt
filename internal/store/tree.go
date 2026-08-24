package store

import (
	"container/list"
	"sort"
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// TopicNode is one level of the topic hierarchy. Nodes with Count > 0 are
// topics that have actually received a message; the rest are pure structure.
type TopicNode struct {
	Segment string // "livingroom"
	Full    string // "home/livingroom"
	Depth   int
	Parent  *TopicNode

	children map[string]*TopicNode
	ordered  []*TopicNode // sorted; rebuilt only when a new child appears
	Expanded bool

	Count     uint64
	Total     uint64 // Count of this node plus every descendant
	Last      *mqtt.Message
	History   *Ring[*mqtt.Message] // nil until the first message arrives
	FirstSeen int64                // unix nanos; 0 = never
	LastSeen  int64
	Retained  bool // the last message on this topic carried the retain flag

	lru     *list.Element // position in the store's LRU list
	visible bool          // survives the active filter
}

func newNode(parent *TopicNode, segment string) *TopicNode {
	full := segment
	depth := 0
	if parent != nil && parent.Full != "" {
		full = parent.Full + "/" + segment
	}
	if parent != nil {
		depth = parent.Depth + 1
	}
	return &TopicNode{
		Segment:  segment,
		Full:     full,
		Depth:    depth,
		Parent:   parent,
		children: make(map[string]*TopicNode, 4),
		Expanded: depth < 1, // top level starts open; deeper levels do not
		visible:  true,
	}
}

// Children returns the sorted child slice. It is the render order and must
// not be mutated by callers.
func (n *TopicNode) Children() []*TopicNode { return n.ordered }

// HasChildren reports whether the node has any children at all.
func (n *TopicNode) HasChildren() bool { return len(n.ordered) > 0 }

// IsTopic reports whether this node has ever received a message, as opposed
// to existing only to hold children.
func (n *TopicNode) IsTopic() bool { return n.Count > 0 }

// child returns the named child, creating it if necessary. created reports
// whether a new node was allocated.
func (n *TopicNode) child(segment string) (c *TopicNode, created bool) {
	if c, ok := n.children[segment]; ok {
		return c, false
	}
	c = newNode(n, segment)
	n.children[segment] = c
	// New children are rare relative to messages, so paying for a sort here
	// keeps every read path free of one.
	idx := sort.Search(len(n.ordered), func(i int) bool {
		return n.ordered[i].Segment >= segment
	})
	n.ordered = append(n.ordered, nil)
	copy(n.ordered[idx+1:], n.ordered[idx:])
	n.ordered[idx] = c
	return c, true
}

func (n *TopicNode) removeChild(segment string) {
	if _, ok := n.children[segment]; !ok {
		return
	}
	delete(n.children, segment)
	for i, c := range n.ordered {
		if c.Segment == segment {
			n.ordered = append(n.ordered[:i], n.ordered[i+1:]...)
			break
		}
	}
}

// SetExpanded expands or collapses the node.
func (n *TopicNode) SetExpanded(v bool) { n.Expanded = v }

// ExpandAncestors opens every parent so this node becomes reachable in the
// flattened view.
func (n *TopicNode) ExpandAncestors() {
	for p := n.Parent; p != nil; p = p.Parent {
		p.Expanded = true
	}
}

// flatten walks depth-first, appending only nodes whose ancestors are all
// expanded and which survive the active filter.
func flatten(root *TopicNode, out []*TopicNode) []*TopicNode {
	for _, c := range root.ordered {
		if !c.visible {
			continue
		}
		out = append(out, c)
		if c.Expanded {
			out = flatten(c, out)
		}
	}
	return out
}

// markVisible recomputes the visible flag bottom-up: a node survives if it
// matches itself or if any descendant does. Returns whether n is visible.
func markVisible(n *TopicNode, match func(*TopicNode) bool) bool {
	visible := false
	for _, c := range n.ordered {
		if markVisible(c, match) {
			visible = true
		}
	}
	if !visible {
		visible = match(n)
	}
	n.visible = visible
	return visible
}

func markAllVisible(n *TopicNode) {
	n.visible = true
	for _, c := range n.ordered {
		markAllVisible(c)
	}
}

// splitTopic is a thin alias so tree code reads naturally; the canonical
// implementation lives with the protocol rules in internal/mqtt.
func splitTopic(topic string) []string { return strings.Split(topic, "/") }
