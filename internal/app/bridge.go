package app

import (
	"context"
	"log/slog"
	"runtime/debug"
	"sync"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Default coalescer tuning.
//
// 50 ms (20 Hz) is the right default: below ~30 ms you re-render faster than
// a human perceives as live and pay for it; above ~100 ms the UI feels laggy.
//
// The batch cap bounds the size of a single Update. Without it a retained
// flood on '#' could hand Ingest a 50,000-element slice and block the UI for
// a visible beat.
const (
	DefaultFlushInterval = 50 * time.Millisecond
	DefaultBatchCap      = 512
)

// Sender is the subset of tea.Program the bridge needs, so bridge tests need
// no terminal and no Bubble Tea program.
type Sender interface{ Send(msg any) }

// SenderFunc adapts a function to Sender.
type SenderFunc func(msg any)

// Send implements Sender.
func (f SenderFunc) Send(msg any) { f(msg) }

// BridgeConfig configures a Bridge.
type BridgeConfig struct {
	FlushInterval time.Duration
	BatchCap      int
	Logger        *slog.Logger

	// Ticks, when non-nil, replaces the internal ticker. Tests inject a
	// channel they control so batching is deterministic.
	Ticks <-chan time.Time
}

// Bridge drains the client's message and event channels and coalesces
// messages into batches before handing them to the UI.
//
// The result: at most 1/FlushInterval Update calls per second from message
// traffic, whether the broker is sending 10 messages or 100,000. The store
// absorbs the full rate; the UI absorbs a fixed rate.
type Bridge struct {
	client mqtt.Client
	out    Sender
	cfg    BridgeConfig

	wg   sync.WaitGroup
	once sync.Once
	stop context.CancelFunc

	// Flushes counts batches sent, for tests and for the debug log.
	flushes int
	mu      sync.Mutex
}

// NewBridge builds a bridge. Nothing runs until Start.
func NewBridge(client mqtt.Client, out Sender, cfg BridgeConfig) *Bridge {
	if cfg.FlushInterval <= 0 {
		cfg.FlushInterval = DefaultFlushInterval
	}
	if cfg.BatchCap <= 0 {
		cfg.BatchCap = DefaultBatchCap
	}
	if cfg.Logger == nil {
		cfg.Logger = slog.New(slog.DiscardHandler)
	}
	return &Bridge{client: client, out: out, cfg: cfg}
}

// Start launches the drain loops. They exit when ctx is cancelled, when Stop
// is called, or when the client's channels close.
func (b *Bridge) Start(ctx context.Context) {
	ctx, cancel := context.WithCancel(ctx)
	b.stop = cancel

	b.wg.Add(2)
	go b.run(ctx, b.drainMessages)
	go b.run(ctx, b.drainEvents)
}

// run wraps a loop in panic recovery. A panic on a background goroutine would
// otherwise crash the process with the terminal still in raw mode.
func (b *Bridge) run(ctx context.Context, fn func(context.Context)) {
	defer b.wg.Done()
	defer func() {
		if r := recover(); r != nil {
			b.cfg.Logger.Error("bridge goroutine panicked", "panic", r, "stack", string(debug.Stack()))
			b.send(ErrorMsg{Err: panicError{value: r, stack: debug.Stack()}, Fatal: true})
		}
	}()
	fn(ctx)
}

// Stop cancels the loops and waits for them to finish.
func (b *Bridge) Stop() {
	b.once.Do(func() {
		if b.stop != nil {
			b.stop()
		}
	})
	b.wg.Wait()
}

// Flushes returns the number of batches sent so far.
func (b *Bridge) Flushes() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.flushes
}

func (b *Bridge) drainMessages(ctx context.Context) {
	batch := make([]*mqtt.Message, 0, b.cfg.BatchCap)

	ticks := b.cfg.Ticks
	if ticks == nil {
		t := time.NewTicker(b.cfg.FlushInterval)
		defer t.Stop()
		ticks = t.C
	}

	flush := func() {
		if len(batch) == 0 {
			return
		}
		// Hand the slice over and start a fresh one: the UI goroutine reads
		// it after this loop has moved on.
		out := batch
		batch = make([]*mqtt.Message, 0, b.cfg.BatchCap)
		b.mu.Lock()
		b.flushes++
		b.mu.Unlock()
		b.send(BatchMsg{Msgs: out, Dropped: b.client.Dropped()})
	}

	src := b.client.Messages()
	for {
		select {
		case <-ctx.Done():
			flush() // drain what we have before stopping
			return

		case m, ok := <-src:
			if !ok {
				flush()
				return
			}
			batch = append(batch, m)
			if len(batch) >= b.cfg.BatchCap {
				flush()
			}

		case <-ticks:
			flush()
		}
	}
}

func (b *Bridge) drainEvents(ctx context.Context) {
	src := b.client.Events()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-src:
			if !ok {
				return
			}
			b.dispatch(ev)
		}
	}
}

func (b *Bridge) dispatch(ev mqtt.Event) {
	switch ev.Kind {
	case mqtt.EventSubAck:
		b.send(SubResultMsg{Subs: ev.Subs, Err: ev.Err})
	case mqtt.EventUnsubAck:
		b.send(UnsubResultMsg{Err: ev.Err})
	case mqtt.EventPubAck:
		b.send(PubResultMsg{Topic: ev.Topic, Err: ev.Err})
	default:
		b.send(ConnStateMsg{Status: ev.Status})
	}
	if ev.Err != nil && ev.Kind == mqtt.EventError {
		b.cfg.Logger.Warn("mqtt error", "err", ev.Err, "state", ev.Status.State.String())
	}
}

func (b *Bridge) send(msg any) {
	if b.out == nil {
		return
	}
	b.out.Send(msg)
}

type panicError struct {
	value any
	stack []byte
}

func (p panicError) Error() string { return "panic in bridge goroutine" }

// Stack returns the captured stack trace, printed after the terminal has been
// restored.
func (p panicError) Stack() []byte { return p.stack }

// Value returns the recovered value.
func (p panicError) Value() any { return p.value }
