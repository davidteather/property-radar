package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

var errRacedDuplicate = errors.New("crawl target inserted concurrently")

func isPendingOnceViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505" && pgErr.ConstraintName == "crawl_targets_pending_once_idx"
}

// ErrDuplicateCrawlTarget accompanies the already-queued row when an identical
// pending once-target exists; the caller decides whether that is an error.
var ErrDuplicateCrawlTarget = listings.ErrDuplicateCrawlTarget

const targetColumns = `
	id, kind, areas, listing_type, max_price, min_beds, enabled,
	status, note, created_at, last_run_id, last_error, last_run_at, last_deep_at`

// Standing scopes first, then queued one-offs, then everything already run or
// disabled; ids order within each group.
const targetOrder = `
ORDER BY CASE
	WHEN kind = 'standing' AND enabled THEN 0
	WHEN kind = 'once' AND status = 'pending' THEN 1
	ELSE 2
END, id`

func (s *Store) CreateCrawlTarget(ctx context.Context, t domain.CrawlTarget) (domain.CrawlTarget, error) {
	const insert = `
INSERT INTO crawl_targets (kind, areas, listing_type, max_price, min_beds, enabled, status, note)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
RETURNING ` + targetColumns

	kind := t.Kind
	if kind == "" {
		kind = domain.TargetOnce
	}
	listingType := t.ListingType
	if listingType == "" {
		listingType = domain.ListingSale
	}
	status := t.Status
	if status == "" {
		status = domain.TargetPending
	}
	areas := normalizeAreas(t.Areas)
	if len(areas) == 0 {
		return domain.CrawlTarget{}, errors.New("create crawl target: areas is empty")
	}

	var out domain.CrawlTarget
	duplicate := false
	err := s.InTx(ctx, func(tx *Store) error {
		// Serialize creates so two identical requests cannot both miss the dedupe.
		if _, err := tx.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('crawl_targets'))`); err != nil {
			return fmt.Errorf("lock crawl targets: %w", err)
		}
		if kind == domain.TargetStanding || status == domain.TargetPending {
			existing, found, err := tx.matchingTarget(ctx, kind, areas, listingType, t.MaxPrice, t.MinBeds)
			if err != nil {
				return err
			}
			if found {
				out, duplicate = existing, true
				return nil
			}
		}
		rows, err := tx.q.Query(ctx, insert, string(kind), areas, string(listingType),
			numPtr[int32](t.MaxPrice), numPtr[int16](t.MinBeds), t.Enabled || kind == domain.TargetOnce,
			string(status), nullText(t.Note))
		if err != nil {
			return fmt.Errorf("insert crawl target: %w", err)
		}
		// Two identical requests racing: one wins the partial unique index; the
		// other trips it and rolls back (a raw 23505 would surface as a 500).
		row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[crawlTargetRow])
		if isPendingOnceViolation(err) {
			return errRacedDuplicate
		}
		if err != nil {
			return fmt.Errorf("scan crawl target: %w", err)
		}
		out = row.toDomain()
		return tx.notifyCrawlDue(ctx)
	})
	if errors.Is(err, errRacedDuplicate) {
		existing, found, err := s.matchingTarget(ctx, kind, areas, listingType, t.MaxPrice, t.MinBeds)
		if err != nil {
			return domain.CrawlTarget{}, err
		}
		if found {
			return existing, ErrDuplicateCrawlTarget
		}
		return domain.CrawlTarget{}, fmt.Errorf("create crawl target: %w", ErrDuplicateCrawlTarget)
	}
	if err != nil {
		return domain.CrawlTarget{}, err
	}
	if duplicate {
		return out, ErrDuplicateCrawlTarget
	}
	return out, nil
}

// SeedCrawlTargets inserts the given standing scopes only when crawl_targets is
// empty (idempotent). It lets an operator pre-seed a fresh install from config;
// there is no hardcoded default scope. Empty slice is a no-op.
func (s *Store) SeedCrawlTargets(ctx context.Context, targets []domain.CrawlTarget) (int, error) {
	if len(targets) == 0 {
		return 0, nil
	}
	inserted := 0
	err := s.InTx(ctx, func(tx *Store) error {
		var count int
		if err := tx.q.QueryRow(ctx, `SELECT count(*) FROM crawl_targets`).Scan(&count); err != nil {
			return fmt.Errorf("count crawl targets: %w", err)
		}
		if count > 0 {
			return nil
		}
		for _, t := range targets {
			_, err := tx.CreateCrawlTarget(ctx, t)
			if errors.Is(err, listings.ErrDuplicateCrawlTarget) {
				continue
			}
			if err != nil {
				return fmt.Errorf("seed crawl target: %w", err)
			}
			inserted++
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return inserted, nil
}

// matchingTarget finds the row a new request would duplicate: any standing
// scope, or a one-off still pending or running (a running one may return to
// pending if interrupted and would then collide with the pending-once index).
func (s *Store) matchingTarget(ctx context.Context, kind domain.TargetKind, areas []string, listingType domain.ListingType, maxPrice *domain.Money, minBeds *int) (domain.CrawlTarget, bool, error) {
	const query = `
SELECT ` + targetColumns + `
FROM crawl_targets
WHERE kind = $5
  AND (kind = 'standing' OR status IN ('pending', 'running'))
  AND areas = $1
  AND listing_type = $2
  AND max_price IS NOT DISTINCT FROM $3
  AND min_beds IS NOT DISTINCT FROM $4
ORDER BY id
LIMIT 1`

	rows, err := s.q.Query(ctx, query, areas, string(listingType), numPtr[int32](maxPrice), numPtr[int16](minBeds), string(kind))
	if err != nil {
		return domain.CrawlTarget{}, false, fmt.Errorf("query pending crawl target: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[crawlTargetRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.CrawlTarget{}, false, nil
	}
	if err != nil {
		return domain.CrawlTarget{}, false, fmt.Errorf("scan pending crawl target: %w", err)
	}
	return row.toDomain(), true, nil
}

func (s *Store) ListCrawlTargets(ctx context.Context) ([]domain.CrawlTarget, error) {
	const query = `SELECT ` + targetColumns + ` FROM crawl_targets` + targetOrder

	rows, err := s.q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query crawl targets: %w", err)
	}
	out, err := collectMapped(rows, crawlTargetRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan crawl targets: %w", err)
	}
	return out, nil
}

// DueCrawlTargets is what one drain crawls: pending one-offs plus enabled
// standing scopes not run within standingEvery (<= 0 means every drain).
func (s *Store) DueCrawlTargets(ctx context.Context, standingEvery time.Duration) ([]domain.CrawlTarget, error) {
	// Pending one-offs (explicit agent/operator requests) drain BEFORE standing
	// background scopes, so a queued request is not stuck behind a large standing
	// crawl. ListCrawlTargets keeps its own display order (standing first).
	const query = `
SELECT ` + targetColumns + `
FROM crawl_targets
WHERE (kind = 'standing' AND enabled AND status <> 'running'
	AND (last_run_at IS NULL OR $1::interval <= interval '0' OR last_run_at < now() - $1::interval))
	OR (kind = 'once' AND status = 'pending')
ORDER BY CASE WHEN kind = 'once' THEN 0 ELSE 1 END, id`

	rows, err := s.q.Query(ctx, query, standingEvery)
	if err != nil {
		return nil, fmt.Errorf("query due crawl targets: %w", err)
	}
	out, err := collectMapped(rows, crawlTargetRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan due crawl targets: %w", err)
	}
	return out, nil
}

// MarkTargetStarted claims a target (once: pending → running; standing: any
// non-running status → running), stamping started_at so a stale claim can be
// recovered. Two drains racing the same target: exactly one wins.
func (s *Store) MarkTargetStarted(ctx context.Context, id domain.CrawlTargetID) error {
	const query = `
UPDATE crawl_targets SET
	status = 'running',
	started_at = now(),
	last_run_at = CASE WHEN kind = 'once' THEN now() ELSE last_run_at END
WHERE id = $1 AND ((kind = 'once' AND status = 'pending') OR (kind <> 'once' AND status <> 'running'))`
	tag, err := s.q.Exec(ctx, query, int64(id))
	if err != nil {
		return fmt.Errorf("mark crawl target %d started: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark crawl target %d started: %w", id, ingest.ErrTargetClaimed)
	}
	return nil
}

// RecoverStaleTargets settles targets left running longer than olderThan: a
// crawler killed mid-run never reaches MarkTargetRun, and nothing else moves a
// running target on. Once targets fail; standing ones go back to pending.
func (s *Store) RecoverStaleTargets(ctx context.Context, olderThan time.Duration) (int, error) {
	const query = `
UPDATE crawl_targets SET
	status = CASE WHEN kind = 'once' THEN 'failed' ELSE 'pending' END,
	last_error = CASE WHEN kind = 'once' THEN 'crawler stopped before recording this run' ELSE last_error END
WHERE status = 'running' AND coalesce(started_at, last_run_at, created_at) < now() - $1::interval`
	tag, err := s.q.Exec(ctx, query, olderThan)
	if err != nil {
		return 0, fmt.Errorf("recover stale crawl targets: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// MarkTargetRun records one drained target's outcome as done/failed (for a
// standing target: its latest pass). A successful deep run also stamps
// last_deep_at, scheduling the next full re-enrich ~a day out.
func (s *Store) MarkTargetRun(ctx context.Context, id domain.CrawlTargetID, runID domain.IngestRunID, deep bool, runErr error) error {
	// An interrupted or never-started run is not an outcome: the once target
	// goes back to pending and a standing one stays due, so the next drain
	// retries instead of recording a failure or waiting out the interval.
	interrupted := ingest.Retryable(runErr)
	const query = `
UPDATE crawl_targets SET
	last_run_id = $2,
	last_error = $3,
	last_run_at = CASE WHEN $5 THEN last_run_at ELSE now() END,
	last_deep_at = CASE WHEN $4 AND $3::text IS NULL THEN now() ELSE last_deep_at END,
	status = CASE
		WHEN $5 THEN 'pending'
		WHEN $3::text IS NULL THEN 'done'
		ELSE 'failed'
	END
WHERE id = $1`

	var run *int64
	if runID != 0 {
		v := int64(runID)
		run = &v
	}
	var message *string
	if runErr != nil {
		msg := runErr.Error()
		if msg == "" {
			msg = "crawl failed"
		}
		message = &msg
	}
	tag, err := s.q.Exec(ctx, query, int64(id), run, message, deep, interrupted)
	if isPendingOnceViolation(err) {
		// A newer identical request was queued while this one ran (a stale-target
		// recovery failed it in between); that one will crawl, this one is settled.
		const supersede = `UPDATE crawl_targets SET last_run_id = $2, status = 'failed',
	last_error = 'interrupted; a newer request for the same scope is pending' WHERE id = $1`
		tag, err = s.q.Exec(ctx, supersede, int64(id), run)
	}
	if err != nil {
		return fmt.Errorf("mark crawl target %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("mark crawl target %d: %w", id, ingest.ErrTargetGone)
	}
	return nil
}

// SetCrawlTargetEnabled pauses or resumes a standing scope (enabled gates
// standing rows in DueCrawlTargets). Reports ErrCrawlTargetNotFound for an
// unknown id. Resuming wakes the drain worker like a new request does.
func (s *Store) SetCrawlTargetEnabled(ctx context.Context, id domain.CrawlTargetID, enabled bool) error {
	return s.InTx(ctx, func(tx *Store) error {
		tag, err := tx.q.Exec(ctx, `UPDATE crawl_targets SET enabled = $2 WHERE id = $1`, int64(id), enabled)
		if err != nil {
			return fmt.Errorf("set crawl target %d enabled: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			return listings.ErrCrawlTargetNotFound
		}
		if !enabled {
			return nil
		}
		return tx.notifyCrawlDue(ctx)
	})
}

// notifyCrawlDue wakes any drain worker listening on crawl_due; inside a
// transaction the notice is delivered on commit, after the row is visible.
func (s *Store) notifyCrawlDue(ctx context.Context) error {
	if _, err := s.q.Exec(ctx, `SELECT pg_notify('crawl_due', '')`); err != nil {
		return fmt.Errorf("notify crawl_due: %w", err)
	}
	return nil
}

// PromoteCrawlTargetToStanding turns a once target into a standing (recurring)
// scope and enables it, so the worker keeps re-crawling it. Idempotent for
// an already-standing target. ErrCrawlTargetNotFound for an unknown id.
func (s *Store) PromoteCrawlTargetToStanding(ctx context.Context, id domain.CrawlTargetID) error {
	return s.InTx(ctx, func(tx *Store) error {
		tag, err := tx.q.Exec(ctx, `UPDATE crawl_targets SET kind = 'standing', enabled = true WHERE id = $1`, int64(id))
		if err != nil {
			return fmt.Errorf("promote crawl target %d to standing: %w", id, err)
		}
		if tag.RowsAffected() == 0 {
			return listings.ErrCrawlTargetNotFound
		}
		return tx.notifyCrawlDue(ctx)
	})
}

// DeleteCrawlTarget removes a crawl scope. Reports ErrCrawlTargetNotFound for an
// unknown id.
func (s *Store) DeleteCrawlTarget(ctx context.Context, id domain.CrawlTargetID) error {
	tag, err := s.q.Exec(ctx, `DELETE FROM crawl_targets WHERE id = $1`, int64(id))
	if err != nil {
		return fmt.Errorf("delete crawl target %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return listings.ErrCrawlTargetNotFound
	}
	return nil
}

// PruneCrawlTargets deletes settled once targets older than keep, so the async
// job rows have a TTL instead of accumulating forever. Standing targets and
// anything pending/running are never touched.
func (s *Store) PruneCrawlTargets(ctx context.Context, keep time.Duration) (int, error) {
	const query = `
DELETE FROM crawl_targets
WHERE kind = 'once' AND status IN ('done','failed') AND created_at < now() - $1::interval`
	tag, err := s.q.Exec(ctx, query, keep)
	if err != nil {
		return 0, fmt.Errorf("prune crawl targets: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

// Areas are stored canonically (trimmed, sorted, deduped) so the dedupe guard
// sees "319,305" and "305,319,305" as the same scope.
func normalizeAreas(areas []string) []string {
	out := xslices.Map(areas, strings.TrimSpace)
	out = slices.DeleteFunc(out, func(a string) bool { return a == "" })
	slices.Sort(out)
	return slices.Compact(out)
}
