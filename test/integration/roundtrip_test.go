//go:build integration

package integration

import (
	"fmt"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func TestRoundTrip(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	c := newClient(t, clientOptions(t, url))
	drainEvents(t, c)

	base := testTopic(t)
	subscribe(t, c, base+"/#", 0)
	publish(t, c, mqtt.PublishRequest{Topic: base + "/hello", Payload: []byte("world")})

	got := awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == base+"/hello"
	})
	if string(got.Payload) != "world" {
		t.Errorf("payload = %q, want \"world\"", got.Payload)
	}
	if got.Seq == 0 {
		t.Error("the adapter did not assign a sequence number")
	}
	if got.ReceivedAt.IsZero() {
		t.Error("the arrival time was not stamped")
	}
}

// A retained message must be delivered on subscribe, to a client that was not
// connected when it was published. This is the single most important MQTT
// behaviour for a viewer: it is what makes the tree populate on connect.
func TestRetainedMessageIsDeliveredOnSubscribe(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	base := testTopic(t)

	publisher := newClient(t, clientOptions(t, url))
	drainEvents(t, publisher)
	publish(t, publisher, mqtt.PublishRequest{
		Topic:   base + "/config",
		Payload: []byte("retained-value"),
		QoS:     1,
		Retain:  true,
	})
	t.Cleanup(func() { clearRetained(t, publisher, base+"/config") })

	// A second, entirely separate client that missed the publish.
	subscriber := newClient(t, clientOptions(t, url))
	drainEvents(t, subscriber)
	subscribe(t, subscriber, base+"/#", 1)

	got := awaitMessage(t, subscriber, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == base+"/config"
	})
	if string(got.Payload) != "retained-value" {
		t.Errorf("payload = %q", got.Payload)
	}
	if !got.Retained {
		t.Error("the retain flag was lost; the viewer would not mark the topic as retained")
	}
}

func TestQoS1And2RoundTrip(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))

	for _, qos := range []byte{1, 2} {
		t.Run(fmt.Sprintf("qos%d", qos), func(t *testing.T) {
			c := newClient(t, clientOptions(t, url))
			drainEvents(t, c)

			base := testTopic(t)
			subscribe(t, c, base+"/#", qos)
			publish(t, c, mqtt.PublishRequest{
				Topic:   base + "/reading",
				Payload: []byte("42"),
				QoS:     qos,
			})

			got := awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool {
				return m.Topic == base+"/reading"
			})
			if string(got.Payload) != "42" {
				t.Errorf("payload = %q", got.Payload)
			}
			if got.QoS != qos {
				t.Errorf("delivered at QoS %d, want %d", got.QoS, qos)
			}
		})
	}
}

// A payload larger than MaxPayloadBytes must be truncated at ingest, with the
// original size preserved for display. Truncating at render time means the
// memory has already been spent.
func TestOversizedPayloadIsTruncatedAtIngest(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	opts := clientOptions(t, url)
	opts.MaxPayloadBytes = 1024

	c := newClient(t, opts)
	drainEvents(t, c)

	base := testTopic(t)
	subscribe(t, c, base+"/#", 0)

	big := make([]byte, 64<<10)
	for i := range big {
		big[i] = byte('a' + i%26)
	}
	publish(t, c, mqtt.PublishRequest{Topic: base + "/big", Payload: big})

	got := awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == base+"/big"
	})
	if len(got.Payload) != 1024 {
		t.Errorf("payload is %d bytes, want it truncated to 1024", len(got.Payload))
	}
	if !got.Truncated {
		t.Error("the message is not flagged as truncated")
	}
	if got.OrigSize != len(big) {
		t.Errorf("OrigSize = %d, want %d", got.OrigSize, len(big))
	}
}

// The granted QoS may be lower than requested, and that has to be visible:
// a viewer silently receiving at QoS 0 while claiming QoS 2 is a lie.
func TestSubAckReportsTheGrantedQoS(t *testing.T) {
	url := requireBroker(t, brokerURL(envBroker, defaultBroker))
	c := newClient(t, clientOptions(t, url))

	base := testTopic(t)
	subscribe(t, c, base+"/#", 2)

	deadline := messageWait
	for {
		ev := waitEvent(t, c, deadline)
		if ev.Kind != mqtt.EventSubAck {
			continue
		}
		for _, s := range ev.Subs {
			if s.Filter != base+"/#" {
				continue
			}
			if s.Err != nil {
				t.Fatalf("SUBACK reported %v", s.Err)
			}
			if !s.Active {
				t.Error("the subscription was not marked active")
			}
			if s.GrantedQoS > 2 {
				t.Errorf("GrantedQoS = %d", s.GrantedQoS)
			}
			return
		}
	}
}

func waitEvent(t *testing.T, c mqtt.Client, timeout time.Duration) mqtt.Event {
	t.Helper()
	select {
	case ev, ok := <-c.Events():
		if !ok {
			t.Fatal("the event channel closed")
		}
		return ev
	case <-time.After(timeout):
		t.Fatalf("timed out after %s waiting for an event", timeout)
		return mqtt.Event{}
	}
}
