package ui

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// A JSON payload is pretty-printed without anyone asking: the selection moves,
// the format command is issued, and the result reaches the detail pane.
func TestJSONIsPrettyPrintedAutomatically(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m, cmd := feed(t, m, "devices/a/state", `{"on":true,"temp":21.5}`)
	if cmd == nil {
		t.Fatal("no command was issued for a JSON payload")
	}
	msg := drainFormat(t, cmd)
	if !msg.JSON {
		t.Fatalf("FormatDoneMsg.JSON = false for %q", msg.Rendered)
	}
	if !strings.Contains(msg.Rendered, "\n  \"on\": true") {
		t.Fatalf("payload was not indented:\n%s", msg.Rendered)
	}

	next, _ := m.Update(msg)
	m = next.(Model)
	body := stripStyling(m.detail.View(m.context(), m.currentMessage(), 60, 12))
	if !strings.Contains(body, "\"temp\"") {
		t.Fatalf("the detail pane does not show the formatted payload:\n%s", body)
	}
}

// The formatter must be asked once per message, not once per frame: it runs on
// a goroutine and a repeat would spawn one on every Update while the selection
// sits still.
func TestFormatIsRequestedOncePerMessage(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m, cmd := feed(t, m, "devices/a/state", `{"a":1}`)
	if cmd == nil {
		t.Fatal("the first update issued no format command")
	}
	// In flight: no second request.
	if _, again := m.Update(app.BatchMsg{}); again != nil {
		t.Fatal("a second format command was issued while one was in flight")
	}
	next, _ := m.Update(drainFormat(t, cmd))
	m = next.(Model)
	// Done: still no second request.
	if _, again := m.Update(app.BatchMsg{}); again != nil {
		t.Fatal("a format command was re-issued for an already formatted message")
	}
}

// A payload that is not JSON must not leave a copy of itself in the model.
// FormatCmd returns the rendered text empty in that case, and the pane keeps
// reading the store.
func TestNonJSONPayloadIsNotCopiedIntoTheModel(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m, cmd := feed(t, m, "devices/a/state", "[not json after all")
	if cmd == nil {
		t.Fatal("a payload starting with [ should be handed to the formatter")
	}
	msg := drainFormat(t, cmd)
	if msg.JSON || msg.Rendered != "" {
		t.Fatalf("JSON = %v, Rendered = %q; want false and empty", msg.JSON, msg.Rendered)
	}
	next, _ := m.Update(msg)
	m = next.(Model)
	if m.detail.Pretty != "" {
		t.Fatalf("the model holds a copy of a non-JSON payload: %q", m.detail.Pretty)
	}
	body := stripStyling(m.detail.View(m.context(), m.currentMessage(), 60, 12))
	if !strings.Contains(body, "not json") {
		t.Fatalf("the raw payload is not shown:\n%s", body)
	}
}

// A payload too large to format automatically is left alone. `F` still
// formats it, which is the difference between asking and being given.
func TestLargePayloadsAreNotFormattedAutomatically(t *testing.T) {
	m := newTestModel(t, 120, 40)
	big := "[" + strings.Repeat(`"x",`, maxAutoFormatBytes/4) + `"x"]`
	if _, cmd := feed(t, m, "devices/a/state", big); cmd != nil {
		t.Fatalf("a %d-byte payload was formatted without being asked", len(big))
	}
}

// `F` is a preference toggle, not a one-shot: turning it off must put the raw
// payload back even though the formatted copy is still in hand.
func TestFormatKeyTogglesBackToRaw(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m, cmd := feed(t, m, "devices/a/state", `{"on":true}`)
	next, _ := m.Update(drainFormat(t, cmd))
	m = next.(Model)

	indented := stripStyling(m.detail.View(m.context(), m.currentMessage(), 60, 12))
	if !strings.Contains(indented, "\n  \"on\"") {
		t.Fatalf("expected an indented payload:\n%s", indented)
	}

	m = press(t, m, "F")
	if m.detail.Format {
		t.Fatal("F did not turn pretty-printing off")
	}
	raw := stripStyling(m.detail.View(m.context(), m.currentMessage(), 60, 12))
	if strings.Contains(raw, "\n  \"on\"") {
		t.Fatalf("the payload is still indented after F:\n%s", raw)
	}
}

// The pane emits colour for JSON. The golden frames are ANSI-stripped, so
// nothing else would notice if highlighting silently stopped.
func TestJSONIsHighlighted(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m, cmd := feed(t, m, "devices/a/state", `{"on":true,"n":1}`)
	next, _ := m.Update(drainFormat(t, cmd))
	m = next.(Model)

	body := m.detail.View(m.context(), m.currentMessage(), 60, 12)
	if strings.Count(body, "\x1b[") < 4 {
		t.Fatalf("expected several styled spans in a highlighted payload:\n%q", body)
	}
}

// feed ingests one message and returns the command the update produced, which
// is where the automatic pretty-print request appears.
func feed(t *testing.T, m Model, topic, payload string) (Model, tea.Cmd) {
	t.Helper()
	ingestSeq++
	next, cmd := m.Update(app.BatchMsg{Msgs: []*mqtt.Message{{
		Seq:        ingestSeq,
		Topic:      topic,
		Payload:    []byte(payload),
		ReceivedAt: time.Unix(0, int64(ingestSeq)).UTC(),
	}}})
	return next.(Model), cmd
}

func drainFormat(t *testing.T, cmd tea.Cmd) app.FormatDoneMsg {
	t.Helper()
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, c := range batch {
			if done, ok := c().(app.FormatDoneMsg); ok {
				return done
			}
		}
		t.Fatal("no FormatDoneMsg in the batch")
	}
	done, ok := msg.(app.FormatDoneMsg)
	if !ok {
		t.Fatalf("command produced %T, want app.FormatDoneMsg", msg)
	}
	return done
}
