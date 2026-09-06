package main

import (
	"bytes"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

func money(v int64) *domain.Money {
	m := domain.Money(v)
	return &m
}

func TestFormatDollars(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "$0"},
		{7, "$7"},
		{999, "$999"},
		{1000, "$1,000"},
		{900000, "$900,000"},
		{1234567, "$1,234,567"},
		{-25000, "-$25,000"},
	}
	for _, tc := range tests {
		if got := formatDollars(tc.in); got != tc.want {
			t.Errorf("formatDollars(%d) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestFormatUnknownValues(t *testing.T) {
	if got := formatMoney(nil); got != unknown {
		t.Errorf("formatMoney(nil) = %q, want %q", got, unknown)
	}
	if got := formatInt(nil); got != unknown {
		t.Errorf("formatInt(nil) = %q, want %q", got, unknown)
	}
	if got := formatBaths(nil); got != unknown {
		t.Errorf("formatBaths(nil) = %q, want %q", got, unknown)
	}
	if got := formatText("   "); got != unknown {
		t.Errorf("formatText(blank) = %q, want %q", got, unknown)
	}
	if got := formatTime(time.Time{}); got != unknown {
		t.Errorf("formatTime(zero) = %q, want %q", got, unknown)
	}
	if got := formatGeo(nil); got != unknown {
		t.Errorf("formatGeo(nil) = %q, want %q", got, unknown)
	}
	if got := formatDimensions(0, 600); got != unknown {
		t.Errorf("formatDimensions(0,600) = %q, want %q", got, unknown)
	}
}

func TestFormatBedsBaths(t *testing.T) {
	tests := []struct {
		beds  *int
		baths *float64
		want  string
	}{
		{ptr.To(2), ptr.To(1.5), "2/1.5"},
		{ptr.To(0), ptr.To(1.0), "0/1"},
		{nil, ptr.To(2.0), "-/2"},
		{ptr.To(3), nil, "3/-"},
	}
	for _, tc := range tests {
		if got := formatBedsBaths(tc.beds, tc.baths); got != tc.want {
			t.Errorf("formatBedsBaths(%v, %v) = %q, want %q", tc.beds, tc.baths, got, tc.want)
		}
	}
}

func TestFormatDuration(t *testing.T) {
	start := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	if got := formatDuration(start, nil); got != "running" {
		t.Errorf("unfinished run = %q, want running", got)
	}
	end := start.Add(92*time.Second + 400*time.Millisecond)
	if got := formatDuration(start, &end); got != "1m32s" {
		t.Errorf("finished run = %q, want 1m32s", got)
	}
}

func TestStreetLineAndTruncate(t *testing.T) {
	addr := domain.Address{Street: "123 Prospect Park West", Unit: "4B", Neighborhood: "Park Slope"}
	if got, want := streetLine(addr), "123 Prospect Park West #4B"; got != want {
		t.Errorf("streetLine = %q, want %q", got, want)
	}
	if got, want := truncate("abcdefghij", 5), "abcd…"; got != want {
		t.Errorf("truncate = %q, want %q", got, want)
	}
	if got, want := truncate("abc", 5), "abc"; got != want {
		t.Errorf("truncate short = %q, want %q", got, want)
	}
}

func TestOneLine(t *testing.T) {
	if got, want := oneLine("failed:\n  a\n  b"), "failed: a b"; got != want {
		t.Errorf("oneLine = %q, want %q", got, want)
	}
}

func sampleProperties() []domain.Property {
	return []domain.Property{
		{
			ID: 12,
			Address: domain.Address{
				Street: "123 Prospect Park West", Unit: "4B",
				Neighborhood: "Park Slope", Zip: "11215",
			},
			ListingType:  domain.ListingSale,
			PropertyType: domain.PropertyCoop,
			Status:       domain.StatusActive,
			Price:        money(900_000),
			Bedrooms:     ptr.To(2),
			Bathrooms:    ptr.To(1.5),
			Sqft:         ptr.To(900),
			Maintenance:  money(1200),
			TaxesMonthly: money(300),
			DaysOnMarket: ptr.To(10),
		},
		{
			ID:          13,
			Address:     domain.Address{Street: "5 Berkeley Place"},
			ListingType: domain.ListingSale,
			Status:      domain.StatusActive,
		},
	}
}

func TestRenderListTable(t *testing.T) {
	var buf bytes.Buffer
	if err := renderList(&buf, sampleProperties()); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	want := `ID  ADDRESS                     PRICE     BD/BA  SQFT  TYPE  CARRY   DOM  NEIGHBORHOOD  STATUS
12  123 Prospect Park West #4B  $900,000  2/1.5  900   coop  $1,500  10   Park Slope    active
13  5 Berkeley Place            -         -/-    -     -     -       -    -             active
`
	if got := buf.String(); got != want {
		t.Errorf("renderList mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderListEmpty(t *testing.T) {
	var buf bytes.Buffer
	if err := renderList(&buf, nil); err != nil {
		t.Fatalf("renderList: %v", err)
	}
	if got, want := buf.String(), "no active listings matched\n"; got != want {
		t.Errorf("renderList empty = %q, want %q", got, want)
	}
}
