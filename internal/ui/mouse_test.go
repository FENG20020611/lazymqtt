package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/state"
)

func TestMouseReportingIsOptIn(t *testing.T) {
	m := newTestModel(t, 120, 40)
	if got := m.View().MouseMode; got != tea.MouseModeNone {
		t.Errorf("mouse mode = %v with ui.mouse unset, want none", got)
	}

	cfg := config.Default()
	cfg.UI.Mouse = true
	m = newModelWithConfig(t, cfg)
	if got := m.View().MouseMode; got != tea.MouseModeCellMotion {
		t.Errorf("mouse mode = %v with ui.mouse: true, want cell motion", got)
	}
}

// A click lands on the panel drawn under it. The arithmetic mirrors
// ComputeLayout, so this is the test that catches the two drifting apart.
func TestClickFocusesThePanelUnderIt(t *testing.T) {
	m := newTestModel(t, 120, 40)
	l := m.layout
	top := l.HeaderH + l.BannerH

	cases := []struct {
		name string
		x, y int
		want Focus
	}{
		{"topics", 2, top + 1, FocusTopics},
		{"subscriptions", 2, top + l.TopicsH + 1, FocusSubs},
		{"messages", l.LeftW + 2, top + 1, FocusMessages},
		{"detail", l.LeftW + 2, top + l.MessagesH + 1, FocusDetail},
	}
	for _, c := range cases {
		got, ok := m.panelAt(c.x, c.y)
		if !ok || got != c.want {
			t.Errorf("%s: panelAt(%d,%d) = %v/%v, want %v", c.name, c.x, c.y, got, ok, c.want)
		}
	}

	// The header and the footer belong to nothing.
	if _, ok := m.panelAt(2, 0); ok {
		t.Error("a click on the header claimed a panel")
	}
	if _, ok := m.panelAt(2, m.height-1); ok {
		t.Error("a click on the footer claimed a panel")
	}
}

func TestWheelScrollsTheFocusedPanel(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "alpha", "bravo", "charlie", "delta", "echo", "foxtrot")
	m.focus = FocusTopics

	before := m.topics.Cursor()
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)
	if m.topics.Cursor() == before {
		t.Fatal("the wheel did not move the topic cursor")
	}
	next, _ = m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelUp})
	m = next.(Model)
	if m.topics.Cursor() != before {
		t.Errorf("cursor = %d after a wheel up and down, want %d", m.topics.Cursor(), before)
	}
}

// The wheel must not move what is behind an overlay: a list scrolling under a
// modal reads as a rendering bug.
func TestWheelIgnoresThePanelBehindAModal(t *testing.T) {
	m := newTestModel(t, 120, 40)
	m = ingest(m, "alpha", "bravo", "charlie", "delta")
	m.focus = FocusTopics
	m = press(t, m, "?") // help overlay

	before := m.topics.Cursor()
	next, _ := m.Update(tea.MouseWheelMsg{Button: tea.MouseWheelDown})
	m = next.(Model)
	if m.topics.Cursor() != before {
		t.Error("the wheel scrolled the topic tree while the help overlay was open")
	}
}

func newModelWithConfig(t *testing.T, cfg config.Config) Model {
	t.Helper()
	return newModelWithState(t, cfg, state.State{})
}
