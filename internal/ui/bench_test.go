package ui

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// The §19 success criterion: 500 topics at 5,000 msg/s, resident memory under
// 100 MB, input latency imperceptible. These benchmarks and the ceiling test
// below are how that is measured without a broker.
const (
	benchTopics  = 500
	benchRate    = 5000
	benchPayload = 200
)

func benchModel(tb testing.TB) Model {
	tb.Helper()
	cfg := config.Default()
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg})
	return m.resize(120, 40)
}

// benchBatch builds one coalesced batch the size the bridge would produce at
// benchRate with a 50 ms flush interval.
func benchBatch(start int, n int) []*mqtt.Message {
	payload := make([]byte, benchPayload)
	for i := range payload {
		payload[i] = byte('a' + i%26)
	}
	msgs := make([]*mqtt.Message, 0, n)
	for i := 0; i < n; i++ {
		seq := start + i
		// The device index alone decides the topic, so the cardinality the
		// store sees is exactly benchTopics rather than some product of two
		// independent moduli.
		device := seq % benchTopics
		msgs = append(msgs, &mqtt.Message{
			Seq:        uint64(seq),
			Topic:      fmt.Sprintf("bench/group%02d/device%04d/telemetry", device/32, device),
			Payload:    payload,
			ReceivedAt: time.Unix(0, int64(seq)),
		})
	}
	return msgs
}

// BenchmarkIngestBatch measures the store write on the UI goroutine. This runs
// once per 50 ms flush in production, so anything above a few hundred
// microseconds per batch is felt as input lag.
func BenchmarkIngestBatch(b *testing.B) {
	const perBatch = benchRate / 20 // one 50 ms flush
	m := benchModel(b)
	batches := make([][]*mqtt.Message, 16)
	for i := range batches {
		batches[i] = benchBatch(i*perBatch, perBatch)
	}

	b.ReportAllocs()
	for i := 0; b.Loop(); i++ {
		next, _ := m.Update(app.BatchMsg{Msgs: batches[i%len(batches)]})
		m = next.(Model)
	}
}

// BenchmarkRenderFrame measures one full frame over a populated tree. At 20
// fps the budget is 50 ms; the target is two orders of magnitude below that.
func BenchmarkRenderFrame(b *testing.B) {
	m := benchModel(b)
	next, _ := m.Update(app.BatchMsg{Msgs: benchBatch(0, benchTopics*4)})
	m = next.(Model)
	for _, n := range m.store.Flatten() {
		n.SetExpanded(true)
	}
	m.store.Invalidate()

	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkRenderFrameWithFilter is the same frame with a filter active, which
// is the path that recomputes node visibility.
func BenchmarkRenderFrameWithFilter(b *testing.B) {
	m := benchModel(b)
	next, _ := m.Update(app.BatchMsg{Msgs: benchBatch(0, benchTopics*4)})
	m = next.(Model)
	m = m.applyFilter("device01")

	b.ReportAllocs()
	for b.Loop() {
		_ = m.View()
	}
}

// BenchmarkKeypress measures the latency of a cursor move, which is what the
// user actually perceives as responsiveness.
func BenchmarkKeypress(b *testing.B) {
	m := benchModel(b)
	next, _ := m.Update(app.BatchMsg{Msgs: benchBatch(0, benchTopics*4)})
	m = next.(Model)
	for _, n := range m.store.Flatten() {
		n.SetExpanded(true)
	}
	m.store.Invalidate()
	m = m.resize(120, 40)

	down := keyMsg("j")
	b.ReportAllocs()
	for b.Loop() {
		next, _ := m.Update(down)
		m = next.(Model)
	}
}

// The caps exist to bound resident memory, so assert on memory.
//
// This drives a full minute of traffic at the §19 rate through the real ingest
// and render path and checks the heap afterwards. An hour cannot be tested in
// CI, but the caps are what make the ceiling flat rather than rising — so if a
// minute stays bounded and eviction is doing its job, an hour does too.
func TestMemoryStaysBoundedAtTheTargetRate(t *testing.T) {
	if testing.Short() {
		t.Skip("the memory ceiling test drives a minute of traffic; skipped under -short")
	}

	m := benchModel(t)
	const (
		perBatch = benchRate / 20 // a 50 ms flush
		batches  = 20 * 60        // one minute
	)

	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)

	for i := 0; i < batches; i++ {
		next, _ := m.Update(app.BatchMsg{Msgs: benchBatch(i*perBatch, perBatch)})
		m = next.(Model)
		// Render on the same cadence the program would, so any per-frame
		// allocation that accumulates shows up here.
		if i%4 == 0 {
			_ = m.View()
		}
	}

	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)

	st := m.store.Stats()
	t.Logf("received %d over %d topics; heap %d MB -> %d MB; evicted %d",
		st.Received, st.Topics, before.HeapAlloc>>20, after.HeapAlloc>>20, st.Evicted)

	if st.Received != uint64(batches*perBatch) {
		t.Errorf("ingested %d messages, expected %d", st.Received, batches*perBatch)
	}
	const ceiling = 100 << 20
	if after.HeapAlloc > ceiling {
		t.Errorf("heap is %d MB after %d messages, the §19 ceiling is %d MB",
			after.HeapAlloc>>20, st.Received, ceiling>>20)
	}
}

// Unbounded topic cardinality is the other half of the ceiling: a namespace
// full of UUIDs must be evicted, not accumulated.
func TestUnboundedCardinalityIsEvictedNotAccumulated(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.MaxTopics = 1000
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(120, 40)

	const total = 50000
	for i := 0; i < total; i += 500 {
		msgs := make([]*mqtt.Message, 0, 500)
		for j := 0; j < 500; j++ {
			seq := i + j
			msgs = append(msgs, &mqtt.Message{
				Seq:        uint64(seq + 1),
				Topic:      fmt.Sprintf("requests/%08x/status", seq),
				Payload:    []byte(`{"code":200}`),
				ReceivedAt: time.Unix(0, int64(seq)),
			})
		}
		next, _ := m.Update(app.BatchMsg{Msgs: msgs})
		m = next.(Model)
	}

	st := m.store.Stats()
	if st.Topics > cfg.Limits.MaxTopics {
		t.Errorf("the store holds %d topics against a cap of %d", st.Topics, cfg.Limits.MaxTopics)
	}
	if st.Evicted == 0 {
		t.Errorf("%d unique topics under a cap of %d evicted nothing", total, cfg.Limits.MaxTopics)
	}

	runtime.GC()
	var mem runtime.MemStats
	runtime.ReadMemStats(&mem)
	const ceiling = 100 << 20
	if mem.HeapAlloc > ceiling {
		t.Errorf("heap is %d MB after %d unique topics, ceiling is %d MB",
			mem.HeapAlloc>>20, total, ceiling>>20)
	}
}
