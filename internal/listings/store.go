// Package listings holds the transport-independent application logic for the listings corpus: the consumer-owned persistence interface, the query/result types, and a Service both the MCP and REST transports drive. It owns no SQL and no wire encoding.
package listings

import (
	"context"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

const (
	DefaultReadLimit = 40
	MaxReadLimit     = 60
	// MaxBulkLimit is the per-page cap for full-corpus paginated search; higher than MaxReadLimit so a caller can sweep everything in few pages.
	MaxBulkLimit = 500
)

// Store is the consumer-owned view of persistence: only the methods this package calls. The concrete internal/store implements it.
type Store interface {
	State(ctx context.Context) (domain.Profile, *domain.Rubric, bool, []domain.Verdict, error)
	SearchProperties(ctx context.Context, f SearchFilter) ([]domain.Property, error)
	CountProperties(ctx context.Context, f SearchFilter) (int, error)
	Candidates(ctx context.Context, p domain.Profile, limit int) ([]domain.Property, error)
	PropertiesByIDs(ctx context.Context, ids []domain.PropertyID) ([]domain.Property, error)
	GetProperty(ctx context.Context, id domain.PropertyID) (domain.Property, []domain.PriceEvent, []domain.Photo, ProvenanceSummary, error)
	PriceDrops(ctx context.Context, ids []domain.PropertyID) (map[domain.PropertyID]bool, error)
	// PhotoCounts reports, per requested listing, the total photo slots and how many carry a cached thumbnail (cached_path IS NOT NULL). Ids absent from the map have no photo rows. Stamps photo_count/photos_cached on compact rows.
	PhotoCounts(ctx context.Context, ids []domain.PropertyID) (map[domain.PropertyID]PhotoCount, error)
	// PhotoKeyAt returns the cached_path of the listing's n-th cached photo in position order (dense: 0..cached-1 all resolve), or ErrNotFound past the end or for an unknown listing. Backs the public resolver GET /img/l/{id}/{n}.
	PhotoKeyAt(ctx context.Context, id domain.PropertyID, n int) (string, error)
	// MarkShown returns how many of ids exist (unknown ids are skipped); a
	// non-nil asOf caps the material-change snapshot at that instant.
	MarkShown(ctx context.Context, ids []domain.PropertyID, asOf *time.Time) (int, error)
	RecordVerdict(ctx context.Context, id domain.PropertyID, kind domain.VerdictKind, note string) (domain.Verdict, error)
	GetProfile(ctx context.Context) (domain.Profile, error)
	// UpdateProfile runs merge on the stored profile and saves the result atomically; a merge error aborts without saving.
	UpdateProfile(ctx context.Context, merge func(*domain.Profile) error) (domain.Profile, error)
	AppendRubric(ctx context.Context, content string, through domain.VerdictID) (domain.Rubric, error)
	// ResetTasteState wipes verdicts, rubrics, shown markers, and the profile in one transaction; the listings corpus and operational state are untouched.
	ResetTasteState(ctx context.Context) (ResetCounts, error)
	CreateCrawlTarget(ctx context.Context, t domain.CrawlTarget) (domain.CrawlTarget, error)
	ListCrawlTargets(ctx context.Context) ([]domain.CrawlTarget, error)
	// GetCrawlTarget reports ErrCrawlTargetNotFound for an unknown id; RunByID backs the async job-status read.
	GetCrawlTarget(ctx context.Context, id domain.CrawlTargetID) (domain.CrawlTarget, error)
	RunByID(ctx context.Context, id domain.IngestRunID) (domain.IngestRun, error)
	CorpusStats(ctx context.Context) (domain.CorpusStats, error)
	// MatchNeighborhoods returns, lowercased, which of names occur as a listing's neighborhood (any status), so a caller can tell a typo from an empty match.
	MatchNeighborhoods(ctx context.Context, names []string) ([]string, error)
	// SetCrawlTargetEnabled and DeleteCrawlTarget manage existing scopes; both report ErrCrawlTargetNotFound for an unknown id.
	SetCrawlTargetEnabled(ctx context.Context, id domain.CrawlTargetID, enabled bool) error
	DeleteCrawlTarget(ctx context.Context, id domain.CrawlTargetID) error
	// PromoteCrawlTargetToStanding turns a once target into a recurring standing scope (and enables it); ErrCrawlTargetNotFound for an unknown id.
	PromoteCrawlTargetToStanding(ctx context.Context, id domain.CrawlTargetID) error
	// Lists and membership. GetList/GetListBySlug/AddToList/RemoveFromList report ErrListNotFound (AddToList reports whether the listing was newly added; removing a non-member is a no-op); DeleteList reports ErrDefaultList for the favorites list; CreateList reports ErrDuplicateList (with the existing list) on a slug clash.
	Lists(ctx context.Context) ([]domain.List, error)
	GetList(ctx context.Context, id domain.ListID) (domain.List, error)
	GetListBySlug(ctx context.Context, slug string) (domain.List, error)
	CreateList(ctx context.Context, name, emoji string) (domain.List, error)
	DeleteList(ctx context.Context, id domain.ListID) error
	AddToList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID, note string) (bool, error)
	RemoveFromList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID) (bool, error)
	ListMembers(ctx context.Context, listID domain.ListID) ([]domain.Property, error)
	ListsForProperty(ctx context.Context, propertyID domain.PropertyID) ([]domain.List, error)
}

// SearchFilter is an application query: nil fields are unconstrained, and price/bed/bath filters exclude unknown values since an unknown price cannot satisfy a ceiling.
type SearchFilter struct {
	ListingType   domain.ListingType // empty means any
	MaxPrice      *domain.Money
	MinBeds       *int
	MinBaths      *float64
	Neighborhoods []string
	PropertyTypes []domain.PropertyType
	Limit         int
	// Offset skips this many rows for paging; IncludeInactive drops the active-only restriction so sold/in-contract/delisted listings are returned.
	Offset          int
	IncludeInactive bool
}

// PhotoCount is the per-listing photo accounting for compact rows: Total counts every photo slot, Cached counts those with a stored thumbnail.
type PhotoCount struct {
	Total  int
	Cached int
}

// ProvenanceSummary is the application view of where a canonical listing came from; it carries no SQL vocabulary.
type ProvenanceSummary struct {
	Provider     string
	ProviderID   string
	URL          string
	SourceStatus string
	FirstSeenAt  time.Time
	LastSeenAt   time.Time
	FetchedAt    time.Time
	MissingRuns  int
}

// effectiveBulkLimit reports the row cap the bulk search path will actually apply (capped at MaxBulkLimit), so results can echo it back to the caller.
func effectiveBulkLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultReadLimit
	case limit > MaxBulkLimit:
		return MaxBulkLimit
	default:
		return limit
	}
}
