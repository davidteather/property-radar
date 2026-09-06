package domain_test

import (
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

func TestParseListingType(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.ListingType
		wantErr bool
	}{
		{"sale", "sale", domain.ListingSale, false},
		{"rent", "rent", domain.ListingRent, false},
		{"uppercase", "SALE", domain.ListingSale, false},
		{"mixed case", "Rent", domain.ListingRent, false},
		{"whitespace", "  sale  ", domain.ListingSale, false},
		{"case and whitespace", "\tReNt\n", domain.ListingRent, false},
		{"empty", "", "", true},
		{"unknown", "lease", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseListingType(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseListingType(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseListingType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParsePropertyType(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.PropertyType
		wantErr bool
	}{
		{"coop", "coop", domain.PropertyCoop, false},
		{"condo", "condo", domain.PropertyCondo, false},
		{"townhouse", "townhouse", domain.PropertyTownhouse, false},
		{"house", "house", domain.PropertyHouse, false},
		{"other", "other", domain.PropertyOther, false},
		{"uppercase", "CONDO", domain.PropertyCondo, false},
		{"mixed case", "TownHouse", domain.PropertyTownhouse, false},
		{"whitespace", "  house  ", domain.PropertyHouse, false},
		{"empty", "", "", true},
		{"unknown", "mansion", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParsePropertyType(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParsePropertyType(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParsePropertyType(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestParseVerdictKind(t *testing.T) {
	tests := []struct {
		name    string
		in      string
		want    domain.VerdictKind
		wantErr bool
	}{
		{"love", "love", domain.VerdictLove, false},
		{"maybe", "maybe", domain.VerdictMaybe, false},
		{"dislike", "dislike", domain.VerdictDislike, false},
		{"uppercase", "LOVE", domain.VerdictLove, false},
		{"mixed case", "Maybe", domain.VerdictMaybe, false},
		{"whitespace", "  dislike  ", domain.VerdictDislike, false},
		{"empty", "", "", true},
		{"unknown", "hate", "", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := domain.ParseVerdictKind(tt.in)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ParseVerdictKind(%q) err = %v, wantErr %v", tt.in, err, tt.wantErr)
			}
			if got != tt.want {
				t.Fatalf("ParseVerdictKind(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
