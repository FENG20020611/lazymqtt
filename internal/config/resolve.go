package config

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// Resolved is everything the app needs to open one connection. It contains no
// file paths and no unresolved secrets: internal/mqtt never learns that a
// config file exists.
type Resolved struct {
	Name    string
	Options mqtt.Options
	Subs    []mqtt.Subscription
	// Insecure records that certificate verification is disabled, which the
	// UI turns into a persistent banner rather than a warning that scrolls
	// away.
	Insecure bool
}

// BrokerRef resolves a --broker argument, which may be a profile name or a
// bare URL. `lazymqtt -b tcp://10.0.0.5:1883` must need no setup at all.
func (c Config) BrokerRef(ref string) (Broker, error) {
	if ref == "" {
		if len(c.Brokers) == 1 {
			for name, b := range c.Brokers {
				b.Name = name
				return b, nil
			}
		}
		return Broker{}, fmt.Errorf("no broker specified")
	}
	if b, ok := c.Brokers[ref]; ok {
		b.Name = ref
		return b, nil
	}
	if strings.Contains(ref, "://") || strings.Contains(ref, ":") {
		return Broker{Name: ref, URL: normaliseURL(ref)}, nil
	}
	names := make([]string, 0, len(c.Brokers))
	for n := range c.Brokers {
		names = append(names, n)
	}
	if len(names) == 0 {
		return Broker{}, fmt.Errorf("unknown broker %q and no profiles are configured; pass a URL such as tcp://localhost:1883", ref)
	}
	return Broker{}, fmt.Errorf("unknown broker %q; configured profiles: %s", ref, strings.Join(names, ", "))
}

// Names returns the configured profile names in a stable order.
func (c Config) Names() []string {
	names := make([]string, 0, len(c.Brokers))
	for n := range c.Brokers {
		names = append(names, n)
	}
	sortStrings(names)
	return names
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j] < s[j-1]; j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// Resolve merges defaults into a broker, builds the TLS config, resolves the
// password and returns a ready-to-connect value.
func (c Config) Resolve(ctx context.Context, b Broker, prompt PromptFunc, overrideTopics []string) (Resolved, error) {
	serverURL, err := b.ServerURL()
	if err != nil {
		return Resolved{}, err
	}

	pass, err := ResolvePassword(ctx, b, prompt)
	if err != nil {
		return Resolved{}, err
	}

	tlsProfile := mqtt.TLSProfile{
		Enabled:            b.TLS.Enabled || isTLSScheme(serverURL),
		CAFile:             b.TLS.CAFile,
		CertFile:           b.TLS.CertFile,
		KeyFile:            b.TLS.KeyFile,
		ServerName:         b.TLS.ServerName,
		InsecureSkipVerify: b.TLS.InsecureSkipVerify,
	}
	if tlsProfile.Enabled && tlsProfile.ServerName == "" {
		if u, err := url.Parse(serverURL); err == nil {
			tlsProfile.ServerName = u.Hostname()
		}
	}
	tlsCfg, err := mqtt.BuildTLSConfig(tlsProfile)
	if err != nil {
		return Resolved{}, fmt.Errorf("brokers.%s: %w", b.Name, err)
	}

	opts := mqtt.Options{
		ServerURL:       serverURL,
		ClientID:        ExpandClientID(firstNonEmpty(b.ClientID, c.Defaults.ClientID)),
		Username:        b.Username,
		Password:        pass,
		KeepAlive:       derefDuration(b.KeepAlive, c.Defaults.KeepAlive),
		ConnectTimeout:  derefDuration(b.ConnectTimeout, c.Defaults.ConnectTimeout),
		CleanStart:      derefBool(b.CleanStart, c.Defaults.CleanStart),
		SessionExpiry:   b.SessionExpiry,
		TLS:             tlsCfg,
		MaxPayloadBytes: c.Limits.MaxPayloadBytes,
		IngestBuffer:    c.Limits.IngestBuffer,
		Protocol:        firstNonEmpty(b.Protocol, c.Defaults.Protocol, "auto"),
	}

	subCfg := b.Subscriptions
	if len(subCfg) == 0 {
		subCfg = c.Defaults.Subscriptions
	}
	var subs []mqtt.Subscription
	if len(overrideTopics) > 0 {
		for _, f := range overrideTopics {
			subs = append(subs, mqtt.Subscription{Filter: f})
		}
	} else {
		for _, s := range subCfg {
			subs = append(subs, mqtt.Subscription{Filter: s.Filter, QoS: s.QoS})
		}
	}

	return Resolved{
		Name:     b.Name,
		Options:  opts,
		Subs:     subs,
		Insecure: b.TLS.InsecureSkipVerify,
	}, nil
}

// ServerURL renders the broker address as a URL autopaho understands.
//
// The scheme is load-bearing, not cosmetic: autopaho decides whether to open a
// TLS connection by looking at it alone, and ignores the *tls.Config entirely
// on a plaintext scheme. So `tls.enabled: true` has to be reflected here or it
// silently does nothing.
func (b Broker) ServerURL() (string, error) {
	if b.URL != "" {
		u, err := url.Parse(normaliseURL(b.URL))
		if err != nil {
			return "", fmt.Errorf("brokers.%s.url: %w", b.Name, err)
		}
		if b.TLS.Enabled {
			u.Scheme = tlsScheme(u.Scheme)
		}
		if u.Port() == "" {
			u.Host = net.JoinHostPort(u.Hostname(), strconv.Itoa(defaultPort(u.Scheme)))
		}
		return u.String(), nil
	}
	scheme := strings.ToLower(b.Scheme)
	if scheme == "" {
		scheme = "tcp"
	}
	if b.TLS.Enabled {
		scheme = tlsScheme(scheme)
	}
	port := b.Port
	if port == 0 {
		port = defaultPort(scheme)
	}
	return scheme + "://" + net.JoinHostPort(b.Host, strconv.Itoa(port)), nil
}

// normaliseURL maps the user-facing schemes onto what autopaho accepts and
// supplies a scheme when the user typed a bare host:port.
func normaliseURL(raw string) string {
	if !strings.Contains(raw, "://") {
		raw = "tcp://" + raw
	}
	for from, to := range map[string]string{"mqtt://": "tcp://", "mqtts://": "tls://", "ssl://": "tls://"} {
		if strings.HasPrefix(raw, from) {
			return to + strings.TrimPrefix(raw, from)
		}
	}
	return raw
}

// tlsScheme upgrades a plaintext scheme to its TLS counterpart, leaving an
// already-encrypted scheme alone. A broker serving TLS on the conventionally
// plaintext port 1883 is unusual but entirely legal, and this is what makes
// `tls.enabled: true` mean what it says regardless of port or URL spelling.
func tlsScheme(scheme string) string {
	switch strings.ToLower(scheme) {
	case "tcp", "mqtt", "":
		return "tls"
	case "ws":
		return "wss"
	}
	return scheme
}

func isTLSScheme(u string) bool {
	return strings.HasPrefix(u, "tls://") || strings.HasPrefix(u, "wss://")
}

func defaultPort(scheme string) int {
	switch strings.ToLower(scheme) {
	case "tls", "ssl", "mqtts":
		return 8883
	case "ws":
		return 8083
	case "wss":
		return 8084
	}
	return 1883
}

// ExpandClientID substitutes the three supported template variables. Keeping
// this to a fixed three rather than a general template engine is deliberate.
func ExpandClientID(id string) string {
	if id == "" {
		id = "lazymqtt-{{hostname}}-{{pid}}"
	}
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "unknown"
	}
	if i := strings.IndexByte(host, '.'); i > 0 {
		host = host[:i]
	}
	r := strings.NewReplacer(
		"{{hostname}}", host,
		"{{pid}}", strconv.Itoa(os.Getpid()),
		"{{user}}", currentUser(),
	)
	out := r.Replace(id)
	// MQTT 3.1.1 brokers may reject client IDs longer than 23 bytes; 64 is a
	// safe ceiling for v5 and keeps the value readable in logs.
	if len(out) > 64 {
		out = out[:64]
	}
	return out
}

func currentUser() string {
	for _, k := range []string{"USER", "LOGNAME", "USERNAME"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return "user"
}
