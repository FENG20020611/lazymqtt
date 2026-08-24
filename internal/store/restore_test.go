package store

import (
	"strings"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Expansion has to be restored lazily: at startup the tree is empty, and the
// nodes named in the state file only exist once a message arrives on them.
func TestRestoreExpandedAppliesToNodesCreatedLater(t *testing.T) {
	s := New(DefaultLimits())
	s.RestoreExpanded([]string{"home/kitchen", "factory/line1/press"})

	s.Ingest([]*mqtt.Message{
		msg("home/kitchen/temp", "21"),
		msg("factory/line1/press/state", "on"),
		msg("devices/lamp/state", "off"),
	})

	for _, want := range []string{"home/kitchen", "factory/line1/press"} {
		n := s.Node(want)
		if n == nil {
			t.Fatalf("%s was never created", want)
		}
		if !n.Expanded {
			t.Errorf("%s did not come back expanded", want)
		}
	}
	// A node nobody asked for keeps the default: closed below the top level.
	if n := s.Node("devices/lamp"); n == nil || n.Expanded {
		t.Error("an unlisted deep node was expanded anyway")
	}
}

func TestRestoreExpandedOpensNodesAlreadyPresent(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{msg("home/kitchen/temp", "21")})
	if s.Node("home/kitchen").Expanded {
		t.Fatal("setup: home/kitchen was already open")
	}

	s.RestoreExpanded([]string{"home/kitchen"})
	if !s.Node("home/kitchen").Expanded {
		t.Error("RestoreExpanded did not open a node that already existed")
	}
}

// The round trip is what matters: what ExpandedPaths reports must be exactly
// what RestoreExpanded then reproduces.
func TestExpandedPathsRoundTrips(t *testing.T) {
	first := New(DefaultLimits())
	msgs := []*mqtt.Message{
		msg("home/kitchen/temp", "21"),
		msg("home/attic/temp", "9"),
		msg("factory/line1/press/state", "on"),
	}
	first.Ingest(msgs)
	first.Node("home/kitchen").SetExpanded(true)
	first.Node("factory/line1").SetExpanded(true)

	paths := first.ExpandedPaths(500)

	second := New(DefaultLimits())
	second.RestoreExpanded(paths)
	second.Ingest(msgs)

	if got := second.ExpandedPaths(500); strings.Join(got, ",") != strings.Join(paths, ",") {
		t.Errorf("round trip changed the expanded set:\n first: %v\nsecond: %v", paths, got)
	}
}

// Only nodes with children are worth recording; a leaf's Expanded flag has no
// visible effect and would bloat the state file on a wide tree.
func TestExpandedPathsSkipsLeavesAndRespectsTheCap(t *testing.T) {
	s := New(DefaultLimits())
	var msgs []*mqtt.Message
	for i := 0; i < 50; i++ {
		msgs = append(msgs, msg(string(rune('a'+i%26))+"/"+itoa(i)+"/leaf", "x"))
	}
	s.Ingest(msgs)
	expandAll(s.Root())

	paths := s.ExpandedPaths(500)
	for _, p := range paths {
		if n := s.Node(p); n != nil && !n.HasChildren() {
			t.Errorf("%s is a leaf and should not be recorded", p)
		}
	}
	if got := len(s.ExpandedPaths(5)); got != 5 {
		t.Errorf("ExpandedPaths(5) returned %d entries", got)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
