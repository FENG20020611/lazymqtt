package panel

import (
	"log/slog"
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/ui/sanitize"
)

// Logs is the in-app log viewer, backed by the same slog ring that feeds
// --log-file. Nothing may be written to stderr while the alt screen is up, so
// this is the only way to see what the client is doing.
type Logs struct {
	offset int
	height int
	Follow bool
}

// NewLogs returns a log panel that follows the tail.
func NewLogs() Logs { return Logs{Follow: true} }

// SetHeight records the body height.
func (p Logs) SetHeight(h int) Logs {
	p.height = h
	return p
}

// Scroll moves the view; scrolling up stops following the tail.
func (p Logs) Scroll(total, delta int) Logs {
	p.offset += delta
	if p.offset < 0 {
		p.offset = 0
	}
	if maxOff := max(total-max(p.height, 1), 0); p.offset > maxOff {
		p.offset = maxOff
	}
	p.Follow = p.offset >= max(total-max(p.height, 1), 0)
	return p
}

// Bottom jumps to the newest entry and resumes following.
func (p Logs) Bottom(total int) Logs {
	p.offset = max(total-max(p.height, 1), 0)
	p.Follow = true
	return p
}

// View renders the visible window of log entries.
func (p Logs) View(ctx Context, entries []logging.Entry, w, h int) string {
	if len(entries) == 0 {
		return ctx.Theme.Dim.Render("  no log entries — run with --debug for more")
	}
	if p.Follow {
		p.offset = max(len(entries)-h, 0)
	}
	from := min(p.offset, max(len(entries)-1, 0))
	to := min(from+h, len(entries))

	var b strings.Builder
	b.Grow(w * h)
	for i := from; i < to; i++ {
		if i > from {
			b.WriteByte('\n')
		}
		e := entries[i]
		style := ctx.Theme.Dim
		switch {
		case e.Level >= slog.LevelError:
			style = ctx.Theme.Error
		case e.Level >= slog.LevelWarn:
			style = ctx.Theme.Warn
		case e.Level >= slog.LevelInfo:
			style = ctx.Theme.Base
		}
		line := e.Time.Format("15:04:05.000") + " " +
			Pad(strings.ToLower(e.Level.String()), 5) + " " +
			sanitize.Topic(e.Message)
		if e.Attrs != "" {
			line += "  " + sanitize.Topic(e.Attrs)
		}
		b.WriteString(style.Render(Truncate(line, w)))
	}
	return b.String()
}
