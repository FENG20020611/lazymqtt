// Package mqtttest provides an in-memory mqtt.Client for tests. Everything
// above the port layer uses it, and it runs in microseconds.
package mqtttest

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Fake is a programmable mqtt.Client.
type Fake struct {
	messages chan *mqtt.Message
	events   chan mqtt.Event

	mu        sync.Mutex
	desired   []mqtt.Subscription
	published []mqtt.PublishRequest
	status    mqtt.ConnStatus
	closed    bool

	seq     atomic.Uint64
	dropped atomic.Uint64

	// SubscribeErr, when set, fails the next SUBSCRIBE and is then cleared.
	SubscribeErr error
	// SubscribeErrOnCall fails the Nth subscribe call (1-based); 0 disables.
	SubscribeErrOnCall int
	subscribeCalls     int
	// GrantQoS, when non-nil, is the QoS the broker grants regardless of the
	// requested value — the silent downgrade users need to see.
	GrantQoS *byte
	// PublishErr fails Publish.
	PublishErr error
	// ConnectErr fails Connect.
	ConnectErr error

	// Resubscribes counts how many times the desired set was re-issued, which
	// is what the reconnect test asserts on.
	Resubscribes atomic.Int32
}

var _ mqtt.Client = (*Fake)(nil)

// New returns a Fake with the given message buffer capacity.
func New(bufferSize int) *Fake {
	if bufferSize < 1 {
		bufferSize = 16
	}
	return &Fake{
		messages: make(chan *mqtt.Message, bufferSize),
		events:   make(chan mqtt.Event, 64),
		status:   mqtt.ConnStatus{State: mqtt.StateDisconnected, Broker: "fake://memory"},
	}
}

// Events implements mqtt.Client.
func (f *Fake) Events() <-chan mqtt.Event { return f.events }

// Messages implements mqtt.Client.
func (f *Fake) Messages() <-chan *mqtt.Message { return f.messages }

// Dropped implements mqtt.Client.
func (f *Fake) Dropped() uint64 { return f.dropped.Load() }

// Connect implements mqtt.Client.
func (f *Fake) Connect(context.Context) error {
	if f.ConnectErr != nil {
		f.Fail(f.ConnectErr)
		return f.ConnectErr
	}
	f.setState(mqtt.StateConnecting, nil)
	f.emit(mqtt.Event{Kind: mqtt.EventConnecting, Status: f.Status()})
	f.Up()
	return nil
}

// Up simulates a successful connection, re-issuing the desired subscription
// set exactly as the real adapter's OnConnectionUp does.
func (f *Fake) Up() {
	f.setState(mqtt.StateConnected, nil)
	f.emit(mqtt.Event{Kind: mqtt.EventUp, Status: f.Status()})

	f.mu.Lock()
	desired := append([]mqtt.Subscription(nil), f.desired...)
	f.mu.Unlock()
	if len(desired) > 0 {
		f.Resubscribes.Add(1)
		_ = f.applySubscribe(desired)
	}
}

// Down simulates losing the connection.
func (f *Fake) Down(err error) {
	f.setState(mqtt.StateReconnecting, err)
	f.emit(mqtt.Event{Kind: mqtt.EventDown, Status: f.Status(), Err: err})
}

// Fail simulates a terminal failure, such as an authentication rejection.
// A failed connection is never retried.
func (f *Fake) Fail(err error) {
	f.setState(mqtt.StateFailed, err)
	f.emit(mqtt.Event{Kind: mqtt.EventError, Status: f.Status(), Err: err})
}

// Inject delivers a message, applying the same drop-oldest-on-full policy as
// the real adapter: a full buffer increments the drop counter and never
// blocks.
func (f *Fake) Inject(topic string, payload string) *mqtt.Message {
	m := &mqtt.Message{
		Seq:        f.seq.Add(1),
		Topic:      topic,
		Payload:    []byte(payload),
		ReceivedAt: time.Now(),
	}
	f.InjectMsg(m)
	return m
}

// InjectMsg delivers a fully formed message.
func (f *Fake) InjectMsg(m *mqtt.Message) {
	select {
	case f.messages <- m:
	default:
		f.dropped.Add(1)
	}
}

// Subscribe implements mqtt.Client.
func (f *Fake) Subscribe(_ context.Context, subs []mqtt.Subscription) error {
	f.mu.Lock()
	for _, s := range subs {
		replaced := false
		for i := range f.desired {
			if f.desired[i].Filter == s.Filter {
				f.desired[i] = s
				replaced = true
			}
		}
		if !replaced {
			f.desired = append(f.desired, s)
		}
	}
	f.mu.Unlock()
	return f.applySubscribe(subs)
}

func (f *Fake) applySubscribe(subs []mqtt.Subscription) error {
	f.mu.Lock()
	f.subscribeCalls++
	call := f.subscribeCalls
	err := f.SubscribeErr
	f.SubscribeErr = nil
	if f.SubscribeErrOnCall != 0 && call == f.SubscribeErrOnCall {
		err = errors.New("suback failure")
	}
	grant := f.GrantQoS
	f.mu.Unlock()

	result := make([]mqtt.Subscription, len(subs))
	copy(result, subs)
	for i := range result {
		if err != nil {
			result[i].Active, result[i].Err = false, err
			continue
		}
		result[i].Active = true
		result[i].GrantedQoS = result[i].QoS
		if grant != nil {
			result[i].GrantedQoS = *grant
		}
		result[i].CreatedAt = time.Now()
	}
	f.emit(mqtt.Event{Kind: mqtt.EventSubAck, Subs: result, Err: err, Status: f.Status()})
	return err
}

// Unsubscribe implements mqtt.Client.
func (f *Fake) Unsubscribe(_ context.Context, filters []string) error {
	f.mu.Lock()
	kept := f.desired[:0]
	for _, d := range f.desired {
		drop := false
		for _, x := range filters {
			if d.Filter == x {
				drop = true
			}
		}
		if !drop {
			kept = append(kept, d)
		}
	}
	f.desired = kept
	f.mu.Unlock()
	f.emit(mqtt.Event{Kind: mqtt.EventUnsubAck, Status: f.Status()})
	return nil
}

// Publish implements mqtt.Client.
func (f *Fake) Publish(_ context.Context, req mqtt.PublishRequest) error {
	if f.PublishErr != nil {
		f.emit(mqtt.Event{Kind: mqtt.EventPubAck, Topic: req.Topic, Err: f.PublishErr, Status: f.Status()})
		return f.PublishErr
	}
	f.mu.Lock()
	f.published = append(f.published, req)
	f.mu.Unlock()
	f.emit(mqtt.Event{Kind: mqtt.EventPubAck, Topic: req.Topic, Status: f.Status()})
	return nil
}

// Published returns every PublishRequest received so far.
func (f *Fake) Published() []mqtt.PublishRequest {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mqtt.PublishRequest(nil), f.published...)
}

// Desired returns the current desired subscription set.
func (f *Fake) Desired() []mqtt.Subscription {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]mqtt.Subscription(nil), f.desired...)
}

// Status returns the current connection status.
func (f *Fake) Status() mqtt.ConnStatus {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.status
}

// Disconnect implements mqtt.Client.
func (f *Fake) Disconnect(context.Context) error {
	f.setState(mqtt.StateDisconnected, nil)
	f.emit(mqtt.Event{Kind: mqtt.EventDown, Status: f.Status()})
	return nil
}

// Close implements mqtt.Client.
func (f *Fake) Close() error {
	f.mu.Lock()
	if f.closed {
		f.mu.Unlock()
		return nil
	}
	f.closed = true
	f.mu.Unlock()
	close(f.messages)
	close(f.events)
	return nil
}

func (f *Fake) setState(s mqtt.ConnState, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.status.State = s
	f.status.Err = err
	f.status.Since = time.Now()
	if s == mqtt.StateReconnecting {
		f.status.Attempt++
		f.status.NextRetryAt = time.Now().Add(time.Second)
	}
	if s == mqtt.StateConnected {
		f.status.Attempt = 0
		f.status.NextRetryAt = time.Time{}
	}
}

func (f *Fake) emit(ev mqtt.Event) {
	f.mu.Lock()
	closed := f.closed
	f.mu.Unlock()
	if closed {
		return
	}
	select {
	case f.events <- ev:
	default:
	}
}
