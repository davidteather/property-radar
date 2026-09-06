package store

import (
	"context"
	"fmt"
	"slices"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

// Trailing comparable clean runs behind the volume baseline.
const baselineWindow = 7

// baselineScan is how far back BaselineVolume looks for an accepted level
// change; baselineAcceptRuns consecutive suspect runs each within
// baselineStableRatio of the previous one make the new volume the baseline.
const (
	baselineScan        = 20
	baselineAcceptRuns  = 3
	baselineStableRatio = 0.7
)

const runColumns = `
	id, provider, scope_hash, started_at, finished_at, complete,
	listings_seen, created, updated, photo_failures, errors, suspect`

// CreateRun opens a run row that carries only its scope hash (preflight and
// tests); StartRun is the crawl's entry point and records the scope itself.
func (s *Store) CreateRun(ctx context.Context, provider, scopeHash string, oneOff bool) (domain.IngestRun, error) {
	return s.insertRun(ctx, provider, scopeHash, oneOff, nil)
}

func (s *Store) StartRun(ctx context.Context, provider string, q ingest.SearchQuery) (domain.IngestRun, error) {
	return s.insertRun(ctx, provider, ingest.ScopeHash(provider, q), q.OneOff, &q)
}

func (s *Store) insertRun(ctx context.Context, provider, scopeHash string, oneOff bool, q *ingest.SearchQuery) (domain.IngestRun, error) {
	const query = `
INSERT INTO ingest_runs (provider, scope_hash, started_at, one_off, listing_type, max_price, min_beds, areas)
VALUES ($1, $2, now(), $3, $4, $5, $6, $7)
RETURNING ` + runColumns

	var (
		listingType *string
		maxPrice    *int32
		minBeds     *int16
		areas       []string
	)
	if q != nil {
		lt := string(q.ListingType)
		if lt == "" {
			lt = string(domain.ListingSale)
		}
		listingType = &lt
		if q.MaxPrice > 0 {
			maxPrice = fitPtr[int32](&q.MaxPrice)
		}
		if q.MinBeds > 0 {
			minBeds = fitPtr[int16](&q.MinBeds)
		}
		areas = normalizeAreas(q.Areas)
	}
	rows, err := s.q.Query(ctx, query, provider, scopeHash, oneOff, listingType, maxPrice, minBeds, areas)
	if err != nil {
		return domain.IngestRun{}, fmt.Errorf("insert ingest run for %s: %w", provider, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[runRow])
	if err != nil {
		return domain.IngestRun{}, fmt.Errorf("scan ingest run for %s: %w", provider, err)
	}
	return row.toDomain()
}

func (s *Store) FinishRun(ctx context.Context, id domain.IngestRunID, stats ingest.RunStats) error {
	const query = `
UPDATE ingest_runs SET
	finished_at = now(),
	complete = $2,
	listings_seen = $3,
	created = $4,
	updated = $5,
	photo_failures = $6,
	errors = $7,
	suspect = $8
WHERE id = $1`

	itemErrors, err := runErrorsToJSON(stats.ItemErrors)
	if err != nil {
		return err
	}
	tag, err := s.q.Exec(ctx, query, int64(id), stats.Complete, stats.ListingsSeen,
		stats.Created, stats.Updated, stats.PhotoFailures, itemErrors, stats.Suspect)
	if err != nil {
		return fmt.Errorf("finish ingest run %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("finish ingest run %d: run not found", id)
	}
	return nil
}

// RecentRuns returns runs newest first.
func (s *Store) RecentRuns(ctx context.Context, limit int) ([]domain.IngestRun, error) {
	if limit <= 0 {
		limit = 10
	}
	const query = `SELECT ` + runColumns + ` FROM ingest_runs ORDER BY id DESC LIMIT $1`

	rows, err := s.q.Query(ctx, query, limit)
	if err != nil {
		return nil, fmt.Errorf("query ingest runs: %w", err)
	}
	runRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[runRow])
	if err != nil {
		return nil, fmt.Errorf("scan ingest runs: %w", err)
	}
	out := make([]domain.IngestRun, 0, len(runRows))
	for _, r := range runRows {
		run, err := r.toDomain()
		if err != nil {
			return nil, err
		}
		out = append(out, run)
	}
	return out, nil
}

// BaselineVolume is the median volume of the scope's recent clean runs. A drop
// is accepted once baselineAcceptRuns consecutive suspect runs agree on the new
// level: they join the baseline and older runs stop counting.
func (s *Store) BaselineVolume(ctx context.Context, provider, scopeHash string) (ingest.Baseline, error) {
	const query = `
SELECT listings_seen, suspect
FROM ingest_runs
WHERE provider = $1
  AND scope_hash = $2
  AND complete
  AND listings_seen > 0
ORDER BY id DESC
LIMIT $3`

	rows, err := s.q.Query(ctx, query, provider, scopeHash, baselineScan)
	if err != nil {
		return ingest.Baseline{}, fmt.Errorf("query baseline volume for %s: %w", provider, err)
	}
	recent, err := pgx.CollectRows(rows, pgx.RowToStructByPos[runVolume])
	if err != nil {
		return ingest.Baseline{}, fmt.Errorf("scan baseline volume for %s: %w", provider, err)
	}
	volumes := baselineVolumes(recent)
	if len(volumes) == 0 {
		return ingest.Baseline{}, nil
	}
	slices.Sort(volumes)
	mid := len(volumes) / 2
	median := float64(volumes[mid])
	if len(volumes)%2 == 0 {
		median = (float64(volumes[mid-1]) + float64(volumes[mid])) / 2
	}
	return ingest.Baseline{Volume: median, Runs: len(volumes)}, nil
}

type runVolume struct {
	Seen    int32
	Suspect bool
}

// baselineVolumes picks the runs (newest first) that form the baseline: clean
// runs, plus the most recent accepted stretch of stable suspect runs, and
// nothing older than that stretch. At most baselineWindow are returned.
func baselineVolumes(recent []runVolume) []int32 {
	start, end := acceptedStretch(recent)
	var out []int32
	for i, r := range recent {
		if i >= end && end > 0 {
			break
		}
		if !r.Suspect || (i >= start && i < end) {
			out = append(out, r.Seen)
		}
		if len(out) == baselineWindow {
			break
		}
	}
	return out
}

// acceptedStretch finds the newest run of baselineAcceptRuns+ consecutive
// suspect runs that are stable against each other; (0, 0) when there is none.
func acceptedStretch(recent []runVolume) (start, end int) {
	for i := 0; i+baselineAcceptRuns <= len(recent); i++ {
		end = i
		for end < len(recent) && recent[end].Suspect && (end == i || stableVolume(recent[end-1].Seen, recent[end].Seen)) {
			end++
		}
		if end-i >= baselineAcceptRuns {
			return i, end
		}
	}
	return 0, 0
}

// stableVolume: both positive and within baselineStableRatio of each other.
// Zero never counts, so an empty-page outage cannot become the accepted level.
func stableVolume(a, b int32) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	return float64(min(a, b)) >= baselineStableRatio*float64(max(a, b))
}
