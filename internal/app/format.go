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
		rendered, err := FormatPayload(payload)
		return FormatDoneMsg{Topic: topic, Seq: seq, Rendered: rendered, Err: err}
	}
}

// FormatPayload pretty-prints JSON and leaves anything else alone.
func FormatPayload(payload []byte) (string, error) {
	trimmed := bytes.TrimSpace(payload)
	if len(trimmed) == 0 || !json.Valid(trimmed) {
		return string(payload), nil
	}
	var buf bytes.Buffer
	if err := json.Indent(&buf, trimmed, "", "  "); err != nil {
		return string(payload), err
	}
	return buf.String(), nil
}

// LooksLikeJSON reports whether a payload should be offered a pretty-print.
func LooksLikeJSON(payload []byte) bool {
	t := bytes.TrimSpace(payload)
	if len(t) == 0 {
		return false
	}
	if t[0] != '{' && t[0] != '[' {
		return false
	}
	return json.Valid(t)
}
