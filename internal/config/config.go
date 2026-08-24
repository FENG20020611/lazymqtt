// Package config owns the on-disk schema, its discovery, parsing, validation
// and the credential resolution chain. Everything downstream receives a
// resolved value; no other package reads a file or an environment variable to
// find out how to connect.
package config

import (
	"time"

	"github.com/Onizuka893/lazymqtt/internal/store"
)

// SchemaVersion is the only value accepted in the `version:` field. It is
// checked before anything else so the schema can be migrated later without
// guessing what a file was written against.
const SchemaVersion = 1

// Config is a parsed config.yaml.
type Config struct {
	Version  int               `yaml:"version"`
	Defaults Defaults          `yaml:"defaults"`
	Limits   Limits            `yaml:"limits"`
	UI       UI                `yaml:"ui"`
	Logging  Logging           `yaml:"logging"`
	Brokers  map[string]Broker `yaml:"brokers"`

	// Path is the file this came from; empty when defaults were synthesised.
	Path string `yaml:"-"`
	// HasLiteralPassword records whether any broker carries a plaintext
	// password, which drives the file-permission check.
	HasLiteralPassword bool `yaml:"-"`
}

// Defaults are applied to every broker that does not override them. The
// pointer fields exist so "absent" is distinguishable from "explicitly zero".
type Defaults struct {
	ClientID       string         `yaml:"client_id"`
	KeepAlive      *Duration      `yaml:"keepalive"`
	ConnectTimeout *Duration      `yaml:"connect_timeout"`
	CleanStart     *bool          `yaml:"clean_start"`
	Protocol       string         `yaml:"protocol"` // auto | 5 | 3.1.1
	Subscriptions  []Subscription `yaml:"subscriptions"`
}

// Subscription is one entry of a broker's subscribe-on-connect set.
type Subscription struct {
	Filter string `yaml:"filter"`
	QoS    byte   `yaml:"qos"`
}

// Limits mirrors store.Limits in YAML form.
type Limits struct {
	MaxTopics       int `yaml:"max_topics"`
	PerTopicHistory int `yaml:"per_topic_history"`
	StreamHistory   int `yaml:"stream_history"`
	MaxPayloadBytes int `yaml:"max_payload_bytes"`
	IngestBuffer    int `yaml:"ingest_buffer"`
}

// Store converts to the store's own cap type.
func (l Limits) Store() store.Limits {
	return store.Limits{
		MaxTopics:       l.MaxTopics,
		PerTopicHistory: l.PerTopicHistory,
		StreamHistory:   l.StreamHistory,
		MaxPayloadBytes: l.MaxPayloadBytes,
	}.Normalize()
}

// UI holds presentation preferences.
type UI struct {
	RefreshMS       int    `yaml:"refresh_ms"`
	TimestampFormat string `yaml:"timestamp_format"`
	Theme           string `yaml:"theme"` // auto | dark | light
	StartPanel      string `yaml:"start_panel"`
	Mouse           bool   `yaml:"mouse"`
}

// Refresh is the coalescer flush interval.
func (u UI) Refresh() time.Duration { return time.Duration(u.RefreshMS) * time.Millisecond }

// Logging configures slog. Note that a file is the only destination: writing
// to stdout while the alt screen is up corrupts the display.
type Logging struct {
	Level          string `yaml:"level"`
	File           string `yaml:"file"`
	RedactPayloads bool   `yaml:"redact_payloads"`
}

// TLS is the per-broker TLS block.
type TLS struct {
	Enabled            bool   `yaml:"enabled"`
	CAFile             string `yaml:"ca_file"`
	CertFile           string `yaml:"cert_file"`
	KeyFile            string `yaml:"key_file"`
	ServerName         string `yaml:"server_name"`
	InsecureSkipVerify bool   `yaml:"insecure_skip_verify"`
}

// Broker is one named connection profile.
type Broker struct {
	Host           string         `yaml:"host"`
	Port           int            `yaml:"port"`
	Scheme         string         `yaml:"scheme"` // tcp | tls | ws | wss; inferred when empty
	URL            string         `yaml:"url"`    // overrides host/port/scheme entirely
	Protocol       string         `yaml:"protocol"`
	ClientID       string         `yaml:"client_id"`
	KeepAlive      *Duration      `yaml:"keepalive"`
	ConnectTimeout *Duration      `yaml:"connect_timeout"`
	CleanStart     *bool          `yaml:"clean_start"`
	SessionExpiry  uint32         `yaml:"session_expiry"`
	TLS            TLS            `yaml:"tls"`
	Username       string         `yaml:"username"`
	Password       string         `yaml:"password"`
	PasswordEnv    string         `yaml:"password_env"`
	PasswordCmd    string         `yaml:"password_cmd"`
	Subscriptions  []Subscription `yaml:"subscriptions"`

	// Name is the map key, filled in during load.
	Name string `yaml:"-"`
}

// Default returns the built-in configuration used when no file exists.
// Missing config is not an error: `lazymqtt` with no setup at all must work.
func Default() Config {
	ka := Duration(30 * time.Second)
	ct := Duration(10 * time.Second)
	clean := true
	return Config{
		Version: SchemaVersion,
		Defaults: Defaults{
			ClientID:       "lazymqtt-{{hostname}}-{{pid}}",
			KeepAlive:      &ka,
			ConnectTimeout: &ct,
			CleanStart:     &clean,
			Protocol:       "auto",
			Subscriptions:  []Subscription{{Filter: "#", QoS: 0}},
		},
		Limits: Limits{
			MaxTopics:       5000,
			PerTopicHistory: 50,
			StreamHistory:   2000,
			MaxPayloadBytes: 1 << 20,
			IngestBuffer:    4096,
		},
		UI: UI{
			RefreshMS:       50,
			TimestampFormat: "15:04:05.000",
			Theme:           "auto",
			StartPanel:      "topics",
		},
		Logging: Logging{Level: "warn", RedactPayloads: true},
		Brokers: map[string]Broker{},
	}
}
