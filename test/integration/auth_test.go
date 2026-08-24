//go:build integration

package integration

import (
	"testing"
	"time"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

func TestCorrectCredentialsConnect(t *testing.T) {
	url := requireBroker(t, brokerURL(envAuthBroker, defaultAuthBroker))
	opts := clientOptions(t, url)
	opts.Username = authUser
	opts.Password = []byte(authPass)

	c := newClient(t, opts)
	drainEvents(t, c)

	base := testTopic(t)
	subscribe(t, c, base+"/#", 0)
	publish(t, c, mqtt.PublishRequest{Topic: base + "/auth", Payload: []byte("ok")})
	awaitMessage(t, c, messageWait, func(m *mqtt.Message) bool { return m.Topic == base+"/auth" })
}

// A rejected password must land in StateFailed and stay there.
//
// autopaho retries a CONNACK denial on the same backoff loop it uses for a
// network blip, which means a typo'd password becomes an authentication
// attempt every few seconds forever — the client looks like a brute-force
// attempt and the user is told only "reconnecting".
func TestWrongPasswordFailsTerminallyAndDoesNotRetry(t *testing.T) {
	url := requireBroker(t, brokerURL(envAuthBroker, defaultAuthBroker))
	opts := clientOptions(t, url)
	opts.Username = authUser
	opts.Password = []byte("definitely-not-the-password")

	a := newAdapter(opts)
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := contextWithTimeout(connectWait)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect returned early: %v", err)
	}

	st := waitState(t, a, mqtt.StateFailed, connectWait)
	if st.Err == nil {
		t.Fatal("the failed state carries no error to show the user")
	}
	if !mqtt.Fatal(st.Err) {
		t.Errorf("an authentication rejection was classified as retryable: %v", st.Err)
	}

	// Nothing may move it off StateFailed afterwards. A later transition to
	// connecting or reconnecting is the retry loop this test exists to catch.
	deadline := time.After(8 * time.Second)
	for {
		select {
		case ev, ok := <-a.Events():
			if !ok {
				return
			}
			switch ev.Status.State {
			case mqtt.StateConnecting, mqtt.StateReconnecting, mqtt.StateConnected:
				t.Fatalf("after a rejected password the adapter moved to %v; it must stop", ev.Status.State)
			}
		case <-deadline:
			return
		}
	}
}

// Connecting anonymously to a broker that requires a password is the same
// terminal class of failure.
func TestMissingCredentialsFailTerminally(t *testing.T) {
	url := requireBroker(t, brokerURL(envAuthBroker, defaultAuthBroker))

	a := newAdapter(clientOptions(t, url))
	t.Cleanup(func() { _ = a.Close() })

	ctx, cancel := contextWithTimeout(connectWait)
	defer cancel()
	if err := a.Connect(ctx); err != nil {
		t.Fatalf("Connect returned early: %v", err)
	}
	st := waitState(t, a, mqtt.StateFailed, connectWait)
	if !mqtt.Fatal(st.Err) {
		t.Errorf("an anonymous rejection was classified as retryable: %v", st.Err)
	}
}
