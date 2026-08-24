package mqtt

import (
	"errors"
	"fmt"
	"time"
)

// ConnState is the coarse connection state shown in the UI.
type ConnState int

const (
	StateDisconnected ConnState = iota
	StateConnecting
	StateConnected
	StateReconnecting
	// StateFailed is terminal: auth rejected, bad TLS. Do not auto-retry.
	StateFailed
)

func (s ConnState) String() string {
	switch s {
	case StateDisconnected:
		return "disconnected"
	case StateConnecting:
		return "connecting"
	case StateConnected:
		return "connected"
	case StateReconnecting:
		return "reconnecting"
	case StateFailed:
		return "failed"
	}
	return "unknown"
}

// ConnStatus is the full connection snapshot carried on every Event.
type ConnStatus struct {
	State          ConnState
	Broker         string
	ClientID       string
	Since          time.Time
	Attempt        int       // reconnect attempt counter
	NextRetryAt    time.Time // drives the "reconnecting in 4s" countdown
	Err            error
	SessionPresent bool
	ProtoVersion   string // "5.0" / "3.1.1", resolved after CONNACK
}

// CONNACK reason codes we classify on.
const (
	ReasonUnsupportedProtocolVersion = 0x84
	ReasonClientIDNotValid           = 0x85
	ReasonBadUserNameOrPassword      = 0x86
	ReasonNotAuthorized              = 0x87
	ReasonBanned                     = 0x8A
	ReasonUseAnotherServer           = 0x9C
	ReasonServerMoved                = 0x9D
)

// ErrProtocolUnsupported signals that the broker rejected the MQTT 5 CONNECT
// and the caller may retry with 3.1.1 when protocol is "auto".
var ErrProtocolUnsupported = errors.New("broker does not support the requested protocol version")

// ConnackError reports that the broker denied the connection in its CONNACK.
//
// Adapters translate their client library's own denial type into this one, so
// the reason-code classification below stays in the port and the port stays
// free of any dependency on a particular MQTT library.
type ConnackError struct {
	ReasonCode byte
	Reason     string
	Err        error
}

func (e *ConnackError) Error() string {
	msg := "connection refused: " + ReasonText(e.ReasonCode)
	if e.Reason != "" {
		msg += " (" + e.Reason + ")"
	}
	return msg
}

// Unwrap exposes the underlying transport error, if any.
func (e *ConnackError) Unwrap() error { return e.Err }

// Code returns the CONNACK reason code.
func (e *ConnackError) Code() byte { return e.ReasonCode }

// reasonCoder is satisfied by *ConnackError and by any adapter error that
// chooses to expose a reason code directly.
type reasonCoder interface{ Code() byte }

// Fatal reports whether a connection failure is terminal — that is, whether
// retrying it in a loop can only ever hammer the broker.
//
// autopaho will happily retry an authentication rejection forever. Classify
// the reason code and stop.
func Fatal(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, ErrProtocolUnsupported) {
		return true
	}
	var rc reasonCoder
	if errors.As(err, &rc) {
		switch rc.Code() {
		case ReasonClientIDNotValid, ReasonBadUserNameOrPassword,
			ReasonNotAuthorized, ReasonBanned:
			return true
		case ReasonUnsupportedProtocolVersion:
			return true
		}
	}
	return isTLSVerificationError(err)
}

// ReasonText renders a CONNACK/DISCONNECT reason code for display.
func ReasonText(code byte) string {
	switch code {
	case 0x00:
		return "success"
	case 0x80:
		return "unspecified error"
	case 0x81:
		return "malformed packet"
	case 0x82:
		return "protocol error"
	case 0x83:
		return "implementation specific error"
	case ReasonUnsupportedProtocolVersion:
		return "unsupported protocol version"
	case ReasonClientIDNotValid:
		return "client identifier not valid"
	case ReasonBadUserNameOrPassword:
		return "bad user name or password"
	case ReasonNotAuthorized:
		return "not authorized"
	case 0x88:
		return "server unavailable"
	case 0x89:
		return "server busy"
	case ReasonBanned:
		return "banned"
	case 0x8B:
		return "server shutting down"
	case 0x8D:
		return "keep alive timeout"
	case 0x8E:
		return "session taken over"
	case 0x8F:
		return "topic filter invalid"
	case 0x90:
		return "topic name invalid"
	case 0x95:
		return "packet too large"
	case 0x97:
		return "quota exceeded"
	case 0x9A:
		return "retain not supported"
	case 0x9B:
		return "qos not supported"
	case ReasonUseAnotherServer:
		return "use another server"
	case ReasonServerMoved:
		return "server moved"
	case 0x9F:
		return "connection rate exceeded"
	}
	return fmt.Sprintf("reason 0x%02X", code)
}
