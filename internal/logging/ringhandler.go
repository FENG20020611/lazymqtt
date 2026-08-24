// Package logging wires log/slog for a program that must never write to
// stdout or stderr: any such write while the alt screen is active corrupts
// the display.
package logging

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// Entry is one captured log record, kept for the in-app logs panel.
type Entry struct {
	Time    time.Time
	Level   slog.Level
	Message string
	Attrs   string // pre-rendered "k=v k=v", so the panel does no formatting
}

// RingHandler is a slog.Handler that keeps the last N records in memory.
//
// It is the one place in the app with a mutex: log records genuinely arrive
// from several goroutines, unlike store.Store which has a single writer.
type RingHandler struct {
	mu      *sync.Mutex
	entries *[]Entry
	head    *int
	count   *int
	level   slog.Leveler
	attrs   []slog.Attr
	group   string
}

// NewRingHandler returns a handler retaining the most recent capacity records.
func NewRingHandler(capacity int, level slog.Leveler) *RingHandler {
	if capacity < 1 {
		capacity = 1
	}
	entries := make([]Entry, capacity)
	head, count := 0, 0
	return &RingHandler{
		mu:      &sync.Mutex{},
		entries: &entries,
		head:    &head,
		count:   &count,
		level:   level,
	}
}

// Enabled implements slog.Handler.
func (h *RingHandler) Enabled(_ context.Context, l slog.Level) bool {
	return l >= h.level.Level()
}

// Handle implements slog.Handler.
func (h *RingHandler) Handle(_ context.Context, r slog.Record) error {
	e := Entry{Time: r.Time, Level: r.Level, Message: r.Message}
	buf := make([]byte, 0, 64)
	for _, a := range h.attrs {
		buf = appendAttr(buf, h.group, a)
	}
	r.Attrs(func(a slog.Attr) bool {
		buf = appendAttr(buf, h.group, a)
		return true
	})
	e.Attrs = string(buf)

	h.mu.Lock()
	defer h.mu.Unlock()
	buffer := *h.entries
	idx := (*h.head + *h.count) % len(buffer)
	if *h.count == len(buffer) {
		*h.head = (*h.head + 1) % len(buffer)
		idx = (idx + len(buffer)) % len(buffer)
	} else {
		*h.count++
	}
	buffer[idx] = e
	return nil
}

func appendAttr(buf []byte, group string, a slog.Attr) []byte {
	if a.Equal(slog.Attr{}) {
		return buf
	}
	if len(buf) > 0 {
		buf = append(buf, ' ')
	}
	if group != "" {
		buf = append(buf, group...)
		buf = append(buf, '.')
	}
	buf = append(buf, a.Key...)
	buf = append(buf, '=')
	return append(buf, a.Value.String()...)
}

// WithAttrs implements slog.Handler.
func (h *RingHandler) WithAttrs(attrs []slog.Attr) slog.Handler {
	clone := *h
	clone.attrs = append(append([]slog.Attr{}, h.attrs...), attrs...)
	return &clone
}

// WithGroup implements slog.Handler.
func (h *RingHandler) WithGroup(name string) slog.Handler {
	clone := *h
	if h.group != "" {
		clone.group = h.group + "." + name
	} else {
		clone.group = name
	}
	return &clone
}

// Snapshot copies the retained entries, oldest first. The UI calls this once
// per frame while the logs panel is open.
func (h *RingHandler) Snapshot() []Entry {
	h.mu.Lock()
	defer h.mu.Unlock()
	buffer := *h.entries
	out := make([]Entry, 0, *h.count)
	for i := 0; i < *h.count; i++ {
		out = append(out, buffer[(*h.head+i)%len(buffer)])
	}
	return out
}

// Len returns the number of retained entries.
func (h *RingHandler) Len() int {
	h.mu.Lock()
	defer h.mu.Unlock()
	return *h.count
}
