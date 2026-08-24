package panel

import (
	"strings"
	"testing"

	"github.com/Onizuka893/lazymqtt/internal/ui/theme"
)

// The point of highlighting is that a key and a string value are told apart,
// so those two must not share a colour.
func TestKeysAndStringValuesDiffer(t *testing.T) {
	got, _ := highlightJSON(theme.Dark, `  "name": "value",`, false)
	key, _ := highlightJSON(theme.Dark, `  "name": 1,`, false)

	if !strings.Contains(got, theme.Dark.JSONKey.Render(`"name"`)) {
		t.Errorf("the key is not coloured as a key: %q", got)
	}
	if !strings.Contains(got, theme.Dark.JSONString.Render(`"value"`)) {
		t.Errorf("the string value is not coloured as a string: %q", got)
	}
	if !strings.Contains(key, theme.Dark.JSONNumber.Render("1")) {
		t.Errorf("the number is not coloured as a number: %q", key)
	}
}

func TestLiteralsAreColoured(t *testing.T) {
	for _, lit := range []string{"true", "false", "null"} {
		got, _ := highlightJSON(theme.Dark, `  "k": `+lit, false)
		if !strings.Contains(got, theme.Dark.JSONLiter.Render(lit)) {
			t.Errorf("%s is not coloured as a literal: %q", lit, got)
		}
	}
}

// Highlighting must never change the text, only its styling: the pane has
// already truncated each line to the panel width, and adding or dropping a
// character there would overflow the box.
func TestHighlightingPreservesTheText(t *testing.T) {
	lines := []string{
		`{`,
		`  "a": "x",`,
		`  "b": [1, 2.5e3, -7],`,
		`  "c": {"d": null},`,
		`  "esc": "a \" b \\",`,
		`}`,
		``,
		`   `,
		`"unterminated`,
	}
	for _, l := range lines {
		got, _ := highlightJSON(theme.Dark, l, false)
		if plain := stripANSI(got); plain != l {
			t.Errorf("highlight(%q) = %q, want the text unchanged", l, plain)
		}
	}
}

// A soft-wrapped string spans rows. The lexer carries that one bit of state,
// so the continuation is still coloured as a string rather than parsed as
// fresh punctuation.
func TestStringsCarryAcrossWrappedRows(t *testing.T) {
	first, open := highlightJSON(theme.Dark, `  "k": "the quick brown`, false)
	if !open {
		t.Fatalf("an unterminated string should leave the lexer open: %q", first)
	}
	second, stillOpen := highlightJSON(theme.Dark, `fox: 1, 2, 3"`, true)
	if stillOpen {
		t.Error("the closing quote should close the string")
	}
	if !strings.Contains(second, theme.Dark.JSONString.Render(`fox: 1, 2, 3"`)) {
		t.Errorf("the continuation is not coloured as one string: %q", second)
	}
}

func stripANSI(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++ // the 'm'
			continue
		}
		b.WriteByte(s[i])
		i++
	}
	return b.String()
}
