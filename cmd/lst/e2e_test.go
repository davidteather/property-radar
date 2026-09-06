package main

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

// Exercises the store-to-table path end to end: seeded listings, a price cut,
// a cached photo, a verdict, and one suspect run.
func TestCommandsAgainstSeededStore(t *testing.T) {
	requirePostgres(t)
	truncateTables(t, "shown", "profile", "rubric_versions", "verdicts", "listing_photos",
		"price_events", "listing_fields", "listing_sources", "ingest_runs", "listing_current", "listings")

	ctx := context.Background()
	s := store.New(pgtest.Pool(t))
	run, err := s.CreateRun(ctx, "streeteasy", "sale-brooklyn-2bd-1a2b3c4d5e", false)
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 8, 18, 9, 0, 0, 0, time.UTC)

	seed := []ingest.SourceProperty{
		{
			Provider: "streeteasy", ProviderID: "1234567", URL: "https://streeteasy.com/building/x/4b",
			Raw: json.RawMessage(`{"id":"1234567"}`), SourceStatus: "active",
			Property: domain.Property{
				Address:     domain.Address{Street: "123 Prospect Park West", Unit: "4B", Neighborhood: "Park Slope", Zip: "11215"},
				Geo:         &domain.GeoPoint{Latitude: 40.6669, Longitude: -73.9787},
				ListingType: domain.ListingSale, PropertyType: domain.PropertyCoop, Status: domain.StatusActive,
				Price: ptr.To(domain.Money(950_000)), Bedrooms: ptr.To(2), Bathrooms: ptr.To(1.5),
				Sqft: ptr.To(1050), Maintenance: ptr.To(domain.Money(1287)), TaxesMonthly: ptr.To(domain.Money(212)),
				DaysOnMarket: ptr.To(14), Description: "Sunny corner two-bedroom with pre-war details and park views.",
			},
			PhotoURLs: []string{"https://photos.example/1234567/1.jpg", "https://photos.example/1234567/2.jpg"},
		},
		{
			Provider: "streeteasy", ProviderID: "7654321", URL: "https://streeteasy.com/building/y/12c",
			Raw: json.RawMessage(`{"id":"7654321"}`), SourceStatus: "active",
			Property: domain.Property{
				Address:     domain.Address{Street: "88 Fort Greene Place", Unit: "12C", Neighborhood: "Fort Greene", Zip: "11217"},
				ListingType: domain.ListingSale, PropertyType: domain.PropertyCondo, Status: domain.StatusActive,
				Price: ptr.To(domain.Money(1_395_000)), Bedrooms: ptr.To(3), Bathrooms: ptr.To(2.0),
				CommonCharges: ptr.To(domain.Money(1104)), TaxesMonthly: ptr.To(domain.Money(640)),
				DaysOnMarket: ptr.To(3), Description: "New development condo, high floor.",
			},
			PhotoURLs: []string{"https://photos.example/7654321/1.jpg"},
		},
		{
			Provider: "streeteasy", ProviderID: "5551212", URL: "https://streeteasy.com/building/z/2r",
			Raw: json.RawMessage(`{"id":"5551212"}`), SourceStatus: "active",
			Property: domain.Property{
				Address:     domain.Address{Street: "5 Berkeley Place", Neighborhood: "Park Slope"},
				ListingType: domain.ListingSale, Status: domain.StatusActive,
			},
		},
	}
	for _, sp := range seed {
		if _, err := s.ApplySourceProperty(ctx, run.ID, sp, at); err != nil {
			t.Fatal(err)
		}
	}
	// A price cut on the first listing, so show has history with a delta.
	dropped := seed[0]
	dropped.Property.Price = ptr.To(domain.Money(899_000))
	dropped.Property.DaysOnMarket = ptr.To(18)
	if _, err := s.ApplySourceProperty(ctx, run.ID, dropped, at.Add(96*time.Hour)); err != nil {
		t.Fatal(err)
	}
	if err := s.SetPhotoCache(ctx, 1, 0, "https://photos.example/1234567/1.jpg", "/thumbs/1/0.jpg", "image/jpeg", 800, 533); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(ctx, 1, domain.VerdictLove, "the park views sell it"); err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, run.ID, ingest.RunStats{
		Complete: true, ListingsSeen: 412, Created: 3, Updated: 1, PhotoFailures: 2,
		ItemErrors: []domain.RunError{{ProviderID: "9990001", Message: "missing price"}},
	}); err != nil {
		t.Fatal(err)
	}
	older, err := s.CreateRun(ctx, "streeteasy", "sale-brooklyn-2bd-1a2b3c4d5e", false)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.FinishRun(ctx, older.ID, ingest.RunStats{Complete: false, ListingsSeen: 87, Suspect: true}); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		args    []string
		want    []string
		notWant []string
	}{
		{
			name: "list",
			args: []string{"list"},
			want: []string{
				"1   123 Prospect Park West #4B  $899,000",
				"2/1.5  1050  coop   $1,499  18   Park Slope    active",
				"2   88 Fort Greene Place #12C   $1,395,000",
				"3   5 Berkeley Place            -           -/-    -     -      -       -    Park Slope",
			},
		},
		{
			name:    "filtered list",
			args:    []string{"list", "--max-price", "1000000", "--min-beds", "2", "--neighborhood", "Park Slope", "--type", "coop"},
			want:    []string{"123 Prospect Park West #4B"},
			notWant: []string{"Fort Greene", "5 Berkeley Place"},
		},
		{
			name: "show",
			args: []string{"show", "1"},
			want: []string{
				"monthly carrying  $1,499",
				"2026-08-18 09:00Z  $950,000  -",
				"2026-08-22 09:00Z  $899,000  -$51,000",
				"provider id    1234567",
				"0    yes     800x533     image/jpeg",
				"1    no      -           -",
				"love     the park views sell it",
			},
		},
		{
			name: "status",
			args: []string{"status"},
			want: []string{
				"412", "active listings                3",
				"runs are suspect", "runs finished incomplete",
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			out, err := runCommand(t, tc.args...)
			if err != nil {
				t.Fatalf("lst %v: %v", tc.args, err)
			}
			for _, want := range tc.want {
				if !strings.Contains(out, want) {
					t.Errorf("output missing %q:\n%s", want, out)
				}
			}
			for _, notWant := range tc.notWant {
				if strings.Contains(out, notWant) {
					t.Errorf("output should not contain %q:\n%s", notWant, out)
				}
			}
		})
	}
}
