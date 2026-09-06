package store_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"slices"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/store"
)

func TestApplySourcePropertyCreatesListing(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)

	sp := newSource("1001")
	res := apply(t, s, runID, sp, baseTime)
	if !res.Created {
		t.Fatalf("expected created listing, got %+v", res)
	}
	if !res.PriceChanged || res.StatusChanged || res.MaterialChanged {
		t.Fatalf("unexpected change flags on creation: %+v", res)
	}

	p, events, photos, prov := mustGet(t, s, res.PropertyID)
	if p.Address.Street != sp.Property.Address.Street || p.Address.Unit != "4B" {
		t.Fatalf("address round-trip failed: %+v", p.Address)
	}
	if p.Price == nil || *p.Price != 900_000 {
		t.Fatalf("price round-trip failed: %v", p.Price)
	}
	if p.Currency != domain.CurrencyUSD {
		t.Fatalf("currency = %q, want USD", p.Currency)
	}
	if p.Bathrooms == nil || *p.Bathrooms != 1.5 {
		t.Fatalf("baths round-trip failed: %v", p.Bathrooms)
	}
	if p.Geo == nil {
		t.Fatal("geo not persisted")
	}
	if p.Status != domain.StatusActive {
		t.Fatalf("status = %q", p.Status)
	}
	if !p.FirstSeen.Equal(baseTime) || !p.LastSeen.Equal(baseTime) || !p.MaterialChangedAt.Equal(baseTime) {
		t.Fatalf("lifecycle timestamps: first=%v last=%v material=%v", p.FirstSeen, p.LastSeen, p.MaterialChangedAt)
	}
	if len(events) != 1 || events[0].Price != 900_000 {
		t.Fatalf("expected one initial price event, got %+v", events)
	}
	if len(photos) != 2 || photos[0].Position != 0 || photos[1].SourceURL != sp.PhotoURLs[1] {
		t.Fatalf("photos not persisted in order: %+v", photos)
	}
	if prov.Provider != testProvider || prov.ProviderID != "1001" || prov.URL != sp.URL {
		t.Fatalf("provenance = %+v", prov)
	}
	if !prov.FetchedAt.Equal(baseTime) || prov.MissingRuns != 0 {
		t.Fatalf("provenance timestamps = %+v", prov)
	}
}

func TestPriceEventOnlyOnActualChange(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	sp := newSource("1001")

	first := apply(t, s, runID, sp, baseTime)
	second := apply(t, s, runID, sp, baseTime.Add(24*time.Hour))
	if second.PriceChanged || second.MaterialChanged {
		t.Fatalf("re-observing the same price changed something: %+v", second)
	}

	p, events, _, _ := mustGet(t, s, first.PropertyID)
	if len(events) != 1 {
		t.Fatalf("expected 1 price event after re-observation, got %d", len(events))
	}
	if !p.MaterialChangedAt.Equal(baseTime) {
		t.Fatalf("material_changed_at moved without a material change: %v", p.MaterialChangedAt)
	}
	if !p.LastSeen.Equal(baseTime.Add(24 * time.Hour)) {
		t.Fatalf("last_seen not advanced: %v", p.LastSeen)
	}

	drop := newSource("1001")
	drop.Property.Price = money(850_000)
	dropAt := baseTime.Add(48 * time.Hour)
	third := apply(t, s, runID, drop, dropAt)
	if !third.PriceChanged || !third.MaterialChanged || third.StatusChanged {
		t.Fatalf("price drop flags = %+v", third)
	}

	p, events, _, _ = mustGet(t, s, first.PropertyID)
	if len(events) != 2 || events[1].Price != 850_000 {
		t.Fatalf("expected 2 price events ending at 850000, got %+v", events)
	}
	if !p.MaterialChangedAt.Equal(dropAt) {
		t.Fatalf("material_changed_at = %v, want %v", p.MaterialChangedAt, dropAt)
	}
}

func TestDescriptionOnlyChangeIsNotMaterial(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	edited := newSource("1001")
	edited.Property.Description = "renovated kitchen, new listing copy"
	res := apply(t, s, runID, edited, baseTime.Add(time.Hour))
	if res.MaterialChanged || res.PriceChanged || res.StatusChanged {
		t.Fatalf("description-only update reported changes: %+v", res)
	}

	p, events, _, _ := mustGet(t, s, created.PropertyID)
	if p.Description != edited.Property.Description {
		t.Fatalf("description not updated: %q", p.Description)
	}
	if !p.MaterialChangedAt.Equal(baseTime) {
		t.Fatalf("material_changed_at moved on a description edit: %v", p.MaterialChangedAt)
	}
	if len(events) != 1 {
		t.Fatalf("description edit created price events: %+v", events)
	}
}

// An incremental search row omits what only the detail page reports (sqft,
// beds, unit, zip, geo, ...); applying it must not erase the deep pass's values.
func TestSearchOnlyApplyKeepsDetailFields(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	sparse := newSource("1001")
	sparse.Enriched = false
	sparse.Property.Bedrooms, sparse.Property.Bathrooms, sparse.Property.Sqft = nil, nil, nil
	sparse.Property.Address.Unit, sparse.Property.Address.Zip, sparse.Property.Address.Neighborhood = "", "", ""
	sparse.Property.Geo = nil
	sparse.Property.Description, sparse.Property.DaysOnMarket = "", nil
	apply(t, s, runID, sparse, baseTime.Add(time.Hour))

	got, _, _, _ := mustGet(t, s, created.PropertyID)
	want := newSource("1001").Property
	if got.Bedrooms == nil || *got.Bedrooms != 2 || got.Bathrooms == nil || *got.Bathrooms != 1.5 || got.Sqft == nil || *got.Sqft != 900 {
		t.Fatalf("search-only apply erased beds/baths/sqft: %+v", got)
	}
	if got.Address.Unit != want.Address.Unit || got.Address.Zip != want.Address.Zip || got.Address.Neighborhood != want.Address.Neighborhood {
		t.Fatalf("search-only apply erased address parts: %+v", got.Address)
	}
	if got.Geo == nil || *got.Geo != *want.Geo || got.Description != want.Description {
		t.Fatalf("search-only apply erased geo/description: geo=%v desc=%q", got.Geo, got.Description)
	}
}

// A blank status (search row whose detail fetch failed) keeps the last detail
// status, except that a delisted listing seen again is back on the market.
func TestBlankStatusKeepsDetailStatusButRevivesDelisted(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	contract := newSource("1010")
	contract.Property.Status = domain.StatusInContract
	res := apply(t, s, runAs(t, s, "scope", false), contract, baseTime)
	blank := newSource("1010")
	blank.Property.Status = ""
	apply(t, s, runAs(t, s, "scope", false), blank, baseTime.Add(time.Hour))
	if got, _, _, _ := mustGet(t, s, res.PropertyID); got.Status != domain.StatusInContract {
		t.Fatalf("status after blank re-sighting = %q, want in_contract kept", got.Status)
	}

	for i := range 2 {
		if _, err := s.MarkMissingSources(ctx, testProvider, nil, runAs(t, s, "scope", false)); err != nil {
			t.Fatalf("mark missing pass %d: %v", i+1, err)
		}
	}
	if got, _, _, _ := mustGet(t, s, res.PropertyID); got.Status != domain.StatusDelisted {
		t.Fatalf("status after two misses = %q, want delisted", got.Status)
	}
	revived := apply(t, s, runAs(t, s, "scope", false), blank, baseTime.Add(2*time.Hour))
	got, _, _, _ := mustGet(t, s, res.PropertyID)
	if got.Status != domain.StatusActive || !revived.StatusChanged || !revived.MaterialChanged {
		t.Fatalf("delisted listing seen again = %q (%+v), want active + material change", got.Status, revived)
	}
}

// A bathroom count the numeric(3,1) column cannot hold is dropped like any
// other unrepresentable number, not allowed to abort the whole apply.
func TestUnrepresentableBathsAreDroppedNotFatal(t *testing.T) {
	s := newStore(t)
	for i, v := range []float64{math.NaN(), math.Inf(1), -1, 100} {
		sp := newSource(fmt.Sprintf("12%02d", i))
		sp.Property.Bathrooms = &v
		res := apply(t, s, runAs(t, s, "scope", false), sp, baseTime)
		if got, _, _, _ := mustGet(t, s, res.PropertyID); got.Bathrooms != nil {
			t.Fatalf("baths %v stored as %v, want nil", v, *got.Bathrooms)
		}
	}
}

func TestStatusChangeIsMaterial(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	contract := newSource("1001")
	contract.Property.Status = domain.StatusInContract
	contract.SourceStatus = "in_contract"
	at := baseTime.Add(72 * time.Hour)
	res := apply(t, s, runID, contract, at)
	if !res.StatusChanged || !res.MaterialChanged || res.PriceChanged {
		t.Fatalf("status change flags = %+v", res)
	}

	p, _, _, prov := mustGet(t, s, created.PropertyID)
	if p.Status != domain.StatusInContract {
		t.Fatalf("status = %q", p.Status)
	}
	if !p.MaterialChangedAt.Equal(at) {
		t.Fatalf("material_changed_at = %v, want %v", p.MaterialChangedAt, at)
	}
	if prov.SourceStatus != "in_contract" {
		t.Fatalf("source_status = %q", prov.SourceStatus)
	}
}

func TestApplyPhotoSlotsAndCache(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)
	ctx := context.Background()

	if err := s.SetPhotoCache(ctx, created.PropertyID, 0, "https://photos.example/1001/1.jpg", "/thumbs/1001-0.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}

	apply(t, s, runID, newSource("1001"), baseTime.Add(time.Hour))
	_, _, photos, _ := mustGet(t, s, created.PropertyID)
	if photos[0].CachedPath != "/thumbs/1001-0.jpg" || photos[0].Width != 800 {
		t.Fatalf("cached thumbnail lost on unchanged re-observation: %+v", photos[0])
	}

	// Position 1 keeps its cache too, and a second listing shares its key.
	if err := s.SetPhotoCache(ctx, created.PropertyID, 1, "https://photos.example/1001/2.jpg", "/thumbs/shared.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache 1: %v", err)
	}
	other := apply(t, s, runID, newSource("1002"), baseTime)
	if err := s.SetPhotoCache(ctx, other.PropertyID, 0, "https://photos.example/1002/1.jpg", "/thumbs/shared.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache other: %v", err)
	}

	changed := newSource("1001")
	changed.PhotoURLs = []string{"https://photos.example/1001/new.jpg"}
	res := apply(t, s, runID, changed, baseTime.Add(2*time.Hour))
	// The replaced slot's key is evicted; the trimmed slot's key survives because
	// listing 1002 still references it.
	if want := []string{"/thumbs/1001-0.jpg"}; !slices.Equal(res.EvictedKeys, want) {
		t.Fatalf("evicted keys = %v, want %v", res.EvictedKeys, want)
	}

	_, _, photos, _ = mustGet(t, s, created.PropertyID)
	if len(photos) != 1 {
		t.Fatalf("photo list not trimmed: %+v", photos)
	}
	if photos[0].CachedPath != "" || photos[0].MIMEType != "" {
		t.Fatalf("stale cache kept for a replaced photo URL: %+v", photos[0])
	}
	if res := apply(t, s, runID, changed, baseTime.Add(3*time.Hour)); len(res.EvictedKeys) != 0 {
		t.Fatalf("unchanged re-observation evicted %v", res.EvictedKeys)
	}
}

// A gallery whose photos merely change order keeps every cached thumbnail,
// each following its source URL to its new slot; nothing is evicted.
func TestReorderedPhotosKeepCache(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	ctx := context.Background()
	sp := newSource("1003")
	sp.PhotoURLs = []string{"https://photos.example/1003/a.jpg", "https://photos.example/1003/b.jpg", "https://photos.example/1003/c.jpg"}
	created := apply(t, s, runID, sp, baseTime)
	for i, url := range sp.PhotoURLs {
		if err := s.SetPhotoCache(ctx, created.PropertyID, i, url, fmt.Sprintf("/thumbs/1003-%d.jpg", i), "image/jpeg", 800, 600); err != nil {
			t.Fatalf("set photo cache %d: %v", i, err)
		}
	}

	slices.Reverse(sp.PhotoURLs)
	res := apply(t, s, runID, sp, baseTime.Add(time.Hour))
	if len(res.EvictedKeys) != 0 {
		t.Fatalf("re-ordering evicted %v", res.EvictedKeys)
	}
	_, _, photos, _ := mustGet(t, s, created.PropertyID)
	if len(photos) != 3 {
		t.Fatalf("photos = %+v, want 3", photos)
	}
	for i, p := range photos {
		if want := fmt.Sprintf("/thumbs/1003-%d.jpg", 2-i); p.CachedPath != want || p.Width != 800 {
			t.Fatalf("photo %d = %+v, want cached path %s carried with its URL", i, p, want)
		}
	}
}

func TestMarkMissingSourcesAdvancesAndDelists(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := startRun(t, s)
	kept := apply(t, s, seed, newSource("1001"), baseTime)
	gone := apply(t, s, seed, newSource("1002"), baseTime)
	finishRun(t, s, seed, ingest.RunStats{Complete: true, ListingsSeen: 2})

	run2 := completeRun(t, s, 1)
	res, err := s.MarkMissingSources(ctx, testProvider, []string{"1001"}, run2)
	if err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	if res.Advanced != 1 || res.Delisted != 0 {
		t.Fatalf("first missing run = %+v, want 1 advanced 0 delisted", res)
	}
	if p, _, _, _ := mustGet(t, s, gone.PropertyID); p.Status != domain.StatusActive {
		t.Fatalf("one missing run delisted the listing: %q", p.Status)
	}

	run3 := completeRun(t, s, 1)
	res, err = s.MarkMissingSources(ctx, testProvider, []string{"1001"}, run3)
	if err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	if res.Advanced != 1 || res.Delisted != 1 {
		t.Fatalf("second missing run = %+v, want 1 advanced 1 delisted", res)
	}
	if p, _, _, _ := mustGet(t, s, gone.PropertyID); p.Status != domain.StatusDelisted {
		t.Fatalf("status after two missing runs = %q", p.Status)
	}
	if p, _, _, _ := mustGet(t, s, kept.PropertyID); p.Status != domain.StatusActive {
		t.Fatalf("seen listing was affected: %q", p.Status)
	}

	active, err := s.ActiveCount(ctx)
	if err != nil {
		t.Fatalf("active count: %v", err)
	}
	if active != 1 {
		t.Fatalf("active count = %d, want 1", active)
	}
	missing, err := s.MissingSourceCount(ctx)
	if err != nil {
		t.Fatalf("missing source count: %v", err)
	}
	if missing != 1 {
		t.Fatalf("missing source count = %d, want 1", missing)
	}

	// Reappearing resets absence counting and restores the status.
	run4 := completeRun(t, s, 2)
	apply(t, s, run4, newSource("1002"), baseTime.Add(96*time.Hour))
	p, _, _, prov := mustGet(t, s, gone.PropertyID)
	if p.Status != domain.StatusActive {
		t.Fatalf("status after reappearance = %q", p.Status)
	}
	if prov.MissingRuns != 0 {
		t.Fatalf("missing_runs not reset: %d", prov.MissingRuns)
	}
}

func TestMarkMissingSourcesRequiresCleanRun(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := startRun(t, s)
	apply(t, s, seed, newSource("1001"), baseTime)
	finishRun(t, s, seed, ingest.RunStats{Complete: true, ListingsSeen: 1})

	incomplete := startRun(t, s)
	finishRun(t, s, incomplete, ingest.RunStats{Complete: false, ListingsSeen: 0})

	suspect := startRun(t, s)
	finishRun(t, s, suspect, ingest.RunStats{Complete: true, ListingsSeen: 1, Suspect: true})

	for name, runID := range map[string]domain.IngestRunID{
		"incomplete": incomplete,
		"suspect":    suspect,
		"unknown":    domain.IngestRunID(99999),
		"unfinished": startRun(t, s),
	} {
		if _, err := s.MarkMissingSources(ctx, testProvider, nil, runID); !errors.Is(err, store.ErrRunNotEligible) {
			t.Fatalf("%s run: err = %v, want ErrRunNotEligible", name, err)
		}
	}

	if n, err := s.MissingSourceCount(ctx); err != nil || n != 0 {
		t.Fatalf("missing source count = %d (err %v), want 0", n, err)
	}
}

func TestExplicitProviderStatusWinsOverAbsence(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := startRun(t, s)
	sold := newSource("1001")
	sold.Property.Status = domain.StatusSold
	sold.SourceStatus = "sold"
	soldRes := apply(t, s, seed, sold, baseTime)
	active := apply(t, s, seed, newSource("1002"), baseTime)
	finishRun(t, s, seed, ingest.RunStats{Complete: true, ListingsSeen: 2})

	if p, _, _, _ := mustGet(t, s, soldRes.PropertyID); p.Status != domain.StatusSold {
		t.Fatalf("explicit sold status not applied: %q", p.Status)
	}

	run2 := completeRun(t, s, 0)
	res, err := s.MarkMissingSources(ctx, testProvider, nil, run2)
	if err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	if res.Advanced != 1 {
		t.Fatalf("advanced = %d, want only the active listing", res.Advanced)
	}
	if p, _, _, prov := mustGet(t, s, soldRes.PropertyID); p.Status != domain.StatusSold || prov.MissingRuns != 0 {
		t.Fatalf("sold listing was absence-counted: status=%q missing=%d", p.Status, prov.MissingRuns)
	}
	if p, _, _, _ := mustGet(t, s, active.PropertyID); p.Status != domain.StatusActive {
		t.Fatalf("active listing delisted too early: %q", p.Status)
	}
}

func TestRentedStatusPersistsAndBlocksAbsenceAdvance(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := startRun(t, s)
	rented := newSource("2001")
	rented.Property.ListingType = domain.ListingRent
	rented.Property.Status = domain.StatusRented
	rented.SourceStatus = "RENTED"
	res := apply(t, s, seed, rented, baseTime)
	finishRun(t, s, seed, ingest.RunStats{Complete: true, ListingsSeen: 1})

	if p, _, _, _ := mustGet(t, s, res.PropertyID); p.Status != domain.StatusRented {
		t.Fatalf("rented status not persisted: %q", p.Status)
	}

	run2 := completeRun(t, s, 0)
	missing, err := s.MarkMissingSources(ctx, testProvider, nil, run2)
	if err != nil {
		t.Fatalf("mark missing: %v", err)
	}
	if missing.Advanced != 0 {
		t.Fatalf("advanced = %d; a rented listing is already terminal", missing.Advanced)
	}
	if p, _, _, _ := mustGet(t, s, res.PropertyID); p.Status != domain.StatusRented {
		t.Fatalf("status after a missing run = %q", p.Status)
	}
}

func TestInTxRollsBackOnError(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	sentinel := errors.New("boom")
	err := s.InTx(ctx, func(tx *store.Store) error {
		if _, err := tx.ApplySourceProperty(ctx, runID, newSource("1001"), baseTime); err != nil {
			return err
		}
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Fatalf("InTx err = %v, want sentinel", err)
	}
	if n, err := s.ActiveCount(ctx); err != nil || n != 0 {
		t.Fatalf("active count after rollback = %d (err %v), want 0", n, err)
	}
}

func TestGetPropertyUnknownID(t *testing.T) {
	s := newStore(t)
	_, _, _, _, err := s.GetProperty(context.Background(), domain.PropertyID(4242))
	if !errors.Is(err, store.ErrPropertyNotFound) {
		t.Fatalf("err = %v, want ErrPropertyNotFound", err)
	}
}

// A search-only re-sighting may carry fewer photos than the detail page did;
// it must neither trim the gallery nor evict thumbnails a deep pass cached.
func TestSearchOnlyApplyNeverShrinksTheGallery(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	ctx := context.Background()
	full := newSource("1004")
	full.PhotoURLs = []string{"https://photos.example/1004/a.jpg", "https://photos.example/1004/b.jpg", "https://photos.example/1004/c.jpg"}
	created := apply(t, s, runID, full, baseTime)
	for i, url := range full.PhotoURLs {
		if err := s.SetPhotoCache(ctx, created.PropertyID, i, url, fmt.Sprintf("/thumbs/1004-%d.jpg", i), "image/jpeg", 800, 600); err != nil {
			t.Fatalf("set photo cache %d: %v", i, err)
		}
	}

	sparse := newSource("1004")
	sparse.Enriched = false
	sparse.PhotoURLs = full.PhotoURLs[:1]
	if res := apply(t, s, runID, sparse, baseTime.Add(time.Hour)); len(res.EvictedKeys) != 0 {
		t.Fatalf("search-only pass evicted %v", res.EvictedKeys)
	}
	if _, _, photos, _ := mustGet(t, s, created.PropertyID); len(photos) != 3 || photos[2].CachedPath == "" {
		t.Fatalf("search-only pass trimmed the gallery: %+v", photos)
	}

	// A search row with a photo the gallery lacks is real news and replaces it.
	sparse.PhotoURLs = []string{"https://photos.example/1004/new.jpg"}
	apply(t, s, runID, sparse, baseTime.Add(2*time.Hour))
	if _, _, photos, _ := mustGet(t, s, created.PropertyID); len(photos) != 1 {
		t.Fatalf("new photo did not replace the gallery: %+v", photos)
	}
	// A deep pass may shrink it.
	full.PhotoURLs = full.PhotoURLs[:2]
	apply(t, s, runID, full, baseTime.Add(3*time.Hour))
	if _, _, photos, _ := mustGet(t, s, created.PropertyID); len(photos) != 2 {
		t.Fatalf("deep pass did not trim the gallery: %+v", photos)
	}
}
