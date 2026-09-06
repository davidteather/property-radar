package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/store"
)

func TestEnrichmentStateRoundTrip(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := completeRun(t, s, 1)

	// Unknown listing: zero state, no error.
	state, err := s.EnrichmentState(ctx, testProvider, "nope")
	if err != nil {
		t.Fatalf("unknown listing: %v", err)
	}
	if state.Exists {
		t.Fatalf("unknown listing state = %+v, want Exists=false", state)
	}

	sp := newSource("2001")
	if _, err := s.ApplySourceProperty(ctx, runID, sp, baseTime); err != nil {
		t.Fatalf("apply: %v", err)
	}
	state, err = s.EnrichmentState(ctx, testProvider, "2001")
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if !state.Exists || state.Status != domain.StatusActive {
		t.Fatalf("state = %+v", state)
	}
	if state.Price == nil || *state.Price != *sp.Property.Price {
		t.Fatalf("state price = %v, want %v", state.Price, sp.Property.Price)
	}
	if state.HasDescription != (sp.Property.Description != "") {
		t.Fatalf("has_description = %v for description %q", state.HasDescription, sp.Property.Description)
	}
}

// A search-only (unenriched) apply must never erase what a deep pass stored:
// description and the detail-only carrying costs keep their values.
func TestSearchOnlyApplyKeepsEnrichedFields(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := completeRun(t, s, 1)

	enriched := newSource("2002")
	enriched.Property.Description = "Sunny corner two-bedroom with oversized windows."
	enriched.Property.Maintenance = money(1_200)
	enriched.Property.DaysOnMarket = intp(30)
	if _, err := s.ApplySourceProperty(ctx, runID, enriched, baseTime); err != nil {
		t.Fatalf("apply enriched: %v", err)
	}

	searchOnly := newSource("2002")
	searchOnly.Property.Description = ""
	searchOnly.Property.Maintenance = nil
	searchOnly.Property.DaysOnMarket = nil
	applied, err := s.ApplySourceProperty(ctx, runID, searchOnly, baseTime.Add(1))
	if err != nil {
		t.Fatalf("apply search-only: %v", err)
	}

	got, _, _, _, err := s.GetProperty(ctx, applied.PropertyID)
	if err != nil {
		t.Fatalf("get property: %v", err)
	}
	if got.Description == "" {
		t.Fatal("search-only apply erased the description")
	}
	if got.Maintenance == nil || *got.Maintenance != 1_200 {
		t.Fatalf("maintenance = %v, want kept 1200", got.Maintenance)
	}
	if got.DaysOnMarket == nil || *got.DaysOnMarket != 30 {
		t.Fatalf("dom = %v, want kept 30", got.DaysOnMarket)
	}

	state, err := s.EnrichmentState(ctx, testProvider, "2002")
	if err != nil || !state.HasDescription {
		t.Fatalf("state after search-only apply = %+v, %v; want HasDescription", state, err)
	}
}

func intp(v int) *int { return &v }

// An observation without a price keeps the last known one: the listing stays
// searchable by price, no phantom price event is recorded when the same price
// returns, and a drop across the gap is still material.
func TestPricelessApplyKeepsLastKnownPrice(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := completeRun(t, s, 1)

	priced := newSource("2003")
	priced.Property.Price = money(900_000)
	applied, err := s.ApplySourceProperty(ctx, runID, priced, baseTime)
	if err != nil {
		t.Fatalf("apply priced: %v", err)
	}
	if _, err := s.MarkShown(ctx, []domain.PropertyID{applied.PropertyID}, nil); err != nil {
		t.Fatalf("mark shown: %v", err)
	}

	priceless := newSource("2003")
	priceless.Property.Price = nil
	if _, err := s.ApplySourceProperty(ctx, runID, priceless, baseTime.Add(time.Hour)); err != nil {
		t.Fatalf("apply priceless: %v", err)
	}
	got, _, _, _, err := s.GetProperty(ctx, applied.PropertyID)
	if err != nil {
		t.Fatalf("get property: %v", err)
	}
	if got.Price == nil || *got.Price != 900_000 {
		t.Fatalf("price after priceless apply = %v, want kept 900000", got.Price)
	}

	same := newSource("2003")
	same.Property.Price = money(900_000)
	if _, err := s.ApplySourceProperty(ctx, runID, same, baseTime.Add(2*time.Hour)); err != nil {
		t.Fatalf("apply same price: %v", err)
	}
	if _, events, _, _, err := s.GetProperty(ctx, applied.PropertyID); err != nil || len(events) != 1 {
		t.Fatalf("price events after the price returned unchanged = %d, %v; want 1", len(events), err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); len(got) != 0 {
		t.Fatalf("unchanged price resurfaced the listing: %v", got)
	}

	drop := newSource("2003")
	drop.Property.Price = money(850_000)
	if _, err := s.ApplySourceProperty(ctx, runID, drop, baseTime.Add(3*time.Hour)); err != nil {
		t.Fatalf("apply drop: %v", err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); !equalIDs(got, []domain.PropertyID{applied.PropertyID}) {
		t.Fatalf("drop after a priceless observation did not resurface the listing: %v", got)
	}
}

// Absence must be scope-scoped: a run for one crawl scope (say, a narrow
// filtered one-off) may never age listings that a different scope tracks —
// otherwise queueing "Manhattan under 1M" would mass-delist Brooklyn.
func TestMarkMissingSourcesIsScopeScoped(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	scopedRun := func(scope string, seen ...string) domain.IngestRunID {
		t.Helper()
		return runAs(t, s, scope, false, seen...)
	}

	scopedRun("scope-brooklyn", "3001") // Brooklyn scope tracks 3001
	runOther := scopedRun("scope-manhattan-filtered", "3002")

	// The filtered scope's run saw only its own listing; the Brooklyn listing
	// must not be advanced by it.
	res, err := s.MarkMissingSources(ctx, testProvider, []string{"3002"}, runOther)
	if err != nil {
		t.Fatalf("mark missing (other scope): %v", err)
	}
	if res.Advanced != 0 || res.Delisted != 0 {
		t.Fatalf("cross-scope advancement = %+v, want none", res)
	}

	// The Brooklyn scope's own empty runs still age and eventually delist it.
	for i, want := range []int{1, 2} {
		runID := scopedRun("scope-brooklyn")
		res, err := s.MarkMissingSources(ctx, testProvider, []string{}, runID)
		if err != nil {
			t.Fatalf("mark missing (own scope, pass %d): %v", i+1, err)
		}
		if res.Advanced != 1 {
			t.Fatalf("own-scope pass %d advanced = %d, want 1", i+1, res.Advanced)
		}
		if want == 2 && res.Delisted != 1 {
			t.Fatalf("own-scope pass %d delisted = %d, want 1", i+1, res.Delisted)
		}
	}
}

// The reverse guarantee: an overlapping one-off (request_crawl with a price
// cap) that re-observes a standing scope's listing must not take absence
// ownership, or that listing could never be delisted by the scope tracking it.
func TestOneOffRunDoesNotStealAbsenceOwnership(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	runAs(t, s, "scope-standing", false, "3001")
	runAs(t, s, "scope-once-filtered", true, "3001")

	for i := range 2 {
		runID := runAs(t, s, "scope-standing", false)
		res, err := s.MarkMissingSources(ctx, testProvider, []string{}, runID)
		if err != nil {
			t.Fatalf("mark missing pass %d: %v", i+1, err)
		}
		if res.Advanced != 1 {
			t.Fatalf("pass %d advanced = %d, want 1 (the one-off must not own 3001)", i+1, res.Advanced)
		}
	}

	// A listing first found by a one-off is adopted by the next standing run
	// that sees it, so it does not stay un-delistable forever.
	runAs(t, s, "scope-once-filtered", true, "3002")
	runAs(t, s, "scope-standing", false, "3002")
	runID := runAs(t, s, "scope-standing", false)
	res, err := s.MarkMissingSources(ctx, testProvider, []string{}, runID)
	if err != nil {
		t.Fatalf("mark missing after adoption: %v", err)
	}
	if res.Advanced != 1 {
		t.Fatalf("advanced = %d, want 1 (standing scope adopted 3002)", res.Advanced)
	}
}

// A listing first seen by a narrow one-off is aged by a standing scope that
// covers it (every area, same listing type, filters at least as wide), so it
// does not linger forever. Narrower, other-type or one-off runs never age it.
func TestStandingSupersetScopeAgesOneOffListings(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	sale := func(oneOff bool, maxPrice int64, areas ...string) ingest.SearchQuery {
		return ingest.SearchQuery{ListingType: domain.ListingSale, MaxPrice: domain.Money(maxPrice), Areas: areas, OneOff: oneOff}
	}
	var listing domain.PropertyID
	runQuery := func(q ingest.SearchQuery, seen ...string) domain.IngestRunID {
		t.Helper()
		run, err := s.StartRun(ctx, testProvider, q)
		if err != nil {
			t.Fatalf("start run: %v", err)
		}
		for _, id := range seen {
			listing = apply(t, s, run.ID, newSource(id), baseTime).PropertyID
		}
		finishRun(t, s, run.ID, ingest.RunStats{Complete: true, ListingsSeen: len(seen)})
		return run.ID
	}
	advanced := func(q ingest.SearchQuery) int {
		t.Helper()
		res, err := s.MarkMissingSources(ctx, testProvider, []string{}, runQuery(q))
		if err != nil {
			t.Fatalf("mark missing: %v", err)
		}
		return res.Advanced
	}

	runQuery(sale(true, 500_000, "305"), "4001")
	for name, q := range map[string]ingest.SearchQuery{
		"narrower areas":       sale(false, 0, "306"),
		"stricter price":       sale(false, 400_000, "305", "306"),
		"other listing type":   {ListingType: domain.ListingRent, Areas: []string{"305", "306"}},
		"one-off superset":     sale(true, 0, "305", "306"),
		"tighter min bedrooms": {ListingType: domain.ListingSale, MinBeds: 2, Areas: []string{"305"}},
	} {
		if n := advanced(q); n != 0 {
			t.Fatalf("%s run advanced %d listings, want 0", name, n)
		}
	}
	if n := advanced(sale(false, 0, "305", "306")); n != 1 {
		t.Fatalf("superset standing run advanced %d, want 1 (the one-off's listing)", n)
	}
	if n := advanced(sale(false, 600_000, "305")); n != 1 {
		t.Fatalf("same-area wider-price standing run advanced %d, want 1", n)
	}
	if p, _, _, _ := mustGet(t, s, listing); p.Status != domain.StatusDelisted {
		t.Fatalf("status after two superset absences = %s, want delisted", p.Status)
	}
}

// enriched_at is stamped only by an apply that carried the detail page and is
// kept across later list-only observations, so a deep pass can resume.
func TestEnrichmentStateTracksEnrichedAt(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := completeRun(t, s, 1)

	sp := newSource("5001")
	apply(t, s, runID, sp, baseTime)
	if state, _ := s.EnrichmentState(ctx, testProvider, "5001"); state.EnrichedAt != nil {
		t.Fatalf("list-only apply stamped enriched_at: %v", state.EnrichedAt)
	}
	sp.Enriched = true
	apply(t, s, runID, sp, baseTime.Add(time.Hour))
	state, err := s.EnrichmentState(ctx, testProvider, "5001")
	if err != nil || state.EnrichedAt == nil || !state.EnrichedAt.Equal(baseTime.Add(time.Hour)) {
		t.Fatalf("enriched_at after detail apply = %v, %v; want %v", state.EnrichedAt, err, baseTime.Add(time.Hour))
	}
	sp.Enriched = false
	apply(t, s, runID, sp, baseTime.Add(2*time.Hour))
	if state, _ = s.EnrichmentState(ctx, testProvider, "5001"); state.EnrichedAt == nil || !state.EnrichedAt.Equal(baseTime.Add(time.Hour)) {
		t.Fatalf("enriched_at after a later list-only apply = %v, want the detail stamp kept", state.EnrichedAt)
	}
}

// runAs performs a complete run for scope that observed seen, as a one-off or not.
func runAs(t *testing.T, s *store.Store, scope string, oneOff bool, seen ...string) domain.IngestRunID {
	t.Helper()
	ctx := context.Background()
	run, err := s.CreateRun(ctx, testProvider, scope, oneOff)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	for _, id := range seen {
		if _, err := s.ApplySourceProperty(ctx, run.ID, newSource(id), baseTime); err != nil {
			t.Fatalf("apply %s: %v", id, err)
		}
	}
	finishRun(t, s, run.ID, ingest.RunStats{Complete: true, ListingsSeen: len(seen)})
	return run.ID
}

// An apply with no run (a CLI import) leaves absence ownership where it was,
// otherwise the listing could never age out of its standing scope.
func TestRunlessApplyKeepsAbsenceOwnership(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	runAs(t, s, "scope-standing", false, "3101")
	apply(t, s, 0, newSource("3101"), baseTime.Add(time.Hour))

	runID := runAs(t, s, "scope-standing", false)
	res, err := s.MarkMissingSources(ctx, testProvider, []string{}, runID)
	if err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	if res.Advanced != 1 {
		t.Fatalf("advanced = %d, want 1 (a run-less apply must not clear last_run_id)", res.Advanced)
	}
}
