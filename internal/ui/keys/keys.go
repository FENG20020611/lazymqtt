// Package keys is the single registry of key bindings.
//
// Both the dispatcher and bubbles/help consume these values, so the footer,
// the help overlay and actual behaviour cannot drift apart.
package keys

import "charm.land/bubbles/v2/key"

// Map is the application keymap.
type Map struct {
	// Global
	Help      key.Binding
	Quit      key.Binding
	ForceQuit key.Binding
	Back      key.Binding
	NextPanel key.Binding
	PrevPanel key.Binding
	Panel1    key.Binding
	Panel2    key.Binding
	Panel3    key.Binding
	Panel4    key.Binding
	Logs      key.Binding
	Pause     key.Binding

	// Navigation
	Up       key.Binding
	Down     key.Binding
	Left     key.Binding
	Right    key.Binding
	Top      key.Binding
	Bottom   key.Binding
	HalfDown key.Binding
	HalfUp   key.Binding
	Select   key.Binding

	// Actions
	Subscribe   key.Binding
	Unsubscribe key.Binding
	Publish     key.Binding
	Connect     key.Binding
	Reconnect   key.Binding
	Filter      key.Binding
	NextMatch   key.Binding
	PrevMatch   key.Binding
	CopyPayload key.Binding
	CopyTopic   key.Binding
	Retained    key.Binding
	Follow      key.Binding
	Autoscroll  key.Binding
	ClearTopic  key.Binding
	ClearAll    key.Binding
	Wrap        key.Binding
	Format      key.Binding

	// Modal-only
	Confirm key.Binding
	Cancel  key.Binding
	Tab     key.Binding
}

// Default returns the shipped keymap.
func Default() Map {
	return Map{
		Help:      key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help")),
		Quit:      key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit")),
		ForceQuit: key.NewBinding(key.WithKeys("ctrl+c"), key.WithHelp("ctrl+c", "force quit")),
		Back:      key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
		NextPanel: key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "next panel")),
		PrevPanel: key.NewBinding(key.WithKeys("shift+tab"), key.WithHelp("⇧⇥", "prev panel")),
		Panel1:    key.NewBinding(key.WithKeys("1"), key.WithHelp("1", "topics")),
		Panel2:    key.NewBinding(key.WithKeys("2"), key.WithHelp("2", "messages")),
		Panel3:    key.NewBinding(key.WithKeys("3"), key.WithHelp("3", "detail")),
		Panel4:    key.NewBinding(key.WithKeys("4"), key.WithHelp("4", "subscriptions")),
		Logs:      key.NewBinding(key.WithKeys("ctrl+l"), key.WithHelp("ctrl+l", "logs")),
		Pause:     key.NewBinding(key.WithKeys("space", " "), key.WithHelp("␣", "pause view")),

		Up:       key.NewBinding(key.WithKeys("k", "up"), key.WithHelp("↑/k", "up")),
		Down:     key.NewBinding(key.WithKeys("j", "down"), key.WithHelp("↓/j", "down")),
		Left:     key.NewBinding(key.WithKeys("h", "left"), key.WithHelp("h", "collapse")),
		Right:    key.NewBinding(key.WithKeys("l", "right"), key.WithHelp("l", "expand")),
		Top:      key.NewBinding(key.WithKeys("g", "home"), key.WithHelp("g", "top")),
		Bottom:   key.NewBinding(key.WithKeys("G", "end"), key.WithHelp("G", "bottom")),
		HalfDown: key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl+d", "half page down")),
		HalfUp:   key.NewBinding(key.WithKeys("ctrl+u"), key.WithHelp("ctrl+u", "half page up")),
		Select:   key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "select")),

		Subscribe:   key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "subscribe")),
		Unsubscribe: key.NewBinding(key.WithKeys("u"), key.WithHelp("u", "unsubscribe")),
		Publish:     key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "publish")),
		Connect:     key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "brokers")),
		// 'r' reads as reload/refresh in every other TUI, so it is reserved
		// for forcing a reconnect — which is what users reach for.
		Reconnect:   key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "reconnect")),
		Filter:      key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter")),
		NextMatch:   key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "next match")),
		PrevMatch:   key.NewBinding(key.WithKeys("N"), key.WithHelp("N", "prev match")),
		CopyPayload: key.NewBinding(key.WithKeys("y"), key.WithHelp("y", "copy payload")),
		CopyTopic:   key.NewBinding(key.WithKeys("Y"), key.WithHelp("Y", "copy topic")),
		Retained:    key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "retained only")),
		Follow:      key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
		Autoscroll:  key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "autoscroll")),
		ClearTopic:  key.NewBinding(key.WithKeys("x"), key.WithHelp("x", "clear topic")),
		ClearAll:    key.NewBinding(key.WithKeys("X"), key.WithHelp("X", "clear all")),
		Wrap:        key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
		Format:      key.NewBinding(key.WithKeys("F"), key.WithHelp("F", "pretty-print on/off")),

		Confirm: key.NewBinding(key.WithKeys("enter"), key.WithHelp("↵", "confirm")),
		Cancel:  key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "cancel")),
		Tab:     key.NewBinding(key.WithKeys("tab"), key.WithHelp("⇥", "next field")),
	}
}

// ShortHelp is the footer key bar. It implements help.KeyMap.
func (m Map) ShortHelp() []key.Binding {
	return []key.Binding{
		m.Down, m.NextPanel, m.Subscribe, m.Publish,
		m.Filter, m.CopyPayload, m.Pause, m.Help, m.Quit,
	}
}

// FullHelp is the help overlay, grouped by column.
func (m Map) FullHelp() [][]key.Binding {
	return [][]key.Binding{
		{m.Help, m.NextPanel, m.PrevPanel, m.Panel1, m.Panel2, m.Panel3, m.Panel4, m.Logs, m.Back, m.Quit, m.ForceQuit},
		{m.Up, m.Down, m.Left, m.Right, m.Top, m.Bottom, m.HalfUp, m.HalfDown, m.Select},
		{m.Connect, m.Reconnect, m.Subscribe, m.Unsubscribe, m.Publish, m.Filter, m.NextMatch, m.PrevMatch},
		{m.CopyPayload, m.CopyTopic, m.Pause, m.Follow, m.Autoscroll, m.Retained, m.Wrap, m.Format, m.ClearTopic, m.ClearAll},
	}
}
