// Package app is the seam between the MQTT world and the TUI. It is the only
// package that knows about both tea.Msg and mqtt.Message, and the only one
// allowed to translate between them.
package app

import (
	"time"

	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Level classifies a toast.
type Level int

const (
	LevelInfo Level = iota
	LevelSuccess
	LevelWarn
	LevelError
)

// --- messages from the bridge ---

// BatchMsg carries a coalesced batch of received messages. This type exists
// so the UI never sees one message per tea.Msg: at 10k msg/s that would be
// 10k Update calls per second and the app would freeze.
type BatchMsg struct {
	Msgs    []*mqtt.Message
	Dropped uint64 // cumulative
}

// ConnStateMsg reports a connection lifecycle transition.
type ConnStateMsg struct{ Status mqtt.ConnStatus }

// SubResultMsg reports the outcome of a SUBSCRIBE.
type SubResultMsg struct {
	Subs []mqtt.Subscription
	Err  error
}

// UnsubResultMsg reports the outcome of an UNSUBSCRIBE.
type UnsubResultMsg struct {
	Filters []string
	Err     error
}

// PubResultMsg reports the outcome of a PUBLISH.
type PubResultMsg struct {
	Topic string
	Err   error
}

// ErrorMsg reports a failure. Fatal errors tear the program down through the
// panic-recovery path so the terminal is restored first.
type ErrorMsg struct {
	Err   error
	Fatal bool
}

// LogMsg delivers a log entry to the in-app logs panel.
type LogMsg struct{ Entry logging.Entry }

// --- internal UI messages ---

// TickMsg fires at 1 Hz and drives the rate calculation, the reconnect
// countdown and the clock.
type TickMsg struct{ Time time.Time }

// ToastMsg requests a transient notification.
type ToastMsg struct {
	Text  string
	Level Level
	TTL   time.Duration
}

// ToastExpiredMsg retires a toast.
type ToastExpiredMsg struct{ ID int }

// FormatDoneMsg carries the result of an asynchronous pretty-print.
//
// Formatting a 2 MB JSON payload takes tens of milliseconds; doing it inside
// Update would freeze input.
type FormatDoneMsg struct {
	Topic string
	Seq   uint64
	// Rendered is the indented text, empty when the payload was not JSON.
	Rendered string
	// JSON reports whether the payload parsed as JSON, which is also what
	// decides whether the detail pane syntax-highlights it.
	JSON bool
	Err  error
}

// ConnectedMsg confirms that a connection attempt was started.
type ConnectedMsg struct {
	Broker string
	Err    error
}

// ClipboardCopiedMsg confirms an OSC52 clipboard write.
type ClipboardCopiedMsg struct{ What string }
