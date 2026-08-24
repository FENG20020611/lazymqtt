package config

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/goccy/go-yaml"
)

// EnvConfigPath overrides the discovery chain.
const EnvConfigPath = "LAZYMQTT_CONFIG"

// Discover returns the first config path that exists, or "" when none does.
// The order is: explicit flag, $LAZYMQTT_CONFIG, $XDG_CONFIG_HOME, then the
// platform config dir (which does the right thing on macOS).
func Discover(explicit string) string {
	for _, p := range candidatePaths(explicit) {
		if p == "" {
			continue
		}
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p
		}
	}
	return ""
}

func candidatePaths(explicit string) []string {
	paths := []string{ExpandPath(explicit), ExpandPath(os.Getenv(EnvConfigPath))}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		paths = append(paths, filepath.Join(xdg, "lazymqtt", "config.yaml"))
	}
	if dir, err := os.UserConfigDir(); err == nil {
		paths = append(paths, filepath.Join(dir, "lazymqtt", "config.yaml"))
	}
	if home, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(home, ".config", "lazymqtt", "config.yaml"))
	}
	return paths
}

// DefaultPath is where `config init` writes, whether or not it exists yet.
func DefaultPath() string {
	if p := ExpandPath(os.Getenv(EnvConfigPath)); p != "" {
		return p
	}
	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "lazymqtt", "config.yaml")
	}
	if dir, err := os.UserConfigDir(); err == nil {
		return filepath.Join(dir, "lazymqtt", "config.yaml")
	}
	return "lazymqtt.yaml"
}

// ErrPermissive is returned when a config file holding a literal password is
// readable by anyone but its owner. OpenSSH refuses private keys for the same
// reason, and this is the same mistake.
var ErrPermissive = errors.New("config file with a literal password is group- or world-readable")

// removed lists config keys that once existed and no longer do, with the
// reason. An unknown-field error naming one of these is almost certainly an
// older config rather than a typo, and "unknown field" alone sends the reader
// looking for a misspelling that is not there.
var removed = map[string]string{
	"redact_payloads": "logging.redact_payloads was removed in v0.1.0: no log " +
		"call at any level records a payload, so the key never did anything. " +
		"Delete the line.",
}

func removedKeyHint(err error) string {
	msg := err.Error()
	for key, hint := range removed {
		if strings.Contains(msg, key) {
			return "hint: " + hint
		}
	}
	return ""
}

// Load reads and validates a config. An empty path yields Default() with no
// error: missing config is not an error.
func Load(path string) (Config, error) {
	cfg := Default()
	if path == "" {
		return cfg, nil
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return cfg, fmt.Errorf("reading %s: %w", path, err)
	}
	if err := checkVersion(raw, path); err != nil {
		return cfg, err
	}
	// Unmarshal onto the defaults so unset blocks keep their built-in values.
	if err := yaml.UnmarshalWithOptions(raw, &cfg, yaml.DisallowUnknownField()); err != nil {
		if hint := removedKeyHint(err); hint != "" {
			return cfg, fmt.Errorf("parsing %s:\n%s\n%s", path,
				yaml.FormatError(err, false, true), hint)
		}
		return cfg, fmt.Errorf("parsing %s:\n%s", path, yaml.FormatError(err, false, true))
	}
	cfg.Path = path

	for name, b := range cfg.Brokers {
		b.Name = name
		b.TLS.CAFile = ExpandPath(b.TLS.CAFile)
		b.TLS.CertFile = ExpandPath(b.TLS.CertFile)
		b.TLS.KeyFile = ExpandPath(b.TLS.KeyFile)
		cfg.Brokers[name] = b
		if b.Password != "" {
			cfg.HasLiteralPassword = true
		}
	}
	cfg.Logging.File = ExpandPath(cfg.Logging.File)

	if cfg.HasLiteralPassword {
		if err := checkPermissions(path); err != nil {
			return cfg, err
		}
	}
	if err := Validate(&cfg); err != nil {
		return cfg, err
	}
	return cfg, nil
}

// checkVersion reads only the version key, so a schema bump produces a clear
// message instead of a wall of unknown-field errors.
func checkVersion(raw []byte, path string) error {
	var probe struct {
		Version *int `yaml:"version"`
	}
	if err := yaml.Unmarshal(raw, &probe); err != nil {
		return fmt.Errorf("parsing %s:\n%s", path, yaml.FormatError(err, false, true))
	}
	if probe.Version == nil {
		return fmt.Errorf("%s: missing required key `version`; this release expects `version: %d`", path, SchemaVersion)
	}
	if *probe.Version != SchemaVersion {
		return fmt.Errorf("%s: unsupported config version %d; this release expects `version: %d`", path, *probe.Version, SchemaVersion)
	}
	return nil
}

func checkPermissions(path string) error {
	st, err := os.Stat(path)
	if err != nil {
		return err
	}
	if mode := st.Mode().Perm(); mode&0o077 != 0 {
		return fmt.Errorf("%w:\n  %s has mode %04o; run: chmod 600 %s",
			ErrPermissive, path, mode, path)
	}
	return nil
}

// ExpandPath expands a leading ~ and any $VAR references.
func ExpandPath(p string) string {
	if p == "" {
		return ""
	}
	p = os.ExpandEnv(p)
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			p = filepath.Join(home, strings.TrimPrefix(strings.TrimPrefix(p, "~"), "/"))
		}
	}
	return p
}

// StarterConfig is the commented file written by `lazymqtt config init`.
const StarterConfig = `# lazymqtt configuration
# Full reference: https://github.com/Onizuka893/lazymqtt/blob/main/docs/configuration.md
#
# lazymqtt only ever reads this file, so your comments and formatting are safe.
version: 1

defaults:
  client_id: "lazymqtt-{{hostname}}-{{pid}}"
  keepalive: 30s
  connect_timeout: 10s
  clean_start: true
  protocol: auto          # auto | 5 (MQTT 3.1.1 is not supported yet)
  subscriptions:
    - filter: "#"
      qos: 0

limits:
  max_topics: 5000        # LRU-evict the least recently updated beyond this
  per_topic_history: 50
  stream_history: 2000
  max_payload_bytes: 1048576
  ingest_buffer: 4096

ui:
  refresh_ms: 50
  timestamp_format: "15:04:05.000"
  theme: auto
  start_panel: topics

logging:
  level: warn             # debug | info | warn | error
  file: ""                # empty = no file logging; never stdout

brokers:
  local:
    host: localhost
    port: 1883

  # production:
  #   host: mqtt.example.com
  #   port: 8883
  #   protocol: 5
  #   tls:
  #     enabled: true
  #     ca_file: ~/.config/lazymqtt/certs/ca.pem
  #     server_name: mqtt.example.com
  #   username: example
  #   # Preferred: read the password from a secret manager at connect time.
  #   password_cmd: "pass show mqtt/production"
  #   subscriptions:
  #     - filter: "devices/+/state"
  #       qos: 1
`

// WriteStarter creates a commented starter config at path, mode 0600.
func WriteStarter(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		if errors.Is(err, fs.ErrExist) {
			return fmt.Errorf("%s already exists; not overwriting it", path)
		}
		return err
	}
	if _, err := f.WriteString(StarterConfig); err != nil {
		_ = f.Close()
		return err
	}
	// Report the close error: on a file we just wrote, that is where a failed
	// flush surfaces.
	return f.Close()
}
