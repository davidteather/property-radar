package listings

import (
	"strings"
	"unicode/utf8"
)

// ExcerptRunes caps the compact description shown in candidate/search rows.
const ExcerptRunes = 200

// Excerpt collapses whitespace and truncates on a word boundary, counting runes so multi-byte text is not cut mid-character.
func Excerpt(s string, limit int) string {
	s = strings.Join(strings.Fields(s), " ")
	if limit <= 0 || utf8.RuneCountInString(s) <= limit {
		return s
	}
	runes := []rune(s)
	cut := string(runes[:limit])
	if i := strings.LastIndexByte(cut, ' '); i > 0 && runes[limit] != ' ' {
		cut = cut[:i]
	}
	return strings.TrimRight(cut, " ,.;:!?-") + "…"
}
