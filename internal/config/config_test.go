package config

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func write(t *testing.T, body string, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(p, []byte(body), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

const goodConfig = `
version: 1
defaults:
  client_id: "lazymqtt-test"
  keepalive: 45s
  protocol: 5
limits:
  max_topics: 1000
ui:
  refresh_ms: 100
brokers:
  local:
    host: localhost
    port: 1883
  prod:
    host: mqtt.example.com
    port: 8883
    keepalive: 90s
    tls:
      enabled: true
      server_name: mqtt.example.com
    username: ops
    password_cmd: "echo hunter2"
    subscriptions:
      - filter: "devices/+/state"
        qos: 1
`

func TestLoadGoodConfig(t *testing.T) {
	cfg, err := Load(write(t, goodConfig, 0o600))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Defaults.KeepAlive.D() != 45*time.Second {
		t.Fatalf("keepalive = %v", cfg.Defaults.KeepAlive.D())
	}
	// Unset blocks keep their built-in defaults.
	if cfg.Defaults.ConnectTimeout.D() != 10*time.Second {
		t.Fatalf("connect_timeout default lost: %v", cfg.Defaults.ConnectTimeout.D())
	}
	if cfg.Limits.StreamHistory != 2000 {
		t.Fatalf("stream_history default lost: %d", cfg.Limits.StreamHistory)
	}
	if !cfg.Logging.RedactPayloads {
		t.Fatal("redact_payloads must default to true")
	}
	if got := cfg.Names(); strings.Join(got, ",") != "local,prod" {
		t.Fatalf("Names = %v", got)
	}
}

func TestPerBrokerOverridesDefaults(t *testing.T) {
	cfg, err := Load(write(t, goodConfig, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	local, _ := cfg.BrokerRef("local")
	prod, _ := cfg.BrokerRef("prod")

	rl, err := cfg.Resolve(context.Background(), local, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rl.Options.KeepAlive != 45*time.Second {
		t.Fatalf("local keepalive = %v, want the default 45s", rl.Options.KeepAlive)
	}
	if rl.Options.ServerURL != "tcp://localhost:1883" {
		t.Fatalf("local url = %s", rl.Options.ServerURL)
	}
	if len(rl.Subs) != 1 || rl.Subs[0].Filter != "#" {
		t.Fatalf("local subs = %+v, want the default #", rl.Subs)
	}

	rp, err := cfg.Resolve(context.Background(), prod, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rp.Options.KeepAlive != 90*time.Second {
		t.Fatalf("prod keepalive = %v, want the override 90s", rp.Options.KeepAlive)
	}
	if rp.Options.ServerURL != "tls://mqtt.example.com:8883" {
		t.Fatalf("prod url = %s", rp.Options.ServerURL)
	}
	if rp.Options.TLS == nil || rp.Options.TLS.ServerName != "mqtt.example.com" {
		t.Fatalf("prod tls = %+v", rp.Options.TLS)
	}
	if string(rp.Options.Password) != "hunter2" {
		t.Fatalf("password_cmd produced %q", rp.Options.Password)
	}
	if len(rp.Subs) != 1 || rp.Subs[0].Filter != "devices/+/state" || rp.Subs[0].QoS != 1 {
		t.Fatalf("prod subs = %+v", rp.Subs)
	}
}

func TestTopicOverrideReplacesConfiguredSubscriptions(t *testing.T) {
	cfg, _ := Load(write(t, goodConfig, 0o600))
	prod, _ := cfg.BrokerRef("prod")
	r, err := cfg.Resolve(context.Background(), prod, nil, []string{"a/#", "b/+"})
	if err != nil {
		t.Fatal(err)
	}
	if len(r.Subs) != 2 || r.Subs[0].Filter != "a/#" {
		t.Fatalf("override subs = %+v", r.Subs)
	}
}

func TestUnknownFieldIsAnErrorNamingTheField(t *testing.T) {
	_, err := Load(write(t, "version: 1\nbrokers:\n  a:\n    hostname: x\n", 0o600))
	if err == nil {
		t.Fatal("a typo'd key was silently ignored")
	}
	if !strings.Contains(err.Error(), "hostname") {
		t.Fatalf("error does not name the offending field: %v", err)
	}
}

func TestVersionIsRequiredAndChecked(t *testing.T) {
	if _, err := Load(write(t, "brokers: {}\n", 0o600)); err == nil ||
		!strings.Contains(err.Error(), "version") {
		t.Fatalf("missing version accepted: %v", err)
	}
	_, err := Load(write(t, "version: 99\n", 0o600))
	if err == nil || !strings.Contains(err.Error(), "99") {
		t.Fatalf("future version accepted: %v", err)
	}
}

func TestPermissionCheckRejectsReadablePasswordFile(t *testing.T) {
	body := "version: 1\nbrokers:\n  a:\n    host: h\n    password: secret\n"
	_, err := Load(write(t, body, 0o644))
	if !errors.Is(err, ErrPermissive) {
		t.Fatalf("mode 0644 with a literal password was accepted: %v", err)
	}
	if _, err := Load(write(t, body, 0o600)); err != nil {
		t.Fatalf("mode 0600 with a literal password was rejected: %v", err)
	}
}

func TestValidationAggregatesEveryProblem(t *testing.T) {
	bad := `
version: 1
defaults:
  protocol: 6
  subscriptions:
    - filter: "a/#/b"
      qos: 5
limits:
  max_topics: 10
ui:
  refresh_ms: 9000
  theme: neon
brokers:
  a:
    host: h
    url: tcp://h:1883
    password: p
    password_cmd: "echo x"
    tls:
      cert_file: /tmp/c.pem
`
	_, err := Load(write(t, bad, 0o600))
	if err == nil {
		t.Fatal("a thoroughly broken config was accepted")
	}
	msg := err.Error()
	for _, want := range []string{
		"defaults.protocol", "subscriptions[0]", "qos 5",
		"max_topics", "refresh_ms", "ui.theme",
		"url or host", "mutually exclusive", "cert_file set without key_file",
	} {
		if !strings.Contains(msg, want) {
			t.Errorf("aggregated report is missing %q:\n%s", want, msg)
		}
	}
}

func TestMissingConfigIsNotAnError(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("empty path: %v", err)
	}
	if cfg.Version != SchemaVersion || cfg.UI.RefreshMS != 50 {
		t.Fatalf("defaults not returned: %+v", cfg)
	}
}

func TestBrokerRefAcceptsAURL(t *testing.T) {
	cfg := Default()
	b, err := cfg.BrokerRef("tcp://10.0.0.5:1883")
	if err != nil {
		t.Fatal(err)
	}
	u, _ := b.ServerURL()
	if u != "tcp://10.0.0.5:1883" {
		t.Fatalf("url = %s", u)
	}
	if _, err := cfg.BrokerRef("nope"); err == nil {
		t.Fatal("an unknown bare name was accepted as a broker")
	}
}

func TestServerURLDefaults(t *testing.T) {
	cases := []struct{ in, want string }{
		{"mqtts://h", "tls://h:8883"},
		{"mqtt://h", "tcp://h:1883"},
		{"h:1884", "tcp://h:1884"},
		{"ws://h", "ws://h:8083"},
	}
	for _, c := range cases {
		b := Broker{URL: c.in}
		got, err := b.ServerURL()
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("ServerURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestResolvePasswordChain(t *testing.T) {
	ctx := context.Background()

	got, err := ResolvePassword(ctx, Broker{PasswordCmd: "printf 'from-cmd\n'"}, nil)
	if err != nil || string(got) != "from-cmd" {
		t.Fatalf("password_cmd = %q, %v", got, err)
	}

	t.Setenv("LAZYMQTT_TEST_PW", "from-env")
	got, err = ResolvePassword(ctx, Broker{PasswordEnv: "LAZYMQTT_TEST_PW"}, nil)
	if err != nil || string(got) != "from-env" {
		t.Fatalf("password_env = %q, %v", got, err)
	}
	if _, err := ResolvePassword(ctx, Broker{PasswordEnv: "LAZYMQTT_UNSET_PW"}, nil); err == nil {
		t.Fatal("an unset password_env was accepted")
	}

	got, err = ResolvePassword(ctx, Broker{Password: "${LAZYMQTT_TEST_PW}"}, nil)
	if err != nil || string(got) != "from-env" {
		t.Fatalf("literal password expansion = %q, %v", got, err)
	}

	prompted := false
	got, err = ResolvePassword(ctx, Broker{Username: "u"}, func(string) ([]byte, error) {
		prompted = true
		return []byte("typed"), nil
	})
	if err != nil || !prompted || string(got) != "typed" {
		t.Fatalf("prompt = %q, %v (prompted=%v)", got, err, prompted)
	}
}

func TestPasswordCmdFailureAndTimeout(t *testing.T) {
	_, err := ResolvePassword(context.Background(), Broker{PasswordCmd: "echo nope >&2; exit 3"}, nil)
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("failing password_cmd = %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	if _, err := ResolvePassword(ctx, Broker{PasswordCmd: "sleep 10"}, nil); err == nil {
		t.Fatal("a hanging password_cmd was not cut off")
	}
	if time.Since(start) > 2*time.Second {
		t.Fatal("password_cmd cancellation took too long")
	}
}

func TestExpandPathTilde(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skip("no home directory")
	}
	if got := ExpandPath("~/a/b"); got != filepath.Join(home, "a", "b") {
		t.Fatalf("ExpandPath(~/a/b) = %s", got)
	}
	if ExpandPath("") != "" {
		t.Fatal("ExpandPath(\"\") should stay empty")
	}
}

func TestExpandClientID(t *testing.T) {
	id := ExpandClientID("lazymqtt-{{hostname}}-{{pid}}")
	if strings.Contains(id, "{{") {
		t.Fatalf("template not expanded: %s", id)
	}
	if len(ExpandClientID(strings.Repeat("x", 200))) != 64 {
		t.Fatal("over-long client id was not clamped")
	}
}

func TestDiscoveryPrefersExplicitPath(t *testing.T) {
	// Point every fallback in the discovery chain at an empty directory.
	// Without this the test passes or fails depending on whether whoever runs
	// it happens to have a real ~/.config/lazymqtt/config.yaml.
	isolateConfigHome(t)

	p := write(t, goodConfig, 0o600)
	t.Setenv(EnvConfigPath, "/nonexistent/lazymqtt.yaml")
	if got := Discover(p); got != p {
		t.Fatalf("Discover(explicit) = %q", got)
	}
	if got := Discover(""); got != "" {
		t.Fatalf("Discover with only a bogus env var = %q", got)
	}
}

func TestDiscoveryFindsTheXDGPath(t *testing.T) {
	dir := isolateConfigHome(t)
	want := filepath.Join(dir, "lazymqtt", "config.yaml")
	if err := os.MkdirAll(filepath.Dir(want), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte(goodConfig), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := Discover(""); got != want {
		t.Fatalf("Discover() = %q, want %q", got, want)
	}
}

// isolateConfigHome redirects the whole discovery chain into a temp dir and
// returns the XDG root it used.
func isolateConfigHome(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", dir)
	t.Setenv("HOME", dir) // os.UserConfigDir and os.UserHomeDir both follow this
	t.Setenv(EnvConfigPath, "")
	return dir
}

func TestStarterConfigIsValid(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "nested", "config.yaml")
	if err := WriteStarter(p); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o600 {
		t.Fatalf("starter config mode = %04o, want 0600", st.Mode().Perm())
	}
	if _, err := Load(p); err != nil {
		t.Fatalf("the starter config does not load: %v", err)
	}
	if err := WriteStarter(p); err == nil {
		t.Fatal("WriteStarter overwrote an existing file")
	}
}

// TLS on the conventionally plaintext port 1883 is unusual but legal, and
// real brokers do it. It must not be warned about, and it must still produce
// an encrypted scheme.
func TestTLSOnPort1883IsHonouredWithoutComplaint(t *testing.T) {
	body := "version: 1\nbrokers:\n  test:\n    host: h\n    port: 1883\n    tls:\n      enabled: true\n"
	cfg, err := Load(write(t, body, 0o600))
	if err != nil {
		t.Fatal(err)
	}
	for _, w := range Check(&cfg).Warnings {
		if strings.Contains(w, "1883") {
			t.Errorf("spurious warning for a legitimate setup: %s", w)
		}
	}
	b, _ := cfg.BrokerRef("test")
	got, err := b.ServerURL()
	if err != nil {
		t.Fatal(err)
	}
	if got != "tls://h:1883" {
		t.Fatalf("ServerURL = %q, want tls://h:1883", got)
	}
}

// autopaho decides whether to use TLS from the URL scheme alone and ignores
// the *tls.Config on a plaintext scheme. A `mqtt://` url with tls.enabled must
// therefore still resolve to an encrypted scheme, or the connection would go
// out in the clear while the config claims otherwise.
func TestTLSEnabledUpgradesAPlaintextURLScheme(t *testing.T) {
	cases := []struct{ url, want string }{
		{"mqtt://h:1883", "tls://h:1883"},
		{"tcp://h:1883", "tls://h:1883"},
		{"h:1883", "tls://h:1883"},
		{"mqtts://h:8883", "tls://h:8883"},
		{"tls://h:8883", "tls://h:8883"},
		{"ws://h:8083", "wss://h:8083"},
	}
	for _, c := range cases {
		b := Broker{Name: "t", URL: c.url, TLS: TLS{Enabled: true}}
		got, err := b.ServerURL()
		if err != nil {
			t.Fatal(err)
		}
		if got != c.want {
			t.Errorf("url %q with tls.enabled -> %q, want %q", c.url, got, c.want)
		}
	}
}

func TestTLSDisabledLeavesTheSchemeAlone(t *testing.T) {
	b := Broker{Name: "t", URL: "mqtt://h:1883"}
	if got, _ := b.ServerURL(); got != "tcp://h:1883" {
		t.Fatalf("ServerURL = %q, want tcp://h:1883", got)
	}
}
