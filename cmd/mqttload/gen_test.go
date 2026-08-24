package main

import (
	"strings"
	"testing"
	"time"
)

// The whole reason the scheduler carries a fractional remainder: at 10 ms
// ticks a rate of 150 msg/s is 1.5 messages per tick, and truncating that
// yields 100 msg/s — a third of what was asked for, silently.
func TestSchedulerHitsTheRequestedRate(t *testing.T) {
	for _, rate := range []float64{1, 37, 150, 1000, 10000} {
		s := NewScheduler(rate, 10*time.Millisecond, 0, PatternSteady)
		total := 0
		for i := 0; i < 100; i++ { // one second of ticks
			total += s.Next()
		}
		want := int(rate)
		if diff := total - want; diff < -1 || diff > 1 {
			t.Errorf("rate %.0f produced %d messages in a second, want %d", rate, total, want)
		}
	}
}

// Sawtooth must actually vary, and must average below the peak; a pattern
// that quietly degenerates to steady would test nothing.
func TestSawtoothVariesAndStaysUnderTheCeiling(t *testing.T) {
	const rate = 1000
	s := NewScheduler(rate, 10*time.Millisecond, time.Second, PatternSawtooth)
	lo, hi, total := 1<<30, 0, 0
	for i := 0; i < 100; i++ {
		n := s.Next()
		total += n
		if n < lo {
			lo = n
		}
		if n > hi {
			hi = n
		}
	}
	if lo >= hi {
		t.Fatalf("the rate never varied: min %d max %d", lo, hi)
	}
	if hi > rate/100+1 {
		t.Errorf("peak tick of %d exceeds the configured rate", hi)
	}
	if total >= rate {
		t.Errorf("a ramp averaged %d msg/s, which is not below the %d peak", total, rate)
	}
}

func TestBuildPayloadIsExactlyTheRequestedSize(t *testing.T) {
	for _, size := range []int{0, 1, 4, 16, 64, 256, 1024, 65536} {
		got := BuildPayload(42, size)
		if len(got) != size {
			t.Errorf("BuildPayload(42, %d) returned %d bytes", size, len(got))
		}
	}
}

// Reproducibility is the point: the same sequence number must always give the
// same bytes, so a payload seen in the viewer identifies its publish.
func TestBuildPayloadIsDeterministicAndCarriesTheSequence(t *testing.T) {
	a := string(BuildPayload(7, 128))
	b := string(BuildPayload(7, 128))
	if a != b {
		t.Fatal("two calls with the same arguments produced different payloads")
	}
	if !strings.Contains(a, `"seq":7`) {
		t.Errorf("the payload does not carry its sequence number: %q", a[:min(40, len(a))])
	}
	if string(BuildPayload(8, 128)) == a {
		t.Error("different sequence numbers produced identical payloads")
	}
}

func TestTopicSetHasTheRequestedCardinality(t *testing.T) {
	set := NewTopicSet("load", 250)
	if set.Len() != 250 {
		t.Fatalf("Len = %d, want 250", set.Len())
	}
	seen := make(map[string]struct{}, 250)
	for i := 0; i < 250; i++ {
		seen[set.Next()] = struct{}{}
	}
	if len(seen) != 250 {
		t.Errorf("rotating 250 times visited %d distinct topics", len(seen))
	}
	// The rotation wraps rather than growing the set.
	if next := set.Next(); next != set.At(0) {
		t.Errorf("the rotation did not wrap: %q", next)
	}
	for _, tp := range []string{set.At(0), set.At(100)} {
		if !strings.HasPrefix(tp, "load/") {
			t.Errorf("topic %q does not use the prefix", tp)
		}
		if strings.Count(tp, "/") < 2 {
			t.Errorf("topic %q is flat; the viewer wants a hierarchy to show", tp)
		}
	}
}

func TestParsePatternRejectsGarbage(t *testing.T) {
	for _, ok := range []string{"steady", "sawtooth", "retained"} {
		if _, err := ParsePattern(ok); err != nil {
			t.Errorf("ParsePattern(%q) = %v", ok, err)
		}
	}
	if _, err := ParsePattern("burst"); err == nil {
		t.Error("an unknown pattern was accepted")
	}
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
