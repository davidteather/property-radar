package listings

import (
	"strings"

	"github.com/davidteather/property-radar/internal/domain"
)

// Row is the compact candidate/search row: domain-typed, no wire tags.
type Row struct {
	ID                 domain.PropertyID
	Address            string
	Neighborhood       string
	Price              *domain.Money
	Beds               *int
	Baths              *float64
	Sqft               *int
	PropertyType       domain.PropertyType
	ListingType        domain.ListingType
	MonthlyCarrying    *domain.Money
	DOM                *int
	DescriptionExcerpt string
	PriceDrop          bool
	Status             domain.PropertyStatus
	URL                string
	// PhotoCount is every photo the listing has; PhotosCached is how many carry a cached thumbnail and are serveable as {base}/img/l/{id}/{n} for n in 0..PhotosCached-1.
	PhotoCount   int
	PhotosCached int
}

// ListingPhotos is the bulk photo summary for one listing: total and cached counts, from which a transport mints {base}/img/l/{id}/{n} for n in 0..Cached-1.
type ListingPhotos struct {
	ID     domain.PropertyID
	Total  int
	Cached int
}

// State retires the store's five-return get_state signature at the Service boundary.
type State struct {
	Profile  domain.Profile
	Rubric   *domain.Rubric
	Stale    bool
	Verdicts []domain.Verdict
}

// SearchQuery is a search request; an empty ListingType defaults to sale.
type SearchQuery struct {
	MaxPrice      *domain.Money
	MinBeds       *int
	MinBaths      *float64
	Neighborhoods []string
	PropertyTypes []domain.PropertyType
	ListingType   domain.ListingType
	Limit         int
	// Offset pages the full filtered set; IncludeInactive also returns sold/in-contract/delisted listings instead of active-only.
	Offset          int
	IncludeInactive bool
}

// SearchResult carries the rows plus the filters actually applied. Total is the full filtered count ignoring limit/offset, so a caller pages until Offset+len(Rows) >= Total; Limit is the effective cap.
type SearchResult struct {
	Rows    []Row
	Filters SearchFilter
	Total   int
	Offset  int
	Limit   int
	// UnmatchedNeighborhoods are requested names no listing carries: a typo or a spelling the corpus does not use.
	UnmatchedNeighborhoods []string
}

// ResetCounts reports how many rows each taste and area-preference table lost in a hard reset. The listings corpus and ingest history are never touched, so never counted.
type ResetCounts struct {
	Verdicts     int
	Rubrics      int
	Shown        int
	Profile      int
	ListItems    int
	CrawlTargets int
}

// ResetResult is the outcome of Service.ResetState: the per-table counts wiped.
type ResetResult struct {
	Cleared ResetCounts
}

// VerdictInput is one entry of a batch verdict write.
type VerdictInput struct {
	PropertyID domain.PropertyID
	Kind       domain.VerdictKind
	Note       string
}

// VerdictBatch is the per-item outcome of RecordVerdicts: unknown ids land in
// Failed rather than erroring the call, so Recorded is never lost to a bad id.
type VerdictBatch struct {
	Recorded []domain.Verdict
	Failed   []VerdictFailure
}

type VerdictFailure struct {
	// Index is the item's position in the request batch.
	Index      int
	PropertyID domain.PropertyID
	Message    string
}

// Detail is one listing in full: canonical fields, price history, provenance, and the photo result (bytes/counts populated per PhotoRequest).
type Detail struct {
	Property    domain.Property
	PriceEvents []domain.PriceEvent
	Provenance  ProvenanceSummary
	Photos      PhotoResult
}

// PhotoRequest controls how Listing handles the listing's cached photos. Include reads bytes and builds image/sheet output; Links returns store-key links (no bytes) for the public /img proxy; Count reports cached/missing tallies only (the CLI passes the zero value). Include wins over Links when both are set.
type PhotoRequest struct {
	Include   bool
	Links     bool
	Count     bool
	MaxPhotos int
	Layout    string
}

// PhotoResult is the photo side of a Detail: raw cached-photo metadata plus, when requested, the read bytes/sheets and counts. Encoding to wire image blocks stays in the transport.
type PhotoResult struct {
	All    []domain.Photo
	Layout string
	Counts PhotoCounts
	Images []PhotoImage
	Links  []PhotoLink
	Sheets []Sheet
}

// Opt models a transport-agnostic optional field: Set distinguishes "absent, leave unchanged" from an explicit value (including a nil/empty Val that clears the field).
type Opt[T any] struct {
	Set bool
	Val T
}

// ProfileUpdate is a partial profile edit; only fields with Set are applied.
type ProfileUpdate struct {
	MaxPrice           Opt[*domain.Money]
	MinBeds            Opt[*int]
	MinBaths           Opt[*float64]
	MaxMonthlyCarrying Opt[*domain.Money]
	ListingType        Opt[domain.ListingType]
	Neighborhoods      Opt[[]string]
	PropertyTypes      Opt[[]domain.PropertyType]
}

// CrawlRequest is a queued crawl scope; Areas are raw ids validated by the Service.
type CrawlRequest struct {
	Areas       []string
	ListingType domain.ListingType
	MaxPrice    *domain.Money
	MinBeds     *int
	Note        string
	// Recurring makes this a standing scope the crawler re-crawls about hourly
	// (incremental) and deep-refreshes ~daily, instead of a one-off. Use it when
	// the user wants an area kept fresh, not just crawled once.
	Recurring bool
}

// CrawlRequestResult reports the queued (or already-pending) target and how many one-off requests are now waiting.
type CrawlRequestResult struct {
	Target          domain.CrawlTarget
	AlreadyQueued   bool
	PendingRequests int
}

// An empty (non-nil) slice becomes nil: the store treats a non-null array filter as "match one of these", so [] would match nothing.
func nilIfEmpty[T any](s []T) []T {
	if len(s) == 0 {
		return nil
	}
	return s
}

// cleanNames trims, drops blanks, and dedupes exact-match names such as
// neighborhoods, so a stray space or empty string cannot silently match nothing.
func cleanNames(names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		if n = strings.TrimSpace(n); n != "" {
			out = append(out, n)
		}
	}
	return nilIfEmpty(distinct(out))
}

func distinct[T comparable](ids []T) []T {
	seen := make(map[T]bool, len(ids))
	out := make([]T, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, id)
	}
	return out
}
