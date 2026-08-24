package logging

import (
	"context"
	"fmt"
	"io"
	stdlog "log"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// Options configures the logger.
type Options struct {
	Level    string // debug | info | warn | error
	File     string // empty = no file sink. Never stdout.
	RingSize int
}

// Logger bundles the app logger with the ring the logs panel reads from.
type Logger struct {
	*slog.Logger
	Ring  *RingHandler
	Level *slog.LevelVar

	closer io.Closer
}

// Setup builds the app logger and redirects the standard log package.
//
// The redirect matters: some dependency will eventually call log.Printf, and
// it would land in the middle of the topic panel.
func Setup(opts Options) (*Logger, error) {
	lv := &slog.LevelVar{}
	lv.Set(ParseLevel(opts.Level))

	size := opts.RingSize
	if size <= 0 {
		size = 500
	}
	ring := NewRingHandler(size, lv)
	handlers := []slog.Handler{ring}

	l := &Logger{Ring: ring, Level: lv}

	if opts.File != "" {
		if err := os.MkdirAll(filepath.Dir(opts.File), 0o700); err != nil {
			return nil, fmt.Errorf("creating log directory: %w", err)
		}
		f, err := os.OpenFile(opts.File, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
		if err != nil {
			return nil, fmt.Errorf("opening log file: %w", err)
		}
		handlers = append(handlers, slog.NewJSONHandler(f, &slog.HandlerOptions{Level: lv}))
		l.closer = f
	}

	l.Logger = slog.New(&fanout{handlers: handlers})

	// Nothing may reach the terminal while the TUI owns it.
	stdlog.SetOutput(io.Discard)
	stdlog.SetFlags(0)
	return l, nil
}

// Close flushes and closes the file sink, if any.
func (l *Logger) Close() error {
	if l == nil || l.closer == nil {
		return nil
	}
	return l.closer.Close()
}

// SetLevel changes the level for every sink at once.
func (l *Logger) SetLevel(level slog.Level) { l.Level.Set(level) }

// ParseLevel maps a config string onto a slog level, defaulting to warn.
func ParseLevel(s string) slog.Level {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "debug":
		return slog.LevelDebug
	case "info":
		return slog.LevelInfo
	case "error":
		return slog.LevelError
	default:
		return slog.LevelWarn
	}
}

// fanout writes each record to every configured sink.
type fanout struct{ handlers []slog.Handler }

func (f *fanout) Enabled(ctx context.Context, l slog.Level) bool {
	for _, h := range f.handlers {
		if h.Enabled(ctx, l) {
			return true
		}
	}
	return false
}

func (f *fanout) Handle(ctx context.Context, r slog.Record) error {
	var firstErr error
	for _, h := range f.handlers {
		if !h.Enabled(ctx, r.Level) {
			continue
		}
		if err := h.Handle(ctx, r.Clone()); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

func (f *fanout) WithAttrs(attrs []slog.Attr) slog.Handler {
	out := &fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithAttrs(attrs)
	}
	return out
}

func (f *fanout) WithGroup(name string) slog.Handler {
	out := &fanout{handlers: make([]slog.Handler, len(f.handlers))}
	for i, h := range f.handlers {
		out.handlers[i] = h.WithGroup(name)
	}
	return out
}

// Discard returns a logger that keeps a ring but writes nothing to disk. Used
// by tests and by the one-shot pub/sub subcommands.
func Discard() *Logger {
	lv := &slog.LevelVar{}
	lv.Set(slog.LevelError + 4) // above every level: nothing is enabled
	ring := NewRingHandler(1, lv)
	return &Logger{Logger: slog.New(ring), Ring: ring, Level: lv}
}
