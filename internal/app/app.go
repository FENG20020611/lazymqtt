package app

import (
	"context"
	"errors"
	"log/slog"
	"sync"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/mqtt/paho5"
)

// ClientFactory builds a client from resolved options. Tests substitute one
// that returns an mqtttest.Fake.
type ClientFactory func(opts mqtt.Options, log *slog.Logger) mqtt.Client

// DefaultClientFactory selects an adapter by protocol. Only the MQTT 5
// adapter ships today; a 3.1.1 adapter slots in behind the same interface.
func DefaultClientFactory(opts mqtt.Options, log *slog.Logger) mqtt.Client {
	return paho5.New(opts, log)
}

// App owns the live connection and the bridge feeding the UI. The root model
// holds a pointer to it and drives it through tea.Cmds.
type App struct {
	Config  config.Config
	Logger  *logging.Logger
	Factory ClientFactory

	// Sender is the tea.Program. It is set once, before the program runs.
	Sender Sender

	mu       sync.Mutex
	client   mqtt.Client
	bridge   *Bridge
	resolved config.Resolved
	cancel   context.CancelFunc
}

// New builds an App.
func New(cfg config.Config, log *logging.Logger) *App {
	if log == nil {
		log = logging.Discard()
	}
	return &App{Config: cfg, Logger: log, Factory: DefaultClientFactory}
}

// Client returns the live client, or nil when disconnected.
func (a *App) Client() mqtt.Client {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.client
}

// Resolved returns the profile behind the live connection.
func (a *App) Resolved() config.Resolved {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.resolved
}

// ErrNotConnected is returned by actions that need a live client.
var ErrNotConnected = errors.New("not connected to a broker")

// Connect tears down any existing connection and opens a new one. The bridge
// is started before Connect so the very first CONNACK is not missed.
func (a *App) Connect(ctx context.Context, r config.Resolved) error {
	a.Disconnect()

	client := a.Factory(r.Options, a.Logger.Logger)
	bridge := NewBridge(client, a.Sender, BridgeConfig{
		FlushInterval: a.Config.UI.Refresh(),
		BatchCap:      DefaultBatchCap,
		Logger:        a.Logger.Logger,
	})

	// One context per connection, cancelled on disconnect, so repeated
	// reconnect cycles cannot accumulate goroutines.
	runCtx, cancel := context.WithCancel(context.WithoutCancel(ctx))
	bridge.Start(runCtx)

	a.mu.Lock()
	a.client, a.bridge, a.resolved, a.cancel = client, bridge, r, cancel
	a.mu.Unlock()

	a.Logger.Info("connecting", "broker", r.Options.ServerURL, "client_id", r.Options.ClientID)

	if err := client.Connect(ctx); err != nil {
		return err
	}
	if len(r.Subs) > 0 {
		// Record the desired set now; the adapter re-issues it on every
		// OnConnectionUp, including the first.
		subCtx, subCancel := context.WithTimeout(ctx, 15*time.Second)
		defer subCancel()
		if err := client.Subscribe(subCtx, r.Subs); err != nil && !errors.Is(err, ErrNotConnected) {
			// A subscribe before CONNACK is expected to fail; the reconnect
			// path will apply it. Log, do not surface.
			a.Logger.Debug("initial subscribe deferred to OnConnectionUp", "err", err)
		}
	}
	return nil
}

// Disconnect closes the live connection and stops its bridge. It is safe to
// call when nothing is connected.
func (a *App) Disconnect() {
	a.mu.Lock()
	client, bridge, cancel := a.client, a.bridge, a.cancel
	a.client, a.bridge, a.cancel = nil, nil, nil
	a.mu.Unlock()

	if client != nil {
		ctx, cancelTimeout := context.WithTimeout(context.Background(), 2*time.Second)
		_ = client.Disconnect(ctx)
		cancelTimeout()
	}
	if bridge != nil {
		bridge.Stop()
	}
	if cancel != nil {
		cancel()
	}
	if client != nil {
		_ = client.Close()
	}
}

// Close shuts everything down. Called once, on exit.
func (a *App) Close() error {
	a.Disconnect()
	return a.Logger.Close()
}
