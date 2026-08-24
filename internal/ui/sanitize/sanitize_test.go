package sanitize

import (
	"strings"
	"testing"
)

func TestStripsEscapeSequences(t *testing.T) {
	// A payload that would otherwise clear the screen and set the window title.
	evil := "\x1b[2J\x1b]0;pwned\x07ok"
	got := Block([]byte(evil))
	if strings.ContainsRune(got, 0x1B) {
		t.Fatalf("ESC survived sanitisation: %q", got)
	}
	if !strings.HasSuffix(got, "ok") {
		t.Fatalf("legitimate text was lost: %q", got)
	}
}

func TestStripsC0AndC1Controls(t *testing.T) {
	got := Block([]byte("a\x00b\x07c\u0085d"))
	for _, r := range got {
		if r < 0x20 && r != '\n' {
			t.Fatalf("C0 control survived: %q", got)
		}
		if r >= 0x80 && r <= 0x9F {
			t.Fatalf("C1 control survived: %q", got)
		}
	}
	if !strings.Contains(got, "a") || !strings.Contains(got, "d") {
		t.Fatalf("content lost: %q", got)
	}
}

func TestNewlineHandling(t *testing.T) {
	if got := Block([]byte("a\r\nb")); got != "a\nb" {
		t.Fatalf("Block(%q) = %q, want \"a\\nb\"", "a\r\nb", got)
	}
	if got := Line([]byte("a\nb"), 0); strings.Contains(got, "\n") {
		t.Fatalf("Line kept a newline: %q", got)
	}
}

func TestInvalidUTF8BecomesReplacement(t *testing.T) {
	got := Block([]byte{0xff, 0xfe, 'a'})
	if !strings.HasPrefix(got, Replacement+Replacement) || !strings.HasSuffix(got, "a") {
		t.Fatalf("invalid UTF-8 handling = %q", got)
	}
}

func TestWideAndEmojiCharactersSurvive(t *testing.T) {
	in := "温度 25°C \U0001F321️"
	got := Block([]byte(in))
	if !strings.Contains(got, "温度") || !strings.Contains(got, "\U0001F321") {
		t.Fatalf("wide characters were mangled: %q", got)
	}
}

func TestBidiOverridesAreNeutralised(t *testing.T) {
	// U+202E makes the rest of the line render right-to-left, so a topic can
	// be made to read as something it is not.
	if got := Topic("home/\u202egnp/x"); strings.ContainsRune(got, '\u202e') {
		t.Fatalf("bidi override survived: %q", got)
	}
}

func TestMaxRunesTruncates(t *testing.T) {
	got := []rune(Line([]byte(strings.Repeat("x", 100)), 10))
	if len(got) != 11 || got[10] != '…' {
		t.Fatalf("truncation = %q", string(got))
	}
}

func TestTabExpansion(t *testing.T) {
	if got := Bytes([]byte("a\tb"), Options{TabWidth: 4}); got != "a    b" {
		t.Fatalf("tab expansion = %q", got)
	}
}

func FuzzBytesProducesNoControlCharacters(f *testing.F) {
	f.Add([]byte("\x1b[31mred"))
	f.Add([]byte("normal"))
	f.Fuzz(func(t *testing.T, in []byte) {
		for _, r := range Block(in) {
			if r == 0x1B || (r < 0x20 && r != '\n') || (r >= 0x80 && r <= 0x9F) {
				t.Fatalf("sanitised output still holds %U", r)
			}
		}
	})
}
