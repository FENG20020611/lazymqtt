//go:build integration

package integration

import (
	"context"
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/store"
)

// floodSize is the retained-message count from §14.2. Large enough to exceed
// the store cap below and to find a deadlock; small enough that the publish
// loop does not dominate the suite's runtime.
const floodSize = 20000

// A viewer subscribed to '#' on a broker with a high-cardinality namespace —
// UUIDs, session IDs, per-request topics — will OOM within minutes without
// LRU eviction. This is that scenario, compressed.
func TestRetainedFloodRespectsMaxTopicsWithoutDeadlock(t *testing.T) {
	if testing.Short() {
		t.Skip("the retained flood is slow; skipped under -short")
	}
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	base := testTopic(t)

	opts := clientOptions(t, url)
	opts.IngestBuffer = 64 << 10
	publisher := newClient(t, opts)
	drainEvents(t, publisher)

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	topics := make([]string, 0, floodSize)
	for i := 0; i < floodSize; i++ {
		topics = append(topics, fmt.Sprintf("%s/dev%05d/state", base, i))
	}
	t.Cleanup(func() { clearRetained(t, publisher, topics...) })

	for i, tp := range topics {
		if err := publisher.Publish(ctx, mqtt.PublishRequest{
			Topic:   tp,
			Payload: []byte(fmt.Sprintf(`{"i":%d}`, i)),
			Retain:  true,
		}); err != nil {
			t.Fatalf("publish %d of %d failed: %v", i, floodSize, err)
		}
	}

	// A fresh subscriber receives the whole retained set on subscribe.
	subOpts := clientOptions(t, url)
	subOpts.IngestBuffer = 64 << 10
	subscriber := newClient(t, subOpts)
	drainEvents(t, subscriber)

	const maxTopics = 5000
	s := store.New(store.Limits{
		MaxTopics:       maxTopics,
		PerTopicHistory: 5,
		StreamHistory:   2000,
		MaxPayloadBytes: 1 << 20,
	})

	subscribe(t, subscriber, base+"/#", 0)

	// Drain in batches, the way the bridge does. A per-message read would
	// test a code path the app does not have.
	idle := time.NewTimer(15 * time.Second)
	defer idle.Stop()
	batch := make([]*mqtt.Message, 0, 512)
drain:
	for {
		select {
		case m, ok := <-subscriber.Messages():
			if !ok {
				break drain
			}
			batch = append(batch, m)
			if len(batch) == cap(batch) {
				s.Ingest(batch)
				batch = batch[:0]
			}
			if !idle.Stop() {
				select {
				case <-idle.C:
				default:
				}
			}
			idle.Reset(5 * time.Second)
		case <-idle.C:
			break drain
		case <-ctx.Done():
			t.Fatal("the flood never finished; something is deadlocked")
		}
	}
	s.Ingest(batch)

	st := s.Stats()
	t.Logf("received %d, dropped %d, evicted %d, topics %d, nodes %d, payload %d bytes",
		st.Received, subscriber.Dropped(), st.Evicted, st.Topics, st.Nodes, st.PayloadBytes)

	if st.Received == 0 {
		t.Fatal("no retained messages arrived at all")
	}
	if st.Topics > maxTopics {
		t.Errorf("the store holds %d topics, above the cap of %d", st.Topics, maxTopics)
	}
	if st.Received > uint64(maxTopics) && st.Evicted == 0 {
		t.Errorf("%d messages across more than %d topics evicted nothing", st.Received, maxTopics)
	}

	// The cap exists to bound memory, so assert on memory rather than only on
	// the counter that is supposed to bound it.
	var mem runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&mem)
	const ceiling = 512 << 20
	if mem.HeapAlloc > ceiling {
		t.Errorf("heap is %d MB after the flood, ceiling is %d MB",
			mem.HeapAlloc>>20, ceiling>>20)
	}
}
