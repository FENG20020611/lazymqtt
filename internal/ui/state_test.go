package ui

import (
	"strings"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/app"
	"github.com/Onizuka893/lazymqtt/internal/config"
	"github.com/Onizuka893/lazymqtt/internal/logging"
	"github.com/Onizuka893/lazymqtt/internal/state"
)

func newModelWithState(t *testing.T, cfg config.Config, s state.State) Model {
	t.Helper()
	return New(Options{
		App:    app.New(cfg, logging.Discard()),
		Config: cfg,
		State:  s,
	}).resize(120, 40)
}

func TestSnapshotRecordsTheExpandedTree(t *testing.T) {
	m := newModelWithState(t, config.Default(), state.State{})
	m = ingest(m, "home/kitchen/temp", "home/attic/temp")
	m.store.Node("home/kitchen").SetExpanded(true)

	got := m.StateSnapshot()
	if !contains(got.Expanded, "home/kitchen") {
		t.Errorf("the snapshot does not record the expanded node: %v", got.Expanded)
	}
}

// The whole point of persisting the expanded set is that the tree comes back
// looking the way it was left.
func TestExpandedNodesSurviveARestart(t *testing.T) {
	cfg := config.Default()
	first := newModelWithState(t, cfg, state.State{})
	first = ingest(first, "home/kitchen/temp", "factory/line1/press/state")
	first.store.Node("home/kitchen").SetExpanded(true)
	first.store.Invalidate()
	wantVisible := len(first.store.Flatten())

	second := newModelWithState(t, cfg, first.StateSnapshot())
	second = ingest(second, "home/kitchen/temp", "factory/line1/press/state")

	if got := len(second.store.Flatten()); got != wantVisible {
		t.Errorf("the restored tree shows %d rows, the saved one showed %d", got, wantVisible)
	}
	if !second.store.Node("home/kitchen").Expanded {
		t.Error("home/kitchen did not come back expanded")
	}
}

// A publish is remembered so the modal prefills next time. Nothing here may
// reach the wire: there is no client, and the persistence must not depend on
// the publish succeeding.
func TestPublishIsRememberedAndPrefillsTheModal(t *testing.T) {
	m := newModelWithState(t, config.Default(), state.State{})
	m = ingest(m, "home/lamp")
	m = press(t, m, "1", "l")
	topic := m.topics.Selected()

	m = press(t, m, "p", "tab")
	for _, r := range "on" {
		m = press(t, m, string(r))
	}
	m = press(t, m, "tab", "1", "tab", "space") // qos 1, retain
	m = press(t, m, "shift+tab", "shift+tab", "shift+tab")
	m = press(t, m, "enter")
	if m.Mode() != ModeNormal {
		t.Fatalf("enter on the topic field did not submit; mode = %v", m.Mode())
	}

	p, ok := m.StateSnapshot().RecentFor(topic)
	if !ok {
		t.Fatalf("publishing to %q was not remembered", topic)
	}
	if p.Payload != "on" || p.QoS != 1 || !p.Retain {
		t.Fatalf("remembered %+v, want payload \"on\" qos 1 retain true", p)
	}

	m = press(t, m, "p")
	req := m.publish.Request()
	if string(req.Payload) != "on" || req.QoS != 1 || !req.Retain {
		t.Errorf("the modal did not prefill from the remembered publish: %+v", req)
	}
}

// The remembered broker is a convenience for `lazymqtt` with no arguments;
// an explicit --broker must always win.
func TestExplicitBrokerOverridesTheRememberedOne(t *testing.T) {
	cfg := config.Default()
	cfg.Brokers = map[string]config.Broker{
		"local": {Host: "localhost", Port: 1883},
		"other": {Host: "elsewhere", Port: 1883},
	}
	m := New(Options{
		App:        app.New(cfg, logging.Discard()),
		Config:     cfg,
		AutoBroker: "other",
		State:      state.State{LastBroker: "local"},
	})
	if m.autoBroker != "other" {
		t.Errorf("autoBroker = %q, want the explicit flag to win", m.autoBroker)
	}

	m = New(Options{App: app.New(cfg, logging.Discard()), Config: cfg, State: state.State{LastBroker: "local"}})
	if m.autoBroker != "local" {
		t.Errorf("autoBroker = %q, want the remembered profile", m.autoBroker)
	}
}

// A profile deleted from config.yaml since the last run must not be dialled.
func TestRememberedBrokerIsIgnoredWhenTheProfileIsGone(t *testing.T) {
	cfg := config.Default()
	cfg.Brokers = map[string]config.Broker{"local": {Host: "localhost", Port: 1883}}
	m := New(Options{
		App:    app.New(cfg, logging.Discard()),
		Config: cfg,
		State:  state.State{LastBroker: "deleted-profile"},
	})
	if m.autoBroker != "" {
		t.Errorf("autoBroker = %q; a profile that no longer exists must not be dialled", m.autoBroker)
	}
}

func contains(hay []string, needle string) bool {
	for _, h := range hay {
		if h == needle || strings.HasSuffix(h, "/"+needle) {
			return true
		}
	}
	return false
}
