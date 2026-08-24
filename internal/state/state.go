// Package state persists the small amount of UI state that should survive a
// restart: the last broker, which nodes were expanded, and recent publish
// payloads.
//
// It is deliberately separate from internal/config. config.yaml is a file the
// user hand-edited and commented, and the app never rewrites it (§21, pitfall
// 18). Everything here is disposable: a missing, unreadable or unparseable
// state file is not an error, it is a first run.
//
// INVARIANT: this file never contains a credential. Nothing in the schema can
// hold one — no password, no username, no URL with userinfo — and Sanitize
// enforces the size caps that keep a state file from growing without bound.
package state

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SchemaVersion is bumped when the shape changes. A file written by a
// different version is ignored rather than migrated: this is a convenience
// cache, and the cost of getting it wrong should be one lost preference, not
// a migration path to maintain.
const SchemaVersion = 1

// EnvStatePath overrides the discovery chain, mainly so tests never touch a
// developer's real state file.
const EnvStatePath = "LAZYMQTT_STATE"

// Caps on what a state file may hold. A viewer that publishes 10 MB payloads
// should not write a 10 MB state file on exit.
const (
	MaxRecentPublishes = 20
	MaxRecentPayload   = 4 << 10
	MaxExpandedNodes   = 500
)

// Publish is one remembered outbound message, used to prefill the publish
// modal the next time the same topic is selected.
type Publish struct {
	Topic   string `json:"topic"`
	Payload string `json:"payload,omitempty"`
	QoS     byte   `json:"qos,omitempty"`
	Retain  bool   `json:"retain,omitempty"`
}

// State is the whole persisted document.
type State struct {
	Version int `json:"version"`
	// LastBroker is a profile name, never a URL with credentials in it.
	LastBroker string `json:"last_broker,omitempty"`
	// Expanded holds the full topics of the nodes that were open, so a
	// restart returns to the same view of the tree.
	Expanded        []string  `json:"expanded,omitempty"`
	RecentPublishes []Publish `json:"recent_publishes,omitempty"`
	UpdatedAt       time.Time `json:"updated_at,omitzero"`
}

// Path returns where state is read from and written to. $XDG_STATE_HOME is
// honoured; the fallback is the XDG default of ~/.local/state even on macOS,
// because this is machine-local scratch data rather than something a user
// browses in Finder.
func Path() string {
	if p := os.Getenv(EnvStatePath); p != "" {
		return p
	}
	if dir := os.Getenv("XDG_STATE_HOME"); dir != "" {
		return filepath.Join(dir, "lazymqtt", "state.json")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "lazymqtt", "state.json")
}

// Load reads the state file. A missing file yields a zero State and no error.
// A corrupt or foreign-version file yields a zero State and an error the
// caller is expected to log and otherwise ignore.
func Load(path string) (State, error) {
	if path == "" {
		return State{}, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return State{}, nil
		}
		return State{}, fmt.Errorf("reading %s: %w", path, err)
	}
	var s State
	if err := json.Unmarshal(raw, &s); err != nil {
		return State{}, fmt.Errorf("parsing %s: %w", path, err)
	}
	if s.Version != SchemaVersion {
		return State{}, fmt.Errorf("%s: ignoring state written by schema version %d", path, s.Version)
	}
	return s.Sanitize(), nil
}

// Save writes the state atomically at mode 0600.
//
// The write goes to a temporary file in the same directory and is renamed into
// place, so an interrupted exit leaves the previous state intact rather than a
// half-written document that Load then has to reject.
func Save(path string, s State) error {
	if path == "" {
		return nil
	}
	s = s.Sanitize()
	s.Version = SchemaVersion
	s.UpdatedAt = time.Now().UTC()

	raw, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')

	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(dir, ".state-*.json")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return err
	}
	if _, err := tmp.Write(raw); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// Sanitize applies the caps and drops entries that cannot be meaningful.
// Load and Save both run it, so neither a hand-edited file nor a long session
// can produce an oversized document.
func (s State) Sanitize() State {
	if len(s.Expanded) > MaxExpandedNodes {
		s.Expanded = s.Expanded[:MaxExpandedNodes]
	}
	expanded := make([]string, 0, len(s.Expanded))
	seen := make(map[string]struct{}, len(s.Expanded))
	for _, e := range s.Expanded {
		if e == "" || strings.ContainsAny(e, "\x00") {
			continue
		}
		if _, dup := seen[e]; dup {
			continue
		}
		seen[e] = struct{}{}
		expanded = append(expanded, e)
	}
	s.Expanded = expanded

	// Keep the most recent, which is what a prefill wants.
	if len(s.RecentPublishes) > MaxRecentPublishes {
		s.RecentPublishes = s.RecentPublishes[len(s.RecentPublishes)-MaxRecentPublishes:]
	}
	pubs := make([]Publish, 0, len(s.RecentPublishes))
	for _, p := range s.RecentPublishes {
		if p.Topic == "" {
			continue
		}
		if len(p.Payload) > MaxRecentPayload {
			p.Payload = p.Payload[:MaxRecentPayload]
		}
		if p.QoS > 2 {
			p.QoS = 2
		}
		pubs = append(pubs, p)
	}
	s.RecentPublishes = pubs
	return s
}

// RememberPublish records an outbound message, replacing any earlier entry
// for the same topic so the list holds distinct topics rather than a hundred
// repeats of the one being tested.
func (s State) RememberPublish(p Publish) State {
	if p.Topic == "" {
		return s
	}
	out := make([]Publish, 0, len(s.RecentPublishes)+1)
	for _, e := range s.RecentPublishes {
		if e.Topic != p.Topic {
			out = append(out, e)
		}
	}
	s.RecentPublishes = append(out, p)
	return s.Sanitize()
}

// RecentFor returns the last payload published to a topic, if any.
func (s State) RecentFor(topic string) (Publish, bool) {
	for i := len(s.RecentPublishes) - 1; i >= 0; i-- {
		if s.RecentPublishes[i].Topic == topic {
			return s.RecentPublishes[i], true
		}
	}
	return Publish{}, false
}
