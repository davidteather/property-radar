package xslices_test

import (
	"slices"
	"testing"

	"github.com/davidteather/property-radar/internal/shared/xslices"
)

func TestMap(t *testing.T) {
	if xslices.Map(nil, func(v int) int { return v }) != nil {
		t.Fatal("Map(nil) should be nil")
	}
	if xslices.Map([]int{}, func(v int) int { return v }) != nil {
		t.Fatal("Map(empty) should be nil")
	}
	got := xslices.Map([]int{1, 2, 3}, func(v int) string { return string(rune('a' + v - 1)) })
	if !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("Map = %v", got)
	}
}

func TestSplitCSV(t *testing.T) {
	if xslices.SplitCSV("") != nil || xslices.SplitCSV(" , ,") != nil {
		t.Fatal("SplitCSV of blanks should be nil")
	}
	if got := xslices.SplitCSV(" a, b ,,c "); !slices.Equal(got, []string{"a", "b", "c"}) {
		t.Fatalf("SplitCSV = %v", got)
	}
}
