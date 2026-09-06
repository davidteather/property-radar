package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/store"
)

func TestRenderDetail(t *testing.T) {
	at := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	p := sampleProperties()[0]
	p.Geo = &domain.GeoPoint{Latitude: 40.66, Longitude: -73.98}
	p.Description = "sunny corner unit"
	p.FirstSeen, p.LastSeen, p.MaterialChangedAt = at, at.Add(72*time.Hour), at.Add(48*time.Hour)

	d := detail{
		property: p,
		priceEvent: []domain.PriceEvent{
			{PropertyID: 12, Price: 950_000, ObservedAt: at},
			{PropertyID: 12, Price: 900_000, ObservedAt: at.Add(48 * time.Hour)},
		},
		photos: []domain.Photo{
			{Position: 0, SourceURL: "https://photos.example/1.jpg", CachedPath: "/thumbs/12/0.jpg", MIMEType: "image/jpeg", Width: 800, Height: 600},
			{Position: 1, SourceURL: "https://photos.example/2.jpg"},
		},
		provenance: store.ProvenanceSummary{
			Provider: "streeteasy", ProviderID: "abc123",
			URL: "https://streeteasy.com/sale/abc123", SourceStatus: "active",
			FirstSeenAt: at, LastSeenAt: at.Add(72 * time.Hour), FetchedAt: at.Add(72 * time.Hour),
			MissingRuns: 1,
		},
		verdicts: []domain.Verdict{
			{ID: 3, PropertyID: 12, Kind: domain.VerdictLove, Note: "great light", CreatedAt: at},
		},
	}

	var buf bytes.Buffer
	if err := renderDetail(&buf, d); err != nil {
		t.Fatalf("renderDetail: %v", err)
	}
	want := `Listing 12
  address           123 Prospect Park West #4B
  neighborhood      Park Slope
  zip               11215
  geo               40.66000, -73.98000
  listing type      sale
  property type     coop
  status            active
  price             $900,000
  currency          USD
  beds              2
  baths             1.5
  sqft              900
  maintenance       $1,200
  common charges    -
  taxes monthly     $300
  monthly carrying  $1,500
  days on market    10
  first seen        2026-08-01 12:00Z
  last seen         2026-08-04 12:00Z
  material changed  2026-08-03 12:00Z

Description
  sunny corner unit

Price history
  OBSERVED           PRICE     CHANGE
  2026-08-01 12:00Z  $950,000  -
  2026-08-03 12:00Z  $900,000  -$50,000

Provenance
  provider       streeteasy
  provider id    abc123
  url            https://streeteasy.com/sale/abc123
  source status  active
  first seen     2026-08-01 12:00Z
  last seen      2026-08-04 12:00Z
  fetched        2026-08-04 12:00Z
  missing runs   1

Photos
  POS  CACHED  DIMENSIONS  MIME        SOURCE
  0    yes     800x600     image/jpeg  https://photos.example/1.jpg
  1    no      -           -           https://photos.example/2.jpg

Verdicts
  ID  RECORDED           VERDICT  NOTE
  3   2026-08-01 12:00Z  love     great light
`
	if got := buf.String(); got != want {
		t.Errorf("renderDetail mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderDetailEmptySections(t *testing.T) {
	var buf bytes.Buffer
	if err := renderDetail(&buf, detail{property: domain.Property{ID: 5}}); err != nil {
		t.Fatalf("renderDetail: %v", err)
	}
	got := buf.String()
	for _, want := range []string{
		"no recorded price changes", "no photos", "no verdicts recorded", "currency          USD",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("renderDetail missing %q in:\n%s", want, got)
		}
	}
}
