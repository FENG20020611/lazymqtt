package panel

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/help"
	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/store"
)

// StatusInput is everything the header needs.
type StatusInput struct {
	Status   mqtt.ConnStatus
	Stats    store.Stats
	Paused   bool
	Buffered int
	Follow   bool
	Insecure bool
	Filter   string
	Now      time.Time
}

// Header renders the top line: connection, uptime, counters and mode flags.
func Header(ctx Context, in StatusInput, w int) string {
	t := ctx.Theme

	dot, dotStyle := "○", t.Dim
	switch in.Status.State {
	case mqtt.StateConnected:
		dot, dotStyle = "●", t.Success
	case mqtt.StateConnecting, mqtt.StateReconnecting:
		dot, dotStyle = "◐", t.Warn
	case mqtt.StateFailed:
		dot, dotStyle = "✗", t.Error
	}

	broker := in.Status.Broker
	if broker == "" {
		broker = "no broker"
	}

	left := []string{dotStyle.Render(dot), t.Value.Render(broker)}
	if in.Status.ProtoVersion != "" && in.Status.State == mqtt.StateConnected {
		left = append(left, t.Dim.Render("v"+in.Status.ProtoVersion))
	}

	switch in.Status.State {
	case mqtt.StateConnected:
		left = append(left, t.Dim.Render("up "+shortDuration(in.Now.Sub(in.Status.Since))))
	case mqtt.StateReconnecting:
		// The countdown is driven by the 1 Hz tick.
		msg := fmt.Sprintf("reconnecting… attempt %d", in.Status.Attempt)
		if !in.Status.NextRetryAt.IsZero() {
			if d := time.Until(in.Status.NextRetryAt); d > 0 {
				msg += fmt.Sprintf(", next try in %ds", int(d.Seconds())+1)
			}
		}
		left = append(left, t.Warn.Render(msg))
	case mqtt.StateFailed:
		left = append(left, t.Error.Render("failed: "+errText(in.Status.Err)))
	case mqtt.StateConnecting:
		left = append(left, t.Warn.Render("connecting…"))
	}

	right := []string{
		t.Dim.Render("msgs ") + t.Value.Render(formatCount(in.Stats.Received)),
		t.Dim.Render(fmt.Sprintf("%.0f/s", in.Stats.RatePerSec)),
		t.Dim.Render("topics ") + t.Value.Render(fmt.Sprintf("%d", in.Stats.Topics)),
	}
	// A lossy view must say so.
	if in.Stats.Dropped > 0 {
		right = append(right, t.Warn.Render("drop "+formatCount(in.Stats.Dropped)))
	}
	if in.Stats.Evicted > 0 {
		right = append(right, t.Dim.Render("evict "+formatCount(in.Stats.Evicted)))
	}
	if in.Filter != "" {
		right = append(right, t.Highlight.Render("/"+Truncate(in.Filter, 16)))
	}
	if in.Follow {
		right = append(right, t.Accent.Render("follow"))
	}
	if in.Paused {
		badge := "⏸ paused"
		if in.Buffered > 0 {
			badge += fmt.Sprintf(" (+%s held)", formatCount(uint64(in.Buffered)))
		}
		right = append(right, t.Warn.Render(badge))
	}

	l := strings.Join(left, " · ")
	r := strings.Join(right, "  ")
	gap := w - lipgloss.Width(l) - lipgloss.Width(r)
	if gap < 1 {
		return Truncate(l+" "+r, w)
	}
	return l + strings.Repeat(" ", gap) + r
}

// InsecureBanner is a persistent warning, not a startup message that scrolls
// away: people leave insecure_skip_verify on by accident and then wonder why
// staging worked.
func InsecureBanner(ctx Context, w int) string {
	return ctx.Theme.Banner.Render(Pad(
		" ⚠ TLS certificate verification is DISABLED for this connection (insecure_skip_verify)", w))
}

// Footer renders the key bar from the same bindings the dispatcher uses.
func Footer(ctx Context, h help.Model, w int) string {
	h.SetWidth(w)
	return ctx.Theme.Footer.Render(Truncate(h.ShortHelpView(ctx.Keys.ShortHelp()), w))
}

func shortDuration(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	s := int(d.Seconds())
	switch {
	case s < 60:
		return fmt.Sprintf("%ds", s)
	case s < 3600:
		return fmt.Sprintf("%dm%02ds", s/60, s%60)
	default:
		return fmt.Sprintf("%dh%02dm", s/3600, (s%3600)/60)
	}
}

func errText(err error) string {
	if err == nil {
		return "unknown error"
	}
	return err.Error()
}
