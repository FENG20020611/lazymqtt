package panel

import (
	"strings"

	"charm.land/bubbles/v2/help"

	"github.com/Onizuka893/lazymqtt/internal/version"
)

// Help is the full-screen key reference.
//
// It is generated from the same key.Binding registry the dispatcher matches
// on, so it cannot drift out of date.
func Help(ctx Context, h help.Model, w int) string {
	h.SetWidth(max(w-4, 20))
	h.ShowAll = true

	var b strings.Builder
	b.WriteString(ctx.Theme.TitleFocused.Render("lazymqtt — keys") + "\n\n")
	b.WriteString(h.FullHelpView(ctx.Keys.FullHelp()))
	b.WriteString("\n\n" + ctx.Theme.Dim.Render(version.Info()))
	b.WriteString("\n" + ctx.Theme.Dim.Render("? or esc to close"))
	return b.String()
}

// Confirm is a yes/no dialog body.
func Confirm(ctx Context, question string, w int) string {
	return ctx.Theme.Value.Render(Truncate(question, w)) + "\n\n" +
		ctx.Theme.Dim.Render("y confirm   n / esc cancel")
}
