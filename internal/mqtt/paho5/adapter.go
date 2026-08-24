// Package paho5 implements mqtt.Client on top of eclipse/paho.golang's
// autopaho, which owns the connection lifecycle: initial connect, backoff,
// reconnect, and an OnConnectionUp callback that fires on every successful
// connection.
package paho5

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Adapter is the MQTT 5 implementation of mqtt.Client.
type Adapter struct {
	opts mqtt.Options
	log  *slog.Logger

	cm     *autopaho.ConnectionManager
	cancel context.CancelFunc

	messages chan *mqtt.Message
	events   chan mqtt.Event

	seq     atomic.Uint64
	dropped atomic.Uint64

	mu      sync.Mutex
	desired []mqtt.Subscription // the app's desired set, re-applied on every EventUp
	status  mqtt.ConnStatus
	closed  bool

	// wg tracks goroutines this adapter spawns, so shutdown is leak-free
	// across repeated reconnect cycles.
	wg sync.WaitGroup
}

var _ mqtt.Client = (*Adapter)(nil)

// New builds an adapter. It does no I/O; Connect starts the connection.
func New(opts mqtt.Options, log *slog.Logger) *Adapter {
	if opts.IngestBuffer < 64 {
		opts.IngestBuffer = 4096
	}
	if opts.MaxPayloadBytes <= 0 {
		opts.MaxPayloadBytes = 1 << 20
	}
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	return &Adapter{
		opts:     opts,
		log:      log,
		messages: make(chan *mqtt.Message, opts.IngestBuffer),
		events:   make(chan mqtt.Event, 16),
		status: mqtt.ConnStatus{
			State:        mqtt.StateDisconnected,
			Broker:       opts.ServerURL,
			ClientID:     opts.ClientID,
			ProtoVersion: "5.0",
		},
	}
}

// Events returns the connection lifecycle channel.
func (a *Adapter) Events() <-chan mqtt.Event { return a.events }

// Messages returns the firehose.
func (a *Adapter) Messages() <-chan *mqtt.Message { return a.messages }

// Dropped returns the number of messages discarded because the buffer was
// full. The UI shows it whenever it is non-zero: dropping is correct for a
// viewer, dropping silently is not.
func (a *Adapter) Dropped() uint64 { return a.dropped.Load() }

// Connect starts the connection manager. It returns as soon as the first
// attempt has been initiated; progress arrives on Events.
func (a *Adapter) Connect(ctx context.Context) error {
	u, err := url.Parse(a.opts.ServerURL)
	if err != nil {
		return fmt.Errorf("invalid broker url %q: %w", a.opts.ServerURL, err)
	}

	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		cancel()
		return errors.New("adapter is closed")
	}
	a.cancel = cancel
	a.status.State = mqtt.StateConnecting
	a.status.Since = time.Now()
	a.status.Attempt = 0
	a.status.Err = nil
	status := a.status
	a.mu.Unlock()

	a.emit(mqtt.Event{Kind: mqtt.EventConnecting, Status: status})

	keepAlive := uint16(a.opts.KeepAlive / time.Second)
	if keepAlive == 0 {
		keepAlive = 30
	}
	connectTimeout := a.opts.ConnectTimeout
	if connectTimeout <= 0 {
		connectTimeout = 10 * time.Second
	}

	cfg := autopaho.ClientConfig{
		ServerUrls:                    []*url.URL{u},
		TlsCfg:                        a.opts.TLS,
		KeepAlive:                     keepAlive,
		CleanStartOnInitialConnection: a.opts.CleanStart,
		SessionExpiryInterval:         a.opts.SessionExpiry,
		ConnectTimeout:                connectTimeout,
		ReconnectBackoff:              backoff,
		ConnectUsername:               a.opts.Username,
		ConnectPassword:               a.opts.Password,

		OnConnectionUp:   a.onConnectionUp,
		OnConnectionDown: a.onConnectionDown,
		OnConnectError:   a.onConnectError,

		Debug:      logging.NewPahoLogger(a.log, slog.LevelDebug, "autopaho"),
		Errors:     logging.NewPahoLogger(a.log, slog.LevelWarn, "autopaho"),
		PahoDebug:  logging.NewPahoLogger(a.log, slog.LevelDebug, "paho"),
		PahoErrors: logging.NewPahoLogger(a.log, slog.LevelWarn, "paho"),

		ClientConfig: paho.ClientConfig{
			ClientID:           a.opts.ClientID,
			OnPublishReceived:  []func(paho.PublishReceived) (bool, error){a.onPublishReceived},
			OnServerDisconnect: a.onServerDisconnect,
			OnClientError:      a.onClientError,
		},
	}

	cm, err := autopaho.NewConnection(runCtx, cfg)
	if err != nil {
		cancel()
		a.setFailed(err)
		return err
	}
	a.mu.Lock()
	a.cm = cm
	a.mu.Unlock()
	return nil
}

// translateConnackError converts a server denial into the port's ConnackError.
func translateConnackError(err error) error {
	var ce *autopaho.ConnackError
	if !errors.As(err, &ce) {
		return err
	}
	return &mqtt.ConnackError{
		ReasonCode: ce.ReasonCode,
		Reason:     ce.Reason,
		Err:        ce.Err,
	}
}

// backoff is 1s → 2s → 4s → … capped at 30s with jitter, matching what the UI
// counts down to.
var backoff = autopaho.NewExponentialBackoff(time.Second, 30*time.Second, 2*time.Second, 2.0)

// onPublishReceived runs on the client's read loop. It must never block:
// blocking it stalls ack processing until the broker drops the connection,
// and the symptom — a broker disconnecting a client that is doing nothing
// wrong — is baffling to diagnose.
func (a *Adapter) onPublishReceived(pr paho.PublishReceived) (bool, error) {
	p := pr.Packet
	if p == nil {
		return false, nil
	}
	orig := len(p.Payload)
	payload := p.Payload
	truncated := false
	if orig > a.opts.MaxPayloadBytes {
		// Truncate here, before the message enters the pipeline. Truncating
		// at render time means the memory cost has already been paid.
		payload = payload[:a.opts.MaxPayloadBytes]
		truncated = true
	}
	buf := make([]byte, len(payload))
	copy(buf, payload)

	m := &mqtt.Message{
		Seq:        a.seq.Add(1),
		Topic:      p.Topic,
		Payload:    buf,
		Truncated:  truncated,
		OrigSize:   orig,
		QoS:        p.QoS,
		Retained:   p.Retain,
		Duplicate:  p.Duplicate(),
		ReceivedAt: time.Now(),
		Props:      convertProperties(p.Properties),
	}

	select {
	case a.messages <- m:
	default:
		a.dropped.Add(1)
	}
	return true, nil
}

func convertProperties(p *paho.PublishProperties) *mqtt.Properties {
	if p == nil {
		return nil
	}
	out := &mqtt.Properties{
		ContentType:     p.ContentType,
		ResponseTopic:   p.ResponseTopic,
		CorrelationData: p.CorrelationData,
		PayloadFormat:   p.PayloadFormat,
		MessageExpiry:   p.MessageExpiry,
	}
	if p.SubscriptionIdentifier != nil {
		out.SubIdentifiers = []int{*p.SubscriptionIdentifier}
	}
	for _, u := range p.User {
		out.User = append(out.User, mqtt.UserProperty{Key: u.Key, Value: u.Value})
	}
	return out
}

// onConnectionUp fires on EVERY successful connection, including reconnects.
// Re-issuing the desired subscription set here rather than once at startup is
// what stops the classic bug where, after a network blip, the UI says
// "Connected" and no message ever arrives again.
func (a *Adapter) onConnectionUp(cm *autopaho.ConnectionManager, connack *paho.Connack) {
	a.mu.Lock()
	a.status.State = mqtt.StateConnected
	a.status.Since = time.Now()
	a.status.Attempt = 0
	a.status.Err = nil
	a.status.NextRetryAt = time.Time{}
	if connack != nil {
		a.status.SessionPresent = connack.SessionPresent
	}
	status := a.status
	desired := append([]mqtt.Subscription(nil), a.desired...)
	a.mu.Unlock()

	a.emit(mqtt.Event{Kind: mqtt.EventUp, Status: status})

	if len(desired) == 0 {
		return
	}
	// The callback must not block, so the SUBSCRIBE goes out on its own
	// goroutine, tracked so shutdown stays leak-free.
	a.wg.Add(1)
	go func() {
		defer a.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		// The outcome is reported as a SubAck event, not through this return.
		_ = a.subscribe(ctx, cm, desired)
	}()
}

func (a *Adapter) onConnectionDown() bool {
	a.mu.Lock()
	if a.status.State != mqtt.StateFailed {
		a.status.State = mqtt.StateReconnecting
	}
	status := a.status
	closed := a.closed
	a.mu.Unlock()
	a.emit(mqtt.Event{Kind: mqtt.EventDown, Status: status})
	// Returning false tells autopaho to stop trying.
	return !closed && status.State != mqtt.StateFailed
}

func (a *Adapter) onConnectError(err error) {
	if err == nil {
		return
	}
	// autopaho reports a server denial as *autopaho.ConnackError, which
	// carries its reason code as a field. Translate it into the port's own
	// error so mqtt.Fatal can classify it — without this, an authentication
	// rejection is indistinguishable from a network blip and autopaho happily
	// hammers the broker with a password it has already been told is wrong.
	err = translateConnackError(err)
	if mqtt.Fatal(err) {
		a.setFailed(err)
		return
	}
	a.mu.Lock()
	a.status.Attempt++
	a.status.State = mqtt.StateReconnecting
	a.status.Err = err
	a.status.NextRetryAt = time.Now().Add(backoff(a.status.Attempt))
	status := a.status
	a.mu.Unlock()
	a.emit(mqtt.Event{Kind: mqtt.EventError, Status: status, Err: err})
}

func (a *Adapter) onServerDisconnect(d *paho.Disconnect) {
	err := errors.New("server sent DISCONNECT")
	if d != nil {
		err = fmt.Errorf("server sent DISCONNECT: %s", mqtt.ReasonText(d.ReasonCode))
	}
	a.mu.Lock()
	a.status.Err = err
	if a.status.State == mqtt.StateConnected {
		a.status.State = mqtt.StateReconnecting
	}
	status := a.status
	a.mu.Unlock()
	a.emit(mqtt.Event{Kind: mqtt.EventDown, Status: status, Err: err})
}

func (a *Adapter) onClientError(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.status.Err = err
	status := a.status
	a.mu.Unlock()
	a.emit(mqtt.Event{Kind: mqtt.EventError, Status: status, Err: err})
}

func (a *Adapter) setFailed(err error) {
	a.mu.Lock()
	a.status.State = mqtt.StateFailed
	a.status.Err = err
	a.status.NextRetryAt = time.Time{}
	status := a.status
	a.mu.Unlock()
	a.log.Error("connection failed permanently", "err", err)
	a.emit(mqtt.Event{Kind: mqtt.EventError, Status: status, Err: err})
}

// emit publishes a lifecycle event. Events are rare, so a modest buffer plus
// a drop-on-full guard is enough to guarantee no callback ever blocks.
func (a *Adapter) emit(ev mqtt.Event) {
	select {
	case a.events <- ev:
	default:
		a.log.Warn("event channel full, dropping event", "kind", ev.Kind.String())
	}
}

// Status returns the current connection status.
func (a *Adapter) Status() mqtt.ConnStatus {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.status
}

// Subscribe records the desired set and applies it. The set is idempotent and
// re-appliable: the adapter does not remember subscriptions on the app's
// behalf beyond replaying this set on reconnect.
func (a *Adapter) Subscribe(ctx context.Context, subs []mqtt.Subscription) error {
	a.mu.Lock()
	for _, s := range subs {
		replaced := false
		for i := range a.desired {
			if a.desired[i].Filter == s.Filter {
				a.desired[i] = s
				replaced = true
				break
			}
		}
		if !replaced {
			a.desired = append(a.desired, s)
		}
	}
	cm := a.cm
	a.mu.Unlock()

	if cm == nil {
		return errors.New("not connected")
	}
	return a.subscribe(ctx, cm, subs)
}

func (a *Adapter) subscribe(ctx context.Context, cm *autopaho.ConnectionManager, subs []mqtt.Subscription) error {
	if len(subs) == 0 {
		return nil
	}
	opts := make([]paho.SubscribeOptions, 0, len(subs))
	for _, s := range subs {
		opts = append(opts, paho.SubscribeOptions{Topic: s.Filter, QoS: s.QoS})
	}
	ack, err := cm.Subscribe(ctx, &paho.Subscribe{Subscriptions: opts})
	result := make([]mqtt.Subscription, len(subs))
	copy(result, subs)
	if err != nil {
		// A subscribe issued before CONNACK, or while reconnecting, is not a
		// failure: the desired set is already recorded and OnConnectionUp
		// re-issues it. Reporting it would put a red toast on screen every
		// time the app starts.
		if errors.Is(err, autopaho.ConnectionDownError) {
			a.log.Debug("subscribe deferred until the connection is up",
				"filters", len(subs))
			return nil
		}
		for i := range result {
			result[i].Active, result[i].Err = false, err
		}
		a.emit(mqtt.Event{Kind: mqtt.EventSubAck, Subs: result, Err: err, Status: a.Status()})
		return err
	}
	for i := range result {
		if ack != nil && i < len(ack.Reasons) {
			code := ack.Reasons[i]
			if code > 2 {
				result[i].Active = false
				result[i].Err = fmt.Errorf("subscribe rejected: %s", mqtt.ReasonText(code))
			} else {
				result[i].Active = true
				result[i].GrantedQoS = code
			}
		} else {
			result[i].Active = true
			result[i].GrantedQoS = result[i].QoS
		}
		if result[i].CreatedAt.IsZero() {
			result[i].CreatedAt = time.Now()
		}
	}
	a.mu.Lock()
	for _, r := range result {
		for i := range a.desired {
			if a.desired[i].Filter == r.Filter {
				a.desired[i].Active = r.Active
				a.desired[i].GrantedQoS = r.GrantedQoS
				a.desired[i].Err = r.Err
				if a.desired[i].CreatedAt.IsZero() {
					a.desired[i].CreatedAt = r.CreatedAt
				}
			}
		}
	}
	a.mu.Unlock()
	a.emit(mqtt.Event{Kind: mqtt.EventSubAck, Subs: result, Status: a.Status()})
	return nil
}

// Unsubscribe drops filters from the desired set and tells the broker.
func (a *Adapter) Unsubscribe(ctx context.Context, filters []string) error {
	a.mu.Lock()
	kept := a.desired[:0]
	for _, d := range a.desired {
		drop := false
		for _, f := range filters {
			if d.Filter == f {
				drop = true
				break
			}
		}
		if !drop {
			kept = append(kept, d)
		}
	}
	a.desired = kept
	cm := a.cm
	a.mu.Unlock()

	if cm == nil {
		return errors.New("not connected")
	}
	_, err := cm.Unsubscribe(ctx, &paho.Unsubscribe{Topics: filters})
	a.emit(mqtt.Event{Kind: mqtt.EventUnsubAck, Err: err, Status: a.Status()})
	return err
}

// Publish sends one message.
func (a *Adapter) Publish(ctx context.Context, req mqtt.PublishRequest) error {
	a.mu.Lock()
	cm := a.cm
	a.mu.Unlock()
	if cm == nil {
		return errors.New("not connected")
	}
	p := &paho.Publish{
		Topic:   req.Topic,
		Payload: req.Payload,
		QoS:     req.QoS,
		Retain:  req.Retain,
	}
	if req.Props != nil {
		p.Properties = &paho.PublishProperties{
			ContentType:     req.Props.ContentType,
			ResponseTopic:   req.Props.ResponseTopic,
			CorrelationData: req.Props.CorrelationData,
			PayloadFormat:   req.Props.PayloadFormat,
			MessageExpiry:   req.Props.MessageExpiry,
		}
		for _, u := range req.Props.User {
			p.Properties.User = append(p.Properties.User, paho.UserProperty{Key: u.Key, Value: u.Value})
		}
	}
	_, err := cm.Publish(ctx, p)
	a.emit(mqtt.Event{Kind: mqtt.EventPubAck, Topic: req.Topic, Err: err, Status: a.Status()})
	return err
}

// Disconnect closes the session cleanly and stops reconnection.
func (a *Adapter) Disconnect(ctx context.Context) error {
	a.mu.Lock()
	cm, cancel := a.cm, a.cancel
	a.cm, a.cancel = nil, nil
	a.status.State = mqtt.StateDisconnected
	a.status.Err = nil
	status := a.status
	a.mu.Unlock()

	var err error
	if cm != nil {
		err = cm.Disconnect(ctx)
		<-cm.Done()
	}
	if cancel != nil {
		cancel()
	}
	a.wg.Wait()
	a.emit(mqtt.Event{Kind: mqtt.EventDown, Status: status})
	return err
}

// Close releases everything and closes the channels. It is safe to call more
// than once.
func (a *Adapter) Close() error {
	a.mu.Lock()
	if a.closed {
		a.mu.Unlock()
		return nil
	}
	a.closed = true
	a.mu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	err := a.Disconnect(ctx)

	close(a.messages)
	close(a.events)
	return err
}
