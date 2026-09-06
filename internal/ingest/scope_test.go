package ingest_test

import (
	"regexp"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

func TestScopeHashIsStableAndOrderIndependent(t *testing.T) {
	base := ingest.SearchQuery{
		ListingType: domain.ListingSale,
		MaxPrice:    2_000_000,
		MinBeds:     2,
		Areas:       []string{"319", "305", "364"},
	}
	want := ingest.ScopeHash("streeteasy", base)

	require.Regexp(t, regexp.MustCompile(`^[0-9a-f]{64}$`), want)
	assert.Equal(t, want, ingest.ScopeHash("streeteasy", base), "same query hashes the same twice")

	reordered := base
	reordered.Areas = []string{"364", " 305 ", "319", "319", ""}
	assert.Equal(t, want, ingest.ScopeHash("streeteasy", reordered), "order, whitespace and duplicates do not matter")

	empty := base
	empty.ListingType = ""
	assert.Equal(t, want, ingest.ScopeHash("streeteasy", empty), "empty listing type defaults to sale")
}

func TestScopeHashSeparatesScopes(t *testing.T) {
	base := ingest.SearchQuery{ListingType: domain.ListingSale, MaxPrice: 2_000_000, MinBeds: 2, Areas: []string{"319"}}
	want := ingest.ScopeHash("streeteasy", base)

	different := map[string]ingest.SearchQuery{
		"other areas":  {ListingType: domain.ListingSale, MaxPrice: 2_000_000, MinBeds: 2, Areas: []string{"135"}},
		"extra area":   {ListingType: domain.ListingSale, MaxPrice: 2_000_000, MinBeds: 2, Areas: []string{"319", "135"}},
		"other price":  {ListingType: domain.ListingSale, MaxPrice: 1_500_000, MinBeds: 2, Areas: []string{"319"}},
		"other beds":   {ListingType: domain.ListingSale, MaxPrice: 2_000_000, MinBeds: 3, Areas: []string{"319"}},
		"rental scope": {ListingType: domain.ListingRent, MaxPrice: 2_000_000, MinBeds: 2, Areas: []string{"319"}},
	}
	for name, q := range different {
		t.Run(name, func(t *testing.T) {
			assert.NotEqual(t, want, ingest.ScopeHash("streeteasy", q))
		})
	}

	assert.NotEqual(t, want, ingest.ScopeHash("zillow", base), "provider is part of the scope")
}
