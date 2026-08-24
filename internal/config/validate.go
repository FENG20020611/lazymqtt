package config

import (
	"fmt"
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Problems is an aggregated validation result. Every problem in the file is
// reported at once: fixing a config one error per run is miserable.
type Problems struct {
	Errors   []string
	Warnings []string
}

func (p *Problems) errorf(format string, a ...any) {
	p.Errors = append(p.Errors, fmt.Sprintf(format, a...))
}

func (p *Problems) warnf(format string, a ...any) {
	p.Warnings = append(p.Warnings, fmt.Sprintf(format, a...))
}

// Err returns a single error describing every problem, or nil.
func (p *Problems) Err() error {
	if len(p.Errors) == 0 {
		return nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d configuration problem(s):", len(p.Errors))
	for _, e := range p.Errors {
		b.WriteString("\n  - ")
		b.WriteString(e)
	}
	for _, w := range p.Warnings {
		b.WriteString("\n  ! ")
		b.WriteString(w)
	}
	return fmt.Errorf("%s", b.String())
}

// Validate performs the semantic checks that parsing cannot.
func Validate(cfg *Config) error {
	p := Check(cfg)
	return p.Err()
}

// Check runs validation and returns everything found, warnings included, so
// `config check` can print warnings even on an otherwise valid file.
func Check(cfg *Config) *Problems {
	p := &Problems{}

	if !validProtocol(cfg.Defaults.Protocol) {
		p.errorf("defaults.protocol: %q is not one of auto, 5, 3.1.1", cfg.Defaults.Protocol)
	}
	checkSubscriptions(p, "defaults", cfg.Defaults.Subscriptions)

	if cfg.Limits.MaxTopics != 0 && cfg.Limits.MaxTopics < 100 {
		p.errorf("limits.max_topics: %d is too low to be useful; use at least 100", cfg.Limits.MaxTopics)
	}
	if cfg.Limits.MaxPayloadBytes < 0 {
		p.errorf("limits.max_payload_bytes: must not be negative")
	}
	if cfg.Limits.IngestBuffer != 0 && cfg.Limits.IngestBuffer < 64 {
		p.errorf("limits.ingest_buffer: %d is too small; use at least 64", cfg.Limits.IngestBuffer)
	}
	if cfg.UI.RefreshMS != 0 && (cfg.UI.RefreshMS < 10 || cfg.UI.RefreshMS > 2000) {
		p.errorf("ui.refresh_ms: %d is outside the usable range 10-2000", cfg.UI.RefreshMS)
	}
	switch cfg.UI.Theme {
	case "", "auto", "dark", "light":
	default:
		p.errorf("ui.theme: %q is not one of auto, dark, light", cfg.UI.Theme)
	}
	switch cfg.UI.StartPanel {
	case "", "topics", "messages", "detail", "subscriptions":
	default:
		p.errorf("ui.start_panel: %q is not one of topics, messages, detail, subscriptions", cfg.UI.StartPanel)
	}
	switch strings.ToLower(cfg.Logging.Level) {
	case "", "debug", "info", "warn", "error":
	default:
		p.errorf("logging.level: %q is not one of debug, info, warn, error", cfg.Logging.Level)
	}

	for name, b := range cfg.Brokers {
		checkBroker(p, name, b)
	}
	return p
}

func checkBroker(p *Problems, name string, b Broker) {
	where := "brokers." + name

	if b.URL == "" && b.Host == "" {
		p.errorf("%s: needs either host or url", where)
	}
	if b.URL != "" && b.Host != "" {
		p.errorf("%s: set either url or host, not both", where)
	}
	if b.Port < 0 || b.Port > 65535 {
		p.errorf("%s.port: %d is not a valid port", where, b.Port)
	}
	if b.Protocol != "" && !validProtocol(b.Protocol) {
		p.errorf("%s.protocol: %q is not one of auto, 5, 3.1.1", where, b.Protocol)
	}
	if b.Scheme != "" && !validScheme(b.Scheme) {
		p.errorf("%s.scheme: %q is not one of tcp, tls, ssl, mqtt, mqtts, ws, wss", where, b.Scheme)
	}

	credentials := 0
	for _, set := range []bool{b.Password != "", b.PasswordEnv != "", b.PasswordCmd != ""} {
		if set {
			credentials++
		}
	}
	if credentials > 1 {
		p.errorf("%s: password, password_env and password_cmd are mutually exclusive; %d are set", where, credentials)
	}
	if b.Password != "" {
		p.warnf("%s: password is stored in plaintext; password_cmd is safer", where)
	}

	if b.TLS.CertFile != "" && b.TLS.KeyFile == "" {
		p.errorf("%s.tls: cert_file set without key_file", where)
	}
	if b.TLS.KeyFile != "" && b.TLS.CertFile == "" {
		p.errorf("%s.tls: key_file set without cert_file", where)
	}
	if !b.TLS.Enabled && (b.TLS.CAFile != "" || b.TLS.CertFile != "") {
		p.warnf("%s.tls: certificate paths are set but tls.enabled is false", where)
	}
	if !b.TLS.Enabled && (b.Port == 8883 || strings.HasPrefix(b.URL, "mqtts://") || strings.HasPrefix(b.URL, "tls://")) {
		p.warnf("%s: port 8883 is the TLS port but tls.enabled is false", where)
	}
	if b.TLS.InsecureSkipVerify {
		p.warnf("%s.tls: insecure_skip_verify is on; certificates will not be verified", where)
	}

	checkSubscriptions(p, where, b.Subscriptions)
}

func checkSubscriptions(p *Problems, where string, subs []Subscription) {
	for i, s := range subs {
		if err := mqtt.ValidateFilter(s.Filter); err != nil {
			p.errorf("%s.subscriptions[%d]: %v", where, i, err)
		}
		if s.QoS > 2 {
			p.errorf("%s.subscriptions[%d]: qos %d is not 0, 1 or 2", where, i, s.QoS)
		}
	}
}

func validProtocol(s string) bool {
	switch s {
	case "", "auto", "5", "5.0", "3.1.1", "311", "3":
		return true
	}
	return false
}

func validScheme(s string) bool {
	switch strings.ToLower(s) {
	case "tcp", "mqtt", "tls", "ssl", "mqtts", "ws", "wss":
		return true
	}
	return false
}
