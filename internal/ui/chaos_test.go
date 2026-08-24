package ui

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// The payloads and topics in this file are what a real broker actually
// carries: half-finished firmware writing raw bytes, a device with an emoji in
// its name, a log topic replaying ANSI-coloured output, and one process
// shipping a 10 MB blob onto a topic nobody expected.
//
// None of it may crash the renderer, corrupt the terminal, or make a frame
// wider than the window.

func chaosModel(t *testing.T, w, h int, msgs ...*mqtt.Message) Model {
	t.Helper()
	cfg := config.Default()
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(w, h)
	next, _ := m.Update(app.BatchMsg{Msgs: msgs})
	return next.(Model)
}

func chaosMsg(seq int, topic string, payload []byte) *mqtt.Message {
	return &mqtt.Message{
		Seq:        uint64(seq),
		Topic:      topic,
		Payload:    payload,
		ReceivedAt: time.Date(2026, 3, 14, 9, 0, 0, 0, time.UTC),
	}
}

// A payload full of escape sequences is the security case: the store keeps the
// raw bytes so a copy stays faithful, but nothing reaching the terminal may
// carry an ESC.
func TestAnsiPayloadsNeverReachTheTerminal(t *testing.T) {
	payloads := [][]byte{
		[]byte("\x1b[31mred\x1b[0m"),
		[]byte("\x1b]0;window title changed\x07"),
		[]byte("\x1b[2J\x1b[H cleared your screen "),
		[]byte("\x1b]52;c;aGVsbG8=\x07"), // OSC52: a clipboard write
		[]byte("\x9b31m"),                // C1 CSI, the single-byte form
		[]byte("before\x00\x01\x02after"),
		[]byte("\xff\xfe invalid utf-8 \xc3"),
		[]byte("bidi \u202eoverride\u202c here"),
	}
	msgs := make([]*mqtt.Message, 0, len(payloads))
	for i, p := range payloads {
		msgs = append(msgs, chaosMsg(i+1, fmt.Sprintf("hostile/%d", i), p))
	}

	m := chaosModel(t, 120, 40, msgs...)
	// Look at both panes: the list cell and the detail block sanitise through
	// different entry points.
	m = press(t, m, "1", "l")
	frames := []string{m.View().Content, press(t, m, "2").View().Content, press(t, m, "3").View().Content}

	for i, f := range frames {
		body := stripStyling(f)
		if strings.ContainsRune(body, 0x1b) {
			t.Errorf("frame %d let an ESC through to the terminal", i)
		}
		if strings.ContainsRune(body, 0x07) {
			t.Errorf("frame %d let a BEL through to the terminal", i)
		}
		if strings.ContainsRune(body, 0x9b) {
			t.Errorf("frame %d let a C1 CSI through to the terminal", i)
		}
	}

	// The raw bytes survive in the store, because that is what `y` copies.
	n := m.store.Node("hostile/0")
	if n == nil || n.Last == nil {
		t.Fatal("hostile/0 is missing from the store")
	}
	if !strings.Contains(string(n.Last.Payload), "\x1b[31m") {
		t.Error("the store sanitised the payload; a copy would no longer be faithful")
	}
}

// Render cost must not scale with payload size.
//
// The detail pane can show at most a screenful, so the obvious implementation
// — sanitise the payload, split it into lines, then take the visible window —
// does megabytes of work per frame to display forty lines of it. The symptom
// is not a crash: it is an app that freezes for seconds while scrolling
// through a topic carrying firmware images, with no clue as to why.
func TestRenderCostDoesNotScaleWithPayloadSize(t *testing.T) {
	small := renderDuration(t, blob(4<<10))
	huge := renderDuration(t, blob(10<<20))

	// 2,500x the payload. Allow generous headroom for scheduling noise on a
	// loaded CI runner, but nothing like a linear relationship.
	if huge > 20*small+50*time.Millisecond {
		t.Errorf("rendering 10 MB took %s against %s for 4 KB; the cost is scaling with the payload",
			huge, small)
	}
	if huge > 250*time.Millisecond {
		t.Errorf("rendering a 10 MB payload took %s; the frame budget is 50 ms", huge)
	}
}

// The same must hold when scrolled deep into a large payload, and with
// wrapping on — the two paths window the payload differently.
func TestScrollingAndWrappingALargePayloadStayBounded(t *testing.T) {
	var lines []byte
	for i := 0; i < 200_000; i++ {
		lines = append(lines, []byte(fmt.Sprintf("line %06d of the firmware log\n", i))...)
	}
	m := chaosModel(t, 120, 40, chaosMsg(1, "firmware/log", lines))
	m = press(t, m, "1", "l", "3")

	for _, keys := range [][]string{{"G"}, {"ctrl+d", "ctrl+d", "ctrl+d"}, {"w"}, {"ctrl+d"}} {
		m = press(t, m, keys...)
		start := time.Now()
		out := m.View().Content
		if elapsed := time.Since(start); elapsed > 250*time.Millisecond {
			t.Errorf("a frame after %v took %s", keys, elapsed)
		}
		assertFits(t, out, 120, 40)
	}
}

func blob(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte('a' + i%26)
	}
	return b
}

// renderDuration is the best of several frames, so a single scheduling hiccup
// does not decide the result.
func renderDuration(t *testing.T, payload []byte) time.Duration {
	t.Helper()
	m := chaosModel(t, 120, 40, chaosMsg(1, "firmware/blob", payload))
	m = press(t, m, "1", "l")

	best := time.Duration(1 << 62)
	for i := 0; i < 5; i++ {
		start := time.Now()
		out := m.View().Content
		if d := time.Since(start); d < best {
			best = d
		}
		assertFits(t, out, 120, 40)
	}
	return best
}

// A payload that is one enormous line has no newline for the wrapper to break
// on, which is the case that produces a single 10-million-column row.
func TestSingleLineMegaPayloadDoesNotOverflowTheFrame(t *testing.T) {
	line := strings.Repeat("0123456789", 100_000) // 1 MB, no newlines
	m := chaosModel(t, 120, 40, chaosMsg(1, "logs/oneline", []byte(line)))
	m = press(t, m, "1", "l")
	assertFits(t, m.View().Content, 120, 40)

	// And with wrapping on, which is the path that actually reflows it.
	m = press(t, m, "w")
	assertFits(t, m.View().Content, 120, 40)
}

// Wide and emoji characters occupy two terminal cells, so a layout that counts
// runes instead of display width overflows by exactly the number of them.
func TestWideAndEmojiTopicsDoNotOverflowTheFrame(t *testing.T) {
	topics := []string{
		"家/居間/温度",
		"devices/🔥-heater/state",
		"devices/lamp-🚀🚀🚀🚀🚀🚀🚀🚀🚀🚀/state",
		"메트릭/서버/부하",
		"emoji/👨‍👩‍👧‍👦/family",
		strings.Repeat("é", 200) + "/long",
	}
	msgs := make([]*mqtt.Message, 0, len(topics))
	for i, tp := range topics {
		msgs = append(msgs, chaosMsg(i+1, tp, []byte("温度 21.5 °C 🔥")))
	}

	for _, size := range []struct{ w, h int }{{120, 40}, {80, 24}, {40, 20}} {
		m := chaosModel(t, size.w, size.h, msgs...)
		for _, n := range m.store.Flatten() {
			n.SetExpanded(true)
		}
		m.store.Invalidate()
		m = m.resize(size.w, size.h)
		assertFits(t, m.View().Content, size.w, size.h)
	}
}

// A deep topic must not push the tree's indentation past the panel width.
func TestVeryDeepTopicsStayInsideThePanel(t *testing.T) {
	segments := make([]string, 200)
	for i := range segments {
		segments[i] = fmt.Sprintf("level%02d", i)
	}
	deep := strings.Join(segments, "/")

	m := chaosModel(t, 120, 40, chaosMsg(1, deep, []byte("x")))
	for _, n := range m.store.Flatten() {
		n.SetExpanded(true)
	}
	m.store.Invalidate()
	m = m.resize(120, 40)
	assertFits(t, m.View().Content, 120, 40)
}

// Empty topic segments are legal MQTT and produce nodes with no label.
func TestEmptyTopicSegmentsAreRenderable(t *testing.T) {
	m := chaosModel(t, 120, 40,
		chaosMsg(1, "a//b", []byte("x")),
		chaosMsg(2, "/leading", []byte("x")),
		chaosMsg(3, "trailing/", []byte("x")),
	)
	for _, n := range m.store.Flatten() {
		n.SetExpanded(true)
	}
	m.store.Invalidate()
	m = m.resize(120, 40)
	assertFits(t, m.View().Content, 120, 40)
}

// A high-rate topic under a filter, on a small terminal, with an overlay open:
// the combinations are where a clamp gets missed.
func TestSustainedIngestUnderAFilterOnASmallTerminal(t *testing.T) {
	cfg := config.Default()
	cfg.Limits.MaxTopics = 64
	cfg.Limits.PerTopicHistory = 4
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(40, 12)

	m = press(t, m, "/")
	for _, r := range "device" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "enter")

	seq := 0
	for round := 0; round < 40; round++ {
		batch := make([]*mqtt.Message, 0, 32)
		for i := 0; i < 32; i++ {
			seq++
			batch = append(batch, chaosMsg(seq, fmt.Sprintf("devices/%03d/state", seq%200), []byte("payload")))
		}
		next, _ := m.Update(app.BatchMsg{Msgs: batch})
		m = next.(Model)
		assertFits(t, m.View().Content, 40, 12)
	}

	// Eviction must have happened, and the cursor must still resolve.
	if st := m.store.Stats(); st.Evicted == 0 {
		t.Errorf("%d messages across 200 topics evicted nothing under a cap of 64", st.Received)
	}
	if m.topics.Cursor() >= len(m.store.Flatten()) && len(m.store.Flatten()) > 0 {
		t.Error("the cursor points past the end of the tree after eviction")
	}
}

// assertFits checks the frame is at most w columns of display width and h rows.
// An overflow here is a frame that scrolls the terminal and destroys the
// alt-screen layout.
func assertFits(t *testing.T, out string, w, h int) {
	t.Helper()
	lines := strings.Split(out, "\n")
	if len(lines) > h {
		t.Errorf("frame is %d rows, want at most %d", len(lines), h)
	}
	for i, line := range lines {
		if got := lipgloss.Width(line); got > w {
			t.Errorf("row %d is %d columns wide, want at most %d: %q",
				i, got, w, truncateForMessage(stripStyling(line)))
			return
		}
	}
}

// stripStyling removes the SGR sequences the renderer adds, so an assertion
// about the payload's own bytes is not confused by the ones the theme emits.
func stripStyling(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		if s[i] == 0x1b && i+1 < len(s) && (s[i+1] == '[' || s[i+1] == ']') {
			j := i + 2
			for j < len(s) && !isTerminator(s[j]) {
				j++
			}
			i = j + 1
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}

func isTerminator(c byte) bool {
	return (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || c == 0x07
}

func truncateForMessage(s string) string {
	if len(s) > 120 {
		return s[:120] + "…"
	}
	return s
}
