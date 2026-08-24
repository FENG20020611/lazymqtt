//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// This is the test the whole suite exists for.
//
// §12.1: autopaho reconnects on its own, but the subscriptions belong to the
// session. A broker restart with clean_start produces a fresh session with no
// subscriptions, and a client that only subscribes once at startup then sits
// on a healthy connection receiving nothing. The UI says "Connected", the
// counters stop, and nothing anywhere reports an error.
//
// Asserting StateConnected is therefore not enough. The assertion that matters
// is that a message published after the restart still arrives.
func TestMessagesFlowAgainAfterABrokerRestart(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))

	c := newClient(t, clientOptions(t, url))
	base := testTopic(t)
	subscribe(t, c, base+"/#", 1)

	publish(t, c, mqtt.PublishRequest{Topic: base + "/before", Payload: []byte("1"), QoS: 1})
	awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool { return m.Topic == base+"/before" })

	dockerCompose(t, "restart", "mosquitto")

	// The connection drops and comes back. Both transitions must be observed:
	// jumping straight to the publish would pass even if the client had never
	// noticed the outage.
	waitState(t, c, mqtt.StateReconnecting, 60*time.Second)
	waitState(t, c, mqtt.StateConnected, 90*time.Second)

	// Give the adapter's OnConnectionUp re-subscribe time to land its SUBACK.
	time.Sleep(2 * time.Second)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Retry the publish: immediately after a restart the broker may still be
	// settling, and a single failed publish would report the wrong fault.
	var published bool
	for attempt := 0; attempt < 5 && !published; attempt++ {
		if err := c.Publish(ctx, mqtt.PublishRequest{
			Topic:   base + "/after",
			Payload: []byte("2"),
			QoS:     1,
		}); err == nil {
			published = true
			break
		}
		time.Sleep(time.Second)
	}
	if !published {
		t.Fatal("could not publish after the restart")
	}

	got := awaitMessage(t, c, 30*time.Second, func(m *mqtt.Message) bool {
		return m.Topic == base+"/after"
	})
	if string(got.Payload) != "2" {
		t.Errorf("payload = %q", got.Payload)
	}
}

// Repeated reconnect cycles must not accumulate goroutines. §21 pitfall 12:
// invisible for an hour, then 4,000 goroutines.
func TestRepeatedConnectCyclesDoNotAccumulateGoroutines(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	opts := clientOptions(t, url)

	// One warm-up cycle first: the runtime, the TLS stack and paho all start
	// background goroutines on first use, and counting those as a leak would
	// make this test fail for the wrong reason.
	cycle(t, opts)
	settle()
	before := goroutines()

	for i := 0; i < 5; i++ {
		cycle(t, opts)
	}
	settle()

	if after := goroutines(); after > before+10 {
		t.Errorf("goroutine count went from %d to %d over five connect cycles", before, after)
	}
}

func cycle(t *testing.T, opts mqtt.Options) {
	t.Helper()
	a := newAdapter(opts)
	ctx, cancel := context.WithTimeout(context.Background(), connectWait)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect: %v", err)
	}
	waitState(t, a, mqtt.StateConnected, connectWait)
	if err := a.Subscribe(ctx, []mqtt.Subscription{{Filter: testTopic(t) + "/#", QoS: 0}}); err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}
