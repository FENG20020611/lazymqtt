package config

import (
	"testing"
	"time"
)

// docs/configuration.md publishes a table of every key and its default. A
// reference that has silently drifted from the code is worse than no
// reference, so these are the numbers that document asserts. Changing one is
// fine; changing one without updating the document is not.
func TestDocumentedDefaults(t *testing.T) {
	d := Default()

	if d.Version != 1 {
		t.Errorf("version default = %d, want 1", d.Version)
	}

	t.Run("defaults", func(t *testing.T) {
		if got := d.Defaults.ClientID; got != "lazymqtt-{{hostname}}-{{pid}}" {
			t.Errorf("client_id = %q", got)
		}
		if got := d.Defaults.KeepAlive.D(); got != 30*time.Second {
			t.Errorf("keepalive = %v, want 30s", got)
		}
		if got := d.Defaults.ConnectTimeout.D(); got != 10*time.Second {
			t.Errorf("connect_timeout = %v, want 10s", got)
		}
		if got := *d.Defaults.CleanStart; !got {
			t.Error("clean_start = false, want true")
		}
		if got := d.Defaults.Protocol; got != "auto" {
			t.Errorf("protocol = %q, want auto", got)
		}
		if got := d.Defaults.Subscriptions; len(got) != 1 || got[0].Filter != "#" || got[0].QoS != 0 {
			t.Errorf("subscriptions = %+v, want one entry of # at qos 0", got)
		}
	})

	t.Run("limits", func(t *testing.T) {
		for _, c := range []struct {
			key  string
			got  int
			want int
		}{
			{"max_topics", d.Limits.MaxTopics, 5000},
			{"per_topic_history", d.Limits.PerTopicHistory, 50},
			{"stream_history", d.Limits.StreamHistory, 2000},
			{"max_payload_bytes", d.Limits.MaxPayloadBytes, 1 << 20},
			{"ingest_buffer", d.Limits.IngestBuffer, 4096},
		} {
			if c.got != c.want {
				t.Errorf("%s = %d, want %d", c.key, c.got, c.want)
			}
		}
	})

	t.Run("ui", func(t *testing.T) {
		if got := d.UI.RefreshMS; got != 50 {
			t.Errorf("refresh_ms = %d, want 50", got)
		}
		if got := d.UI.TimestampFormat; got != "15:04:05.000" {
			t.Errorf("timestamp_format = %q", got)
		}
		if got := d.UI.Theme; got != "auto" {
			t.Errorf("theme = %q, want auto", got)
		}
		if got := d.UI.StartPanel; got != "topics" {
			t.Errorf("start_panel = %q, want topics", got)
		}
		if d.UI.Mouse {
			t.Error("mouse = true, want false")
		}
	})

	t.Run("logging", func(t *testing.T) {
		if got := d.Logging.Level; got != "warn" {
			t.Errorf("level = %q, want warn", got)
		}
		if got := d.Logging.File; got != "" {
			t.Errorf("file = %q, want empty", got)
		}
	})
}

// The document warns that writing `0` is not the same as omitting a key, and
// that the two halves of the limits block disagree about what zero means. That
// asymmetry is worth pinning: it is the kind of thing a well-meaning cleanup
// "fixes" in one direction and silently changes behaviour.
func TestZeroIsNotTheSameAsOmittingALimit(t *testing.T) {
	got := Limits{}.Store()

	if got.MaxTopics != 5000 {
		t.Errorf("max_topics: 0 gave %d, want the 5000 default", got.MaxTopics)
	}
	if got.MaxPayloadBytes != 1<<20 {
		t.Errorf("max_payload_bytes: 0 gave %d, want the 1MiB default", got.MaxPayloadBytes)
	}
	// These two clamp to 1 rather than to their defaults, so a config with an
	// explicit zero keeps a single message and looks broken.
	if got.PerTopicHistory != 1 {
		t.Errorf("per_topic_history: 0 gave %d, want 1", got.PerTopicHistory)
	}
	if got.StreamHistory != 1 {
		t.Errorf("stream_history: 0 gave %d, want 1", got.StreamHistory)
	}
}

// The default-port table in the reference, including the websocket schemes the
// existing ServerURL test does not reach.
func TestDocumentedDefaultPorts(t *testing.T) {
	for _, c := range []struct{ scheme, want string }{
		{"tcp", "tcp://h:1883"},
		{"mqtt", "tcp://h:1883"},
		{"tls", "tls://h:8883"},
		{"ssl", "tls://h:8883"},
		{"mqtts", "tls://h:8883"},
		{"ws", "ws://h:8083"},
		{"wss", "wss://h:8084"},
	} {
		got, err := Broker{URL: c.scheme + "://h"}.ServerURL()
		if err != nil {
			t.Fatalf("%s: %v", c.scheme, err)
		}
		if got != c.want {
			t.Errorf("%s:// resolved to %q, want %q", c.scheme, got, c.want)
		}
	}
}

// The reference claims a bare `lazymqtt` uses the only configured profile, and
// that an unknown name is an error naming the alternatives rather than a
// silent fallback.
func TestBrokerRefWithASingleProfile(t *testing.T) {
	cfg := Default()
	cfg.Brokers = map[string]Broker{"only": {Host: "h"}}

	b, err := cfg.BrokerRef("")
	if err != nil {
		t.Fatalf("BrokerRef(\"\") with one profile: %v", err)
	}
	if b.Name != "only" {
		t.Errorf("resolved to %q, want only", b.Name)
	}

	cfg.Brokers["second"] = Broker{Host: "h2"}
	if _, err := cfg.BrokerRef(""); err == nil {
		t.Error("BrokerRef(\"\") with two profiles should not guess")
	}
	if _, err := cfg.BrokerRef("typo"); err == nil {
		t.Error("an unknown profile name should be an error")
	}
}

// `{{user}}` is documented as a client_id substitution, and the 64-byte
// ceiling is what keeps a long hostname from producing an ID the broker
// rejects.
func TestClientIDTemplateAndCeiling(t *testing.T) {
	t.Setenv("USER", "alice")
	if got := ExpandClientID("x-{{user}}"); got != "x-alice" {
		t.Errorf("ExpandClientID = %q, want x-alice", got)
	}

	long := ""
	for i := 0; i < 100; i++ {
		long += "a"
	}
	if got := ExpandClientID(long); len(got) != 64 {
		t.Errorf("a %d-byte client_id became %d bytes, want 64", len(long), len(got))
	}
}
