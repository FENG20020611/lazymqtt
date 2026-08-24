package logging

import (
	"context"
	"fmt"
	"log/slog"
)

// PahoLogger adapts paho's Println/Printf logger interface onto slog.
//
// Wiring this is not optional: paho's default loggers write to stderr, and a
// debug-level MQTT session would shred the UI within seconds. Its debug
// output also includes CONNECT packets, which is why level and content are
// routed through the app logger rather than anywhere near the terminal.
type PahoLogger struct {
	log    *slog.Logger
	level  slog.Level
	source string
}

// NewPahoLogger returns a logger for one paho sink ("paho", "autopaho", …).
func NewPahoLogger(l *slog.Logger, level slog.Level, source string) *PahoLogger {
	if l == nil {
		l = slog.New(slog.DiscardHandler)
	}
	return &PahoLogger{log: l, level: level, source: source}
}

// Println implements paho/log.Logger.
func (p *PahoLogger) Println(v ...any) {
	p.log.Log(context.Background(), p.level, trimNewline(fmt.Sprint(v...)), "source", p.source)
}

// Printf implements paho/log.Logger.
func (p *PahoLogger) Printf(format string, v ...any) {
	p.log.Log(context.Background(), p.level, trimNewline(fmt.Sprintf(format, v...)), "source", p.source)
}

func trimNewline(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
