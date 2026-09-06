package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

func (s *Store) CorpusStats(ctx context.Context) (domain.CorpusStats, error) {
	const query = `
SELECT
	(SELECT count(*) FROM listings) AS listings,
	(SELECT count(*) FROM listings WHERE status = 'active') AS active_listings,
	(SELECT count(*) FROM listings WHERE status = 'active' AND listing_type = 'sale') AS active_sale,
	(SELECT count(*) FROM listings WHERE status = 'active' AND listing_type = 'rent') AS active_rent,
	(SELECT count(*) FROM listing_photos WHERE cached_path IS NOT NULL) AS photos_cached,
	(SELECT count(*) FROM verdicts) AS verdicts,
	(SELECT count(*) FROM lists) AS lists,
	(SELECT count(*) FROM crawl_targets WHERE kind = 'once' AND status IN ('pending','running')) AS pending_crawls,
	(SELECT count(*) FROM crawl_targets WHERE kind = 'standing' AND enabled) AS standing_scopes,
	(SELECT max(finished_at) FROM ingest_runs WHERE complete) AS last_crawl_at`

	var out domain.CorpusStats
	err := s.q.QueryRow(ctx, query).Scan(
		&out.Listings, &out.ActiveListings, &out.ActiveSale, &out.ActiveRent,
		&out.PhotosCached, &out.Verdicts, &out.Lists,
		&out.PendingCrawls, &out.StandingScopes, &out.LastCrawlAt,
	)
	if err != nil {
		return domain.CorpusStats{}, fmt.Errorf("query corpus stats: %w", err)
	}
	rows, err := s.q.Query(ctx, `
SELECT neighborhood, count(*) FROM listings
WHERE status = 'active' AND neighborhood <> ''
GROUP BY neighborhood ORDER BY count(*) DESC, neighborhood LIMIT $1`, MaxCorpusNeighborhoods)
	if err != nil {
		return domain.CorpusStats{}, fmt.Errorf("query corpus neighborhoods: %w", err)
	}
	out.Neighborhoods, err = pgx.CollectRows(rows, func(row pgx.CollectableRow) (domain.NeighborhoodCount, error) {
		var n domain.NeighborhoodCount
		err := row.Scan(&n.Name, &n.Active)
		return n, err
	})
	if err != nil {
		return domain.CorpusStats{}, fmt.Errorf("scan corpus neighborhoods: %w", err)
	}
	return out, nil
}

// MatchNeighborhoods reports which of names (case-insensitively) any listing carries.
func (s *Store) MatchNeighborhoods(ctx context.Context, names []string) ([]string, error) {
	if len(names) == 0 {
		return nil, nil
	}
	rows, err := s.q.Query(ctx, `SELECT DISTINCT lower(neighborhood) FROM listings WHERE lower(neighborhood) = ANY($1)`, lowerAll(names))
	if err != nil {
		return nil, fmt.Errorf("match neighborhoods: %w", err)
	}
	return pgx.CollectRows(rows, pgx.RowTo[string])
}

// MaxCorpusNeighborhoods caps the neighborhood roster in CorpusStats; NYC has
// a few hundred, so the cap only guards against a runaway corpus.
const MaxCorpusNeighborhoods = 400

// GetCrawlTarget loads one target (async crawl job) by id.
func (s *Store) GetCrawlTarget(ctx context.Context, id domain.CrawlTargetID) (domain.CrawlTarget, error) {
	const query = `SELECT ` + targetColumns + ` FROM crawl_targets WHERE id = $1`
	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return domain.CrawlTarget{}, fmt.Errorf("query crawl target %d: %w", id, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[crawlTargetRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CrawlTarget{}, listings.ErrCrawlTargetNotFound
	}
	if err != nil {
		return domain.CrawlTarget{}, fmt.Errorf("scan crawl target %d: %w", id, err)
	}
	return row.toDomain(), nil
}

// RunByID loads one ingest run, for reporting a finished crawl's outcome.
func (s *Store) RunByID(ctx context.Context, id domain.IngestRunID) (domain.IngestRun, error) {
	const query = `SELECT ` + runColumns + ` FROM ingest_runs WHERE id = $1`
	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return domain.IngestRun{}, fmt.Errorf("query ingest run %d: %w", id, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[runRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.IngestRun{}, listings.ErrCrawlTargetNotFound
	}
	if err != nil {
		return domain.IngestRun{}, fmt.Errorf("scan ingest run %d: %w", id, err)
	}
	return row.toDomain()
}
