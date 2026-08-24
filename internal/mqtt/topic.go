package mqtt

import (
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"
)

// MaxTopicBytes is the MQTT limit on an encoded topic name or filter.
const MaxTopicBytes = 65535

// Errors returned by ValidateFilter and ValidateTopic.
var (
	ErrEmptyFilter    = errors.New("topic filter is empty")
	ErrFilterTooLong  = errors.New("topic filter exceeds 65535 bytes")
	ErrInvalidUTF8    = errors.New("topic is not valid UTF-8")
	ErrNullCharacter  = errors.New("topic contains a null character")
	ErrWildcardInName = errors.New("topic name may not contain wildcards")
)

// SplitTopic splits a topic into its levels. MQTT levels may be empty, so
// this is a plain split and never collapses separators.
func SplitTopic(topic string) []string {
	return strings.Split(topic, "/")
}

// ValidateFilter checks a subscription filter against the MQTT 3.1.1/5.0
// rules: '#' is legal only as the entire final level, '+' only as an entire
// level. Empty levels are legal.
func ValidateFilter(filter string) error {
	if filter == "" {
		return ErrEmptyFilter
	}
	if len(filter) > MaxTopicBytes {
		return ErrFilterTooLong
	}
	if !utf8.ValidString(filter) {
		return ErrInvalidUTF8
	}
	if strings.ContainsRune(filter, 0) {
		return ErrNullCharacter
	}
	levels := SplitTopic(filter)
	for i, lv := range levels {
		switch {
		case lv == "#":
			if i != len(levels)-1 {
				return fmt.Errorf("'#' must be the last level in %q", filter)
			}
		case lv == "+":
			// fine anywhere
		case strings.ContainsAny(lv, "#+"):
			return fmt.Errorf("level %q in %q mixes a wildcard with other characters", lv, filter)
		}
	}
	return nil
}

// ValidateTopic checks a topic name for publishing: same as a filter, but no
// wildcards at all.
func ValidateTopic(topic string) error {
	if topic == "" {
		return ErrEmptyFilter
	}
	if len(topic) > MaxTopicBytes {
		return ErrFilterTooLong
	}
	if !utf8.ValidString(topic) {
		return ErrInvalidUTF8
	}
	if strings.ContainsRune(topic, 0) {
		return ErrNullCharacter
	}
	if strings.ContainsAny(topic, "#+") {
		return ErrWildcardInName
	}
	return nil
}

// MatchTopic reports whether topic matches filter.
//
// The rules, all of which are covered by the table test:
//   - "sport/#" matches "sport" (the parent level itself)
//   - "sport/+" does NOT match "sport"
//   - "+" and "#" at the FIRST level do not match topics beginning with '$'
//     (so "#" does not see $SYS)
func MatchTopic(filter, topic string) bool {
	if filter == "" || topic == "" {
		return false
	}
	if !strings.ContainsAny(filter, "#+") {
		return filter == topic
	}
	// $-prefixed topics are invisible to a leading wildcard.
	if strings.HasPrefix(topic, "$") {
		if filter[0] == '#' || filter[0] == '+' {
			return false
		}
	}
	f := SplitTopic(filter)
	t := SplitTopic(topic)

	for i, lv := range f {
		if lv == "#" {
			// "#" matches the parent level too, but only the remainder.
			return true
		}
		if i >= len(t) {
			return false
		}
		if lv != "+" && lv != t[i] {
			return false
		}
	}
	return len(f) == len(t)
}

// HasWildcard reports whether s contains a subscription wildcard.
func HasWildcard(s string) bool { return strings.ContainsAny(s, "#+") }
