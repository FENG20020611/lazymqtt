package paho5

import (
	"testing"

	"github.com/eclipse/paho.golang/paho"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// active builds a subscription that has been SUBACK'd.
func active(filter string) mqtt.Subscription {
	return mqtt.Subscription{Filter: filter, Active: true}
}

// withSubs returns an adapter whose dedupe snapshot reflects subs, with
// identifiers assigned in the order given.
func withSubs(t *testing.T, subs ...mqtt.Subscription) *Adapter {
	t.Helper()
	a := New(mqtt.Options{ServerURL: "tcp://127.0.0.1:1", ClientID: "dedupe"}, nil)
	t.Cleanup(func() { _ = a.Close() })
	a.mu.Lock()
	for _, s := range subs {
		a.desired = append(a.desired, s)
		a.subIDLocked(s.Filter)
	}
	a.rebuildCanonicalLocked()
	a.mu.Unlock()
	return a
}

// subIDOf returns the identifier assigned to a filter.
func subIDOf(t *testing.T, a *Adapter, filter string) int {
	t.Helper()
	a.mu.Lock()
	defer a.mu.Unlock()
	id, ok := a.subIDs[filter]
	if !ok {
		t.Fatalf("no subscription identifier for %q", filter)
	}
	return id
}

func props(ids ...int) *mqtt.Properties {
	return &mqtt.Properties{SubIdentifiers: ids}
}

// The overlap that motivates all of this: `#` and `devices/+/state` both match
// `devices/a/state`, so mosquitto sends the message twice.
func TestOverlappingSubscriptionsKeepExactlyOneCopy(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))
	hash := subIDOf(t, a, "#")
	state := subIDOf(t, a, "devices/+/state")

	if a.isDuplicateDelivery("devices/a/state", props(hash)) {
		t.Error("the copy from the canonical subscription was discarded; the message is lost entirely")
	}
	if !a.isDuplicateDelivery("devices/a/state", props(state)) {
		t.Error("the second copy was kept; the message list shows it twice")
	}
}

// A broker is allowed to send one copy carrying every matching identifier
// instead of one copy per subscription. That copy is the only one there is.
func TestASingleCopyCarryingEveryIdentifierIsKept(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))
	hash := subIDOf(t, a, "#")
	state := subIDOf(t, a, "devices/+/state")

	if a.isDuplicateDelivery("devices/a/state", props(state, hash)) {
		t.Error("dropped the only copy of the message")
	}
}

// Without an identifier there is no way to distinguish a duplicate delivery
// from a device publishing the same value twice, and hiding a real message is
// the worse failure.
func TestDeliveriesWithoutAnIdentifierAreKept(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))

	if a.isDuplicateDelivery("devices/a/state", nil) {
		t.Error("dropped a delivery that carried no identifier")
	}
	if a.isDuplicateDelivery("devices/a/state", props()) {
		t.Error("dropped a delivery whose identifier list was empty")
	}
}

// One subscription cannot overlap with itself, so the whole mechanism must be
// inert — this is the common case and it should cost nothing.
func TestASingleSubscriptionNeverSuppressesAnything(t *testing.T) {
	a := withSubs(t, active("#"))
	id := subIDOf(t, a, "#")

	if a.isDuplicateDelivery("devices/a/state", props(id)) {
		t.Error("suppressed a delivery with only one subscription active")
	}
	// Even an identifier we never issued: a stale delivery from a
	// subscription that has since been removed is still a real message.
	if a.isDuplicateDelivery("devices/a/state", props(99)) {
		t.Error("suppressed a delivery carrying an unknown identifier")
	}
}

// Subscriptions that do not overlap must both deliver, whatever their
// identifiers happen to be.
func TestNonOverlappingSubscriptionsBothDeliver(t *testing.T) {
	a := withSubs(t, active("sensors/#"), active("devices/#"))
	sensors := subIDOf(t, a, "sensors/#")
	devices := subIDOf(t, a, "devices/#")

	if a.isDuplicateDelivery("sensors/a/temp", props(sensors)) {
		t.Error("dropped a sensors message")
	}
	// devices/# has the higher identifier, but it is the only subscription
	// matching this topic, so it is canonical for it.
	if a.isDuplicateDelivery("devices/a/state", props(devices)) {
		t.Error("dropped a devices message because its identifier was not the lowest overall")
	}
}

// A rejected or not-yet-acknowledged subscription delivers nothing. Treating
// it as canonical would suppress every copy rather than one, which turns an
// overlap into total silence on those topics.
func TestAnInactiveSubscriptionIsNotCanonical(t *testing.T) {
	a := withSubs(t,
		mqtt.Subscription{Filter: "#"}, // requested, never SUBACK'd
		active("devices/+/state"),
		active("sensors/#"),
	)
	state := subIDOf(t, a, "devices/+/state")

	if a.isDuplicateDelivery("devices/a/state", props(state)) {
		t.Error("an unacknowledged subscription was treated as canonical, so the topic goes silent")
	}
}

// The canonical choice must not change across a reconnect: identifiers are
// keyed on the filter and survive the desired set being replayed.
func TestIdentifiersSurviveAReconnectReplay(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))
	before := subIDOf(t, a, "devices/+/state")

	// Replay, as OnConnectionUp does.
	a.mu.Lock()
	a.rebuildCanonicalLocked()
	after := a.subIDLocked("devices/+/state")
	a.mu.Unlock()

	if before != after {
		t.Fatalf("identifier changed across replay: %d then %d", before, after)
	}
	if !a.isDuplicateDelivery("devices/a/state", props(after)) {
		t.Error("the canonical subscription flipped after a reconnect, so counts would jump")
	}
}

// Unsubscribing from the canonical filter must promote the survivor, or every
// message on the overlapping topics is suppressed.
func TestUnsubscribingTheCanonicalFilterPromotesTheSurvivor(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))
	state := subIDOf(t, a, "devices/+/state")

	if !a.isDuplicateDelivery("devices/a/state", props(state)) {
		t.Fatal("precondition: devices/+/state should not be canonical while # is active")
	}

	a.mu.Lock()
	a.desired = []mqtt.Subscription{active("devices/+/state")}
	delete(a.subIDs, "#")
	a.rebuildCanonicalLocked()
	a.mu.Unlock()

	if a.isDuplicateDelivery("devices/a/state", props(state)) {
		t.Error("still suppressing deliveries after the overlapping subscription was removed")
	}
}

// The end-to-end path through the receive callback: a suppressed delivery must
// not reach the message channel, and must be counted so the difference between
// "dropped" and "deduped" stays visible.
func TestSuppressedDeliveriesNeverReachTheChannel(t *testing.T) {
	a := withSubs(t, active("#"), active("devices/+/state"))
	hash := subIDOf(t, a, "#")
	state := subIDOf(t, a, "devices/+/state")

	deliver := func(id int) {
		t.Helper()
		sid := id
		if _, err := a.onPublishReceived(paho.PublishReceived{Packet: &paho.Publish{
			Topic:      "devices/a/state",
			Payload:    []byte(`{"on":true}`),
			Properties: &paho.PublishProperties{SubscriptionIdentifier: &sid},
		}}); err != nil {
			t.Fatalf("onPublishReceived: %v", err)
		}
	}

	deliver(hash)
	deliver(state)

	if got := len(a.messages); got != 1 {
		t.Errorf("%d messages queued, want 1: the duplicate was not suppressed", got)
	}
	if got := a.Deduped(); got != 1 {
		t.Errorf("Deduped() = %d, want 1", got)
	}
	if got := a.Dropped(); got != 0 {
		t.Errorf("Dropped() = %d; a deduped delivery must not read as a dropped one", got)
	}

	m := <-a.messages
	if m.Topic != "devices/a/state" {
		t.Errorf("kept the wrong message: %q", m.Topic)
	}
}
