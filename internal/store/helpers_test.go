package store_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

const testProvider = "streeteasy"

var baseTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

func newSource(providerID string) ingest.SourceProperty {
	return ingest.SourceProperty{
		Provider:     testProvider,
		ProviderID:   providerID,
		URL:          "https://streeteasy.com/sale/" + providerID,
		Raw:          json.RawMessage(`{"id":"` + providerID + `"}`),
		SourceStatus: "active",
		Property: domain.Property{
			Address: domain.Address{
				Street:       "123 Prospect Park West",
				Unit:         "4B",
				Neighborhood: "Park Slope",
				Zip:          "11215",
			},
			Geo:          &domain.GeoPoint{Latitude: 40.66, Longitude: -73.98},
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
			Description:  "sunny corner unit",
		},
		PhotoURLs: []string{
			"https://photos.example/" + providerID + "/1.jpg",
			"https://photos.example/" + providerID + "/2.jpg",
		},
	}
}

func startRun(t *testing.T, s *store.Store) domain.IngestRunID {
	t.Helper()
	run, err := s.CreateRun(context.Background(), testProvider, "scope-a", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return run.ID
}

// completeRun starts and finishes a clean run, the only kind allowed to advance
// lifecycle state.
func completeRun(t *testing.T, s *store.Store, listingsSeen int) domain.IngestRunID {
	t.Helper()
	id := startRun(t, s)
	finishRun(t, s, id, ingest.RunStats{Complete: true, ListingsSeen: listingsSeen})
	return id
}

func finishRun(t *testing.T, s *store.Store, id domain.IngestRunID, stats ingest.RunStats) {
	t.Helper()
	if err := s.FinishRun(context.Background(), id, stats); err != nil {
		t.Fatalf("finish run: %v", err)
	}
}

func apply(t *testing.T, s *store.Store, runID domain.IngestRunID, sp ingest.SourceProperty, at time.Time) ingest.ApplyResult {
	t.Helper()
	res, err := s.ApplySourceProperty(context.Background(), runID, sp, at)
	if err != nil {
		t.Fatalf("apply source property %s: %v", sp.ProviderID, err)
	}
	return res
}

func mustGet(t *testing.T, s *store.Store, id domain.PropertyID) (domain.Property, []domain.PriceEvent, []domain.Photo, store.ProvenanceSummary) {
	t.Helper()
	p, events, photos, prov, err := s.GetProperty(context.Background(), id)
	if err != nil {
		t.Fatalf("get property %d: %v", id, err)
	}
	return p, events, photos, prov
}

func candidateIDs(t *testing.T, s *store.Store, p domain.Profile) []domain.PropertyID {
	t.Helper()
	props, err := s.Candidates(context.Background(), p, 0)
	if err != nil {
		t.Fatalf("candidates: %v", err)
	}
	return idsOf(props)
}

func idsOf(props []domain.Property) []domain.PropertyID {
	out := make([]domain.PropertyID, 0, len(props))
	for _, p := range props {
		out = append(out, p.ID)
	}
	return out
}

func equalIDs(a, b []domain.PropertyID) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func money(v int64) *domain.Money {
	m := domain.Money(v)
	return &m
}
