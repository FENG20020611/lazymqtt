package panel

import (
	"strings"

	"github.com/Onizuka893/lazymqtt/internal/ui/theme"
)

// highlightJSON colours one line of already-indented JSON.
//
// It lexes a line at a time rather than the whole document for the same
// reason payloadLines windows the payload: the detail pane must cost what it
// displays, not what it holds. A 10 MB message shows forty lines, so forty
// lines is what gets tokenised.
//
// `inString` carries the one piece of state a line boundary can split: a
// string long enough that the soft-wrapper broke it across rows. JSON itself
// cannot contain a literal newline inside a string, so nothing else survives
// a line break and the lexer needs no other state.
func highlightJSON(t *theme.Palette, line string, inString bool) (string, bool) {
	if line == "" {
		return line, inString
	}
	var b strings.Builder
	b.Grow(len(line) * 2)

	i := 0
	if inString {
		end, closed := scanString(line, 0, true)
		b.WriteString(t.JSONString.Render(line[:end]))
		i = end
		if !closed {
			return b.String(), true
		}
	}

	for i < len(line) {
		c := line[i]
		switch {
		case c == '"':
			end, closed := scanString(line, i, false)
			style := t.JSONString
			// A string followed by a colon is a key. Colouring keys and
			// values alike is what makes a deep object unreadable, which is
			// the whole reason for pretty-printing it.
			if closed && isKeyPosition(line, end) {
				style = t.JSONKey
			}
			b.WriteString(style.Render(line[i:end]))
			i = end
			if !closed {
				return b.String(), true
			}

		case c == '-' || (c >= '0' && c <= '9'):
			end := i + 1
			for end < len(line) && isNumberByte(line[end]) {
				end++
			}
			b.WriteString(t.JSONNumber.Render(line[i:end]))
			i = end

		case strings.HasPrefix(line[i:], "true"):
			b.WriteString(t.JSONLiter.Render("true"))
			i += len("true")
		case strings.HasPrefix(line[i:], "false"):
			b.WriteString(t.JSONLiter.Render("false"))
			i += len("false")
		case strings.HasPrefix(line[i:], "null"):
			b.WriteString(t.JSONLiter.Render("null"))
			i += len("null")

		default:
			// Braces, brackets, commas, colons and the indent runs between
			// them. Emitted as one span so a 40-space indent is one Render
			// call rather than forty.
			end := i + 1
			for end < len(line) && isPunctByte(line[end]) {
				end++
			}
			b.WriteString(t.JSONPunct.Render(line[i:end]))
			i = end
		}
	}
	return b.String(), false
}

// scanString returns the index just past the closing quote, and whether one
// was found. When cont is true the scan starts inside a string that opened on
// an earlier line.
func scanString(s string, from int, cont bool) (int, bool) {
	i := from
	if !cont {
		i++ // the opening quote
	}
	for i < len(s) {
		switch s[i] {
		case '\\':
			i += 2 // an escape cannot be the last byte of valid JSON
			continue
		case '"':
			return i + 1, true
		}
		i++
	}
	return len(s), false
}

// isKeyPosition reports whether the next non-space byte after a string is a
// colon.
func isKeyPosition(s string, from int) bool {
	for i := from; i < len(s); i++ {
		switch s[i] {
		case ' ', '\t':
		case ':':
			return true
		default:
			return false
		}
	}
	return false
}

func isNumberByte(c byte) bool {
	return c >= '0' && c <= '9' || c == '.' || c == 'e' || c == 'E' || c == '+' || c == '-'
}

func isPunctByte(c byte) bool {
	switch {
	case c == '"', c == '-', c >= '0' && c <= '9':
		return false
	case c == 't' || c == 'f' || c == 'n':
		return false // possible start of true/false/null
	}
	return true
}
