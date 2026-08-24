package logging

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The constraint that shapes this whole package: any write to stdout or stderr
// while the alt screen is active corrupts the display. Setup must therefore
// leave the standard log package pointed at nothing, because some dependency
// will eventually call log.Printf and it would land in the middle of the topic
// panel.
func TestSetupSilencesTheStandardLogPackage(t *testing.T) {
	l, err := Setup(Options{Level: "debug"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	realStderr := os.Stderr
	os.Stderr = w
	t.Cleanup(func() { os.Stderr = realStderr })

	stdlogPrintln("this must not appear anywhere near a terminal")
	l.Info("nor this")

	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	var buf strings.Builder
	if _, err := copyAll(&buf, r); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Errorf("something reached stderr: %q", buf.String())
	}
}

func TestSetupWritesJSONToTheConfiguredFileOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nested", "lazymqtt.log")
	l, err := Setup(Options{Level: "info", File: path})
	if err != nil {
		t.Fatal(err)
	}

	l.Info("connected", "broker", "tcp://localhost:1883")
	l.Debug("suppressed by the level")
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("the log file was not created: %v", err)
	}
	body := string(raw)
	if !strings.Contains(body, `"msg":"connected"`) {
		t.Errorf("the record is not JSON or is missing: %q", body)
	}
	if !strings.Contains(body, `"broker":"tcp://localhost:1883"`) {
		t.Errorf("attributes were dropped: %q", body)
	}
	if strings.Contains(body, "suppressed") {
		t.Errorf("a record below the configured level was written: %q", body)
	}

	// The file holds credentials-adjacent data, so it must not be readable by
	// anyone but its owner.
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("the log file has mode %04o; want owner-only", mode)
	}
}

// The ring backs the in-app logs panel, so a record has to reach both sinks.
func TestRecordsReachBothTheRingAndTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "lazymqtt.log")
	l, err := Setup(Options{Level: "info", File: path, RingSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	l.Warn("subscribe failed", "filter", "a/#")

	if got := l.Ring.Len(); got != 1 {
		t.Fatalf("the ring holds %d entries, want 1", got)
	}
	entry := l.Ring.Snapshot()[0]
	if entry.Message != "subscribe failed" {
		t.Errorf("ring message = %q", entry.Message)
	}
	if !strings.Contains(entry.Attrs, "filter=a/#") {
		t.Errorf("ring attrs = %q", entry.Attrs)
	}
	if entry.Level != slog.LevelWarn {
		t.Errorf("ring level = %v", entry.Level)
	}
}

func TestRingDropsTheOldestBeyondCapacity(t *testing.T) {
	l, err := Setup(Options{Level: "debug", RingSize: 3})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	for i := 0; i < 10; i++ {
		l.Info("entry", "i", i)
	}
	snap := l.Ring.Snapshot()
	if len(snap) != 3 {
		t.Fatalf("the ring holds %d entries, want its capacity of 3", len(snap))
	}
	// Oldest first, so the window must be the last three.
	for i, want := range []string{"i=7", "i=8", "i=9"} {
		if !strings.Contains(snap[i].Attrs, want) {
			t.Errorf("entry %d = %q, want %s", i, snap[i].Attrs, want)
		}
	}
}

// Log records genuinely arrive from several goroutines: the paho read loop,
// the bridge, and tea.Cmds. This is the one place in the app that needs a
// mutex, so exercise it under the race detector.
func TestRingHandlerIsSafeUnderConcurrentWriters(t *testing.T) {
	l, err := Setup(Options{Level: "debug", RingSize: 64})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	var wg sync.WaitGroup
	for w := 0; w < 8; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 200; i++ {
				l.Info("concurrent", "worker", w, "i", i)
			}
		}(w)
	}
	// A reader too: the logs panel snapshots once per frame while this runs.
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 200; i++ {
			_ = l.Ring.Snapshot()
			_ = l.Ring.Len()
		}
	}()
	wg.Wait()

	if got := l.Ring.Len(); got != 64 {
		t.Errorf("the ring holds %d entries, want its capacity of 64", got)
	}
}

// WithAttrs and WithGroup must not corrupt the shared ring: the handler clones
// itself but the buffer behind it is deliberately shared.
func TestDerivedHandlersWriteToTheSameRing(t *testing.T) {
	l, err := Setup(Options{Level: "debug", RingSize: 8})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Close() })

	l.With("component", "adapter").WithGroup("mqtt").Info("connected", "attempt", 1)

	if got := l.Ring.Len(); got != 1 {
		t.Fatalf("the ring holds %d entries, want 1", got)
	}
	attrs := l.Ring.Snapshot()[0].Attrs
	if !strings.Contains(attrs, "component=adapter") {
		t.Errorf("the With attribute was lost: %q", attrs)
	}
	if !strings.Contains(attrs, "attempt=1") {
		t.Errorf("the record attribute was lost: %q", attrs)
	}
}

func TestParseLevelDefaultsToWarn(t *testing.T) {
	cases := map[string]slog.Level{
		"debug":    slog.LevelDebug,
		"INFO":     slog.LevelInfo,
		" error ":  slog.LevelError,
		"warn":     slog.LevelWarn,
		"":         slog.LevelWarn,
		"nonsense": slog.LevelWarn,
	}
	for in, want := range cases {
		if got := ParseLevel(in); got != want {
			t.Errorf("ParseLevel(%q) = %v, want %v", in, got, want)
		}
	}
}

// Discard is what tests and the one-shot subcommands use. It must enable
// nothing, so a debug-level MQTT session costs no formatting at all.
func TestDiscardEnablesNothing(t *testing.T) {
	l := Discard()
	l.Debug("x")
	l.Info("x")
	l.Warn("x")
	l.Error("x")
	if got := l.Ring.Len(); got != 0 {
		t.Errorf("the discard logger retained %d entries", got)
	}
	if err := l.Close(); err != nil {
		t.Errorf("Close on a discard logger returned %v", err)
	}
}

// Close is reached from both the deferred close in main and the app teardown.
func TestCloseIsSafeOnAZeroLogger(t *testing.T) {
	var l *Logger
	if err := l.Close(); err != nil {
		t.Errorf("Close on a nil logger returned %v", err)
	}
}
