package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

func TestCandidateOrderingIsDeterministic(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)

	older := apply(t, s, runID, newSource("1001"), baseTime)
	newerA := apply(t, s, runID, newSource("1002"), baseTime.Add(time.Hour))
	newerB := apply(t, s, runID, newSource("1003"), baseTime.Add(time.Hour))

	got := candidateIDs(t, s, domain.Profile{})
	want := []domain.PropertyID{newerA.PropertyID, newerB.PropertyID, older.PropertyID}
	if !equalIDs(got, want) {
		t.Fatalf("candidate order = %v, want %v (material DESC, first_seen DESC, id)", got, want)
	}

	// A price drop on the oldest listing moves it to the front.
	drop := newSource("1001")
	drop.Property.Price = money(800_000)
	apply(t, s, runID, drop, baseTime.Add(2*time.Hour))

	got = candidateIDs(t, s, domain.Profile{})
	want = []domain.PropertyID{older.PropertyID, newerA.PropertyID, newerB.PropertyID}
	if !equalIDs(got, want) {
		t.Fatalf("candidate order after price drop = %v, want %v", got, want)
	}
}

func TestCandidatesResurfaceAfterMaterialChange(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	if _, err := s.MarkShown(ctx, []domain.PropertyID{created.PropertyID}, nil); err != nil {
		t.Fatalf("mark shown: %v", err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); len(got) != 0 {
		t.Fatalf("shown listing still a candidate: %v", got)
	}

	// Non-material update: still suppressed.
	edited := newSource("1001")
	edited.Property.Description = "new copy"
	apply(t, s, runID, edited, baseTime.Add(time.Hour))
	if got := candidateIDs(t, s, domain.Profile{}); len(got) != 0 {
		t.Fatalf("non-material update resurfaced the listing: %v", got)
	}

	drop := newSource("1001")
	drop.Property.Price = money(825_000)
	apply(t, s, runID, drop, baseTime.Add(2*time.Hour))

	got := candidateIDs(t, s, domain.Profile{})
	if !equalIDs(got, []domain.PropertyID{created.PropertyID}) {
		t.Fatalf("listing did not resurface after a price change: %v", got)
	}

	// Re-marking shown snapshots the new material_changed_at.
	if _, err := s.MarkShown(ctx, []domain.PropertyID{created.PropertyID}, nil); err != nil {
		t.Fatalf("mark shown: %v", err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); len(got) != 0 {
		t.Fatalf("re-shown listing still a candidate: %v", got)
	}

	// A change committed between the read and mark_shown is not swallowed
	// when the caller says when it read the rows.
	readAt := baseTime.Add(2*time.Hour + time.Minute)
	drop.Property.Price = money(800_000)
	apply(t, s, runID, drop, baseTime.Add(3*time.Hour))
	if _, err := s.MarkShown(ctx, []domain.PropertyID{created.PropertyID}, &readAt); err != nil {
		t.Fatalf("mark shown as of: %v", err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); !equalIDs(got, []domain.PropertyID{created.PropertyID}) {
		t.Fatalf("change after as_of did not resurface the listing: %v", got)
	}
	// And with as_of after the change, it is suppressed as usual.
	readAt = baseTime.Add(4 * time.Hour)
	if _, err := s.MarkShown(ctx, []domain.PropertyID{created.PropertyID}, &readAt); err != nil {
		t.Fatalf("mark shown as of: %v", err)
	}
	if got := candidateIDs(t, s, domain.Profile{}); len(got) != 0 {
		t.Fatalf("listing shown after the change still a candidate: %v", got)
	}
}

func TestCandidatesExcludeVerdictedListings(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	judged := apply(t, s, runID, newSource("1001"), baseTime)
	open := apply(t, s, runID, newSource("1002"), baseTime)

	if _, err := s.RecordVerdict(ctx, judged.PropertyID, domain.VerdictDislike, "too dark"); err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	got := candidateIDs(t, s, domain.Profile{})
	if !equalIDs(got, []domain.PropertyID{open.PropertyID}) {
		t.Fatalf("candidates = %v, want only the unjudged listing %d", got, open.PropertyID)
	}
}

func TestCandidatesApplyProfileHardFilters(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)

	cheap := newSource("1001")
	cheap.Property.Price = money(700_000)
	cheap.Property.Bedrooms = ptr.To(2)
	cheapRes := apply(t, s, runID, cheap, baseTime)

	pricey := newSource("1002")
	pricey.Property.Price = money(2_000_000)
	priceyRes := apply(t, s, runID, pricey, baseTime)

	unknownPrice := newSource("1003")
	unknownPrice.Property.Price = nil
	unknownRes := apply(t, s, runID, unknownPrice, baseTime)

	studio := newSource("1004")
	studio.Property.Price = money(500_000)
	studio.Property.Bedrooms = ptr.To(0)
	studioRes := apply(t, s, runID, studio, baseTime)

	got := candidateIDs(t, s, domain.Profile{MaxPrice: money(1_000_000)})
	if !equalIDs(got, []domain.PropertyID{cheapRes.PropertyID, studioRes.PropertyID}) {
		t.Fatalf("price filter = %v, want the two affordable listings (excluding %d and unknown-price %d)",
			got, priceyRes.PropertyID, unknownRes.PropertyID)
	}

	got = candidateIDs(t, s, domain.Profile{MinBeds: ptr.To(2)})
	if !equalIDs(got, []domain.PropertyID{cheapRes.PropertyID, priceyRes.PropertyID, unknownRes.PropertyID}) {
		t.Fatalf("min-beds filter = %v", got)
	}

	got = candidateIDs(t, s, domain.Profile{Neighborhoods: []string{"Nowhere"}})
	if len(got) != 0 {
		t.Fatalf("neighborhood filter matched unexpectedly: %v", got)
	}
	// Casing differs from the provider's "Park Slope"; the filter is case-insensitive.
	if got = candidateIDs(t, s, domain.Profile{Neighborhoods: []string{"park slope"}}); len(got) != 4 {
		t.Fatalf("lowercase neighborhood filter = %v, want all four", got)
	}
	if got = candidateIDs(t, s, domain.Profile{Neighborhoods: []string{}}); len(got) != 4 {
		t.Fatalf("empty neighborhood filter = %v, want all four (filter off)", got)
	}

	got = candidateIDs(t, s, domain.Profile{PropertyTypes: []domain.PropertyType{domain.PropertyCoop}})
	if len(got) != 4 {
		t.Fatalf("property-type filter = %v, want all four coops", got)
	}

	// A zero minimum is no minimum: it must not drop listings whose count is unknown.
	noBeds := newSource("1005")
	noBeds.Property.Bedrooms, noBeds.Property.Bathrooms = nil, nil
	noBedsRes := apply(t, s, runID, noBeds, baseTime)
	if got = candidateIDs(t, s, domain.Profile{MinBeds: ptr.To(0), MinBaths: ptr.To(0.0)}); !slices.Contains(got, noBedsRes.PropertyID) {
		t.Fatalf("min_beds=0 filter = %v, want the unknown-beds listing %d included", got, noBedsRes.PropertyID)
	}
	if got = candidateIDs(t, s, domain.Profile{MinBeds: ptr.To(1)}); slices.Contains(got, noBedsRes.PropertyID) {
		t.Fatalf("min_beds=1 filter = %v, want the unknown-beds listing %d excluded", got, noBedsRes.PropertyID)
	}
}

func TestCandidatesCarryingCostFilter(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)

	pricey := newSource("1001")
	pricey.Property.Maintenance = money(3000)
	pricey.Property.TaxesMonthly = money(500)
	priceyRes := apply(t, s, runID, pricey, baseTime)

	modest := newSource("1002")
	modest.Property.Maintenance = money(900)
	modest.Property.TaxesMonthly = money(100)
	modestRes := apply(t, s, runID, modest, baseTime)

	unknown := newSource("1003")
	unknown.Property.Maintenance = nil
	unknown.Property.CommonCharges = nil
	unknown.Property.TaxesMonthly = nil
	unknownRes := apply(t, s, runID, unknown, baseTime)

	got := candidateIDs(t, s, domain.Profile{MaxMonthlyCarrying: money(1500)})
	if !equalIDs(got, []domain.PropertyID{modestRes.PropertyID, unknownRes.PropertyID}) {
		t.Fatalf("carrying filter = %v, want modest %d and unknown %d (excluding %d)",
			got, modestRes.PropertyID, unknownRes.PropertyID, priceyRes.PropertyID)
	}
}

func TestSearchPropertiesActiveOnlyWithFilters(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	active := apply(t, s, runID, newSource("1001"), baseTime)

	sold := newSource("1002")
	sold.Property.Status = domain.StatusSold
	apply(t, s, runID, sold, baseTime)

	// Verdicted and shown listings still appear in search, unlike candidates.
	if _, err := s.RecordVerdict(ctx, active.PropertyID, domain.VerdictLove, ""); err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if _, err := s.MarkShown(ctx, []domain.PropertyID{active.PropertyID}, nil); err != nil {
		t.Fatalf("mark shown: %v", err)
	}

	props, err := s.SearchProperties(ctx, store.SearchFilter{MaxPrice: money(1_000_000), MinBeds: ptr.To(2), MinBaths: ptr.To(1.5)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !equalIDs(idsOf(props), []domain.PropertyID{active.PropertyID}) {
		t.Fatalf("search = %v, want only the active listing", idsOf(props))
	}
	if props[0].URL != "https://streeteasy.com/sale/1001" {
		t.Fatalf("search row url = %q, want the shareable listing link", props[0].URL)
	}

	props, err = s.SearchProperties(ctx, store.SearchFilter{MinBaths: ptr.To(3.0)})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if len(props) != 0 {
		t.Fatalf("min-baths filter matched unexpectedly: %v", idsOf(props))
	}
	props, err = s.SearchProperties(ctx, store.SearchFilter{Neighborhoods: []string{"PARK SLOPE "}})
	if err != nil || !equalIDs(idsOf(props), []domain.PropertyID{active.PropertyID}) {
		t.Fatalf("case-insensitive neighborhood search = %v, %v; want the active listing", idsOf(props), err)
	}
}

func TestSearchPropertiesListingTypeFilter(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	sale := apply(t, s, runID, newSource("1101"), baseTime)

	rent := newSource("1102")
	rent.Property.ListingType = domain.ListingRent
	rentRes := apply(t, s, runID, rent, baseTime)

	props, err := s.SearchProperties(ctx, store.SearchFilter{ListingType: domain.ListingSale})
	if err != nil {
		t.Fatalf("search sale: %v", err)
	}
	if !equalIDs(idsOf(props), []domain.PropertyID{sale.PropertyID}) {
		t.Fatalf("sale search = %v, want only %d", idsOf(props), sale.PropertyID)
	}

	props, err = s.SearchProperties(ctx, store.SearchFilter{})
	if err != nil {
		t.Fatalf("search any: %v", err)
	}
	if len(props) != 2 {
		t.Fatalf("unfiltered search = %v, want both %d and %d", idsOf(props), sale.PropertyID, rentRes.PropertyID)
	}
}

func TestSearchPropertiesPaginationAndTotal(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	const n = 7
	ordered := make([]domain.PropertyID, 0, n)
	// Newest material_changed_at first in the deterministic order, so build the
	// expected sweep order from newest to oldest.
	for i := 0; i < n; i++ {
		res := apply(t, s, runID, newSource(fmt.Sprintf("50%02d", i)), baseTime.Add(time.Duration(i)*time.Hour))
		ordered = append([]domain.PropertyID{res.PropertyID}, ordered...)
	}

	total, err := s.CountProperties(ctx, store.SearchFilter{})
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if total != n {
		t.Fatalf("count = %d, want %d", total, n)
	}

	// Page with a limit smaller than n and assert every id is seen exactly once,
	// in the deterministic order, with no overlap or gap.
	const page = 3
	var swept []domain.PropertyID
	for offset := 0; offset < total; offset += page {
		props, err := s.SearchProperties(ctx, store.SearchFilter{Limit: page, Offset: offset})
		if err != nil {
			t.Fatalf("search offset %d: %v", offset, err)
		}
		swept = append(swept, idsOf(props)...)
	}
	if !equalIDs(swept, ordered) {
		t.Fatalf("full sweep = %v, want %v (every id once, deterministic order)", swept, ordered)
	}

	// A mid-corpus page is exactly the expected window.
	props, err := s.SearchProperties(ctx, store.SearchFilter{Limit: 2, Offset: 2})
	if err != nil {
		t.Fatalf("windowed search: %v", err)
	}
	if !equalIDs(idsOf(props), ordered[2:4]) {
		t.Fatalf("window[2:4] = %v, want %v", idsOf(props), ordered[2:4])
	}
}

func TestSearchPropertiesIncludeInactive(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	active := apply(t, s, runID, newSource("5101"), baseTime)

	sold := newSource("5102")
	sold.Property.Status = domain.StatusSold
	sold.SourceStatus = "closed"
	soldRes := apply(t, s, runID, sold, baseTime.Add(time.Hour))

	// Default: active only, in both the rows and the count.
	props, err := s.SearchProperties(ctx, store.SearchFilter{})
	if err != nil {
		t.Fatalf("search: %v", err)
	}
	if !equalIDs(idsOf(props), []domain.PropertyID{active.PropertyID}) {
		t.Fatalf("default search = %v, want only the active listing", idsOf(props))
	}
	if total, err := s.CountProperties(ctx, store.SearchFilter{}); err != nil || total != 1 {
		t.Fatalf("default count = %d (err %v), want 1", total, err)
	}

	// IncludeInactive: the sold listing appears too.
	props, err = s.SearchProperties(ctx, store.SearchFilter{IncludeInactive: true})
	if err != nil {
		t.Fatalf("search inactive: %v", err)
	}
	if !equalIDs(idsOf(props), []domain.PropertyID{soldRes.PropertyID, active.PropertyID}) {
		t.Fatalf("include_inactive search = %v, want sold %d then active %d", idsOf(props), soldRes.PropertyID, active.PropertyID)
	}
	if total, err := s.CountProperties(ctx, store.SearchFilter{IncludeInactive: true}); err != nil || total != 2 {
		t.Fatalf("include_inactive count = %d (err %v), want 2", total, err)
	}
}

func TestPriceDropsComparesLastTwoEvents(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	// No price at all, so no price events.
	noPrice := newSource("1201")
	noPrice.Property.Price = nil
	none := apply(t, s, runID, noPrice, baseTime)

	initial := apply(t, s, runID, newSource("1202"), baseTime)

	dropped := apply(t, s, runID, newSource("1203"), baseTime)
	lower := newSource("1203")
	lower.Property.Price = money(800_000)
	apply(t, s, runID, lower, baseTime.Add(time.Hour))

	raised := apply(t, s, runID, newSource("1204"), baseTime)
	higher := newSource("1204")
	higher.Property.Price = money(950_000)
	apply(t, s, runID, higher, baseTime.Add(time.Hour))

	// A drop followed by a recovery is not a current price drop.
	recovered := apply(t, s, runID, newSource("1205"), baseTime)
	for i, price := range []int64{800_000, 880_000} {
		next := newSource("1205")
		next.Property.Price = money(price)
		apply(t, s, runID, next, baseTime.Add(time.Duration(i+1)*time.Hour))
	}

	ids := []domain.PropertyID{
		none.PropertyID, initial.PropertyID, dropped.PropertyID,
		raised.PropertyID, recovered.PropertyID,
	}
	drops, err := s.PriceDrops(ctx, ids)
	if err != nil {
		t.Fatalf("price drops: %v", err)
	}

	for _, tc := range []struct {
		name string
		id   domain.PropertyID
		want bool
	}{
		{"no price events", none.PropertyID, false},
		{"initial price only", initial.PropertyID, false},
		{"price drop", dropped.PropertyID, true},
		{"price increase", raised.PropertyID, false},
		{"drop then increase", recovered.PropertyID, false},
	} {
		if got := drops[tc.id]; got != tc.want {
			t.Errorf("%s: price drop = %t, want %t", tc.name, got, tc.want)
		}
	}
}

func TestPhotoCountsAndCachedPositions(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	// Listing with three photo slots; cache positions 0 and 2, leave 1 uncached.
	sp := newSource("1001")
	sp.PhotoURLs = []string{
		"https://photos.example/1001/0.jpg",
		"https://photos.example/1001/1.jpg",
		"https://photos.example/1001/2.jpg",
	}
	withPhotos := apply(t, s, runID, sp, baseTime)
	if err := s.SetPhotoCache(ctx, withPhotos.PropertyID, 0, "https://photos.example/1001/0.jpg", "streeteasy/ab/a0.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache 0: %v", err)
	}
	if err := s.SetPhotoCache(ctx, withPhotos.PropertyID, 2, "https://photos.example/1001/2.jpg", "streeteasy/ab/a2.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache 2: %v", err)
	}

	// Listing with photo slots but nothing cached.
	spB := newSource("1002")
	spB.PhotoURLs = []string{"https://photos.example/1002/0.jpg"}
	noCache := apply(t, s, runID, spB, baseTime)

	ids := []domain.PropertyID{withPhotos.PropertyID, noCache.PropertyID}
	counts, err := s.PhotoCounts(ctx, ids)
	if err != nil {
		t.Fatalf("photo counts: %v", err)
	}
	if c := counts[withPhotos.PropertyID]; c.Total != 3 || c.Cached != 2 {
		t.Fatalf("counts for cached listing = %+v, want total 3 cached 2", c)
	}
	if c := counts[noCache.PropertyID]; c.Total != 1 || c.Cached != 0 {
		t.Fatalf("counts for uncached listing = %+v, want total 1 cached 0", c)
	}

	// PhotoKeyAt is dense over cached photos: index 1 is the photo at position 2,
	// so every n in 0..cached-1 resolves and cached itself is the end.
	key, err := s.PhotoKeyAt(ctx, withPhotos.PropertyID, 0)
	if err != nil || key != "streeteasy/ab/a0.jpg" {
		t.Fatalf("PhotoKeyAt(0) = %q, %v, want the cached key", key, err)
	}
	key, err = s.PhotoKeyAt(ctx, withPhotos.PropertyID, 1)
	if err != nil || key != "streeteasy/ab/a2.jpg" {
		t.Fatalf("PhotoKeyAt(1) = %q, %v, want the second cached key (position 2)", key, err)
	}
	if _, err := s.PhotoKeyAt(ctx, withPhotos.PropertyID, 2); !errors.Is(err, store.ErrPropertyNotFound) {
		t.Fatalf("PhotoKeyAt(2) past-the-end err = %v, want ErrPropertyNotFound", err)
	}
	if _, err := s.PhotoKeyAt(ctx, noCache.PropertyID, 0); !errors.Is(err, store.ErrPropertyNotFound) {
		t.Fatalf("PhotoKeyAt on uncached listing err = %v, want ErrPropertyNotFound", err)
	}
	if _, err := s.PhotoKeyAt(ctx, domain.PropertyID(999999), 0); !errors.Is(err, store.ErrPropertyNotFound) {
		t.Fatalf("PhotoKeyAt unknown listing err = %v, want ErrPropertyNotFound", err)
	}
}

func TestPhotoCountsEmptyRequest(t *testing.T) {
	s := newStore(t)
	counts, err := s.PhotoCounts(context.Background(), nil)
	if err != nil {
		t.Fatalf("photo counts: %v", err)
	}
	if len(counts) != 0 {
		t.Fatalf("photo counts for no ids = %v, want empty", counts)
	}
}

func TestPriceDropsEmptyRequest(t *testing.T) {
	s := newStore(t)
	drops, err := s.PriceDrops(context.Background(), nil)
	if err != nil {
		t.Fatalf("price drops: %v", err)
	}
	if len(drops) != 0 {
		t.Fatalf("price drops for no ids = %v, want empty", drops)
	}
}
