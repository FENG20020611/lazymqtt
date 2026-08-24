//go:build integration

package integration

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// certDir holds the development CA produced by `make certs`.
func certDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(repoRoot(t), "deploy", "certs")
	if _, err := os.Stat(filepath.Join(dir, "ca.pem")); err != nil {
		t.Skip("no dev certificates; run: make certs")
	}
	return dir
}

func tlsOptions(t *testing.T, serverURL string, profile mqtt.TLSProfile) mqtt.Options {
	t.Helper()
	cfg, err := mqtt.BuildTLSConfig(profile)
	if err != nil {
		t.Fatalf("BuildTLSConfig: %v", err)
	}
	opts := clientOptions(t, serverURL)
	opts.TLS = cfg
	return opts
}

func TestTLSWithTheDevCA(t *testing.T) {
	dir := certDir(t)
	url := requireBroker(t, brokerURL(envTLSBroker, defaultTLSBroker))

	c := newClient(t, tlsOptions(t, url, mqtt.TLSProfile{
		Enabled:    true,
		CAFile:     filepath.Join(dir, "ca.pem"),
		ServerName: "localhost",
	}))
	drainEvents(t, c)

	base := testTopic(t)
	subscribe(t, c, base+"/#", 0)
	publish(t, c, mqtt.PublishRequest{Topic: base + "/tls", Payload: []byte("encrypted")})

	got := awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == base+"/tls"
	})
	if string(got.Payload) != "encrypted" {
		t.Errorf("payload = %q", got.Payload)
	}
}

func TestMutualTLSWithAClientCertificate(t *testing.T) {
	dir := certDir(t)
	url := requireBroker(t, brokerURL(envMTLSBroker, defaultMTLSBroker))

	c := newClient(t, tlsOptions(t, url, mqtt.TLSProfile{
		Enabled:    true,
		CAFile:     filepath.Join(dir, "ca.pem"),
		CertFile:   filepath.Join(dir, "client.pem"),
		KeyFile:    filepath.Join(dir, "client-key.pem"),
		ServerName: "localhost",
	}))
	drainEvents(t, c)

	base := testTopic(t)
	subscribe(t, c, base+"/#", 0)
	publish(t, c, mqtt.PublishRequest{Topic: base + "/mtls", Payload: []byte("mutual")})

	got := awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool {
		return m.Topic == base+"/mtls"
	})
	if string(got.Payload) != "mutual" {
		t.Errorf("payload = %q", got.Payload)
	}
}

// An untrusted certificate must fail, and fail terminally. A TLS verification
// error retried on a backoff loop is a client that never tells you your CA is
// wrong.
func TestUntrustedCertificateFailsTerminally(t *testing.T) {
	certDir(t) // the TLS listener needs certs even though we do not trust them
	url := requireBroker(t, brokerURL(envTLSBroker, defaultTLSBroker))

	a := newAdapter(tlsOptions(t, url, mqtt.TLSProfile{
		Enabled:    true,
		ServerName: "localhost",
		// No CA file: the dev CA is self-signed, so the system pool rejects it.
	}))
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := contextWithTimeout(connectWait)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		// Failing synchronously is also acceptable, as long as it is fatal.
		if !mqtt.Fatal(err) {
			t.Errorf("Connect failed with a retryable error: %v", err)
		}
		return
	}
	st := waitState(t, a, mqtt.StateFailed, connectWait)
	if st.Err == nil {
		t.Fatal("the failed state carries no error")
	}
	if !mqtt.Fatal(st.Err) {
		t.Errorf("a certificate verification failure was classified as retryable: %v", st.Err)
	}
}
