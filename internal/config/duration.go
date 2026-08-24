package config

import (
	"fmt"
	"strings"
	"time"
)

// Duration is a time.Duration that unmarshals from a YAML string such as
// "30s" or "1m". Durations are strings and never bare integers: `keepalive:
// 30` is ambiguous, `30s` is not.
type Duration time.Duration

// D returns the underlying time.Duration.
func (d Duration) D() time.Duration { return time.Duration(d) }

// String renders in the same form it parses.
func (d Duration) String() string { return time.Duration(d).String() }

// UnmarshalYAML implements yaml.BytesUnmarshaler via the interface goccy
// looks for on scalar nodes.
func (d *Duration) UnmarshalYAML(b []byte) error {
	s := strings.TrimSpace(string(b))
	if len(s) >= 2 && (s[0] == '"' || s[0] == '\'') && s[len(s)-1] == s[0] {
		s = s[1 : len(s)-1]
	}
	v, err := time.ParseDuration(s)
	if err != nil {
		return fmt.Errorf("invalid duration %q: durations are strings like \"30s\" or \"1m\"", s)
	}
	*d = Duration(v)
	return nil
}

// MarshalYAML writes the duration back out as a string.
func (d Duration) MarshalYAML() ([]byte, error) {
	return []byte(time.Duration(d).String()), nil
}

func derefDuration(vals ...*Duration) time.Duration {
	for _, v := range vals {
		if v != nil {
			return time.Duration(*v)
		}
	}
	return 0
}

func derefBool(vals ...*bool) bool {
	for _, v := range vals {
		if v != nil {
			return *v
		}
	}
	return false
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
