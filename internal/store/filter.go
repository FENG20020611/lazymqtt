package store

import "strings"

// Filter narrows the topic tree and the message list. The zero value matches
// everything.
type Filter struct {
	Text         string // matched against the topic, and the payload when Payload is set
	Payload      bool   // also search payload bytes
	RetainedOnly bool
	// CaseSensitive is off by default: an operator hunting for a topic does
	// not want to think about case.
	CaseSensitive bool

	folded string // lowercased Text, cached
}

// Normalize prepares the internal comparison form. Call after mutating Text.
func (f Filter) Normalize() Filter {
	f.folded = strings.ToLower(f.Text)
	return f
}

// Empty reports whether the filter matches everything.
func (f Filter) Empty() bool { return f.Text == "" && !f.RetainedOnly }

func (f Filter) contains(hay string) bool {
	if f.CaseSensitive {
		return strings.Contains(hay, f.Text)
	}
	return strings.Contains(strings.ToLower(hay), f.folded)
}

// MatchNode reports whether a tree node survives the filter. Structural nodes
// (those with no messages) are matched on their topic path alone.
func (f Filter) MatchNode(n *TopicNode) bool {
	if f.RetainedOnly && !n.Retained {
		return false
	}
	if f.Text == "" {
		return true
	}
	if f.contains(n.Full) {
		return true
	}
	if f.Payload && n.Last != nil && f.contains(string(n.Last.Payload)) {
		return true
	}
	return false
}
