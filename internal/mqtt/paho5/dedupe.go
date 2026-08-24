package paho5

import (
	"cmp"
	"slices"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// subFilter pairs an active subscription with the MQTT 5 subscription
// identifier we asked the broker to tag its deliveries with.
type subFilter struct {
	id     int
	filter string
}

// Subscribe to both `#` and `devices/+/state` and a message on
// `devices/a/state` matches two subscriptions. A broker is free to handle that
// either way: MQTT 5 §3.3.4 lets it send one copy carrying every matching
// subscription identifier, or one copy per subscription each carrying its own.
// Mosquitto does the latter, so without this the message list shows every
// overlapping message twice, the per-topic counts double, and the rate in the
// header is inflated — and the user's reasonable conclusion is that lazymqtt
// is inventing traffic (§21 pitfall 14).
//
// The rule: for any topic, the matching subscription with the lowest
// identifier is the canonical one. Keep the copy that carries it, drop the
// rest. Identifiers are assigned in subscribe order and kept stable across
// reconnects, so the choice is deterministic and does not flip when the
// connection blips.
//
// This deliberately does not compare payloads. Two identical messages
// published twice are two messages, and a viewer that hides the second one is
// lying about the broker.
func (a *Adapter) isDuplicateDelivery(topic string, props *mqtt.Properties) bool {
	tbl := a.canonical.Load()
	// With fewer than two active subscriptions no delivery can overlap.
	if tbl == nil || len(*tbl) < 2 {
		return false
	}
	if props == nil || len(props.SubIdentifiers) == 0 {
		// Either the broker does not support subscription identifiers or it
		// chose not to send one. Without it there is no way to tell a
		// duplicate delivery from a genuine republish, and showing a message
		// twice is a smaller failure than hiding one.
		return false
	}
	for _, sf := range *tbl {
		if !mqtt.MatchTopic(sf.filter, topic) {
			continue
		}
		// First match in ascending identifier order, so this is the canonical
		// subscription for this topic.
		return !slices.Contains(props.SubIdentifiers, sf.id)
	}
	return false
}

// Deduped counts deliveries suppressed as duplicates of an overlapping
// subscription.
func (a *Adapter) Deduped() uint64 { return a.deduped.Load() }

// subIDLocked returns the subscription identifier for a filter, assigning one
// on first use. Callers must hold a.mu.
//
// Identifiers are per-filter and survive reconnects, because the canonical
// choice above must not change when the connection drops. MQTT 5 allows
// 1..268,435,455; nextID only advances on a new filter, so a session would
// have to subscribe to a quarter of a billion distinct filters to exhaust it.
func (a *Adapter) subIDLocked(filter string) int {
	if id, ok := a.subIDs[filter]; ok {
		return id
	}
	a.nextID++
	a.subIDs[filter] = a.nextID
	return a.nextID
}

// rebuildCanonicalLocked republishes the dedupe snapshot from the desired set.
// Callers must hold a.mu.
//
// Only active subscriptions are included. A filter that was rejected or is
// still awaiting its SUBACK delivers nothing, so treating it as canonical
// would suppress every copy of the messages it covers rather than one.
func (a *Adapter) rebuildCanonicalLocked() {
	tbl := make([]subFilter, 0, len(a.desired))
	for _, d := range a.desired {
		if !d.Active {
			continue
		}
		if id, ok := a.subIDs[d.Filter]; ok {
			tbl = append(tbl, subFilter{id: id, filter: d.Filter})
		}
	}
	slices.SortFunc(tbl, func(x, y subFilter) int { return cmp.Compare(x.id, y.id) })
	a.canonical.Store(&tbl)
}
