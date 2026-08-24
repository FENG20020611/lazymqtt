package main

import (
	"fmt"
	"math"
	"strconv"
	"strings"
	"time"
)

// Pattern selects how the offered rate varies over time.
type Pattern string

// The supported traffic shapes.
const (
	// PatternSteady holds the configured rate for the whole run.
	PatternSteady Pattern = "steady"
	// PatternSawtooth ramps from a trickle to the full rate and drops back,
	// which is what actually exercises the coalescer: a steady stream fills
	// every batch identically, while a ramp walks the batch size across the
	// whole range including the 512-message cap.
	PatternSawtooth Pattern = "sawtooth"
	// PatternRetained publishes a fixed count of retained messages as fast
	// as the broker accepts them, then stops. This is the flood that finds
	// missing LRU eviction.
	PatternRetained Pattern = "retained"
)

// ParsePattern validates a --pattern value.
func ParsePattern(s string) (Pattern, error) {
	switch Pattern(s) {
	case PatternSteady, PatternSawtooth, PatternRetained:
		return Pattern(s), nil
	}
	return "", fmt.Errorf("unknown pattern %q; want steady, sawtooth or retained", s)
}

// Scheduler converts a target rate into per-tick message quotas.
//
// A ticker per message is not an option: at 10,000 msg/s that is a 100 µs
// ticker, which on a general-purpose scheduler spends more time waking up than
// publishing and produces a rate nowhere near the one requested. Instead the
// generator ticks at a coarse interval and emits a batch per tick, carrying
// the fractional remainder forward so the average rate is exact rather than
// truncated on every tick.
type Scheduler struct {
	rate     float64
	interval time.Duration
	pattern  Pattern
	period   time.Duration

	carry   float64
	elapsed time.Duration
}

// NewScheduler builds a scheduler for a rate in messages per second.
func NewScheduler(rate float64, interval, period time.Duration, pattern Pattern) *Scheduler {
	if interval <= 0 {
		interval = 10 * time.Millisecond
	}
	if period <= 0 {
		period = 5 * time.Second
	}
	return &Scheduler{rate: rate, interval: interval, pattern: pattern, period: period}
}

// Interval is the tick period the caller should use.
func (s *Scheduler) Interval() time.Duration { return s.interval }

// Next returns how many messages to publish for the tick that has just
// elapsed.
func (s *Scheduler) Next() int {
	s.elapsed += s.interval
	want := s.rate*s.factor()*s.interval.Seconds() + s.carry
	n := int(want)
	if n < 0 {
		n = 0
	}
	s.carry = want - float64(n)
	return n
}

// factor scales the base rate for the active pattern.
func (s *Scheduler) factor() float64 {
	if s.pattern != PatternSawtooth {
		return 1
	}
	// A linear ramp from 10% to 100% and an instant drop back. The sharp edge
	// is the point: a smooth sine never produces the sudden burst that a
	// device fleet reconnecting does.
	const floor = 0.1
	phase := math.Mod(s.elapsed.Seconds(), s.period.Seconds()) / s.period.Seconds()
	return floor + (1-floor)*phase
}

// TopicSet is a fixed set of topics, cycled through so the topic cardinality
// the store sees is exactly what was asked for.
type TopicSet struct {
	topics []string
	next   int
}

// NewTopicSet builds n topics under prefix, spread over a two-level hierarchy
// so the tree in the viewer looks like something rather than one flat list of
// a thousand siblings.
func NewTopicSet(prefix string, n int) *TopicSet {
	if n < 1 {
		n = 1
	}
	prefix = strings.Trim(prefix, "/")
	if prefix == "" {
		prefix = "load"
	}
	topics := make([]string, 0, n)
	for i := 0; i < n; i++ {
		topics = append(topics, fmt.Sprintf("%s/group%02d/device%04d/telemetry", prefix, i%16, i))
	}
	return &TopicSet{topics: topics}
}

// Len is the topic cardinality.
func (t *TopicSet) Len() int { return len(t.topics) }

// At returns one topic by index, wrapping.
func (t *TopicSet) At(i int) string { return t.topics[i%len(t.topics)] }

// Next returns the next topic in rotation.
func (t *TopicSet) Next() string {
	s := t.topics[t.next%len(t.topics)]
	t.next++
	return s
}

// BuildPayload returns a payload of exactly size bytes whose content is
// deterministic for a given sequence number, so a run is reproducible and a
// payload seen in the viewer can be traced back to its publish.
//
// The prefix is valid JSON-ish text so the detail pane has something readable
// to show; the remainder is padding.
func BuildPayload(seq uint64, size int) []byte {
	head := `{"seq":` + strconv.FormatUint(seq, 10) + `,"pad":"`
	tail := `"}`
	if size <= 0 {
		return []byte{}
	}
	if size < len(head)+len(tail) {
		// Too small for the envelope: emit the sequence number, truncated.
		s := strconv.FormatUint(seq, 10)
		if len(s) > size {
			s = s[:size]
		}
		return []byte(s + strings.Repeat("x", size-len(s)))
	}
	pad := size - len(head) - len(tail)
	var b strings.Builder
	b.Grow(size)
	b.WriteString(head)
	// A repeating alphabet rather than random bytes: incompressible enough to
	// be honest about wire cost, and reproducible.
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	for i := 0; i < pad; i++ {
		b.WriteByte(alphabet[i%len(alphabet)])
	}
	b.WriteString(tail)
	return []byte(b.String())
}
