package ui

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/Onizuka893/lazymqtt/internal/ui/panel"
)

// View renders the whole frame.
//
// In Bubble Tea v2 alt-screen and cursor state are fields on the returned
// value rather than commands fired at startup, which removes a class of
// ordering bug.
func (m Model) View() tea.View {
	v := tea.NewView("")
	v.AltScreen = true
	v.WindowTitle = "lazymqtt"
	if m.quitting {
		return v
	}
	if m.layout.Width == 0 {
		m = m.resize(m.width, m.height)
	}

	if m.layout.TooSmall {
		v.SetContent(m.theme.Warn.Render("terminal too small"))
		return v
	}

	ctx := m.context()
	body := m.renderBody(ctx)

	rows := make([]string, 0, 4)
	rows = append(rows, panel.Header(ctx, m.statusInput(), m.width))
	if m.insecure {
		rows = append(rows, panel.InsecureBanner(ctx, m.width))
	}
	rows = append(rows, body, m.renderFooter(ctx))

	screen := strings.Join(rows, "\n")

	// Overlays replace the body rather than compositing over it: a modal that
	// is hard to read is worse than one that covers what is behind it.
	if m.mode != ModeNormal && m.mode != ModePrompt {
		screen = m.renderOverlay(ctx, rows[0])
	}

	v.SetContent(screen)
	if cur := m.overlayCursor(); cur != nil {
		v.Cursor = cur
	}
	m.store.ClearDirty()
	return v
}

func (m Model) statusInput() panel.StatusInput {
	return panel.StatusInput{
		Status:   m.status,
		Stats:    m.store.Stats(),
		Paused:   m.paused,
		Buffered: len(m.pending),
		Follow:   m.topics.Follow,
		Insecure: m.insecure,
		Filter:   m.filter,
		Now:      m.now,
	}
}

func (m Model) renderBody(ctx panel.Context) string {
	l := m.layout

	if l.Stacked {
		// Too narrow for two columns: show only the focused panel.
		title, body := m.panelFor(ctx, m.focus, l.Width, l.BodyH)
		return panel.Box(m.theme, true, title, body, l.Width, l.BodyH)
	}

	topicsTitle, topicsBody := m.panelFor(ctx, FocusTopics, l.LeftW, l.TopicsH)
	subsTitle, subsBody := m.panelFor(ctx, FocusSubs, l.LeftW, l.SubsH)
	msgTitle, msgBody := m.panelFor(ctx, FocusMessages, l.RightW, l.MessagesH)
	detTitle, detBody := m.panelFor(ctx, FocusDetail, l.RightW, l.DetailH)

	left := lipgloss.JoinVertical(lipgloss.Left,
		panel.Box(m.theme, m.focus == FocusTopics, topicsTitle, topicsBody, l.LeftW, l.TopicsH),
		panel.Box(m.theme, m.focus == FocusSubs, subsTitle, subsBody, l.LeftW, l.SubsH),
	)
	right := lipgloss.JoinVertical(lipgloss.Left,
		panel.Box(m.theme, m.focus == FocusMessages, msgTitle, msgBody, l.RightW, l.MessagesH),
		panel.Box(m.theme, m.focus == FocusDetail, detTitle, detBody, l.RightW, l.DetailH),
	)
	return lipgloss.JoinHorizontal(lipgloss.Top, left, right)
}

// panelFor renders one panel's title and body at the given outer size.
func (m Model) panelFor(ctx panel.Context, f Focus, outerW, outerH int) (string, string) {
	w, h := panel.BoxInner(outerW, outerH)
	switch f {
	case FocusTopics:
		title := sprintf("[1] Topics (%d)", nodeCount(m.store))
		return title, m.topics.View(ctx, w, h)
	case FocusMessages:
		topic := m.messages.Topic()
		label := "all topics"
		if topic != "" {
			label = topic
		}
		title := sprintf("[2] Messages  %s  (%d)", panel.Truncate(label, max(w-24, 8)), m.messages.Len(ctx))
		if !m.messages.Autoscroll {
			title += "  ⏸"
		}
		return title, m.messages.View(ctx, w, h)
	case FocusDetail:
		return "[3] Detail", m.detail.View(ctx, m.currentMessage(), w, h)
	case FocusSubs:
		return sprintf("[4] Subscriptions (%d)", len(m.subscriptions)), m.subs.View(ctx, m.subscriptions, w, h)
	}
	return "", ""
}

func (m Model) renderFooter(ctx panel.Context) string {
	if m.mode == ModePrompt {
		return m.prompt.View(ctx, m.width)
	}
	left := panel.Footer(ctx, m.help, m.width)
	if len(m.toasts) == 0 {
		return left
	}
	t := m.toasts[len(m.toasts)-1]
	badge := m.theme.Toast[clampLevel(int(t.level))].Render(panel.Truncate(t.text, max(m.width/2, 10)))
	gap := m.width - lipgloss.Width(left) - lipgloss.Width(badge)
	if gap < 1 {
		return panel.Truncate(badge, m.width)
	}
	return left + strings.Repeat(" ", gap) + badge
}

func (m Model) renderOverlay(ctx panel.Context, header string) string {
	w := max(min(m.width-4, 100), 20)
	h := max(m.height-4, 6)

	var title, body string
	switch m.mode {
	case ModeHelp:
		title, body = "Help", panel.Help(ctx, m.help, w)
	case ModeLogs:
		title, body = "Logs", m.logs.View(ctx, m.app.Logger.Ring.Snapshot(), w-2, h-3)
	case ModeBrokers:
		title, body = "Brokers", m.brokers.View(ctx, m.cfg, w-2)
		h = min(h, len(m.cfg.Names())+5)
	case ModeConfirm:
		title, body = "Confirm", panel.Confirm(ctx, m.confirmText, w-2)
		h = 6
	case ModePublish:
		title, body = "Publish", m.publish.View(ctx, w-2)
		h = min(h, 18)
	default:
		return header
	}

	box := panel.Box(m.theme, true, title, body, w, h)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, box)
}

func (m Model) overlayCursor() *tea.Cursor {
	switch m.mode {
	case ModePrompt:
		return m.prompt.Cursor()
	case ModePublish:
		return m.publish.Cursor()
	}
	return nil
}

func sprintf(format string, a ...any) string { return fmt.Sprintf(format, a...) }

func clampLevel(i int) int {
	if i < 0 {
		return 0
	}
	if i > 3 {
		return 3
	}
	return i
}
