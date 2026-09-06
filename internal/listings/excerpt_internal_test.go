package listings

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestExcerptTruncatesOnWordBoundaries(t *testing.T) {
	for _, tc := range []struct {
		name  string
		in    string
		limit int
		want  string
	}{
		{"under the limit is untouched", "sunny corner unit", 200, "sunny corner unit"},
		{"exactly at the limit is untouched", "abcde", 5, "abcde"},
		{"collapses whitespace", "sunny\n\tcorner   unit\n", 200, "sunny corner unit"},
		{"cuts at the last space", "alpha bravo charlie delta", 20, "alpha bravo charlie…"},
		{"a word ending exactly at the limit is kept", "alpha bravo charlie delta", 19, "alpha bravo charlie…"},
		{"drops trailing punctuation", "alpha bravo, charlie", 13, "alpha bravo…"},
		{"hard cuts a single long word", "abcdefghijklmnop", 5, "abcde…"},
		{"empty stays empty", "   ", 200, ""},
		{"non-positive limit is a no-op", "alpha bravo", 0, "alpha bravo"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := Excerpt(tc.in, tc.limit); got != tc.want {
				t.Fatalf("Excerpt(%q, %d) = %q, want %q", tc.in, tc.limit, got, tc.want)
			}
		})
	}
}

func TestExcerptCountsRunesNotBytes(t *testing.T) {
	// Multi-byte throughout: a byte-based cut would split a character.
	const in = "Charmante rénovation près du café; très ensoleillée façade sud"
	got := Excerpt(in, 20)

	if !utf8.ValidString(got) {
		t.Fatalf("Excerpt produced invalid utf-8: %q", got)
	}
	if !strings.HasSuffix(got, "…") {
		t.Fatalf("Excerpt(%q) = %q, want it truncated", in, got)
	}
	if n := utf8.RuneCountInString(strings.TrimSuffix(got, "…")); n > 20 {
		t.Fatalf("Excerpt kept %d runes, want at most 20", n)
	}
	if !strings.HasPrefix(in, strings.TrimSuffix(got, "…")) {
		t.Fatalf("Excerpt %q is not a prefix of the input", got)
	}

	cjk := strings.Repeat("日", 30)
	if got := Excerpt(cjk, 10); got != strings.Repeat("日", 10)+"…" {
		t.Fatalf("Excerpt of space-free text = %q", got)
	}
}
