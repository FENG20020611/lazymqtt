package paho5

import (
	"context"
	"strings"
	"testing"
	"time"

	"go.uber.org/goleak"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// A connect that never succeeds is the common case in practice: a typo'd host,
// a VPN that is down, a broker that has not started yet. Each attempt starts
// an autopaho connection manager, and if Close does not reap it the process
// accumulates one connection manager per attempt while showing "reconnecting".
func TestClosingAnAdapterThatNeverConnectedLeavesNothingRunning(t *testing.T) {
	// Port 1 on the loopback interface: reserved, and nothing listens there.
	a := New(mqtt.Options{
		ServerURL:      "tcp://127.0.0.1:1",
		ClientID:       "leak-test",
		ConnectTimeout: 200 * time.Millisecond,
		KeepAlive:      time.Second,
	}, nil)

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	// Let it fail at least once, so there is a retry loop to reap.
	select {
	case <-a.Events():
	case <-time.After(2 * time.Second):
	}

	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	// A second Close must be harmless: the UI's shutdown path and the
	// deferred close in main can both reach it.
	if err := a.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

// A panic on a goroutine the adapter spawned cannot be caught by main's
// deferred recover — it takes the process down with the terminal still in raw
// mode and the alt screen active. The deferred SUBSCRIBE runs on such a
// goroutine, on every single connection-up, so this is on the reconnect path.
func TestAPanicOnASpawnedGoroutineIsReportedNotFatal(t *testing.T) {
	a := New(mqtt.Options{ServerURL: "tcp://127.0.0.1:1", ClientID: "panic-test"}, nil)
	t.Cleanup(func() { _ = a.Close() })

	a.spawn(func() { panic("subscribe went wrong") })

	select {
	case ev := <-a.Events():
		if ev.Kind != mqtt.EventError {
			t.Fatalf("got event %v, want an error event", ev.Kind)
		}
		if ev.Err == nil || !strings.Contains(ev.Err.Error(), "subscribe went wrong") {
			t.Fatalf("error %v does not carry the panic value", ev.Err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("a panicking goroutine reported nothing; the UI would show no sign of it")
	}

	// The WaitGroup must still be balanced, or Close hangs forever on a
	// panic — which is its own way of leaving the terminal broken.
	done := make(chan struct{})
	go func() { a.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("wg.Wait blocked after a panic; Close would hang")
	}
}

// The same, repeated: this is the shape of the reconnect leak in §21 pitfall
// 12, where nothing looks wrong for an hour and then there are 4,000
// goroutines.
func TestRepeatedFailedConnectsDoNotAccumulate(t *testing.T) {
	for i := 0; i < 5; i++ {
		a := New(mqtt.Options{
			ServerURL:      "tcp://127.0.0.1:1",
			ClientID:       "leak-cycle",
			ConnectTimeout: 100 * time.Millisecond,
			KeepAlive:      time.Second,
		}, nil)
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		if err := a.Connect(ctx); err != nil {
			cancel()
			t.Fatalf("Connect %d: %v", i, err)
		}
		if err := a.Close(); err != nil {
			cancel()
			t.Fatalf("Close %d: %v", i, err)
		}
		cancel()
	}
	// goleak.VerifyTestMain does the assertion; getting here without a hang
	// is the other half of it.
}
