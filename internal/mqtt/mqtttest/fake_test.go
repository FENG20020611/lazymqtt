package mqtttest

import (
	"context"
	"errors"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func drain(f *Fake) []mqtt.Event {
	var out []mqtt.Event
	for {
		select {
		case ev := <-f.Events():
			out = append(out, ev)
		default:
			return out
		}
	}
}

func TestSubscriptionSetIsReissuedOnEveryConnectionUp(t *testing.T) {
	f := New(8)
	ctx := context.Background()
	if err := f.Connect(ctx); err != nil {
		t.Fatal(err)
	}
	if err := f.Subscribe(ctx, []mqtt.Subscription{{Filter: "a/#", QoS: 1}}); err != nil {
		t.Fatal(err)
	}
	before := f.Resubscribes.Load()

	// A network blip: down, then up again.
	f.Down(errors.New("connection reset"))
	f.Up()

	if got := f.Resubscribes.Load(); got != before+1 {
		t.Fatalf("resubscribes = %d, want %d — the desired set must be re-issued on reconnect", got, before+1)
	}
	if subs := f.Desired(); len(subs) != 1 || subs[0].Filter != "a/#" {
		t.Fatalf("desired set = %+v", subs)
	}
}

func TestAuthRejectionIsTerminal(t *testing.T) {
	f := New(4)
	f.ConnectErr = errors.New("not authorized")
	if err := f.Connect(context.Background()); err == nil {
		t.Fatal("Connect should have failed")
	}
	if got := f.Status().State; got != mqtt.StateFailed {
		t.Fatalf("state = %v, want failed (a rejected credential must not be retried)", got)
	}
	if !f.Status().NextRetryAt.IsZero() {
		t.Fatal("a failed connection scheduled a retry")
	}
}

func TestFullBufferDropsAndCounts(t *testing.T) {
	f := New(2)
	for i := 0; i < 5; i++ {
		f.Inject("a", "x")
	}
	if got := f.Dropped(); got != 3 {
		t.Fatalf("dropped = %d, want 3", got)
	}
	if len(f.Messages()) != 2 {
		t.Fatalf("buffered = %d, want 2", len(f.Messages()))
	}
}

func TestSubAckReportsGrantedQoSDowngrade(t *testing.T) {
	f := New(4)
	granted := byte(0)
	f.GrantQoS = &granted
	_ = f.Connect(context.Background())
	drain(f)
	if err := f.Subscribe(context.Background(), []mqtt.Subscription{{Filter: "a/#", QoS: 2}}); err != nil {
		t.Fatal(err)
	}
	for _, ev := range drain(f) {
		if ev.Kind == mqtt.EventSubAck {
			if len(ev.Subs) != 1 || ev.Subs[0].GrantedQoS != 0 || ev.Subs[0].QoS != 2 {
				t.Fatalf("suback = %+v, want a QoS 2 request granted at 0", ev.Subs)
			}
			return
		}
	}
	t.Fatal("no SubAck event was emitted")
}

func TestSubscribeFailureIsReported(t *testing.T) {
	f := New(4)
	f.SubscribeErr = errors.New("topic filter invalid")
	err := f.Subscribe(context.Background(), []mqtt.Subscription{{Filter: "a/#"}})
	if err == nil {
		t.Fatal("Subscribe should have failed")
	}
	for _, ev := range drain(f) {
		if ev.Kind == mqtt.EventSubAck && ev.Err != nil && !ev.Subs[0].Active {
			return
		}
	}
	t.Fatal("the failure was not surfaced as a SubAck event")
}
