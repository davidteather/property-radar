package mcp_test

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Test-local mirrors of the wire shape: they assert the JSON contract callers actually see, independently of the adapter's internal DTOs.

type profileResult struct {
	MaxPrice           *int64   `json:"max_price"`
	MinBeds            *int     `json:"min_beds"`
	MinBaths           *float64 `json:"min_baths"`
	MaxMonthlyCarrying *int64   `json:"max_monthly_carrying"`
	ListingType        string   `json:"listing_type"`
	Neighborhoods      []string `json:"neighborhoods"`
	PropertyTypes      []string `json:"property_types"`
	UpdatedAt          *string  `json:"updated_at"`
}

type rubricResult struct {
	Content          string `json:"content"`
	ThroughVerdictID int64  `json:"through_verdict_id"`
	CreatedAt        string `json:"created_at"`
}

type verdictResult struct {
	ID        int64  `json:"id"`
	ListingID int64  `json:"listing_id"`
	Verdict   string `json:"verdict"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

type stateResult struct {
	Profile         profileResult   `json:"profile"`
	Rubric          *rubricResult   `json:"rubric"`
	RubricStale     bool            `json:"rubric_stale"`
	Verdicts        []verdictResult `json:"verdicts"`
	VerdictsTotal   int             `json:"verdicts_total"`
	LatestVerdictID int64           `json:"latest_verdict_id"`
	ImageBaseURL    string          `json:"image_base_url"`
}

type listingRowResult struct {
	ID                 int64    `json:"id"`
	Address            string   `json:"address"`
	Neighborhood       string   `json:"neighborhood"`
	Price              *int64   `json:"price"`
	Beds               *int     `json:"beds"`
	Baths              *float64 `json:"baths"`
	Sqft               *int     `json:"sqft"`
	PropertyType       string   `json:"property_type"`
	MonthlyCarrying    *int64   `json:"monthly_carrying"`
	DOM                *int     `json:"dom"`
	DescriptionExcerpt string   `json:"description_preview"`
	PriceDrop          bool     `json:"price_drop"`
	Status             string   `json:"status"`
	URL                string   `json:"url"`
	PhotoCount         int      `json:"photo_count"`
	PhotosCached       int      `json:"photos_cached"`
}

type listingPhotosResult struct {
	ID           int64    `json:"id"`
	PhotoCount   int      `json:"photo_count"`
	PhotosCached int      `json:"photos_cached"`
	ImageURIs    []string `json:"image_uris"`
}

type listingsPhotosResult struct {
	Count    int                   `json:"count"`
	Listings []listingPhotosResult `json:"listings"`
}

type effectiveFiltersResult struct {
	ListingType   string   `json:"listing_type"`
	MaxPrice      *int64   `json:"max_price"`
	MinBeds       *int     `json:"min_beds"`
	MinBaths      *float64 `json:"min_baths"`
	Neighborhoods []string `json:"neighborhoods"`
	PropertyTypes []string `json:"property_types"`
	Limit         int      `json:"limit"`
}

type searchResult struct {
	EffectiveFilters effectiveFiltersResult `json:"effective_filters"`
	Count            int                    `json:"count"`
	Total            int                    `json:"total"`
	Offset           int                    `json:"offset"`
	Listings         []listingRowResult     `json:"listings"`
	Unmatched        []string               `json:"unmatched_neighborhoods"`
}

type recordVerdictsResult struct {
	Recorded    []verdictResult `json:"recorded"`
	Count       int             `json:"count"`
	FailedCount int             `json:"failed_count"`
	Failed      []struct {
		Index     int    `json:"index"`
		ListingID int64  `json:"listing_id"`
		Error     string `json:"error"`
	} `json:"failed"`
}

type candidatesResult struct {
	Count     int                `json:"count"`
	QueriedAt string             `json:"queried_at"`
	Listings  []listingRowResult `json:"listings"`
}

type priceEventResult struct {
	Price      int64  `json:"price"`
	ObservedAt string `json:"observed_at"`
}

type listingDetailResult struct {
	ID              int64              `json:"id"`
	Address         string             `json:"address"`
	Description     string             `json:"description"`
	URL             string             `json:"url"`
	MonthlyCarrying *int64             `json:"monthly_carrying"`
	PriceHistory    []priceEventResult `json:"price_history"`
	Provenance      struct {
		Provider   string `json:"provider"`
		ProviderID string `json:"provider_id"`
		URL        string `json:"url"`
	} `json:"provenance"`
	PhotoCount     int    `json:"photo_count"`
	PhotosCached   int    `json:"photos_cached"`
	PhotosReturned int    `json:"photos_returned"`
	PhotoMissing   int    `json:"photo_missing"`
	PhotoLayout    string `json:"photo_layout"`
	Sheets         int    `json:"sheets"`
	PhotoSheets    []struct {
		Sheet     int   `json:"sheet"`
		Positions []int `json:"positions"`
		Photos    int   `json:"photos"`
	} `json:"photo_sheets"`
}

type markShownResult struct {
	Marked int `json:"marked"`
}

type recordVerdictResult struct {
	Verdict       verdictResult `json:"verdict"`
	InspectPhotos string        `json:"inspect_photos"`
}

type setProfileResult struct {
	Profile   profileResult `json:"profile"`
	Unmatched []string      `json:"unmatched_neighborhoods"`
}

type updateRubricResult struct {
	Rubric rubricResult `json:"rubric"`
}

type resetStateResult struct {
	Reset   bool   `json:"reset"`
	Message string `json:"message"`
	Cleared struct {
		Verdicts     int `json:"verdicts"`
		Rubrics      int `json:"rubrics"`
		Shown        int `json:"shown"`
		Profile      int `json:"profile"`
		ListItems    int `json:"list_items"`
		CrawlTargets int `json:"crawl_targets"`
	} `json:"cleared"`
}

func TestToolSurfaceIsRegistered(t *testing.T) {
	h := newHarness(t)

	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	got := make([]string, 0, len(res.Tools))
	for _, tool := range res.Tools {
		got = append(got, tool.Name)
		if tool.Description == "" {
			t.Errorf("tool %s has no description", tool.Name)
		}
		if tool.InputSchema == nil {
			t.Errorf("tool %s has no input schema", tool.Name)
		}
		if tool.OutputSchema == nil {
			t.Errorf("tool %s has no output schema", tool.Name)
		}
		if tool.Annotations == nil || tool.Annotations.DestructiveHint == nil {
			t.Errorf("tool %s has no effect annotations", tool.Name)
		}
	}
	slices.Sort(got)
	want := []string{
		"add_to_list", "create_list", "get_candidates", "get_console_url",
		"get_corpus_stats", "get_crawl_status", "get_list", "get_listing",
		"get_listings_photos", "get_lists", "get_state",
		"list_crawl_targets", "manage_crawl_target", "mark_shown", "record_verdict", "record_verdicts",
		"remove_from_list", "request_crawl", "reset_state", "resolve_areas",
		"search_listings", "set_profile", "update_rubric",
	}
	if !slices.Equal(got, want) {
		t.Fatalf("registered tools = %v, want %v", got, want)
	}
}

// A long verdict history is truncated to the most recent verdicts_limit
// entries, still oldest first, with the total and newest id reported so the
// rubric can be rebuilt without guessing ids.
func TestGetStateBoundsTheVerdictHistory(t *testing.T) {
	h := newHarness(t)
	ids := make([]int64, 5)
	for i := range ids {
		ids[i] = int64(h.apply(newSource(fmt.Sprintf("10%02d", i)), baseTime))
		h.mustCall("record_verdict", map[string]any{"listing_id": ids[i], "verdict": "maybe"})
	}
	state := decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{"verdicts_limit": 2}))
	if len(state.Verdicts) != 2 || state.VerdictsTotal != 5 {
		t.Fatalf("verdicts = %d of total %d, want the 2 most recent of 5", len(state.Verdicts), state.VerdictsTotal)
	}
	if state.Verdicts[0].ListingID != ids[3] || state.Verdicts[1].ListingID != ids[4] {
		t.Fatalf("truncated history = %+v, want the last two oldest-first", state.Verdicts)
	}
	if state.LatestVerdictID != state.Verdicts[1].ID || state.LatestVerdictID <= state.Verdicts[0].ID {
		t.Fatalf("latest_verdict_id = %d, want the newest verdict's id %d", state.LatestVerdictID, state.Verdicts[1].ID)
	}
	full := decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if len(full.Verdicts) != 5 || full.LatestVerdictID != state.LatestVerdictID {
		t.Fatalf("default limit returned %d verdicts (latest %d)", len(full.Verdicts), full.LatestVerdictID)
	}
	if msg := h.mustFail("update_rubric", map[string]any{"content": "x", "through_verdict_id": ids[4] + 1000}); !strings.Contains(msg, "not listing ids") {
		t.Fatalf("future through_verdict_id error = %q, want the id-provenance hint", msg)
	}
}

func TestGetStateRoundTripsVerdictsAndRubric(t *testing.T) {
	h := newHarness(t)
	id := h.apply(newSource("1001"), baseTime)

	state := decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if state.Rubric != nil || state.RubricStale || len(state.Verdicts) != 0 {
		t.Fatalf("fresh state = %+v, want no rubric, not stale, no verdicts", state)
	}
	if state.Profile.ListingType != string(domain.ListingSale) {
		t.Fatalf("default profile listing type = %q, want sale", state.Profile.ListingType)
	}

	recorded := decodeResult[recordVerdictResult](t, h.mustCall("record_verdict", map[string]any{
		"listing_id": int64(id), "verdict": "love", "note": "great light",
	}))
	if recorded.Verdict.Verdict != "love" || recorded.Verdict.Note != "great light" {
		t.Fatalf("recorded verdict = %+v", recorded.Verdict)
	}

	state = decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if !state.RubricStale {
		t.Fatal("rubric_stale = false after a verdict with no rubric, want true")
	}
	if len(state.Verdicts) != 1 || state.Verdicts[0].ListingID != int64(id) {
		t.Fatalf("verdict history = %+v", state.Verdicts)
	}
	latest := state.Verdicts[0].ID

	saved := decodeResult[updateRubricResult](t, h.mustCall("update_rubric", map[string]any{
		"content": "Loves bright corner units.", "through_verdict_id": latest,
	}))
	if saved.Rubric.ThroughVerdictID != latest {
		t.Fatalf("saved rubric through = %d, want %d", saved.Rubric.ThroughVerdictID, latest)
	}

	state = decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if state.RubricStale {
		t.Fatal("rubric_stale = true immediately after update_rubric, want false")
	}
	if state.Rubric == nil || state.Rubric.Content != "Loves bright corner units." {
		t.Fatalf("state rubric = %+v", state.Rubric)
	}

	h.mustCall("record_verdict", map[string]any{"listing_id": int64(id), "verdict": "maybe"})
	state = decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if !state.RubricStale {
		t.Fatal("rubric_stale = false after a newer verdict, want true")
	}
	if len(state.Verdicts) != 2 {
		t.Fatalf("verdict history = %+v, want both verdicts retained", state.Verdicts)
	}
}

func TestUpdateRubricRejectsFutureVerdict(t *testing.T) {
	h := newHarness(t)

	msg := h.mustFail("update_rubric", map[string]any{"content": "guesswork", "through_verdict_id": 99})
	if !strings.Contains(msg, "get_state") {
		t.Fatalf("future-verdict error = %q, want it to point at get_state", msg)
	}

	if msg := h.mustFail("update_rubric", map[string]any{"content": "  ", "through_verdict_id": 0}); msg == "" {
		t.Fatal("empty rubric content was accepted")
	}
}

func TestSearchListingsExcerptsAndFlagsPriceDrops(t *testing.T) {
	h := newHarness(t)

	long := newSource("1001")
	long.Property.Description = strings.TrimSpace(strings.Repeat("beautifully renovated prewar corner unit with southern exposure ", 8))
	dropped := h.apply(long, baseTime)

	lower := newSource("1001")
	lower.Property.Description = long.Property.Description
	lower.Property.Price = money(825_000)
	h.apply(lower, baseTime.Add(time.Hour))

	steady := h.apply(newSource("1002"), baseTime)

	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{}))
	if res.Count != 2 || len(res.Listings) != 2 {
		t.Fatalf("search returned %d rows, want 2", res.Count)
	}
	if res.EffectiveFilters.ListingType != string(domain.ListingSale) || res.EffectiveFilters.Limit != 40 {
		t.Fatalf("effective filters = %+v, want sale/40", res.EffectiveFilters)
	}

	rows := rowsByID(res.Listings)
	drop, steadyRow := rows[int64(dropped)], rows[int64(steady)]
	if !drop.PriceDrop {
		t.Error("price_drop = false for a listing whose newest price is lower")
	}
	if steadyRow.PriceDrop {
		t.Error("price_drop = true for a listing with only an initial price")
	}
	if got := ptr.Deref(drop.MonthlyCarrying); got != 1500 {
		t.Errorf("monthly_carrying = %d, want 1500 (maintenance + taxes)", got)
	}
	if drop.Address != "123 Prospect Park West #4B, Park Slope" {
		t.Errorf("address = %q", drop.Address)
	}
	if !strings.HasSuffix(drop.DescriptionExcerpt, "…") {
		t.Errorf("long description was not truncated: %q", drop.DescriptionExcerpt)
	}
	if n := len([]rune(drop.DescriptionExcerpt)); n > 201 {
		t.Errorf("excerpt is %d runes, want ~200 plus the ellipsis", n)
	}
	if strings.HasSuffix(strings.TrimSuffix(drop.DescriptionExcerpt, "…"), " ") {
		t.Errorf("excerpt was not trimmed at a word boundary: %q", drop.DescriptionExcerpt)
	}
	if steadyRow.DescriptionExcerpt != "sunny corner unit" {
		t.Errorf("short description was altered: %q", steadyRow.DescriptionExcerpt)
	}
	if steadyRow.URL != "https://streeteasy.com/sale/1002" {
		t.Errorf("compact row url = %q, want the shareable listing link", steadyRow.URL)
	}
}

func TestSearchListingsAppliesAndEchoesFilters(t *testing.T) {
	h := newHarness(t)

	cheap := newSource("1001")
	cheap.Property.Price = money(700_000)
	cheapID := h.apply(cheap, baseTime)
	h.apply(newSource("1002"), baseTime)

	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{
		"max_price": 750_000, "min_beds": 2, "property_type": "coop", "limit": 5,
	}))
	if res.Count != 1 || res.Listings[0].ID != int64(cheapID) {
		t.Fatalf("filtered search = %+v, want only listing %d", res.Listings, cheapID)
	}
	if got := ptr.Deref(res.EffectiveFilters.MaxPrice); got != 750_000 {
		t.Errorf("effective max_price = %d", got)
	}
	if !slices.Equal(res.EffectiveFilters.PropertyTypes, []string{"coop"}) {
		t.Errorf("effective property_types = %v", res.EffectiveFilters.PropertyTypes)
	}
	if res.EffectiveFilters.Limit != 5 {
		t.Errorf("effective limit = %d, want 5", res.EffectiveFilters.Limit)
	}

	// The store cap is reported, not the caller's request.
	res = decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{"limit": 1000}))
	if res.EffectiveFilters.Limit != 500 {
		t.Errorf("effective limit for an over-cap request = %d, want 500", res.EffectiveFilters.Limit)
	}

	if msg := h.mustFail("search_listings", map[string]any{"property_type": "castle"}); !strings.Contains(msg, "coop") {
		t.Errorf("bad property type error = %q, want the allowed values", msg)
	}
}

func TestSearchListingsListingType(t *testing.T) {
	h := newHarness(t)

	saleID := h.apply(newSource("1001"), baseTime)
	rent := newSource("1002")
	rent.Property.ListingType = domain.ListingRent
	rent.Property.Price = money(4_500)
	rentID := h.apply(rent, baseTime)

	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{}))
	if res.Count != 1 || res.Listings[0].ID != int64(saleID) {
		t.Fatalf("default search = %+v, want only sale listing %d", res.Listings, saleID)
	}
	res = decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{"listing_type": "rent"}))
	if res.Count != 1 || res.Listings[0].ID != int64(rentID) {
		t.Fatalf("rent search = %+v, want only rental %d", res.Listings, rentID)
	}
	if res.EffectiveFilters.ListingType != "rent" {
		t.Errorf("effective listing_type = %q, want rent", res.EffectiveFilters.ListingType)
	}
	if msg := h.mustFail("search_listings", map[string]any{"listing_type": "timeshare"}); !strings.Contains(msg, "sale") {
		t.Errorf("bad listing type error = %q, want the allowed values", msg)
	}
	// The text block leads with the id and marks rent as monthly so a text-only host cannot misread $4,500 as a sale price.
	text := resultText(h.mustCall("get_listing", map[string]any{"id": int64(rentID), "photos": "none"}))
	if !strings.HasPrefix(text, fmt.Sprintf("#%d ", rentID)) || !strings.Contains(text, "$4,500/mo ·") {
		t.Errorf("rent text = %q, want #id prefix and $4,500/mo", text)
	}
	if text := resultText(h.mustCall("get_listing", map[string]any{"id": int64(saleID), "photos": "none"})); strings.Contains(text, "/mo ·") {
		t.Errorf("sale text = %q, must not mark the price monthly", text)
	}
	// An empty photos string means "unset" to a model and gets the default.
	if res := h.call("get_listing", map[string]any{"id": int64(saleID), "photos": ""}); res.IsError {
		t.Errorf("photos=\"\" rejected: %s", resultText(res))
	}
}

func TestSearchListingsPagesWholeCorpus(t *testing.T) {
	h := newHarness(t)

	const total = 7
	for i := 0; i < total; i++ {
		// Distinct material_changed_at so ordering is stable across pages.
		h.apply(newSource(fmt.Sprintf("20%02d", i)), baseTime.Add(time.Duration(i)*time.Hour))
	}

	seen := map[int64]int{}
	pages := 0
	for offset := 0; ; {
		res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{
			"limit": 3, "offset": offset,
		}))
		if res.Total != total {
			t.Fatalf("total = %d, want %d", res.Total, total)
		}
		if res.Offset != offset {
			t.Fatalf("echoed offset = %d, want %d", res.Offset, offset)
		}
		pages++
		for _, row := range res.Listings {
			seen[row.ID]++
		}
		offset += len(res.Listings)
		if len(res.Listings) == 0 || offset >= res.Total {
			break
		}
	}
	if len(seen) != total {
		t.Fatalf("swept %d distinct ids, want %d", len(seen), total)
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("listing %d appeared %d times across pages, want exactly once", id, n)
		}
	}
	if pages < 3 {
		t.Fatalf("swept the corpus in %d pages of 3, want at least 3", pages)
	}
}

func TestSearchListingsIncludeInactive(t *testing.T) {
	h := newHarness(t)

	active := h.apply(newSource("3001"), baseTime)
	sold := newSource("3002")
	sold.Property.Status = domain.StatusSold
	sold.SourceStatus = "closed"
	soldID := h.apply(sold, baseTime.Add(time.Hour))

	// Default: active only.
	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{}))
	if res.Total != 1 || len(res.Listings) != 1 || res.Listings[0].ID != int64(active) {
		t.Fatalf("default search = %+v, want only the active listing %d", res.Listings, active)
	}

	// include_inactive: sold listing appears too.
	res = decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{"include_inactive": true}))
	if res.Total != 2 || len(res.Listings) != 2 {
		t.Fatalf("include_inactive search returned %d rows (total %d), want 2", len(res.Listings), res.Total)
	}
	rows := rowsByID(res.Listings)
	if _, ok := rows[int64(soldID)]; !ok {
		t.Fatalf("sold listing %d missing from include_inactive results", soldID)
	}
}

func TestRecordVerdictsBatch(t *testing.T) {
	h := newHarness(t)
	a := h.apply(newSource("4001"), baseTime)
	b := h.apply(newSource("4002"), baseTime.Add(time.Hour))

	res := decodeResult[recordVerdictsResult](t, h.mustCall("record_verdicts", map[string]any{
		"verdicts": []map[string]any{
			{"listing_id": int64(a), "verdict": "love", "note": "bright"},
			{"listing_id": int64(b), "verdict": "dislike"},
		},
	}))
	if res.Count != 2 || len(res.Recorded) != 2 {
		t.Fatalf("recorded %d verdicts, want 2", res.Count)
	}

	state := decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if len(state.Verdicts) != 2 {
		t.Fatalf("verdict history = %d, want 2", len(state.Verdicts))
	}

	// One bad id: the valid id is recorded and returned, the bad one is in failed.
	c := h.apply(newSource("4003"), baseTime.Add(2*time.Hour))
	res = decodeResult[recordVerdictsResult](t, h.mustCall("record_verdicts", map[string]any{
		"verdicts": []map[string]any{
			{"listing_id": int64(c), "verdict": "maybe"},
			{"listing_id": int64(999999), "verdict": "love"},
		},
	}))
	if res.Count != 1 || res.FailedCount != 1 || len(res.Failed) != 1 {
		t.Fatalf("partial batch = %+v, want 1 recorded and 1 failed", res)
	}
	if f := res.Failed[0]; f.Index != 1 || f.ListingID != 999999 || !strings.Contains(f.Error, "does not exist") {
		t.Fatalf("failed item = %+v, want index 1, listing 999999, 'does not exist'", f)
	}
	state = decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if len(state.Verdicts) != 3 {
		t.Fatalf("verdict history after partial batch = %d, want 3 (the valid id was still recorded)", len(state.Verdicts))
	}
}

func TestGetCandidatesRespectsProfileShownAndVerdicts(t *testing.T) {
	h := newHarness(t)

	cheap := newSource("1001")
	cheap.Property.Price = money(700_000)
	cheapID := h.apply(cheap, baseTime)

	mid := newSource("1002")
	mid.Property.Price = money(800_000)
	midID := h.apply(mid, baseTime.Add(time.Hour))

	pricey := newSource("1003")
	pricey.Property.Price = money(2_000_000)
	h.apply(pricey, baseTime.Add(2*time.Hour))

	h.mustCall("set_profile", map[string]any{"max_price": 900_000})

	res := decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if !slices.Equal(candidateIDs(res), []int64{int64(midID), int64(cheapID)}) {
		t.Fatalf("candidates = %v, want [%d %d] in material-change order", candidateIDs(res), midID, cheapID)
	}

	marked := decodeResult[markShownResult](t, h.mustCall("mark_shown", map[string]any{
		"ids": []int64{int64(midID), int64(midID)},
	}))
	if marked.Marked != 1 {
		t.Fatalf("marked = %d for a duplicated id, want 1", marked.Marked)
	}
	unknown := decodeResult[markShownResult](t, h.mustCall("mark_shown", map[string]any{
		"ids": []int64{int64(midID), 999999},
	}))
	if unknown.Marked != 1 {
		t.Fatalf("marked = %d with one unknown id, want 1 (unknown ids are skipped)", unknown.Marked)
	}

	res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if !slices.Equal(candidateIDs(res), []int64{int64(cheapID)}) {
		t.Fatalf("candidates after mark_shown = %v, want only %d", candidateIDs(res), cheapID)
	}

	h.mustCall("record_verdict", map[string]any{"listing_id": int64(cheapID), "verdict": "dislike"})
	res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if res.Count != 0 {
		t.Fatalf("candidates after a verdict = %v, want none", candidateIDs(res))
	}

	// get_candidates itself must never mark anything shown.
	drop := newSource("1002")
	drop.Property.Price = money(775_000)
	h.apply(drop, baseTime.Add(3*time.Hour))
	for range 2 {
		res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
		if !slices.Equal(candidateIDs(res), []int64{int64(midID)}) {
			t.Fatalf("resurfaced candidates = %v, want %d on every read", candidateIDs(res), midID)
		}
	}

	// A change that lands between the read and mark_shown is not swallowed
	// when the read's queried_at is passed back as as_of.
	readAt, err := time.Parse(time.RFC3339, res.QueriedAt)
	if err != nil || readAt.IsZero() {
		t.Fatalf("queried_at = %q, want an RFC 3339 instant (%v)", res.QueriedAt, err)
	}
	drop.Property.Price = money(750_000)
	h.apply(drop, time.Now().Add(time.Minute))
	h.mustCall("mark_shown", map[string]any{"ids": []int64{int64(midID)}, "as_of": res.QueriedAt})
	res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if !slices.Equal(candidateIDs(res), []int64{int64(midID)}) {
		t.Fatalf("candidates after mark_shown as_of = %v, want %d resurfaced by the later drop", candidateIDs(res), midID)
	}
	// ...while a change that landed just BEFORE the read is covered by it: a
	// second-truncated queried_at would sort it after the read and resurface it.
	drop.Property.Price = money(740_000)
	h.apply(drop, time.Now())
	res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	h.mustCall("mark_shown", map[string]any{"ids": []int64{int64(midID)}, "as_of": res.QueriedAt})
	res = decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if res.Count != 0 {
		t.Fatalf("candidates after mark_shown as_of = %v, want none (the change predates the read)", candidateIDs(res))
	}
	if msg := h.mustFail("mark_shown", map[string]any{"ids": []int64{int64(midID)}, "as_of": "yesterday"}); !strings.Contains(msg, "RFC 3339") {
		t.Fatalf("bad as_of error = %q", msg)
	}
}

func TestGetListingReturnsDetailAndCachedPhotos(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	sp := newSource("1001")
	sp.PhotoURLs = []string{
		"https://photos.example/1001/1.jpg",
		"https://photos.example/1001/2.jpg",
		"https://photos.example/1001/3.jpg",
	}
	id := h.apply(sp, baseTime)

	drop := sp
	drop.Property.Price = money(850_000)
	h.apply(drop, baseTime.Add(time.Hour))

	want := writeThumbnail(t, h.thumbs, "streeteasy/ab/abcdef.jpg")
	if err := h.store.SetPhotoCache(ctx, id, 0, "https://photos.example/1001/1.jpg", "streeteasy/ab/abcdef.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}
	// Cached in the database but the file is gone: a skipped photo, not an error.
	if err := h.store.SetPhotoCache(ctx, id, 1, "https://photos.example/1001/2.jpg", "streeteasy/cd/missing.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}

	// The default get_listing call returns an inline contact sheet, so the naive
	// call lets the model actually see the listing.
	res := h.mustCall("get_listing", map[string]any{"id": int64(id)})
	if len(imageBlocks(res)) == 0 {
		t.Fatal("default get_listing returned no inline images; the contact sheet should be the default")
	}

	// photos="none" is the data-only path: no images.
	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "none"})
	detail := decodeResult[listingDetailResult](t, res)
	if len(imageBlocks(res)) != 0 {
		t.Fatal("get_listing returned images with photos=none")
	}
	// Position 2 was never cached: not a failure, so it counts in neither photos_cached nor photo_missing.
	if detail.PhotoCount != 3 || detail.PhotosCached != 1 || detail.PhotosReturned != 0 || detail.PhotoMissing != 1 {
		t.Fatalf("photo metadata without photos = %+v", detail)
	}
	if detail.Description != "sunny corner unit" {
		t.Errorf("description = %q", detail.Description)
	}
	if detail.Provenance.Provider != testProvider || detail.Provenance.ProviderID != "1001" {
		t.Errorf("provenance = %+v", detail.Provenance)
	}
	// The listing link is top-level (the server instructions tell the model to
	// share it) and closes the text block so a text-only host still gets it.
	if detail.URL != sp.URL || !strings.HasSuffix(resultText(res), sp.URL) {
		t.Errorf("url = %q, text = %q; want %q top-level and at the end of the text", detail.URL, resultText(res), sp.URL)
	}
	if len(detail.PriceHistory) != 2 || detail.PriceHistory[0].Price != 900_000 || detail.PriceHistory[1].Price != 850_000 {
		t.Errorf("price history = %+v, want both prices oldest first", detail.PriceHistory)
	}

	// Default photos=true: readable thumbnails come back as resource_link blocks pointing at the public /img proxy, not base64 image blocks.
	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "links"})
	detail = decodeResult[listingDetailResult](t, res)
	if len(imageBlocks(res)) != 0 {
		t.Fatal("default photos=true returned base64 image blocks, want resource links")
	}
	links := resourceLinks(res)
	if len(links) != 1 {
		t.Fatalf("got %d resource links, want 1 readable thumbnail", len(links))
	}
	if links[0].URI != testPublicBase+"/img/streeteasy/ab/abcdef.jpg" {
		t.Errorf("resource link URI = %q, want the public /img URL", links[0].URI)
	}
	if links[0].MIMEType != "image/jpeg" {
		t.Errorf("resource link mime type = %q", links[0].MIMEType)
	}
	if links[0].Annotations == nil || len(links[0].Annotations.Audience) == 0 || links[0].Annotations.Audience[0] != "user" {
		t.Errorf("resource link annotations = %+v, want audience [user]", links[0].Annotations)
	}
	if detail.PhotosReturned != 1 || detail.PhotosCached != 1 || detail.PhotoMissing != 1 {
		t.Fatalf("photo metadata with photos = %+v, want 1 returned, 1 cached, 1 missing", detail)
	}
	if resultText(res) == "" {
		t.Error("photo result dropped the textual summary block")
	}

	// photos="individual" returns base64 image blocks, one per photo.
	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "individual"})
	images := imageBlocks(res)
	if len(images) != 1 {
		t.Fatalf("got %d image blocks with photos=individual, want 1", len(images))
	}
	if !bytes.Equal(images[0].Data, want) {
		t.Error("inline image block does not carry the cached thumbnail bytes")
	}
	if len(resourceLinks(res)) != 0 {
		t.Error("photos=individual still emitted resource links")
	}
	if resultText(res) == "" {
		t.Error("inline photo result dropped the textual summary block")
	}
}

func TestRecordVerdictNudgesPhotoInspection(t *testing.T) {
	h := newHarness(t)

	// A listing with cached photos: the verdict result reminds the model to look.
	withPhotos := seedPhotos(t, h, 3)
	r := decodeResult[recordVerdictResult](t, h.mustCall("record_verdict", map[string]any{
		"listing_id": int64(withPhotos), "verdict": "maybe", "note": "on paper",
	}))
	if !strings.Contains(r.InspectPhotos, "get_listing") {
		t.Errorf("inspect_photos = %q, want a reminder to look at the photos", r.InspectPhotos)
	}

	// A listing with no cached photos: no nudge.
	noPhotos := h.apply(newSource("1002"), baseTime)
	r = decodeResult[recordVerdictResult](t, h.mustCall("record_verdict", map[string]any{
		"listing_id": int64(noPhotos), "verdict": "love",
	}))
	if r.InspectPhotos != "" {
		t.Errorf("inspect_photos = %q for a listing with no photos, want empty", r.InspectPhotos)
	}
}

// seedPhotos caches n thumbnails for one listing and returns its id.
func seedPhotos(t *testing.T, h *harness, n int) domain.PropertyID {
	t.Helper()
	ctx := context.Background()

	sp := newSource("1001")
	sp.PhotoURLs = nil
	for i := range n {
		sp.PhotoURLs = append(sp.PhotoURLs, fmt.Sprintf("https://photos.example/1001/%d.jpg", i))
	}
	id := h.apply(sp, baseTime)
	for i := range n {
		rel := fmt.Sprintf("streeteasy/ab/photo-%d.jpg", i)
		// Alternating shapes so tiles have to be letterboxed both ways.
		w, hgt := 800, 600
		if i%3 == 1 {
			w, hgt = 600, 800
		}
		writeThumbnailSize(t, h.thumbs, rel, w, hgt)
		if err := h.store.SetPhotoCache(ctx, id, i, sp.PhotoURLs[i], rel, "image/jpeg", w, hgt); err != nil {
			t.Fatalf("set photo cache: %v", err)
		}
	}
	return id
}

func TestGetListingsPhotosBulkConstructableURLs(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	// Listing A: three photo slots, positions 0 and 2 cached, 1 left uncached, so the URLs must follow the actual cached positions, not a 0..k-1 run.
	spA := newSource("2001")
	spA.PhotoURLs = []string{
		"https://photos.example/2001/0.jpg",
		"https://photos.example/2001/1.jpg",
		"https://photos.example/2001/2.jpg",
	}
	idA := h.apply(spA, baseTime)
	writeThumbnail(t, h.thumbs, "streeteasy/ab/a0.jpg")
	writeThumbnail(t, h.thumbs, "streeteasy/ab/a2.jpg")
	if err := h.store.SetPhotoCache(ctx, idA, 0, "https://photos.example/2001/0.jpg", "streeteasy/ab/a0.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}
	if err := h.store.SetPhotoCache(ctx, idA, 2, "https://photos.example/2001/2.jpg", "streeteasy/ab/a2.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}

	// Listing B: has photo slots but none cached yet -> zero URLs, not an error.
	spB := newSource("2002")
	spB.PhotoURLs = []string{"https://photos.example/2002/0.jpg"}
	idB := h.apply(spB, baseTime)

	res := h.mustCall("get_listings_photos", map[string]any{"ids": []int64{int64(idA), int64(idB), 999999}})
	out := decodeResult[listingsPhotosResult](t, res)
	if out.Count != 3 {
		t.Fatalf("count = %d, want 3 (both real ids plus the unknown one)", out.Count)
	}

	byID := map[int64]listingPhotosResult{}
	for _, l := range out.Listings {
		byID[l.ID] = l
	}
	a := byID[int64(idA)]
	if a.PhotoCount != 3 || a.PhotosCached != 2 {
		t.Fatalf("listing A counts = %+v, want photo_count 3 photos_cached 2", a)
	}
	// Dense: the uncached slot 1 does not leave a gap, and /1 resolves to the photo at position 2.
	wantURIs := []string{
		testPublicBase + "/img/l/" + fmt.Sprint(idA) + "/0",
		testPublicBase + "/img/l/" + fmt.Sprint(idA) + "/1",
	}
	if !slices.Equal(a.ImageURIs, wantURIs) {
		t.Fatalf("listing A image_uris = %v, want %v (dense over cached photos)", a.ImageURIs, wantURIs)
	}
	if key, err := h.store.PhotoKeyAt(ctx, idA, 1); err != nil || key != "streeteasy/ab/a2.jpg" {
		t.Fatalf("/img/l/%d/1 resolves to %q, %v; want the photo at position 2", idA, key, err)
	}
	b := byID[int64(idB)]
	if b.PhotoCount != 1 || b.PhotosCached != 0 || len(b.ImageURIs) != 0 {
		t.Fatalf("listing B = %+v, want photo_count 1, no cached URLs", b)
	}
	unknown := byID[999999]
	if unknown.PhotoCount != 0 || len(unknown.ImageURIs) != 0 {
		t.Fatalf("unknown id = %+v, want zero counts and no URLs", unknown)
	}
}

func TestSearchListingsRowsCarryPhotoCounts(t *testing.T) {
	h := newHarness(t)
	id := seedPhotos(t, h, 4)

	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{}))
	var found bool
	for _, row := range res.Listings {
		if row.ID == int64(id) {
			found = true
			if row.PhotoCount != 4 || row.PhotosCached != 4 {
				t.Fatalf("compact row photo counts = %+v, want photo_count 4 photos_cached 4", row)
			}
		}
	}
	if !found {
		t.Fatalf("seeded listing %d not in search rows", id)
	}
}

func TestGetListingPhotoCaps(t *testing.T) {
	h := newHarness(t)
	id := seedPhotos(t, h, 14)

	// Links cost no bytes, so they share the 30-photo contact-sheet cap.
	res := h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "links", "max_photos": 50})
	if got := len(resourceLinks(res)); got != 14 {
		t.Fatalf("max_photos=50 returned %d links, want all 14 (cap 30)", got)
	}
	detail := decodeResult[listingDetailResult](t, res)
	if detail.PhotosReturned != 14 || detail.PhotosCached != 14 || detail.PhotoMissing != 0 {
		t.Fatalf("link photo metadata = %+v, want 14 returned of 14 cached", detail)
	}

	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "links"})
	if got := len(resourceLinks(res)); got != 6 {
		t.Fatalf("max_photos unset returned %d links, want the default of 6", got)
	}

	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "links", "max_photos": 2})
	links := resourceLinks(res)
	if len(links) != 2 {
		t.Fatalf("max_photos=2 returned %d links", len(links))
	}
	for i, link := range links {
		if link.MIMEType != "image/jpeg" || !strings.HasPrefix(link.URI, testPublicBase+"/img/") {
			t.Errorf("resource link %d = %q at %q", i, link.MIMEType, link.URI)
		}
	}

	// Inline base64 images keep the hard cap of 10.
	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "individual", "max_photos": 50})
	if got := len(imageBlocks(res)); got != 10 {
		t.Fatalf("inline max_photos=50 returned %d images, want the hard cap of 10", got)
	}
}

func TestGetListingContactSheet(t *testing.T) {
	h := newHarness(t)
	id := seedPhotos(t, h, 14)

	res := h.mustCall("get_listing", map[string]any{
		"id": int64(id), "photos": "contact_sheet", "max_photos": 14,
	})
	detail := decodeResult[listingDetailResult](t, res)
	sheets := imageBlocks(res)

	if detail.PhotoLayout != "contact_sheet" || detail.Sheets != 2 || len(sheets) != 2 {
		t.Fatalf("contact sheet metadata = %+v with %d image blocks", detail, len(sheets))
	}
	if detail.PhotoCount != 14 || detail.PhotosCached != 14 || detail.PhotosReturned != 14 || detail.PhotoMissing != 0 {
		t.Fatalf("contact sheet photo counts = %+v", detail)
	}
	if resultText(res) == "" {
		t.Error("contact sheet result dropped the textual detail block")
	}

	wantRanges := [][]int{{0, 11}, {12, 13}}
	wantPhotos := []int{12, 2}
	wantHeights := []int{4 * 300, 1 * 300}
	for i, sheet := range detail.PhotoSheets {
		if sheet.Sheet != i+1 || !slices.Equal(sheet.Positions, wantRanges[i]) || sheet.Photos != wantPhotos[i] {
			t.Errorf("photo_sheets[%d] = %+v, want sheet %d over %v", i, sheet, i+1, wantRanges[i])
		}
		if sheets[i].MIMEType != "image/jpeg" || len(sheets[i].Data) == 0 {
			t.Fatalf("sheet %d block = %q with %d bytes", i+1, sheets[i].MIMEType, len(sheets[i].Data))
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(sheets[i].Data))
		if err != nil {
			t.Fatalf("decode sheet %d: %v", i+1, err)
		}
		if format != "jpeg" || cfg.Width != 1200 || cfg.Height != wantHeights[i] {
			t.Errorf("sheet %d is %s %dx%d, want jpeg 1200x%d", i+1, format, cfg.Width, cfg.Height, wantHeights[i])
		}
	}

	// The default contact-sheet budget is 12 photos (one sheet of 4 rows here).
	res = h.mustCall("get_listing", map[string]any{"id": int64(id), "photos": "contact_sheet"})
	detail = decodeResult[listingDetailResult](t, res)
	if detail.Sheets != 1 || detail.PhotosReturned != 12 || len(imageBlocks(res)) != 1 {
		t.Fatalf("default contact sheet = %+v with %d blocks", detail, len(imageBlocks(res)))
	}
	if !slices.Equal(detail.PhotoSheets[0].Positions, []int{0, 11}) {
		t.Errorf("default sheet positions = %v, want [0 11]", detail.PhotoSheets[0].Positions)
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(imageBlocks(res)[0].Data)); err != nil {
		t.Fatalf("decode default sheet: %v", err)
	} else if cfg.Width != 1200 || cfg.Height != 4*300 {
		t.Errorf("default sheet is %dx%d, want 1200x1200", cfg.Width, cfg.Height)
	}

	if msg := h.mustFail("get_listing", map[string]any{"id": int64(id), "photos": "mosaic"}); !strings.Contains(msg, "contact_sheet") {
		t.Errorf("bad photos-mode error = %q, want the allowed values", msg)
	}
}

func TestContactSheetSkipsUnreadableThumbnails(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	id := seedPhotos(t, h, 4)

	// Cached in the database, file gone: skipped from the grid, counted missing.
	if err := h.store.SetPhotoCache(ctx, id, 1, "https://photos.example/1001/1.jpg", "streeteasy/cd/missing.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}
	// Present but not a decodable image: also missing, never a tool error.
	rel := "streeteasy/cd/corrupt.jpg"
	full := filepath.Join(h.thumbs, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create thumbnail dir: %v", err)
	}
	if err := os.WriteFile(full, []byte("not a jpeg"), 0o600); err != nil {
		t.Fatalf("write corrupt thumbnail: %v", err)
	}
	if err := h.store.SetPhotoCache(ctx, id, 2, "https://photos.example/1001/2.jpg", rel, "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}

	res := h.mustCall("get_listing", map[string]any{
		"id": int64(id), "photos": "contact_sheet",
	})
	detail := decodeResult[listingDetailResult](t, res)
	if detail.PhotosReturned != 2 || detail.PhotoMissing != 2 || detail.PhotosCached != 2 {
		t.Fatalf("photo counts = %+v, want 2 returned, 2 cached, 2 missing", detail)
	}
	if detail.Sheets != 1 || len(imageBlocks(res)) != 1 {
		t.Fatalf("sheets = %d with %d blocks, want a single sheet", detail.Sheets, len(imageBlocks(res)))
	}
	if !slices.Equal(detail.PhotoSheets[0].Positions, []int{0, 3}) {
		t.Errorf("sheet positions = %v, want the surviving [0 3]", detail.PhotoSheets[0].Positions)
	}
}

func TestUnknownListingIsAToolError(t *testing.T) {
	h := newHarness(t)

	if msg := h.mustFail("get_listing", map[string]any{"id": 4242}); !strings.Contains(msg, "4242") {
		t.Errorf("get_listing error = %q", msg)
	}
	msg := h.mustFail("record_verdict", map[string]any{"listing_id": 4242, "verdict": "love"})
	if !strings.Contains(msg, "4242") {
		t.Errorf("record_verdict error = %q", msg)
	}
	if msg := h.mustFail("record_verdict", map[string]any{"listing_id": 1, "verdict": "adore"}); !strings.Contains(msg, "love") {
		t.Errorf("bad verdict error = %q, want the allowed values", msg)
	}
}

// A neighborhood the corpus never spells that way matches nothing; both the
// search and the saved profile say so instead of returning a silent empty page.
func TestUnknownNeighborhoodsAreFlagged(t *testing.T) {
	h := newHarness(t)
	h.apply(newSource("1001"), baseTime) // Park Slope

	res := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{
		"neighborhoods": []string{"park slope", "Prospect Hts"},
	}))
	if res.Count != 1 || !slices.Equal(res.Unmatched, []string{"Prospect Hts"}) {
		t.Fatalf("count = %d, unmatched = %v; want 1 row and the misspelt name flagged", res.Count, res.Unmatched)
	}
	res = decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{"neighborhoods": []string{"Park Slope"}}))
	if res.Unmatched != nil {
		t.Fatalf("unmatched = %v for a name the corpus carries", res.Unmatched)
	}

	prof := decodeResult[setProfileResult](t, h.mustCall("set_profile", map[string]any{
		"neighborhoods": []string{"Park Slope", "Prospect Hts"},
	}))
	if !slices.Equal(prof.Unmatched, []string{"Prospect Hts"}) {
		t.Fatalf("set_profile unmatched = %v, want the misspelt name", prof.Unmatched)
	}
}

func TestSetProfileUpdatesOnlyTheFieldsGiven(t *testing.T) {
	h := newHarness(t)

	res := decodeResult[setProfileResult](t, h.mustCall("set_profile", map[string]any{
		"max_price":     1_200_000,
		"min_beds":      2,
		"min_baths":     1.5,
		"neighborhoods": []string{"Park Slope", "Carroll Gardens"},
	}))
	if ptr.Deref(res.Profile.MaxPrice) != 1_200_000 || ptr.Deref(res.Profile.MinBeds) != 2 {
		t.Fatalf("initial profile = %+v", res.Profile)
	}

	// Absent fields are untouched.
	res = decodeResult[setProfileResult](t, h.mustCall("set_profile", map[string]any{"min_beds": 3}))
	if ptr.Deref(res.Profile.MinBeds) != 3 {
		t.Errorf("min_beds = %v, want 3", res.Profile.MinBeds)
	}
	if ptr.Deref(res.Profile.MaxPrice) != 1_200_000 {
		t.Errorf("max_price = %v, want the previous 1200000", res.Profile.MaxPrice)
	}
	if !slices.Equal(res.Profile.Neighborhoods, []string{"Park Slope", "Carroll Gardens"}) {
		t.Errorf("neighborhoods = %v, want them preserved", res.Profile.Neighborhoods)
	}

	// Explicit null clears; an empty array clears a list.
	res = decodeResult[setProfileResult](t, h.mustCall("set_profile", map[string]any{
		"max_price": nil, "neighborhoods": []string{},
	}))
	if res.Profile.MaxPrice != nil {
		t.Errorf("max_price = %v after explicit null, want cleared", *res.Profile.MaxPrice)
	}
	if len(res.Profile.Neighborhoods) != 0 {
		t.Errorf("neighborhoods = %v after an empty array, want cleared", res.Profile.Neighborhoods)
	}
	if ptr.Deref(res.Profile.MinBeds) != 3 {
		t.Errorf("min_beds = %v, want the untouched 3", res.Profile.MinBeds)
	}

	// A cleared neighborhood list must not filter everything out.
	sp := newSource("1001")
	sp.Property.Bedrooms = ptr.To(3)
	h.apply(sp, baseTime)
	candidates := decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if candidates.Count != 1 {
		t.Fatalf("candidates after clearing neighborhoods = %d, want 1", candidates.Count)
	}

	if msg := h.mustFail("set_profile", map[string]any{"property_types": []string{"yurt"}}); !strings.Contains(msg, "condo") {
		t.Errorf("bad property type error = %q", msg)
	}
	if msg := h.mustFail("set_profile", map[string]any{"listing_type": "timeshare"}); !strings.Contains(msg, "sale") {
		t.Errorf("bad listing type error = %q", msg)
	}
}

func rowsByID(rows []listingRowResult) map[int64]listingRowResult {
	out := make(map[int64]listingRowResult, len(rows))
	for _, r := range rows {
		out[r.ID] = r
	}
	return out
}

func candidateIDs(res candidatesResult) []int64 {
	out := make([]int64, 0, len(res.Listings))
	for _, r := range res.Listings {
		out = append(out, r.ID)
	}
	return out
}

func writeThumbnail(t *testing.T, dir, rel string) []byte {
	t.Helper()
	return writeThumbnailSize(t, dir, rel, 4, 4)
}

func writeThumbnailSize(t *testing.T, dir, rel string, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 40, A: 255})
		}
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	full := filepath.Join(dir, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("create thumbnail dir: %v", err)
	}
	if err := os.WriteFile(full, buf.Bytes(), 0o600); err != nil {
		t.Fatalf("write thumbnail: %v", err)
	}
	return buf.Bytes()
}

func TestResetStateConfirmGuardAndWipe(t *testing.T) {
	h := newHarness(t)
	id := h.apply(newSource("1001"), baseTime)

	if _, err := h.store.RecordVerdict(context.Background(), id, domain.VerdictLove, "great light"); err != nil {
		t.Fatalf("seed verdict: %v", err)
	}
	h.mustCall("set_profile", map[string]any{"min_beds": 2})
	// An area preference (crawl scope) that a reset must also wipe.
	if _, err := h.store.CreateCrawlTarget(context.Background(), domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{"305"},
		ListingType: domain.ListingSale,
		Status:      domain.TargetPending,
	}); err != nil {
		t.Fatalf("seed crawl target: %v", err)
	}
	// A saved list entry: reset empties lists too and must say so.
	custom, err := h.store.CreateList(context.Background(), "Tour", "")
	if err != nil {
		t.Fatalf("seed list: %v", err)
	}
	if _, err := h.store.AddToList(context.Background(), custom.ID, id, ""); err != nil {
		t.Fatalf("seed list item: %v", err)
	}

	// confirm omitted: a no-op that deletes nothing.
	noop := decodeResult[resetStateResult](t, h.mustCall("reset_state", map[string]any{}))
	if noop.Reset {
		t.Fatalf("reset_state without confirm reported reset=true: %+v", noop)
	}
	if !strings.Contains(noop.Message, "confirm=true") {
		t.Fatalf("no-op message = %q, want it to require confirm=true", noop.Message)
	}
	state := decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if len(state.Verdicts) != 1 {
		t.Fatalf("verdict cleared by a no-op: %+v", state.Verdicts)
	}

	// confirm=true: the taste tables are wiped and counts returned.
	done := decodeResult[resetStateResult](t, h.mustCall("reset_state", map[string]any{"confirm": true}))
	if !done.Reset || done.Cleared.Verdicts != 1 || done.Cleared.Profile != 1 || done.Cleared.CrawlTargets != 1 || done.Cleared.ListItems != 1 {
		t.Fatalf("confirmed reset = %+v, want reset=true with counts", done)
	}
	if !strings.Contains(done.Message, "listings corpus was kept") || !strings.Contains(done.Message, "1 saved list item") {
		t.Fatalf("reset message = %q", done.Message)
	}

	state = decodeResult[stateResult](t, h.mustCall("get_state", map[string]any{}))
	if state.Rubric != nil || state.RubricStale || len(state.Verdicts) != 0 {
		t.Fatalf("post-reset state = %+v, want zero taste state", state)
	}
	if state.Profile.MinBeds != nil || state.Profile.ListingType != string(domain.ListingSale) {
		t.Fatalf("post-reset profile = %+v, want the zero profile", state.Profile)
	}

	// The formerly-verdicted active listing is a fresh candidate again.
	cands := decodeResult[candidatesResult](t, h.mustCall("get_candidates", map[string]any{}))
	if len(cands.Listings) != 1 || cands.Listings[0].ID != int64(id) {
		t.Fatalf("candidates after reset = %+v, want the listing to reappear", cands.Listings)
	}
}

func TestToolAnnotationsAndEnums(t *testing.T) {
	h := newHarness(t)
	res, err := h.session.ListTools(context.Background(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	byName := map[string]*mcpsdk.Tool{}
	for _, tool := range res.Tools {
		byName[tool.Name] = tool
	}
	if a := byName["get_state"].Annotations; !a.ReadOnlyHint || *a.DestructiveHint {
		t.Errorf("get_state annotations = %+v, want read-only", a)
	}
	if a := byName["reset_state"].Annotations; a.ReadOnlyHint || !*a.DestructiveHint {
		t.Errorf("reset_state annotations = %+v, want destructive", a)
	}
	if a := byName["record_verdict"].Annotations; a.ReadOnlyHint || *a.DestructiveHint || a.IdempotentHint {
		t.Errorf("record_verdict annotations = %+v, want additive, non-idempotent", a)
	}

	// The schema, not the handler, rejects a bad verdict.
	out := h.call("record_verdict", map[string]any{"listing_id": 1, "verdict": "meh"})
	if !out.IsError || !strings.Contains(resultText(out), "meh") {
		t.Fatalf("bad verdict = %+v, want schema error naming the value", out)
	}
	out = h.call("record_verdicts", map[string]any{"verdicts": []map[string]any{{"listing_id": 1, "verdict": "nope"}}})
	if !out.IsError || !strings.Contains(resultText(out), "nope") {
		t.Fatalf("bad nested verdict = %+v, want schema error naming the value", out)
	}
	out = h.call("get_listing", map[string]any{"id": 1, "photos": "all"})
	if !out.IsError || !strings.Contains(resultText(out), "all") {
		t.Fatalf("bad photos mode = %+v, want schema error", out)
	}

	// Enums must not break the documented "explicit null clears" contract.
	h.mustCall("set_profile", map[string]any{"listing_type": "rent", "property_types": []string{"condo"}})
	prof := decodeResult[setProfileResult](t, h.mustCall("set_profile", map[string]any{"listing_type": nil, "property_types": nil})).Profile
	if prof.ListingType != "sale" || len(prof.PropertyTypes) != 0 {
		t.Fatalf("null did not clear enum'd fields: %+v", prof)
	}
	h.mustCall("search_listings", map[string]any{"property_type": nil})

	// Column widths are enforced in the service: nothing wraps in the store.
	for _, tool := range []string{"set_profile", "search_listings"} {
		if msg := h.mustFail(tool, map[string]any{"max_price": 3_000_000_000}); !strings.Contains(msg, "exceeds the maximum") {
			t.Errorf("%s huge max_price = %q, want a bounds error", tool, msg)
		}
	}
	if msg := h.mustFail("set_profile", map[string]any{"min_beds": 70000}); !strings.Contains(msg, "exceeds the maximum") {
		t.Errorf("huge min_beds = %q, want a bounds error", msg)
	}
}
