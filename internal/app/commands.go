package app

import (
	"context"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// ActionTimeout bounds a single user-initiated MQTT operation.
const ActionTimeout = 10 * time.Second

// ConnectCmd resolves a broker profile and opens a connection. Resolution
// runs off the UI goroutine because password_cmd may take seconds.
func (a *App) ConnectCmd(b config.Broker, topics []string, prompt config.PromptFunc) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		r, err := a.Config.Resolve(ctx, b, prompt, topics)
		if err != nil {
			return ConnectedMsg{Broker: b.Name, Err: err}
		}
		if err := a.Connect(ctx, r); err != nil {
			return ConnectedMsg{Broker: b.Name, Err: err}
		}
		return ConnectedMsg{Broker: b.Name}
	}
}

// DisconnectCmd closes the live connection.
func (a *App) DisconnectCmd() tea.Cmd {
	return func() tea.Msg {
		a.Disconnect()
		return ConnStateMsg{Status: mqtt.ConnStatus{State: mqtt.StateDisconnected}}
	}
}

// SubscribeCmd adds one filter to the desired set.
func (a *App) SubscribeCmd(filter string, qos byte) tea.Cmd {
	return func() tea.Msg {
		if err := mqtt.ValidateFilter(filter); err != nil {
			return SubResultMsg{Subs: []mqtt.Subscription{{Filter: filter, QoS: qos}}, Err: err}
		}
		client := a.Client()
		if client == nil {
			return SubResultMsg{Subs: []mqtt.Subscription{{Filter: filter, QoS: qos}}, Err: ErrNotConnected}
		}
		ctx, cancel := context.WithTimeout(context.Background(), ActionTimeout)
		defer cancel()
		// The adapter emits the SubAck event; returning nil avoids reporting
		// the same outcome twice.
		_ = client.Subscribe(ctx, []mqtt.Subscription{{Filter: filter, QoS: qos}})
		return nil
	}
}

// UnsubscribeCmd removes a filter from the desired set.
func (a *App) UnsubscribeCmd(filter string) tea.Cmd {
	return func() tea.Msg {
		client := a.Client()
		if client == nil {
			return UnsubResultMsg{Filters: []string{filter}, Err: ErrNotConnected}
		}
		ctx, cancel := context.WithTimeout(context.Background(), ActionTimeout)
		defer cancel()
		err := client.Unsubscribe(ctx, []string{filter})
		return UnsubResultMsg{Filters: []string{filter}, Err: err}
	}
}

// PublishCmd sends one message. From the UI's perspective publishing is
// fire-and-forget; the QoS 1/2 acknowledgement arrives as an event.
func (a *App) PublishCmd(req mqtt.PublishRequest) tea.Cmd {
	return func() tea.Msg {
		if err := mqtt.ValidateTopic(req.Topic); err != nil {
			return PubResultMsg{Topic: req.Topic, Err: err}
		}
		client := a.Client()
		if client == nil {
			return PubResultMsg{Topic: req.Topic, Err: ErrNotConnected}
		}
		ctx, cancel := context.WithTimeout(context.Background(), ActionTimeout)
		defer cancel()
		if err := client.Publish(ctx, req); err != nil {
			return PubResultMsg{Topic: req.Topic, Err: err}
		}
		return nil
	}
}

// ReconnectCmd forces an immediate retry of the last profile.
func (a *App) ReconnectCmd() tea.Cmd {
	r := a.Resolved()
	if r.Options.ServerURL == "" {
		return nil
	}
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := a.Connect(ctx, r); err != nil {
			return ConnectedMsg{Broker: r.Name, Err: err}
		}
		return ConnectedMsg{Broker: r.Name}
	}
}

// TickCmd schedules the next 1 Hz tick.
func TickCmd() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return TickMsg{Time: t} })
}

// Toast returns a command producing a transient notification.
func Toast(level Level, format string, args ...any) tea.Cmd {
	text := format
	if len(args) > 0 {
		text = sprintf(format, args...)
	}
	return func() tea.Msg {
		return ToastMsg{Text: text, Level: level, TTL: 4 * time.Second}
	}
}

// ExpireToastCmd retires a toast after its TTL.
func ExpireToastCmd(id int, ttl time.Duration) tea.Cmd {
	return tea.Tick(ttl, func(time.Time) tea.Msg { return ToastExpiredMsg{ID: id} })
}
