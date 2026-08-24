package state

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadMissingFileIsAFirstRunNotAnError(t *testing.T) {
	path := filepath.Join(t.TempDir(), "nope", "state.json")
	s, err := Load(path)
	if err != nil {
		t.Fatalf("Load of a missing file returned %v; a first run is not an error", err)
	}
	if s.LastBroker != "" || len(s.Expanded) != 0 {
		t.Fatalf("missing file produced a populated state: %+v", s)
	}
}

func TestSaveThenLoadRoundTrips(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	want := State{
		LastBroker: "local",
		Expanded:   []string{"home", "home/kitchen"},
	}
	want = want.RememberPublish(Publish{Topic: "home/lamp", Payload: "on", QoS: 1, Retain: true})

	if err := Save(path, want); err != nil {
		t.Fatalf("Save: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.LastBroker != "local" {
		t.Errorf("LastBroker = %q", got.LastBroker)
	}
	if strings.Join(got.Expanded, ",") != "home,home/kitchen" {
		t.Errorf("Expanded = %v", got.Expanded)
	}
	p, ok := got.RecentFor("home/lamp")
	if !ok || p.Payload != "on" || p.QoS != 1 || !p.Retain {
		t.Errorf("RecentFor(home/lamp) = %+v, %v", p, ok)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt was not stamped")
	}
}

// The state file may name a broker profile; it must never be able to carry the
// credential behind it. This is a schema guarantee, so assert on the bytes.
func TestSavedFileCarriesNoCredentialFields(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := Save(path, State{LastBroker: "production"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for _, forbidden := range []string{"password", "username", "token", "secret"} {
		if strings.Contains(strings.ToLower(string(raw)), forbidden) {
			t.Errorf("the state document mentions %q:\n%s", forbidden, raw)
		}
	}
}

func TestSaveUsesOwnerOnlyPermissions(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sub", "state.json")
	if err := Save(path, State{LastBroker: "local"}); err != nil {
		t.Fatalf("Save: %v", err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		t.Errorf("state.json has mode %04o; want owner-only", mode)
	}
}

// A foreign schema version is ignored, not migrated and not fatal.
func TestForeignVersionIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte(`{"version":99,"last_broker":"x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := Load(path)
	if err == nil {
		t.Error("a foreign version produced no diagnostic")
	}
	if s.LastBroker != "" {
		t.Errorf("a foreign version was applied anyway: %+v", s)
	}
}

func TestCorruptFileIsIgnored(t *testing.T) {
	path := filepath.Join(t.TempDir(), "state.json")
	if err := os.WriteFile(path, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err == nil {
		t.Error("a corrupt state file produced no diagnostic")
	}
}

func TestSanitizeAppliesTheCaps(t *testing.T) {
	var s State
	for i := 0; i < MaxExpandedNodes+50; i++ {
		s.Expanded = append(s.Expanded, string(rune('a'+i%26))+string(rune('0'+i%10))+"/"+itoa(i))
	}
	for i := 0; i < MaxRecentPublishes+10; i++ {
		s = s.RememberPublish(Publish{Topic: "t/" + itoa(i), Payload: strings.Repeat("x", MaxRecentPayload+1)})
	}
	s = s.Sanitize()

	if len(s.Expanded) > MaxExpandedNodes {
		t.Errorf("Expanded held %d entries, cap is %d", len(s.Expanded), MaxExpandedNodes)
	}
	if len(s.RecentPublishes) > MaxRecentPublishes {
		t.Errorf("RecentPublishes held %d entries, cap is %d", len(s.RecentPublishes), MaxRecentPublishes)
	}
	for _, p := range s.RecentPublishes {
		if len(p.Payload) > MaxRecentPayload {
			t.Fatalf("payload of %d bytes survived the %d-byte cap", len(p.Payload), MaxRecentPayload)
		}
	}
}

// Republishing to the same topic updates the entry instead of appending, so a
// session spent testing one topic does not evict every other memory.
func TestRememberPublishReplacesTheSameTopic(t *testing.T) {
	var s State
	s = s.RememberPublish(Publish{Topic: "a", Payload: "1"})
	s = s.RememberPublish(Publish{Topic: "b", Payload: "2"})
	s = s.RememberPublish(Publish{Topic: "a", Payload: "3"})

	if len(s.RecentPublishes) != 2 {
		t.Fatalf("held %d entries, want 2", len(s.RecentPublishes))
	}
	if p, _ := s.RecentFor("a"); p.Payload != "3" {
		t.Errorf("RecentFor(a) = %q, want the latest payload", p.Payload)
	}
}

func TestPathHonoursTheEnvironment(t *testing.T) {
	t.Setenv(EnvStatePath, "/tmp/explicit.json")
	if got := Path(); got != "/tmp/explicit.json" {
		t.Errorf("Path() = %q with %s set", got, EnvStatePath)
	}
	t.Setenv(EnvStatePath, "")
	t.Setenv("XDG_STATE_HOME", "/tmp/xdg")
	if got, want := Path(), filepath.Join("/tmp/xdg", "lazymqtt", "state.json"); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
}

func itoa(i int) string {
	if i == 0 {
		return "0"
	}
	var b []byte
	for i > 0 {
		b = append([]byte{byte('0' + i%10)}, b...)
		i /= 10
	}
	return string(b)
}
