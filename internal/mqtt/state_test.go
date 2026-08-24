package mqtt

import (
	"errors"
	"fmt"
	"net"
	"strings"
	"testing"
)

// A rejected credential must be terminal. autopaho will otherwise retry
// forever, hammering the broker with a password it has already been told is
// wrong.
func TestFatalClassifiesConnackReasonCodes(t *testing.T) {
	terminal := map[byte]string{
		ReasonClientIDNotValid:           "client id not valid",
		ReasonBadUserNameOrPassword:      "bad username or password",
		ReasonNotAuthorized:              "not authorized",
		ReasonBanned:                     "banned",
		ReasonUnsupportedProtocolVersion: "unsupported protocol version",
	}
	for code, name := range terminal {
		err := error(&ConnackError{ReasonCode: code})
		if !Fatal(err) {
			t.Errorf("%s (0x%02X) is not classified as terminal; it would be retried in a loop", name, code)
		}
		// Classification must survive wrapping, which is how it arrives.
		if !Fatal(fmt.Errorf("connecting: %w", err)) {
			t.Errorf("%s is not classified as terminal once wrapped", name)
		}
	}

	retryable := []byte{0x88 /* server unavailable */, 0x89 /* server busy */, 0x97 /* quota exceeded */}
	for _, code := range retryable {
		if Fatal(&ConnackError{ReasonCode: code}) {
			t.Errorf("reason 0x%02X should be retried, not treated as terminal", code)
		}
	}
}

func TestFatalLeavesNetworkErrorsRetryable(t *testing.T) {
	for _, err := range []error{
		nil,
		errors.New("dial tcp 127.0.0.1:1883: connect: connection refused"),
		&net.OpError{Op: "dial", Err: errors.New("no route to host")},
		&net.DNSError{Err: "no such host", IsNotFound: true},
	} {
		if Fatal(err) {
			t.Errorf("%v was classified as terminal; a network failure must be retried", err)
		}
	}
}

func TestConnackErrorMessageNamesTheReason(t *testing.T) {
	err := &ConnackError{ReasonCode: ReasonBadUserNameOrPassword, Reason: "check your credentials"}
	msg := err.Error()
	if !contains(msg, "bad user name or password") || !contains(msg, "check your credentials") {
		t.Fatalf("unhelpful error message: %q", msg)
	}
	inner := errors.New("underlying")
	if !errors.Is(&ConnackError{Err: inner}, inner) {
		t.Fatal("ConnackError does not unwrap")
	}
}

func contains(s, sub string) bool { return strings.Contains(s, sub) }
