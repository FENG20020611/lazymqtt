package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/mqtt/mqtttest"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

type collector struct {
	mu   sync.Mutex
	msgs []any
}

func (c *collector) Send(msg any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.msgs = append(c.msgs, msg)
}

func (c *collector) batches() []BatchMsg {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []BatchMsg
	for _, m := range c.msgs {
		if b, ok := m.(BatchMsg); ok {
			out = append(out, b)
		}
	}
	return out
}

func (c *collector) all() []any {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]any(nil), c.msgs...)
}

func newTestBridge(t *testing.T, bufSize, batchCap int) (*Fake, *collector, *Bridge, chan time.Time) {
	t.Helper()
	fake := mqtttest.New(bufSize)
	out := &collector{}
	ticks := make(chan time.Time, 1)
	b := NewBridge(fake, out, BridgeConfig{BatchCap: batchCap, Ticks: ticks})
	return fake, out, b, ticks
}

// Fake is an alias so the helper signature stays readable.
type Fake = mqtttest.Fake

func TestBatchCapSplitsLargeBursts(t *testing.T) {
	fake, out, b, ticks := newTestBridge(t, 4096, 512)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)

	const n = 1300 // ceil(1300/512) = 3 batches, the last one on a tick
	for i := 0; i < n; i++ {
		fake.Inject("a/b", "x")
	}
	waitFor(t, func() bool { return len(out.batches()) >= 2 })
	waitForTicked(t, ticks, func() bool { return countMsgs(out.batches()) == n })

	batches := out.batches()
	// Every message arrives exactly once, and no single Update is ever handed
	// more than the cap — that bound is what keeps a retained flood from
	// blocking the UI for a visible beat.
	if got := countMsgs(batches); got != n {
		t.Fatalf("delivered %d messages, want %d", got, n)
	}
	if len(batches) < 3 {
		t.Fatalf("got %d batches, want at least 3 for %d messages at a cap of 512", len(batches), n)
	}
	full := 0
	for i, b := range batches {
		if len(b.Msgs) > 512 {
			t.Fatalf("batch %d holds %d messages, over the cap of 512", i, len(b.Msgs))
		}
		if len(b.Msgs) == 512 {
			full++
		}
	}
	if full < 2 {
		t.Fatalf("only %d batches hit the cap; the cap flush is not firing", full)
	}

	cancel()
	b.Stop()
	_ = fake.Close()
}

func TestNoSendOnAnEmptyTick(t *testing.T) {
	fake, out, b, ticks := newTestBridge(t, 64, 512)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)

	for i := 0; i < 5; i++ {
		ticks <- time.Now()
	}
	// Give the loop a chance to do the wrong thing.
	waitForStable(t)
	if got := len(out.batches()); got != 0 {
		t.Fatalf("%d batches sent with nothing buffered", got)
	}

	cancel()
	b.Stop()
	_ = fake.Close()
}

func TestDropCounterIsPropagated(t *testing.T) {
	fake, out, b, ticks := newTestBridge(t, 4, 512)
	ctx, cancel := context.WithCancel(context.Background())

	// Overfill before the drain loop starts so the drops are unambiguous.
	for i := 0; i < 10; i++ {
		fake.Inject("a", "x")
	}
	if fake.Dropped() != 6 {
		t.Fatalf("fake dropped %d, want 6", fake.Dropped())
	}
	b.Start(ctx)
	waitForTicked(t, ticks, func() bool { return len(out.batches()) > 0 })

	if got := out.batches()[0].Dropped; got != 6 {
		t.Fatalf("BatchMsg.Dropped = %d, want 6 — a lossy view must say so", got)
	}
	cancel()
	b.Stop()
	_ = fake.Close()
}

func TestCancellationDrainsWhatIsBuffered(t *testing.T) {
	fake, out, b, _ := newTestBridge(t, 64, 512)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)

	fake.Inject("a", "1")
	fake.Inject("a", "2")
	waitFor(t, func() bool { return len(fake.Messages()) == 0 })

	cancel()
	b.Stop()

	if n := countMsgs(out.batches()); n != 2 {
		t.Fatalf("drained %d messages on shutdown, want 2", n)
	}
	_ = fake.Close()
}

func TestClosedChannelStopsTheLoops(t *testing.T) {
	fake, _, b, _ := newTestBridge(t, 8, 512)
	b.Start(context.Background())
	_ = fake.Close()
	// Stop must return; if the loops ignored the closed channel it would hang
	// and goleak would fail the package.
	b.Stop()
}

func TestEventsBecomeTypedMessages(t *testing.T) {
	fake, out, b, _ := newTestBridge(t, 8, 512)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)

	_ = fake.Connect(ctx)
	_ = fake.Subscribe(ctx, []mqtt.Subscription{{Filter: "a/#", QoS: 1}})
	_ = fake.Publish(ctx, mqtt.PublishRequest{Topic: "a/b"})

	waitFor(t, func() bool {
		var conn, sub, pub bool
		for _, m := range out.all() {
			switch m.(type) {
			case ConnStateMsg:
				conn = true
			case SubResultMsg:
				sub = true
			case PubResultMsg:
				pub = true
			}
		}
		return conn && sub && pub
	})

	cancel()
	b.Stop()
	_ = fake.Close()
}

func TestConcurrentProducersUnderRace(t *testing.T) {
	fake, out, b, ticks := newTestBridge(t, 4096, 128)
	ctx, cancel := context.WithCancel(context.Background())
	b.Start(ctx)

	var wg sync.WaitGroup
	for p := 0; p < 8; p++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 500; i++ {
				fake.Inject("load/topic", "payload")
			}
		}()
	}
	wg.Wait()
	waitForTicked(t, ticks, func() bool {
		return uint64(countMsgs(out.batches()))+fake.Dropped() == 4000
	})
	cancel()
	b.Stop()

	total := uint64(countMsgs(out.batches())) + fake.Dropped()
	if total != 4000 {
		t.Fatalf("delivered %d + dropped %d != 4000", countMsgs(out.batches()), fake.Dropped())
	}
	_ = fake.Close()
}

func countMsgs(bs []BatchMsg) int {
	n := 0
	for _, b := range bs {
		n += len(b.Msgs)
	}
	return n
}

// waitForTicked polls cond while feeding the injected ticker. A single tick
// is not enough: the drain loop may consume it while the batch is still
// empty, in which case the flush is a no-op and nothing more would arrive.
func waitForTicked(t *testing.T, ticks chan<- time.Time, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		select {
		case ticks <- time.Now():
		default:
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within 2s")
}

func waitForStable(t *testing.T) {
	t.Helper()
	time.Sleep(20 * time.Millisecond)
}
