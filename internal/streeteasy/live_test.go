package streeteasy

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

// Opt-in provider smoke test: STREETEASY_LIVE=1 go test ./internal/streeteasy -run TestLive
func TestLiveSalesSearch(t *testing.T) {
	liveSmoke(t, domain.ListingSale)
}

func TestLiveRentalsSearch(t *testing.T) {
	liveSmoke(t, domain.ListingRent)
}

func liveSmoke(t *testing.T, listingType domain.ListingType) {
	t.Helper()
	if os.Getenv("STREETEASY_LIVE") != "1" {
		t.Skip("set STREETEASY_LIVE=1 to run the live provider smoke test")
	}

	c := NewClient(&http.Client{Timeout: 30 * time.Second}, Config{
		PerPage: 5,
		Delay:   2 * time.Second,
	})

	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()

	var (
		count      int
		enriched   int
		priced     int
		itemErrors int
	)
	for sp, err := range c.Search(ctx, ingest.SearchQuery{
		ListingType: listingType,
		Areas:       []string{"322"}, // Cobble Hill: small enough for a smoke test
	}) {
		if err != nil {
			if sp.ProviderID == "" {
				t.Fatalf("provider-level failure: %v", err)
			}
			itemErrors++
			t.Logf("item error for %s: %v", sp.ProviderID, err)
		}
		if sp.ProviderID == "" || sp.URL == "" || len(sp.Raw) == 0 {
			t.Errorf("incomplete result: %+v", sp)
		}
		if sp.Property.ListingType != listingType {
			t.Errorf("listingType = %q, want %q", sp.Property.ListingType, listingType)
		}
		if sp.Property.Price != nil {
			priced++
		}
		if sp.Property.Description != "" {
			enriched++
		}
		count++
		if count >= 3 {
			break
		}
	}

	if count == 0 {
		t.Fatalf("live %s search returned no listings; provider shape may have drifted", listingType)
	}
	if priced == 0 {
		t.Errorf("no listing carried a price across %d results", count)
	}
	if enriched == 0 {
		t.Errorf("no listing was enriched with a description across %d results (%d item errors)", count, itemErrors)
	}
	t.Logf("live %s smoke: %d listings, %d priced, %d enriched, %d item errors", listingType, count, priced, enriched, itemErrors)
}
