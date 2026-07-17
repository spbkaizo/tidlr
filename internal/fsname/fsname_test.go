package fsname

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestSanitizeReplacesIllegalChars(t *testing.T) {
	got := Sanitize("AC/DC: Back<in>Black?")
	want := "AC-DC- BackinBlack"
	if got != want {
		t.Errorf("Sanitize() = %q, want %q", got, want)
	}
}

func TestSanitizeEmptyBecomesUnknown(t *testing.T) {
	for _, in := range []string{"", "   ", "???", "..."} {
		if got := Sanitize(in); got != "Unknown" {
			t.Errorf("Sanitize(%q) = %q, want %q", in, got, "Unknown")
		}
	}
}

// The 100 gecs case: a title whose bytes far exceed the filesystem limit.
func TestSanitizeCapsLongUnicodeNames(t *testing.T) {
	long := strings.Repeat("ʅ͡͡͡(̸̢̛̼̞̭͋ͅ)̸͚̰͛̔̾̀̿͒͂ ࿃ूੂ✧⃛ ⃝͢ ∷፨◉☼⃝◞⊖◟☼⃝", 40)
	if len(long) <= MaxComponent {
		t.Fatalf("test input is only %d bytes; not exercising the cap", len(long))
	}

	got := Sanitize(long)
	if len(got) > MaxComponent {
		t.Errorf("Sanitize() returned %d bytes, want <= %d", len(got), MaxComponent)
	}
	if !utf8.ValidString(got) {
		t.Errorf("Sanitize() produced invalid UTF-8: %q", got)
	}
}

// Truncation must never cut a multi-byte rune in half.
func TestSanitizeTruncatesOnRuneBoundary(t *testing.T) {
	// 3 bytes per rune: a cap that is not a multiple of 3 forces a mid-rune cut.
	for n := MaxComponent; n < MaxComponent+3; n++ {
		in := strings.Repeat("あ", n)
		got := Sanitize(in)
		if !utf8.ValidString(got) {
			t.Errorf("Sanitize(%d runes) produced invalid UTF-8", n)
		}
		if len(got) > MaxComponent {
			t.Errorf("Sanitize(%d runes) = %d bytes, want <= %d", n, len(got), MaxComponent)
		}
	}
}

// A sanitized base plus the suffixes the download path appends must still fit
// inside the real 255-byte filesystem limit.
func TestSanitizeLeavesRoomForSuffixes(t *testing.T) {
	const fsLimit = 255
	base := Sanitize(strings.Repeat("x", 500))
	for _, suffix := range []string{".raw.flac", ".dl-1938039338", ".m4a"} {
		if n := len(base + suffix); n > fsLimit {
			t.Errorf("base+%q = %d bytes, exceeds filesystem limit %d", suffix, n, fsLimit)
		}
	}
}

func TestSanitizeKeepsShortNamesIntact(t *testing.T) {
	for _, in := range []string{"Yard Act", "Nia Archives", "Emotional Junglist"} {
		if got := Sanitize(in); got != in {
			t.Errorf("Sanitize(%q) = %q, want unchanged", in, got)
		}
	}
}
