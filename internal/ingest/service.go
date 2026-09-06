package ingest

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

const (
	DefaultPhotoCap = 10

	// Workers per listing; the per-worker delay lives in ThumbConfig.Delay.
	photoConcurrency = 4

	// A scope needs 3 clean runs before the 30% threshold can mark a run suspect.
	minBaselineRuns = 3
	suspectFraction = 0.30

	// Item errors stored per run are capped; the tail is summarized as a count
	// so a provider-wide outage cannot bloat one ingest_runs row without bound.
	maxItemErrors = 200

	finishTimeout = 15 * time.Second
	// Evicted thumbnails are deleted deleteWorkers at a time, deletePerKey each.
	deleteWorkers = 8
	deletePerKey  = 250 * time.Millisecond
)

// PhotoCacher caches one capped thumbnail per provider photo URL; Delete (by
// store key, idempotent) runs on delist and TTL eviction.
type PhotoCacher interface {
	Cache(ctx context.Context, provider, sourceURL string) (CachedPhoto, error)
	Delete(ctx context.Context, key string) error
	Exists(ctx context.Context, key string) (bool, error)
}

type Options struct {
	PhotoCap int
	Logger   *slog.Logger
	// StandingEvery spaces standing-scope runs; 0 uses DefaultStandingInterval, < 0 runs them every drain.
	StandingEvery time.Duration
}

type Service struct {
	source   Source
	store    Store
	photos   PhotoCacher
	photoCap int
	log      *slog.Logger

	standingEvery time.Duration
}

// NewService builds the crawl service; photos may be nil to disable thumbnail caching.
func NewService(source Source, st Store, photos PhotoCacher, opts Options) *Service {
	if opts.PhotoCap <= 0 {
		opts.PhotoCap = DefaultPhotoCap
	}
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	if opts.StandingEvery == 0 {
		opts.StandingEvery = DefaultStandingInterval
	}
	return &Service{
		source:        source,
		store:         st,
		photos:        photos,
		photoCap:      opts.PhotoCap,
		log:           opts.Logger,
		standingEvery: opts.StandingEvery,
	}
}

type Result struct {
	Run      domain.IngestRun
	Baseline Baseline
	Missing  MissingResult
	// Stats is the in-memory run tally, including counters the run row does not persist.
	Stats RunStats
}

// Run crawls one scope end to end. Result is populated whenever the run row was
// written, so callers can summarize even when Run returns an error.
func (s *Service) Run(ctx context.Context, q SearchQuery) (Result, error) {
	provider := s.source.Name()
	scope := ScopeHash(provider, q)

	run, err := s.store.StartRun(ctx, provider, q)
	if err != nil {
		return Result{}, fmt.Errorf("%w: create ingest run: %w", ErrNotStarted, err)
	}

	stats, seen, crawlErr := s.crawl(ctx, run.ID, q)

	// The run record must still land even when the crawl was cancelled.
	ctx, cancel := finishContext(ctx)
	defer cancel()

	baseline, baselineErr := s.store.BaselineVolume(ctx, provider, scope)
	if baselineErr != nil {
		s.log.Warn("baseline volume unavailable", "provider", provider, "err", baselineErr)
	}
	stats.Suspect = isSuspect(baseline, stats.ListingsSeen)

	res := Result{Run: withStats(run, stats), Baseline: baseline, Stats: stats}
	if stats.EnrichAttempts >= minEnrichForOutage && stats.EnrichFailures == stats.EnrichAttempts {
		s.log.Warn("every detail fetch failed; provider is likely blocking detail pages", "attempts", stats.EnrichAttempts, "scope", scope)
	}
	if err := s.finishRun(ctx, run.ID, stats); err != nil {
		// Joined so an interrupted crawl stays classified as a retry, not a failure.
		return res, errors.Join(fmt.Errorf("finish ingest run %d: %w", run.ID, err), crawlErr)
	}

	// Unknown baseline means the guardrail didn't run, so absence must not advance;
	// nor may an empty page (a provider glitch would otherwise delist a whole scope).
	if baselineErr == nil && stats.Complete && !stats.Suspect && stats.ListingsSeen > 0 {
		missing, err := s.store.MarkMissingSources(ctx, provider, seen, run.ID)
		if err != nil {
			return res, fmt.Errorf("advance missing sources: %w", err)
		}
		res.Missing = missing
		// Delisting freed these thumbnails in the DB; drop the bytes too so a dead
		// listing leaves no archived photos behind.
		s.deleteKeys(ctx, missing.EvictedKeys, "delisted")
	}
	return res, crawlErr
}

// finishRetryWait spaces FinishRun retries; a var so tests can shrink it.
var finishRetryWait = 2 * time.Second

const finishAttempts = 3

// finishRun retries the (idempotent) run record a few times within the finish
// budget: a DB blip here would otherwise report a completed crawl as failed.
func (s *Service) finishRun(ctx context.Context, id domain.IngestRunID, stats RunStats) error {
	var err error
	for attempt := 1; attempt <= finishAttempts; attempt++ {
		if err = s.store.FinishRun(ctx, id, stats); err == nil || ctx.Err() != nil || attempt == finishAttempts {
			return err
		}
		s.log.Warn("finish ingest run failed; retrying", "run_id", int64(id), "attempt", attempt, "err", err)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(finishRetryWait):
		}
	}
	return err
}

// PhotoSweepBatch bounds one TTL-sweep transaction so a large backlog drains
// over several passes rather than one giant DELETE.
const PhotoSweepBatch = 500

// SweepExpiredPhotos evicts cached thumbnails not refreshed within ttl: it frees
// them in the DB (/img stops resolving) and deletes the bytes, making the cache
// a rolling window not an archive. ttl <= 0 disables it. Returns rows evicted.
func (s *Service) SweepExpiredPhotos(ctx context.Context, ttl time.Duration) (int, error) {
	if ttl <= 0 || s.photos == nil {
		return 0, nil
	}
	cutoff := time.Now().Add(-ttl)
	total := 0
	for {
		if err := ctx.Err(); err != nil {
			return total, err
		}
		keys, freed, err := s.store.FreeExpiredCachedPhotos(ctx, cutoff, PhotoSweepBatch)
		if err != nil {
			return total, fmt.Errorf("free expired cached photos: %w", err)
		}
		// Rows are already freed; the bytes must go even if ctx just died.
		dctx, cancel := finishContext(ctx)
		s.deleteKeys(dctx, keys, "expired")
		cancel()
		total += freed
		// A short page means the backlog is drained.
		if freed < PhotoSweepBatch {
			break
		}
	}
	if total > 0 {
		s.log.Info("swept expired thumbnails", "count", total, "ttl", ttl.String())
	}
	return total, nil
}

// deleteKeys removes freed thumbnail bytes from the photo store. Best-effort: a
// failed delete leaves an orphaned object (already unreachable via the DB, which
// no longer has its cached_path) and is logged, never fatal.
func (s *Service) deleteKeys(ctx context.Context, keys []string, reason string) {
	if s.photos == nil || len(keys) == 0 {
		return
	}
	// Its own budget, sized to the batch: a 500-key sweep against S3 outlives
	// the 15s bookkeeping window, and rows are already freed by now.
	budget := max(finishTimeout, deletePerKey*time.Duration(len(keys)))
	dctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), budget)
	defer cancel()
	ok := make([]bool, len(keys))
	sem := make(chan struct{}, deleteWorkers)
	var wg sync.WaitGroup
	for i, key := range keys {
		wg.Add(1)
		sem <- struct{}{}
		go func() {
			defer wg.Done()
			defer func() { <-sem }()
			if err := s.photos.Delete(dctx, key); err != nil {
				s.log.Warn("delete evicted thumbnail", "key", key, "reason", reason, "err", err)
				return
			}
			ok[i] = true
		}()
	}
	wg.Wait()
	deleted := make([]string, 0, len(keys))
	for i, key := range keys {
		if ok[i] {
			deleted = append(deleted, key)
		}
	}
	if len(deleted) == 0 {
		return
	}
	s.log.Info("evicted thumbnails", "count", len(deleted), "reason", reason)
	// A concurrent drain may have re-pointed a row at a key between the DB free
	// and the byte delete; forget those so /img never resolves to missing bytes.
	fctx, cancel := finishContext(ctx)
	defer cancel()
	if n, err := s.store.ForgetPhotoKeys(fctx, deleted); err != nil {
		s.log.Warn("forget deleted thumbnail keys", "reason", reason, "err", err)
	} else if n > 0 {
		s.log.Info("forgot re-pointed thumbnails", "count", n, "reason", reason)
	}
}

func (s *Service) crawl(ctx context.Context, runID domain.IngestRunID, q SearchQuery) (RunStats, []string, error) {
	stats := RunStats{Complete: true}
	seen := []string{}
	var crawlErr error
	errs := itemErrors{}

	for sp, err := range s.source.Search(ctx, q) {
		// Checked first so a dead context does not churn through the rest of
		// an already-decoded page (each step failing fast but logging).
		if ctx.Err() != nil {
			break
		}
		if errors.Is(err, ErrSearchAborted) {
			stats.Complete = false
			errs.add(domain.RunError{Message: err.Error()})
			crawlErr = fmt.Errorf("provider search: %w", err)
			break
		}
		if err != nil {
			errs.add(domain.RunError{ProviderID: sp.ProviderID, Message: err.Error()})
			if sp.ProviderID == "" {
				continue
			}
		}

		stats.ListingsSeen++
		seen = append(seen, sp.ProviderID)
		if errors.Is(err, ErrUnusableListing) {
			continue
		}

		if s.shouldEnrich(ctx, q, sp) {
			stats.EnrichAttempts++
			enriched, err := s.source.EnrichDetail(ctx, sp)
			if err != nil {
				stats.EnrichFailures++
				// Soft: the search-only listing still applies; its missing
				// description makes the next incremental run retry enrichment.
				errs.add(domain.RunError{ProviderID: sp.ProviderID, Message: err.Error()})
				// A search row only ever says "active": without the detail page it
				// must not overturn a status (in_contract, sold) that page reported.
				if sp.Property.Status == domain.StatusActive {
					sp.Property.Status = ""
				}
			} else {
				sp = enriched
			}
		}

		applied, err := s.store.ApplySourceProperty(ctx, runID, sp, time.Now())
		if err != nil {
			errs.add(domain.RunError{ProviderID: sp.ProviderID, Message: err.Error()})
			continue
		}
		if applied.Created {
			stats.Created++
		} else {
			stats.Updated++
		}
		s.deleteKeys(ctx, applied.EvictedKeys, "replaced")
		stats.PhotoFailures += s.cachePhotos(ctx, sp, applied.PropertyID)
	}

	// A dead context is classified from the context itself: a provider's own
	// timeout also reads as DeadlineExceeded but is a failure, not a pause.
	if ctx.Err() != nil {
		stats.Complete = false
		if crawlErr == nil {
			crawlErr = ctx.Err()
		}
		crawlErr = fmt.Errorf("%w: %w", ErrInterrupted, crawlErr)
	}
	stats.ItemErrors = errs.list()
	return stats, seen, crawlErr
}

// itemErrors keeps the first maxItemErrors run errors and counts the rest.
type itemErrors struct {
	kept    []domain.RunError
	dropped int
}

func (e *itemErrors) add(err domain.RunError) {
	if len(e.kept) >= maxItemErrors {
		e.dropped++
		return
	}
	e.kept = append(e.kept, err)
}

func (e *itemErrors) list() []domain.RunError {
	if e.dropped == 0 {
		return e.kept
	}
	return append(e.kept, domain.RunError{Message: fmt.Sprintf("%d more errors not recorded", e.dropped)})
}

// shouldEnrich: deep runs fetch every detail page not merged within DeepInterval
// (so an interrupted deep pass resumes, not restarts); incremental runs only what
// is new, changed, or never enriched. An unreadable state enriches.
func (s *Service) shouldEnrich(ctx context.Context, q SearchQuery, sp SourceProperty) bool {
	state, err := s.store.EnrichmentState(ctx, sp.Provider, sp.ProviderID)
	if err != nil {
		s.log.Warn("enrichment state unavailable; enriching", "provider_id", sp.ProviderID, "err", err)
		return true
	}
	// A listing enriched once and still without a description has none; do not
	// refetch it every run.
	if !state.Exists || (state.EnrichedAt == nil && !state.HasDescription) {
		return true
	}
	if q.Deep && (state.EnrichedAt == nil || time.Since(*state.EnrichedAt) >= DeepInterval) {
		return true
	}
	if !moneyEqual(state.Price, sp.Property.Price) {
		return true
	}
	return sp.Property.Status != "" && sp.Property.Status != state.Status
}

func moneyEqual(a, b *domain.Money) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

type photoResult struct {
	photo     CachedPhoto
	err       error
	attempted bool
}

// Photo failures are soft: they are counted and logged, never fatal to the listing.
func (s *Service) cachePhotos(ctx context.Context, sp SourceProperty, id domain.PropertyID) int {
	if s.photos == nil || id == 0 {
		return 0
	}
	urls := sp.PhotoURLs
	if len(urls) > s.photoCap {
		urls = urls[:s.photoCap]
	}

	failures := 0
	// Fetched concurrently but applied in position order, so stored output
	// never depends on which fetch finished first. Bytes a worker already wrote
	// are recorded even after ctx dies, or they would be orphaned in the store.
	results := s.fetchPhotos(ctx, sp.Provider, urls)
	rctx, cancel := finishContext(ctx)
	defer cancel()
	for position, res := range results {
		if !res.attempted {
			continue
		}
		if res.err != nil {
			if ctx.Err() != nil {
				continue
			}
			failures++
			s.log.Warn("cache photo", "provider_id", sp.ProviderID, "position", position, "err", res.err)
			continue
		}
		if err := s.store.SetPhotoCache(rctx, id, position, urls[position], res.photo.RelPath, res.photo.MIMEType, res.photo.Width, res.photo.Height); err != nil {
			failures++
			s.log.Warn("record photo cache", "provider_id", sp.ProviderID, "position", position, "err", err)
			continue
		}
		if res.photo.Reused && !s.stillCached(rctx, res.photo.RelPath) {
			failures++
			s.log.Warn("thumbnail evicted mid-crawl", "provider_id", sp.ProviderID, "position", position, "key", res.photo.RelPath)
		}
	}
	return failures
}

// stillCached re-checks a reused key after its row is written: a sweep in
// another process may have deleted the bytes between the hit and the write,
// so the row is forgotten rather than left pointing at nothing.
func (s *Service) stillCached(ctx context.Context, key string) bool {
	ok, err := s.photos.Exists(ctx, key)
	if err != nil || ok {
		return true
	}
	if _, err := s.store.ForgetPhotoKeys(ctx, []string{key}); err != nil {
		s.log.Warn("forget evicted thumbnail", "key", key, "err", err)
	}
	return false
}

func (s *Service) fetchPhotos(ctx context.Context, provider string, urls []string) []photoResult {
	results := make([]photoResult, len(urls))
	positions := make(chan int)
	var wg sync.WaitGroup

	for range min(photoConcurrency, len(urls)) {
		wg.Go(func() {
			for position := range positions {
				// Drain rather than return: an early return would block the sender.
				if ctx.Err() != nil {
					continue
				}
				photo, err := s.cacheOne(ctx, provider, urls[position])
				results[position] = photoResult{photo: photo, err: err, attempted: true}
			}
		})
	}
	for position := range urls {
		positions <- position
	}
	close(positions)
	wg.Wait()
	return results
}

// cacheOne turns a panic in the image decoder (a hostile JPEG) into one photo's
// failure instead of taking the whole crawler down.
func (s *Service) cacheOne(ctx context.Context, provider, url string) (photo CachedPhoto, err error) {
	defer func() {
		if r := recover(); r != nil {
			photo, err = CachedPhoto{}, fmt.Errorf("cache photo panicked: %v", r)
		}
	}()
	return s.photos.Cache(ctx, provider, url)
}

// finishContext detaches bookkeeping from the run's ctx: a signal or drain
// deadline landing mid-statement must not leave a run or target unrecorded.
func finishContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), finishTimeout)
}

func isSuspect(b Baseline, listingsSeen int) bool {
	return b.Runs >= minBaselineRuns && float64(listingsSeen) < suspectFraction*b.Volume
}

func withStats(run domain.IngestRun, stats RunStats) domain.IngestRun {
	finished := time.Now()
	run.FinishedAt = &finished
	run.Complete = stats.Complete
	run.ListingsSeen = stats.ListingsSeen
	run.Created = stats.Created
	run.Updated = stats.Updated
	run.PhotoFailures = stats.PhotoFailures
	run.ItemErrors = stats.ItemErrors
	run.Suspect = stats.Suspect
	return run
}
