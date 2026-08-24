package paho5

import (
	"errors"
	"fmt"
	"testing"

	"github.com/eclipse/paho.golang/autopaho"

	"github.com/Onizuka893/lazymqtt/internal/mqtt"
)

// autopaho carries the CONNACK reason as a struct field, not a method, so the
// adapter has to translate it before the port can classify it. Without this
// step a wrong password is retried forever.
func TestConnackErrorIsTranslatedAndClassifiedAsFatal(t *testing.T) {
	src := error(&autopaho.ConnackError{
		ReasonCode: mqtt.ReasonBadUserNameOrPassword,
		Reason:     "denied",
	})
	got := translateConnackError(fmt.Errorf("connecting: %w", src))

	var ce *mqtt.ConnackError
	if !errors.As(got, &ce) {
		t.Fatalf("autopaho denial was not translated: %T", got)
	}
	if ce.ReasonCode != mqtt.ReasonBadUserNameOrPassword {
		t.Fatalf("reason code = 0x%02X", ce.ReasonCode)
	}
	if !mqtt.Fatal(got) {
		t.Fatal("a translated auth rejection is still not terminal")
	}
}

func TestNonConnackErrorsPassThroughUntouched(t *testing.T) {
	src := errors.New("dial tcp: connection refused")
	if got := translateConnackError(src); got != src {
		t.Fatalf("a network error was rewritten: %v", got)
	}
	if mqtt.Fatal(src) {
		t.Fatal("a network error must stay retryable")
	}
}
