package ui

import (
	"flag"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

var update = flag.Bool("update", false, "rewrite the golden files")

// Golden frames are stored with every escape sequence removed.
//
// §21 pitfall 19: a golden file containing colour is a golden file that passes
// on the author's iTerm2 and fails on a CI runner with a different TERM,
// because the styles resolve against whatever profile the terminal advertises.
// Stripping the sequences pins the test to layout and content — which is what
// these tests are actually about — and leaves colour to the eye.
func frame(m Model) string {
	return ansi.Strip(m.View().Content)
}

// goldenModel builds a model whose every rendered value is fixed. Anything
// derived from the wall clock has to be pinned or the frame differs on every
// run: the header clock, the message timestamps, and the connection uptime.
func goldenModel(t *testing.T, w, h int) Model {
	t.Helper()
	cfg := config.Default()
	cfg.UI.TimestampFormat = "15:04:05.000"
	m := New(Options{App: app.New(cfg, logging.Discard()), Config: cfg}).resize(w, h)
	m.now = time.Date(2026, 3, 14, 9, 26, 53, 0, time.UTC)
	return m
}

// goldenMessages is a small realistic tree: a couple of hierarchies, a
// retained topic, a JSON payload and a plain one.
func goldenMessages() []*mqtt.Message {
	base := time.Date(2026, 3, 14, 9, 26, 51, 0, time.UTC)
	specs := []struct {
		topic    string
		payload  string
		retained bool
		qos      byte
	}{
		{"home/livingroom/temperature", `21.5`, true, 0},
		{"home/livingroom/humidity", `48`, false, 0},
		{"home/kitchen/temperature", `23.1`, false, 1},
		{"devices/lamp-01/state", `{"on":true,"brightness":80}`, true, 1},
		{"devices/lamp-01/available", `online`, false, 0},
		{"factory/line1/press/cycles", `10428`, false, 2},
	}
	msgs := make([]*mqtt.Message, 0, len(specs))
	for i, s := range specs {
		msgs = append(msgs, &mqtt.Message{
			Seq:        uint64(i + 1),
			Topic:      s.topic,
			Payload:    []byte(s.payload),
			QoS:        s.qos,
			Retained:   s.retained,
			ReceivedAt: base.Add(time.Duration(i) * 100 * time.Millisecond),
		})
	}
	return msgs
}

func goldenPopulated(t *testing.T, w, h int) Model {
	t.Helper()
	m := goldenModel(t, w, h)
	next, _ := m.Update(app.BatchMsg{Msgs: goldenMessages()})
	return next.(Model)
}

func TestGoldenFrames(t *testing.T) {
	cases := []struct {
		name  string
		build func(t *testing.T) Model
	}{
		{"empty-80x24", func(t *testing.T) Model { return goldenModel(t, 80, 24) }},
		{"populated-80x24", func(t *testing.T) Model { return goldenPopulated(t, 80, 24) }},
		{"populated-120x40", func(t *testing.T) Model { return goldenPopulated(t, 120, 40) }},
		{"expanded-120x40", func(t *testing.T) Model {
			m := goldenPopulated(t, 120, 40)
			for _, n := range m.store.Flatten() {
				n.SetExpanded(true)
			}
			m.store.Invalidate()
			return m.resize(120, 40)
		}},
		// Selecting a branch shows the firehose; selecting a leaf shows that
		// topic's history. Both are pinned, because the fallback between them
		// is easy to break and invisible in a unit test.
		{"branch-selected-120x40", func(t *testing.T) Model {
			return press(t, goldenPopulated(t, 120, 40), "1", "l", "l")
		}},
		{"leaf-selected-120x40", func(t *testing.T) Model {
			m := press(t, goldenPopulated(t, 120, 40), "1", "l", "l", "l", "l")
			if got := m.messages.Topic(); got == "" {
				t.Fatalf("setup: the cursor is on %q, not a leaf", m.topics.Selected())
			}
			return m
		}},
		{"help-80x24", func(t *testing.T) Model { return press(t, goldenPopulated(t, 80, 24), "?") }},
		{"publish-120x40", func(t *testing.T) Model { return press(t, goldenPopulated(t, 120, 40), "p") }},
		{"filter-prompt-80x24", func(t *testing.T) Model {
			m := press(t, goldenPopulated(t, 80, 24), "/")
			for _, r := range "kitchen" {
				m = press(t, m, string(r))
			}
			return m
		}},
		// Under 64 columns the layout stacks to a single panel; that path has
		// its own arithmetic and is worth pinning.
		{"stacked-40x20", func(t *testing.T) Model { return goldenPopulated(t, 40, 20) }},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := frame(c.build(t))
			path := filepath.Join("testdata", c.name+".golden")

			if *update {
				if err := os.MkdirAll("testdata", 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte(got), 0o644); err != nil {
					t.Fatal(err)
				}
				return
			}

			want, err := os.ReadFile(path)
			if err != nil {
				t.Fatalf("%v\nrun: go test ./internal/ui -update", err)
			}
			if got != string(want) {
				t.Errorf("frame differs from %s\n--- want ---\n%s\n--- got ---\n%s\n%s",
					path, want, got, firstDiff(string(want), got))
			}
		})
	}
}

// Rendering the same model twice must produce the same bytes. A frame that
// depends on map iteration order or on the wall clock makes every golden file
// a flake waiting to happen.
func TestRenderIsDeterministic(t *testing.T) {
	m := goldenPopulated(t, 120, 40)
	first := frame(m)
	for i := 0; i < 20; i++ {
		if got := frame(m); got != first {
			t.Fatalf("render %d differs from the first:\n%s", i, firstDiff(first, got))
		}
	}
}

// The golden frames are stripped of colour, so assert separately that the
// renderer does emit styling — otherwise a regression that dropped every
// style would leave these tests green.
func TestFramesAreStyled(t *testing.T) {
	if !strings.Contains(goldenPopulated(t, 120, 40).View().Content, "\x1b[") {
		t.Error("the rendered frame carries no escape sequences at all")
	}
}

// firstDiff names the first differing line, because eyeballing two 40-line
// frames is how a real regression gets waved through.
func firstDiff(want, got string) string {
	wl, gl := strings.Split(want, "\n"), strings.Split(got, "\n")
	for i := 0; i < len(wl) || i < len(gl); i++ {
		var w, g string
		if i < len(wl) {
			w = wl[i]
		}
		if i < len(gl) {
			g = gl[i]
		}
		if w != g {
			return "first difference at line " + itoa(i+1) + ":\nwant: " + w + "\n got: " + g
		}
	}
	return "no line-level difference; check trailing whitespace"
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
