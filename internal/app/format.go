package app

import (
	"bytes"
	"encoding/json"
	"fmt"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func sprintf(format string, args ...any) string { return fmt.Sprintf(format, args...) }

// FormatCmd pretty-prints a payload off the UI goroutine.
//
// A 14 MB JSON payload takes tens of milliseconds to indent; doing that
// inside Update blocks input for a visible beat.
func FormatCmd(m *mqtt.Message) tea.Cmd {
	if m == nil {
		return nil
	}
	topic, seq, payload := m.Topic, m.Seq, m.Payload
	return func() tea.Msg {
		rendered, isJSON, err := FormatPayload(payload)
		if !isJSON {
			// Returning the payload verbatim would have the UI hold a second
			// copy of every non-JSON message it displays, which on a 10 MB
			// payload is 10 MB of garbage per selection.
			rendered = ""
		}
		return FormatDoneMsg{Topic: topic, Seq: seq, Rendered: rendered, JSON: isJSON, Err: err}
	}
}

// FormatPayload pretty-prints JSON and leaves anything else alone. The second
// result reports whether the payload actually was JSON.
func FormatPayload(payload []byte) (string, bool, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return string(payload), false, nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "", "  "); err != nil {
		return string(payload), false, err
	}
	return buf.String(), true, nil
}

// LooksLikeJSON reports whether a payload really is JSON. It validates the
// whole document, so it belongs on a tea.Cmd goroutine rather than in Update.
func LooksLikeJSON(payload []byte) bool {
	return MaybeJSON(payload) && json.Valid(bytes.TrimSpace(payload))
}

// MaybeJSON is the cheap first-byte test used to decide whether a payload is
// worth handing to the formatter at all.
//
// It runs on the Bubble Tea goroutine for every message the detail pane
// shows — which under `follow` is every batch, twenty times a second — so it
// must not be the full json.Valid scan. A false positive costs one goroutine
// that returns "not JSON"; a full validation of a megabyte per batch costs
// frames.
func MaybeJSON(payload []byte) bool {
	t := bytes.TrimSpace(payload)
	return len(t) > 0 && (t[0] == '{' || t[0] == '[')
}
