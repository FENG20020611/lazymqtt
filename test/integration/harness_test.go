//go:build integration

// Package integration exercises the MQTT adapter against a real broker.
//
// Everything here is behind the `integration` build tag and is skipped, not
// failed, when no broker is reachable: a contributor running `go test ./...`
// on a laptop with no Docker should see green, and CI decides whether the
// integration matrix is mandatory.
//
// Run with:
//
//	docker compose -f deploy/docker-compose.yml up -d
//	go test -tags=integration -race ./test/...
package integration

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
	"github.com/Onizuka893/lazymqtt/internal/mqtt/paho5"
)

// Environment overrides, so the suite can be pointed at any broker.
const (
	envBroker     = "MQTT_BROKER_URL"
	envAuthBroker = "MQTT_AUTH_BROKER_URL"
	envTLSBroker  = "MQTT_TLS_BROKER_URL"
	envMTLSBroker = "MQTT_MTLS_BROKER_URL"
)

// The dev compose stack. Credentials for the auth broker are fixed by
// deploy/docker-compose.yml and are not secrets.
const (
	defaultBroker     = "tcp://localhost:1883"
	defaultAuthBroker = "tcp://localhost:1884"
	defaultTLSBroker  = "tls://localhost:8883"
	defaultMTLSBroker = "tls://localhost:8884"

	authUser = "lazymqtt"
	authPass = "secret"
)

// Timeouts. Generous enough for a loaded CI runner, short enough that a real
// hang fails the run rather than eating the whole job.
const (
	connectWait = 20 * time.Second
	messageWait = 15 * time.Second
	settleWait  = 500 * time.Millisecond
)

func brokerURL(env, fallback string) string {
	if v := os.Getenv(env); v != "" {
		return v
	}
	return fallback
}

// requireBroker skips the test unless something is listening. A skip here
// means "no broker", never "the broker is broken": every real failure below
// this point is a genuine one.
func requireBroker(t *testing.T, raw string) string {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("bad broker url %q: %v", raw, err)
	}
	conn, err := net.DialTimeout("tcp", u.Host, 2*time.Second)
	if err != nil {
		t.Skipf("no broker at %s (%v); run: docker compose -f deploy/docker-compose.yml up -d", raw, err)
	}
	_ = conn.Close()
	return raw
}

// clientOptions builds a connection configuration with a unique client ID, so
// two tests running in parallel never take each other's session over.
func clientOptions(t *testing.T, serverURL string) mqtt.Options {
	t.Helper()
	return mqtt.Options{
		ServerURL:       serverURL,
		ClientID:        "lazymqtt-it-" + randomSuffix(t),
		KeepAlive:       10 * time.Second,
		ConnectTimeout:  10 * time.Second,
		CleanStart:      true,
		MaxPayloadBytes: 1 << 20,
		IngestBuffer:    64 << 10,
		Protocol:        "5",
	}
}

// newAdapter builds an adapter without connecting it, for tests that drive
// the lifecycle themselves.
func newAdapter(opts mqtt.Options) *paho5.Adapter { return paho5.New(opts, nil) }

func contextWithTimeout(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), d)
}

// goroutines reports the current goroutine count, for the leak checks.
func goroutines() int { return runtime.NumGoroutine() }

// settle gives background goroutines a chance to finish before counting them.
// This is unavoidably a heuristic: goleak's retry loop does the same thing.
func settle() {
	for i := 0; i < 10; i++ {
		runtime.GC()
		time.Sleep(100 * time.Millisecond)
	}
}

// newClient connects an adapter and registers its teardown.
func newClient(t *testing.T, opts mqtt.Options) *paho5.Adapter {
	t.Helper()
	a := paho5.New(opts, nil)
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), connectWait)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect(%s): %v", opts.ServerURL, err)
	}
	waitState(t, a, mqtt.StateConnected, connectWait)
	return a
}

// waitState blocks until the connection reaches want, draining events. It
// reports every state seen on the way so a failure says what actually
// happened rather than only that it timed out.
func waitState(t *testing.T, c mqtt.Client, want mqtt.ConnState, timeout time.Duration) mqtt.ConnStatus {
	t.Helper()
	deadline := time.After(timeout)
	var seen []mqtt.ConnState
	for {
		select {
		case ev, ok := <-c.Events():
			if !ok {
				t.Fatalf("the event channel closed while waiting for %v (saw %v)", want, seen)
			}
			seen = append(seen, ev.Status.State)
			if ev.Status.State == want {
				return ev.Status
			}
			if ev.Status.State == mqtt.StateFailed && want != mqtt.StateFailed {
				t.Fatalf("connection failed while waiting for %v: %v", want, ev.Status.Err)
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for %v; states seen: %v", timeout, want, seen)
		}
	}
}

// drainEvents consumes lifecycle events in the background so the adapter's
// 16-slot event channel cannot back up during a long publish loop.
func drainEvents(t *testing.T, c mqtt.Client) {
	t.Helper()
	done := make(chan struct{})
	t.Cleanup(func() { close(done) })
	go func() {
		for {
			select {
			case <-done:
				return
			case _, ok := <-c.Events():
				if !ok {
					return
				}
			}
		}
	}()
}

// awaitMessage waits for a message satisfying match.
func awaitMessage(t *testing.T, c mqtt.Client, timeout time.Duration, match func(*mqtt.Message) bool) *mqtt.Message {
	t.Helper()
	deadline := time.After(timeout)
	for {
		select {
		case m, ok := <-c.Messages():
			if !ok {
				t.Fatal("the message channel closed before a matching message arrived")
			}
			if match == nil || match(m) {
				return m
			}
		case <-deadline:
			t.Fatalf("timed out after %s waiting for a matching message", timeout)
			return nil
		}
	}
}

func subscribe(t *testing.T, c mqtt.Client, filter string, qos byte) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Subscribe(ctx, []mqtt.Subscription{{Filter: filter, QoS: qos}}); err != nil {
		t.Fatalf("Subscribe(%q): %v", filter, err)
	}
	// The SUBACK is asynchronous; a publish issued immediately after
	// Subscribe returns can beat it and the message is then never delivered.
	time.Sleep(settleWait)
}

func publish(t *testing.T, c mqtt.Client, req mqtt.PublishRequest) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := c.Publish(ctx, req); err != nil {
		t.Fatalf("Publish(%q): %v", req.Topic, err)
	}
}

// testTopic returns a prefix unique to this test run, so a retained message
// left behind by an earlier run cannot make a later one pass or fail.
func testTopic(t *testing.T) string {
	t.Helper()
	return "lazymqtt-it/" + randomSuffix(t)
}

func randomSuffix(t *testing.T) string {
	t.Helper()
	var b [6]byte
	if _, err := rand.Read(b[:]); err != nil {
		t.Fatal(err)
	}
	return hex.EncodeToString(b[:])
}

// clearRetained publishes a zero-length retained message, which is how MQTT
// deletes one. Retained state outlives the test process, so tests that set it
// must clean up after themselves.
func clearRetained(t *testing.T, c mqtt.Client, topics ...string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	for _, tp := range topics {
		_ = c.Publish(ctx, mqtt.PublishRequest{Topic: tp, Payload: nil, Retain: true})
	}
}

// repoRoot locates the module root by walking up to go.mod, so cert paths and
// compose files resolve regardless of where `go test` was invoked.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("could not find the module root")
		}
		dir = parent
	}
}

// composeFile is the dev stack, used by the reconnect test.
func composeFile(t *testing.T) string {
	t.Helper()
	return filepath.Join(repoRoot(t), "deploy", "docker-compose.yml")
}

// dockerCompose runs a compose subcommand, skipping the test when Docker is
// not usable. Only the reconnect test needs this.
func dockerCompose(t *testing.T, args ...string) {
	t.Helper()
	if _, err := exec.LookPath("docker"); err != nil {
		t.Skip("docker is not installed; the reconnect test needs to restart the broker")
	}
	full := append([]string{"compose", "-f", composeFile(t)}, args...)
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "docker", full...).CombinedOutput()
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			t.Fatalf("docker compose %v timed out:\n%s", args, out)
		}
		t.Skipf("docker compose %v failed (%v); is the daemon running?\n%s", args, err, out)
	}
}
