// Package sanitize turns attacker-controllable bytes into something safe to
// write to a terminal.
//
// This is a security control, not cosmetics. An MQTT payload is written by
// whoever can publish to the broker and goes straight into a terminal. Raw
// escape sequences can reposition the cursor, rewrite the screen, change the
// window title and — on some terminals — trigger clipboard writes or response
// injection that ends up on the user's shell input.
//
// Sanitisation happens at the render boundary. The store keeps the raw bytes
// so copy and export remain faithful.
package sanitize

import (
	"strings"
	"unicode"
	"unicode/utf8"
)

// Placeholders for characters that are removed.
const (
	EscapeGlyph  = "␛" // ␛
	ControlGlyph = "·" // ·
	Replacement  = "�" // �
)

// Options tunes the transformation.
type Options struct {
	// KeepNewlines preserves \n (and converts \r\n to \n). The detail pane
	// wants this; a single-line list cell does not.
	KeepNewlines bool
	// TabWidth expands tabs to this many spaces. 0 uses 4.
	TabWidth int
	// MaxRunes truncates the result, appending an ellipsis. 0 means no limit.
	MaxRunes int
}

// String sanitises a UTF-8 string.
func String(s string, opt Options) string { return Bytes([]byte(s), opt) }

// Bytes sanitises arbitrary bytes for display.
//
// Invalid UTF-8 becomes U+FFFD, C0 and C1 control characters become a visible
// placeholder, and ESC never survives — so no escape sequence can be
// reconstructed downstream.
func Bytes(b []byte, opt Options) string {
	if opt.TabWidth <= 0 {
		opt.TabWidth = 4
	}
	var sb strings.Builder
	sb.Grow(len(b))

	runes := 0
	for i := 0; i < len(b); {
		r, size := utf8.DecodeRune(b[i:])
		if r == utf8.RuneError && size <= 1 {
			sb.WriteString(Replacement)
			i++
			runes++
			if opt.MaxRunes > 0 && runes >= opt.MaxRunes {
				sb.WriteString("…")
				return sb.String()
			}
			continue
		}
		i += size

		switch {
		case r == '\n' || r == '\r':
			if !opt.KeepNewlines {
				sb.WriteString(ControlGlyph)
				runes++
				break
			}
			if r == '\r' {
				// Swallow CR; a following LF supplies the break.
				break
			}
			sb.WriteByte('\n')
			runes++

		case r == '\t':
			for j := 0; j < opt.TabWidth; j++ {
				sb.WriteByte(' ')
			}
			runes += opt.TabWidth

		case r == 0x1B:
			// ESC is the gateway to every escape sequence. It never survives.
			sb.WriteString(EscapeGlyph)
			runes++

		case r < 0x20 || r == 0x7F:
			// Remaining C0 controls.
			sb.WriteString(ControlGlyph)
			runes++

		case r >= 0x80 && r <= 0x9F:
			// C1 controls, which some terminals accept as single-byte
			// equivalents of ESC sequences.
			sb.WriteString(ControlGlyph)
			runes++

		case isUnsafeFormatting(r):
			// Bidi overrides and zero-width characters can make a topic
			// render as something other than what it is.
			sb.WriteString(ControlGlyph)
			runes++

		default:
			sb.WriteRune(r)
			runes++
		}

		if opt.MaxRunes > 0 && runes >= opt.MaxRunes {
			sb.WriteString("…")
			return sb.String()
		}
	}
	return sb.String()
}

// isUnsafeFormatting reports whether r is a formatting character that can
// misrepresent the text around it: bidi overrides, zero-width joiners and
// the like. Emoji sequences pay a small price here, which is the right trade
// for a pane rendering untrusted input.
func isUnsafeFormatting(r rune) bool {
	// Unicode category Cf covers the bidi overrides (U+202A-202E, U+2066-2069),
	// the zero-width joiners and the byte-order mark in one predicate.
	return unicode.Is(unicode.Cf, r)
}

// Line sanitises a value for a single-line cell: no newlines, no tabs.
func Line(b []byte, maxRunes int) string {
	return Bytes(b, Options{KeepNewlines: false, TabWidth: 1, MaxRunes: maxRunes})
}

// Block sanitises a value for a multi-line pane.
func Block(b []byte) string {
	return Bytes(b, Options{KeepNewlines: true})
}

// Topic sanitises a topic name for display.
func Topic(s string) string {
	return String(s, Options{KeepNewlines: false, TabWidth: 1})
}
