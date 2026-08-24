package store

// Limits are the four independent caps that bound resident memory. All are
// enforced at ingest.
//
// Worst-case resident message memory is
//
//	MaxTopics * PerTopicHistory * average payload size
//
// which at the defaults with 200-byte payloads is roughly 62 MB.
type Limits struct {
	MaxTopics       int // number of tree leaves before LRU eviction
	PerTopicHistory int // messages retained per topic
	StreamHistory   int // the global firehose ring
	MaxPayloadBytes int // truncate any single payload beyond this
}

// DefaultLimits matches the shipped config defaults.
func DefaultLimits() Limits {
	return Limits{
		MaxTopics:       5000,
		PerTopicHistory: 50,
		StreamHistory:   2000,
		MaxPayloadBytes: 1 << 20,
	}
}

// Normalize clamps nonsensical values so a hand-edited config cannot produce
// a store that panics or grows without bound.
func (l Limits) Normalize() Limits {
	d := DefaultLimits()
	if l.MaxTopics < 1 {
		l.MaxTopics = d.MaxTopics
	}
	if l.PerTopicHistory < 1 {
		l.PerTopicHistory = 1
	}
	if l.StreamHistory < 1 {
		l.StreamHistory = 1
	}
	if l.MaxPayloadBytes < 1 {
		l.MaxPayloadBytes = d.MaxPayloadBytes
	}
	return l
}

// Stats is the counter set shown in the header.
type Stats struct {
	Received     uint64
	Dropped      uint64
	Truncated    uint64
	Evicted      uint64
	Topics       int // leaves that have received at least one message
	Nodes        int // total tree nodes
	PayloadBytes int64
	RatePerSec   float64 // EWMA over the 1 Hz tick
	PeakRate     float64
}
