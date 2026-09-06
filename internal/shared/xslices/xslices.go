// Package xslices extends the standard slices package with transformations
// shared across the application's conversion boundaries.
package xslices

import "strings"

func Map[T, U any](in []T, f func(T) U) []U {
	if len(in) == 0 {
		return nil
	}
	out := make([]U, 0, len(in))
	for _, v := range in {
		out = append(out, f(v))
	}
	return out
}

// SplitCSV splits a comma-separated value into trimmed, non-empty entries; nil
// when none remain.
func SplitCSV(v string) []string {
	var out []string
	for part := range strings.SplitSeq(v, ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}
	return out
}
