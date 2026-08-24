// Package mqtt defines the port through which lazymqtt talks to an MQTT
// broker, plus the domain types that cross that boundary.
//
// This package must never import anything from internal/ui or internal/app.
// The dependency direction is ui -> app -> {store, mqtt, config}, and it is
// what makes the MQTT layer testable without a terminal.
package mqtt

import (
	"context"
	"crypto/tls"
	"time"
)

// Message is a single received PUBLISH, normalised across protocol versions.
type Message struct {
	Seq        uint64 // monotonic, assigned at ingest; ordering + dedupe key
	Topic      string
	Payload    []byte // truncated at ingest to Options.MaxPayloadBytes
	Truncated  bool   // true if the original exceeded the cap
	OrigSize   int    // pre-truncation size, for display
	QoS        byte
	Retained   bool
	Duplicate  bool
	ReceivedAt time.Time   // LOCAL clock — MQTT carries no broker timestamp
	Props      *Properties // nil for MQTT 3.1.1
}

// Properties carries the MQTT 5 PUBLISH properties. It is behind a pointer so
// that a v3.1.1 message costs one nil word rather than a zeroed struct.
type Properties struct {
	ContentType     string
	ResponseTopic   string
	CorrelationData []byte
	PayloadFormat   *byte // 0 = bytes, 1 = UTF-8
	MessageExpiry   *uint32
	SubIdentifiers  []int
	User            []UserProperty
}

// UserProperty is one MQTT 5 user property key/value pair.
type UserProperty struct{ Key, Value string }

// Subscription is one desired subscription and what the broker made of it.
type Subscription struct {
	Filter     string // may contain + and #
	QoS        byte
	Active     bool // false = requested but not yet SUBACK'd, or failed
	GrantedQoS byte
	Count      uint64 // messages matched
	CreatedAt  time.Time
	Err        error // SUBACK failure reason
}

// PublishRequest is an outbound PUBLISH.
type PublishRequest struct {
	Topic   string
	Payload []byte
	QoS     byte
	Retain  bool
	Props   *Properties
}

// EventKind discriminates the connection-lifecycle events.
type EventKind int

const (
	EventConnecting EventKind = iota
	EventUp
	EventDown
	EventSubAck
	EventUnsubAck
	EventPubAck
	EventError
)

func (k EventKind) String() string {
	switch k {
	case EventConnecting:
		return "connecting"
	case EventUp:
		return "up"
	case EventDown:
		return "down"
	case EventSubAck:
		return "suback"
	case EventUnsubAck:
		return "unsuback"
	case EventPubAck:
		return "puback"
	case EventError:
		return "error"
	}
	return "unknown"
}

// Event is a connection-lifecycle notification. Events are rare and must not
// be dropped; messages are frequent and must be droppable. That is why they
// travel on two separate channels.
type Event struct {
	Kind   EventKind
	Status ConnStatus
	Subs   []Subscription
	Topic  string
	Err    error
}

// Options is a fully resolved connection configuration. The adapter performs
// no file I/O, no secret resolution and no environment lookup: everything is
// already resolved by internal/config before it gets here.
type Options struct {
	ServerURL       string // tcp:// | tls:// | mqtts:// | ws:// | wss://
	ClientID        string
	Username        string
	Password        []byte
	KeepAlive       time.Duration
	ConnectTimeout  time.Duration
	CleanStart      bool
	SessionExpiry   uint32
	TLS             *tls.Config
	MaxPayloadBytes int
	IngestBuffer    int
	Protocol        string // "5" | "3.1.1" | "auto"
}

// Client is the port. Two implementations are planned (MQTT 5 via autopaho
// and MQTT 3.1.1 via paho.mqtt.golang) plus an in-memory fake for tests,
// which is what earns this interface its place.
type Client interface {
	Connect(ctx context.Context) error
	Disconnect(ctx context.Context) error
	Subscribe(ctx context.Context, subs []Subscription) error
	Unsubscribe(ctx context.Context, filters []string) error
	Publish(ctx context.Context, msg PublishRequest) error
	Events() <-chan Event      // connection lifecycle, NOT messages
	Messages() <-chan *Message // the firehose
	Dropped() uint64           // messages discarded because the buffer was full
	Close() error
}
