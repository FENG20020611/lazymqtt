package ui

import (
	tea "charm.land/bubbletea/v2"
)

// wheelLines is how far one notch of the wheel scrolls. Three is what most
// terminals and editors use; one feels broken and a page feels like a jump.
const wheelLines = 3

// handleMouse routes wheel and click events. It is only reached when
// `ui.mouse: true`, because the View asks for mouse reporting only then —
// with reporting off, the terminal keeps its own selection and copy
// behaviour, which is why this is opt-in rather than the default.
func (m Model) handleMouse(msg tea.MouseMsg) (tea.Model, tea.Cmd) {
	mouse := msg.Mouse()

	switch msg.(type) {
	case tea.MouseWheelMsg:
		switch mouse.Button {
		case tea.MouseWheelUp:
			return m.scrollBy(-wheelLines), nil
		case tea.MouseWheelDown:
			return m.scrollBy(wheelLines), nil
		}
		return m, nil

	case tea.MouseClickMsg:
		if mouse.Button != tea.MouseLeft || m.mode != ModeNormal {
			return m, nil
		}
		if f, ok := m.panelAt(mouse.X, mouse.Y); ok && f != m.focus {
			m.focus = f
		}
		return m, nil
	}
	return m, nil
}

// scrollBy moves whatever the wheel should move: the open overlay if there is
// one, otherwise the focused panel. Scrolling the panel behind a modal is the
// kind of thing that looks like a rendering bug.
func (m Model) scrollBy(delta int) Model {
	if m.mode == ModeLogs {
		m.logs = m.logs.Scroll(m.app.Logger.Ring.Len(), delta)
		return m
	}
	if m.mode != ModeNormal {
		return m
	}

	ctx := m.context()
	switch m.focus {
	case FocusTopics:
		m.topics = m.topics.Move(m.store.Flatten(), delta)
		m.messages = m.messages.SetTopic(m.selectedTopic())
		m.detail = m.detail.Reset()
	case FocusMessages:
		m.messages = m.messages.Move(ctx, delta)
		m.detail = m.detail.Reset()
	case FocusDetail:
		m.detail = m.detail.Scroll(delta)
	case FocusSubs:
		m.subs = m.subs.Move(m.subscriptions, delta)
	}
	return m
}

// panelAt maps a cell to the panel drawn there, using the same arithmetic
// ComputeLayout used to place them. Coordinates are zero-based from the top
// left of the window.
func (m Model) panelAt(x, y int) (Focus, bool) {
	l := m.layout
	if l.TooSmall {
		return 0, false
	}
	// Only the focused panel is drawn when stacked, so a click cannot change
	// the focus.
	if l.Stacked {
		return 0, false
	}

	top := l.HeaderH + l.BannerH
	if y < top || y >= top+l.BodyH {
		return 0, false // header, banner or footer
	}
	row := y - top

	if x < l.LeftW {
		if row < l.TopicsH {
			return FocusTopics, true
		}
		return FocusSubs, true
	}
	if row < l.MessagesH {
		return FocusMessages, true
	}
	return FocusDetail, true
}
