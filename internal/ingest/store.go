package ingest

import (
	"context"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

// RunStats is the terminal state of one crawl. Complete and Suspect together
// gate lifecycle advancement.
type RunStats struct {
	Complete      bool
	ListingsSeen  int
	Created       int
	Updated       int
	PhotoFailures int
	ItemErrors    []domain.RunError
	Suspect       bool
	// EnrichAttempts/EnrichFailures count detail-page fetches; all failing is a blocked crawler, not bad listings.
	EnrichAttempts int
	EnrichFailures int
}

// ApplyResult reports what one applied listing changed. PriceChanged means a
// price_events row was written (including first price); MaterialChanged means
// material_changed_at advanced.
type ApplyResult struct {
	PropertyID domain.PropertyID
	// EvictedKeys are thumbnail keys this observation displaced (photo replaced
	// or removed) that no other row references; the caller deletes the bytes.
	EvictedKeys     []string
	Created         bool
	PriceChanged    bool
	StatusChanged   bool
	MaterialChanged bool
}

// Baseline is the median listings volume over recent clean runs for one scope;
// Runs is how many contributed. The "3 runs, 30%" policy is applied by Service.
type Baseline struct {
	Volume float64
	Runs   int
}

type MissingResult struct {
	Advanced int
	Delisted int
	// EvictedKeys are the content-addressed thumbnail keys freed because their
	// listing was delisted; the caller deletes these bytes from the photo store.
	EvictedKeys []string
}

// EnrichState is what the incremental crawl compares a search row against to
// decide whether the listing's detail page is worth fetching.
type EnrichState struct {
	Exists         bool
	Price          *domain.Money
	Status         domain.PropertyStatus
	HasDescription bool
	// EnrichedAt is when the detail page was last merged in; nil if never.
	EnrichedAt *time.Time
}

// Store is the persistence the crawl workflow consumes. Absence advancement is
// gated here only as a backstop; the policy lives in Service.
type Store interface {
	// StartRun opens a run row carrying the swept scope (hash and filters).
	StartRun(ctx context.Context, provider string, q SearchQuery) (domain.IngestRun, error)
	// EnrichmentState reports the stored snapshot the incremental crawl compares
	// against; Exists=false for a listing the corpus has never seen.
	EnrichmentState(ctx context.Context, provider, providerID string) (EnrichState, error)
	ApplySourceProperty(ctx context.Context, runID domain.IngestRunID, sp SourceProperty, observedAt time.Time) (ApplyResult, error)
	SetPhotoCache(ctx context.Context, id domain.PropertyID, position int, sourceURL, cachedPath, mimeType string, width, height int) error
	BaselineVolume(ctx context.Context, provider, scopeHash string) (Baseline, error)
	FinishRun(ctx context.Context, id domain.IngestRunID, stats RunStats) error
	MarkMissingSources(ctx context.Context, provider string, seenProviderIDs []string, runID domain.IngestRunID) (MissingResult, error)
	// FreeExpiredCachedPhotos NULLs cached_path for up to limit photos not
	// refreshed since cutoff (oldest first) and returns the freed store keys
	// plus the number of rows freed (for batch pagination).
	FreeExpiredCachedPhotos(ctx context.Context, cutoff time.Time, limit int) ([]string, int, error)
	// ForgetPhotoKeys NULLs any row still pointing at keys whose bytes were just
	// deleted, closing the race with a drain that re-pointed a row meanwhile.
	ForgetPhotoKeys(ctx context.Context, keys []string) (int, error)
}
