//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// §21 pitfall 14. Two subscriptions that both match a topic mean the broker
// delivers the message twice — mosquitto sends one copy per matching
// subscription — and the message list then shows every overlapping message
// twice, with per-topic counts and the header rate to match.
//
// This is the test that decides whether the dedupe in paho5 is right, because
// it depends on behaviour MQTT 5 leaves to the broker: §3.3.4 permits either
// one copy carrying every matching subscription identifier or one copy per
// subscription. The unit tests cover both shapes; only a real broker says
// which one you get.
func TestOverlappingSubscriptionsDeliverEachMessageOnce(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	sub := newClient(t, clientOptions(t, url))
	pub := newClient(t, clientOptions(t, url))
	drainEvents(t, pub)

	base := testTopic(t)
	// Three subscriptions matching the same topic, to catch an off-by-one
	// that would suppress one duplicate out of two. All are scoped under the
	// run's unique prefix: a global `#` would work too, but it would also
	// pull in the seed container's retained tree and every other test's
	// traffic, which makes a failure here much harder to read.
	subscribe(t, sub, base+"/#", 0)
	subscribe(t, sub, base+"/devices/+/state", 0)
	subscribe(t, sub, base+"/devices/#", 0)

	const count = 20
	for i := 0; i < count; i++ {
		publish(t, pub, mqtt.PublishRequest{
			Topic:   base + "/devices/a/state",
			Payload: []byte(fmt.Sprintf(`{"seq":%d}`, i)),
			QoS:     1,
		})
	}

	// Collect for a fixed window rather than stopping at `count`: stopping
	// early is exactly how a duplicate-delivery bug hides, since the first
	// `count` arrivals look correct.
	seen := make(map[string]int)
	deadline := time.After(messageWait)
	total := 0
collect:
	for {
		select {
		case m, ok := <-sub.Messages():
			if !ok {
				t.Fatal("the message channel closed early")
			}
			if m.Topic != base+"/devices/a/state" {
				continue
			}
			seen[string(m.Payload)]++
			total++
			if total >= count {
				// Keep draining briefly: a duplicate arrives just after its
				// original, so a strict count would stop before seeing it.
				drainDeadline := time.After(2 * time.Second)
				for {
					select {
					case extra, ok := <-sub.Messages():
						if !ok {
							break collect
						}
						if extra.Topic == base+"/devices/a/state" {
							seen[string(extra.Payload)]++
							total++
						}
					case <-drainDeadline:
						break collect
					}
				}
			}
		case <-deadline:
			break collect
		}
	}

	if total != count {
		t.Errorf("received %d deliveries of %d published messages", total, count)
	}
	for i := 0; i < count; i++ {
		payload := fmt.Sprintf(`{"seq":%d}`, i)
		switch n := seen[payload]; {
		case n == 0:
			t.Errorf("message %s never arrived; dedupe suppressed a real message", payload)
		case n > 1:
			t.Errorf("message %s arrived %d times; overlapping subscriptions are double-counted", payload, n)
		}
	}

	// Deduped must be non-zero, or the test proved nothing: a broker that
	// collapses the copies itself would pass the assertions above without
	// exercising any of the logic under test.
	if got := sub.Deduped(); got == 0 {
		t.Log("the broker sent one copy per message and carried every matching " +
			"subscription identifier; dedupe was not exercised")
	} else {
		t.Logf("suppressed %d duplicate deliveries", got)
	}
	if got := sub.Dropped(); got != 0 {
		t.Errorf("Dropped() = %d; the ingest buffer should not have overflowed", got)
	}
}

// Removing the subscription that dedupe treats as canonical must promote the
// survivor. Get this wrong and unsubscribing from `#` silently stops delivery
// on every topic the narrower filter covers, which looks like the broker
// having gone quiet.
func TestUnsubscribingTheBroaderFilterKeepsMessagesFlowing(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	sub := newClient(t, clientOptions(t, url))
	pub := newClient(t, clientOptions(t, url))
	drainEvents(t, pub)

	base := testTopic(t)
	topic := base + "/devices/a/state"
	subscribe(t, sub, base+"/#", 0)
	subscribe(t, sub, base+"/devices/+/state", 0)

	publish(t, pub, mqtt.PublishRequest{Topic: topic, Payload: []byte("first"), QoS: 1})
	if m := awaitMessage(t, sub, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == topic
	}); string(m.Payload) != "first" {
		t.Fatalf("payload %q, want first", m.Payload)
	}

	ctx, cancel := contextWithTimeout(10 * time.Second)
	defer cancel()
	if err := sub.Unsubscribe(ctx, []string{base + "/#"}); err != nil {
		t.Fatalf("Unsubscribe: %v", err)
	}
	time.Sleep(settleWait)

	publish(t, pub, mqtt.PublishRequest{Topic: topic, Payload: []byte("second"), QoS: 1})
	if m := awaitMessage(t, sub, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == topic
	}); string(m.Payload) != "second" {
		t.Fatalf("payload %q, want second", m.Payload)
	}
}

// The canonical subscription must survive a reconnect, because the desired set
// is replayed wholesale in OnConnectionUp. If identifiers were reassigned in a
// different order the canonical choice could flip mid-session, which shows up
// as counts that jump for no reason.
func TestDedupeSurvivesAReconnect(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	sub := newClient(t, clientOptions(t, url))
	pub := newClient(t, clientOptions(t, url))
	drainEvents(t, pub)

	base := testTopic(t)
	topic := base + "/devices/a/state"
	subscribe(t, sub, base+"/#", 0)
	subscribe(t, sub, base+"/devices/+/state", 0)

	publish(t, pub, mqtt.PublishRequest{Topic: topic, Payload: []byte("before"), QoS: 1})
	awaitMessage(t, sub, messageWait, func(m *mqtt.Message) bool { return m.Topic == topic })
	before := sub.Deduped()

	dockerCompose(t, "restart", "mosquitto")
	waitState(t, sub, mqtt.StateConnected, connectWait)
	// The publisher lost the broker too; publishing before it is back fails
	// with "connection with the MQTT server is currently down".
	waitConnected(t, pub, connectWait)
	time.Sleep(settleWait)

	// Both subscriptions were replayed, so the overlap is back. One copy.
	publish(t, pub, mqtt.PublishRequest{Topic: topic, Payload: []byte("after"), QoS: 1})
	m := awaitMessage(t, sub, messageWait, func(m *mqtt.Message) bool { return m.Topic == topic })
	if string(m.Payload) != "after" {
		t.Fatalf("payload %q, want after", m.Payload)
	}

	select {
	case dup := <-sub.Messages():
		if dup.Topic == topic {
			t.Errorf("a duplicate arrived after the reconnect: dedupe did not survive the replay")
		}
	case <-time.After(2 * time.Second):
	}

	t.Logf("suppressed %d duplicates before the reconnect, %d after", before, sub.Deduped())
}
