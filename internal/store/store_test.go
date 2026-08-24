package store

import (
	"fmt"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

var seq uint64

func msg(topic, payload string) *mqtt.Message {
	seq++
	return &mqtt.Message{
		Seq:        seq,
		Topic:      topic,
		Payload:    []byte(payload),
		ReceivedAt: time.Now(),
	}
}

func topics(nodes []*TopicNode) []string {
	out := make([]string, len(nodes))
	for i, n := range nodes {
		out[i] = n.Full
	}
	return out
}

func expandAll(n *TopicNode) {
	n.Expanded = true
	for _, c := range n.ordered {
		expandAll(c)
	}
}

func TestIngestCreatesIntermediateNodes(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{msg("home/livingroom/temperature", "21.5")})

	for _, path := range []string{"home", "home/livingroom", "home/livingroom/temperature"} {
		if s.Node(path) == nil {
			t.Fatalf("node %q was not created", path)
		}
	}
	leaf := s.Node("home/livingroom/temperature")
	if !leaf.IsTopic() || leaf.Count != 1 {
		t.Fatalf("leaf Count = %d, want 1", leaf.Count)
	}
	if mid := s.Node("home/livingroom"); mid.IsTopic() {
		t.Fatal("an intermediate node was marked as a topic")
	}
	if got := s.Node("home").Total; got != 1 {
		t.Fatalf("ancestor Total = %d, want 1", got)
	}
	if st := s.Stats(); st.Received != 1 || st.Topics != 1 || st.Nodes != 3 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestFlattenRespectsCollapse(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{
		msg("home/kitchen/temp", "1"),
		msg("home/kitchen/hum", "2"),
		msg("devices/a", "3"),
	})
	expandAll(s.Root())
	s.Invalidate()

	want := []string{"devices", "devices/a", "home", "home/kitchen", "home/kitchen/hum", "home/kitchen/temp"}
	if got := topics(s.Flatten()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("flatten = %v, want %v", got, want)
	}

	s.Node("home/kitchen").SetExpanded(false)
	s.Invalidate()
	want = []string{"devices", "devices/a", "home", "home/kitchen"}
	if got := topics(s.Flatten()); fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("collapsed flatten = %v, want %v", got, want)
	}
}

func TestFlattenIsCachedUntilInvalidated(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{msg("a/b", "1")})
	first := s.Flatten()
	if &first[0] != &s.Flatten()[0] {
		t.Fatal("Flatten rebuilt without an invalidation")
	}
}

func TestPerTopicHistoryCapAndByteAccounting(t *testing.T) {
	l := DefaultLimits()
	l.PerTopicHistory = 3
	s := New(l)
	for i := 0; i < 10; i++ {
		s.Ingest([]*mqtt.Message{msg("a/b", "xxxx")}) // 4 bytes each
	}
	n := s.Node("a/b")
	if n.History.Len() != 3 {
		t.Fatalf("history length = %d, want 3", n.History.Len())
	}
	if got := s.Stats().PayloadBytes; got != 12 {
		t.Fatalf("PayloadBytes = %d, want 12", got)
	}
	if n.Count != 10 {
		t.Fatalf("Count = %d, want 10 (the counter is not capped)", n.Count)
	}
}

func TestStreamHistoryCap(t *testing.T) {
	l := DefaultLimits()
	l.StreamHistory = 5
	s := New(l)
	for i := 0; i < 20; i++ {
		s.Ingest([]*mqtt.Message{msg(fmt.Sprintf("t/%d", i), "p")})
	}
	if got := s.Stream().Len(); got != 5 {
		t.Fatalf("stream length = %d, want 5", got)
	}
}

func TestLRUEvictionPrunesEmptyParents(t *testing.T) {
	l := DefaultLimits()
	l.MaxTopics = 3
	s := New(l)
	for i := 0; i < 5; i++ {
		s.Ingest([]*mqtt.Message{msg(fmt.Sprintf("sensors/%d/data", i), "p")})
	}
	if got := len(s.index); got != 3 {
		t.Fatalf("live topics = %d, want 3", got)
	}
	// The two oldest topics and the branches that existed only for them are gone.
	for i := 0; i < 2; i++ {
		if n := s.Node(fmt.Sprintf("sensors/%d", i)); n != nil {
			t.Fatalf("empty parent sensors/%d survived eviction", i)
		}
	}
	if s.Node("sensors/4/data") == nil {
		t.Fatal("the most recent topic was evicted")
	}
	if s.Stats().Evicted != 2 {
		t.Fatalf("Evicted = %d, want 2", s.Stats().Evicted)
	}
	if s.Stats().PayloadBytes != 3 {
		t.Fatalf("PayloadBytes after eviction = %d, want 3", s.Stats().PayloadBytes)
	}
}

func TestLRUEvictsLeastRecentlyUpdated(t *testing.T) {
	l := DefaultLimits()
	l.MaxTopics = 2
	s := New(l)
	s.Ingest([]*mqtt.Message{msg("a", "1"), msg("b", "2")})
	s.Ingest([]*mqtt.Message{msg("a", "3")}) // a is now the freshest
	s.Ingest([]*mqtt.Message{msg("c", "4")})
	if s.Node("b") != nil {
		t.Fatal("b should have been evicted as least recently updated")
	}
	if s.Node("a") == nil || s.Node("c") == nil {
		t.Fatal("a and c should have survived")
	}
}

func TestFilterNarrowsTreeAndRestores(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{
		msg("home/kitchen/temp", "cold"),
		msg("home/kitchen/hum", "wet"),
		msg("devices/lamp", "on"),
	})
	expandAll(s.Root())

	s.SetFilter(Filter{Text: "kitchen"})
	got := topics(s.Flatten())
	want := []string{"home", "home/kitchen", "home/kitchen/hum", "home/kitchen/temp"}
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Fatalf("filtered = %v, want %v", got, want)
	}

	s.SetFilter(Filter{Text: "lamp", Payload: true})
	if got := topics(s.Flatten()); fmt.Sprint(got) != "[devices devices/lamp]" {
		t.Fatalf("payload filter = %v", got)
	}

	s.SetFilter(Filter{})
	if len(s.Flatten()) != 6 {
		t.Fatalf("clearing the filter did not restore the tree: %v", topics(s.Flatten()))
	}
}

func TestRetainedOnlyFilter(t *testing.T) {
	s := New(DefaultLimits())
	r := msg("a/retained", "x")
	r.Retained = true
	s.Ingest([]*mqtt.Message{r, msg("a/live", "y")})
	expandAll(s.Root())
	s.SetFilter(Filter{RetainedOnly: true})
	if got := topics(s.Flatten()); fmt.Sprint(got) != "[a a/retained]" {
		t.Fatalf("retained-only = %v", got)
	}
}

func TestClearTopicKeepsNodeAndClearAllResets(t *testing.T) {
	s := New(DefaultLimits())
	s.Ingest([]*mqtt.Message{msg("a/b", "hello")})
	s.ClearTopic("a/b")
	n := s.Node("a/b")
	if n == nil {
		t.Fatal("ClearTopic removed the node")
	}
	if n.History.Len() != 0 || n.Last != nil {
		t.Fatal("ClearTopic left messages behind")
	}
	if s.Stats().PayloadBytes != 0 {
		t.Fatalf("PayloadBytes = %d after ClearTopic", s.Stats().PayloadBytes)
	}

	s.AddDropped(4)
	s.ClearAll()
	st := s.Stats()
	if st.Received != 0 || st.Topics != 0 || st.Nodes != 0 {
		t.Fatalf("ClearAll left state behind: %+v", st)
	}
	if st.Dropped != 4 {
		t.Fatalf("ClearAll reset the drop counter to %d; it records what was lost", st.Dropped)
	}
}

func TestTruncationCounter(t *testing.T) {
	s := New(DefaultLimits())
	m := msg("a", "abc")
	m.Truncated, m.OrigSize = true, 9999
	s.Ingest([]*mqtt.Message{m})
	if s.Stats().Truncated != 1 {
		t.Fatal("truncation was not counted")
	}
}

func TestTickComputesRate(t *testing.T) {
	s := New(DefaultLimits())
	t0 := time.Now()
	s.Tick(t0)
	for i := 0; i < 100; i++ {
		s.Ingest([]*mqtt.Message{msg("a", "p")})
	}
	s.Tick(t0.Add(time.Second))
	if r := s.Stats().RatePerSec; r <= 0 || r > 100 {
		t.Fatalf("RatePerSec = %v, want a value in (0,100]", r)
	}
	if s.Stats().PeakRate != s.Stats().RatePerSec {
		t.Fatal("PeakRate did not follow the first rate sample")
	}
}

func BenchmarkIngest(b *testing.B) {
	s := New(DefaultLimits())
	batch := make([]*mqtt.Message, 512)
	for i := range batch {
		batch[i] = msg(fmt.Sprintf("bench/%d/value", i%500), "0123456789")
	}
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		s.Ingest(batch)
	}
}
