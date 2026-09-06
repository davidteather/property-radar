package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

const profileColumns = `
	max_price, min_beds, min_baths::float8 AS min_baths, max_monthly_carrying,
	listing_type, neighborhoods, property_types, updated_at`

func (s *Store) RecordVerdict(ctx context.Context, propertyID domain.PropertyID, kind domain.VerdictKind, note string) (domain.Verdict, error) {
	const query = `
INSERT INTO verdicts (listing_id, verdict, note)
VALUES ($1, $2, $3)
RETURNING id, listing_id, verdict, note, created_at`

	rows, err := s.q.Query(ctx, query, int64(propertyID), string(kind), nullText(note))
	if err == nil {
		var row verdictRow
		row, err = pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[verdictRow])
		if err == nil {
			return row.toDomain(), nil
		}
	}
	if isForeignKeyViolation(err) {
		return domain.Verdict{}, fmt.Errorf("record verdict for listing %d: %w", propertyID, ErrPropertyNotFound)
	}
	return domain.Verdict{}, fmt.Errorf("record verdict for listing %d: %w", propertyID, err)
}

// Verdicts returns the full history, oldest first; the latest row for a listing
// is its current opinion.
func (s *Store) Verdicts(ctx context.Context) ([]domain.Verdict, error) {
	const query = `SELECT id, listing_id, verdict, note, created_at FROM verdicts ORDER BY id`

	rows, err := s.q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query verdicts: %w", err)
	}
	out, err := collectMapped(rows, verdictRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan verdicts: %w", err)
	}
	return out, nil
}

// AppendRubric rejects a through-verdict newer than the latest recorded verdict
// with ErrFutureThroughVerdict. A zero through means "summarizes no verdict".
func (s *Store) AppendRubric(ctx context.Context, content string, through domain.VerdictID) (domain.Rubric, error) {
	const insert = `
INSERT INTO rubric_versions (content, through_verdict_id)
VALUES ($1, $2)
RETURNING id, content, through_verdict_id, created_at`

	var out domain.Rubric
	err := s.InTx(ctx, func(tx *Store) error {
		latest, err := tx.latestVerdictID(ctx)
		if err != nil {
			return err
		}
		if through > latest {
			return fmt.Errorf("rubric through verdict %d, latest verdict %d: %w", through, latest, ErrFutureThroughVerdict)
		}
		// A 0 with verdicts present would be stale the moment it is saved.
		if through == 0 && latest > 0 {
			return listings.Invalidf("through_verdict_id is 0 but verdicts exist; pass the newest verdict id (%d) from get_state", latest)
		}
		var throughArg *int64
		if through != 0 {
			// Ids survive reset_state (identity sequence), so a remembered id can
			// be below latest yet gone; say so instead of leaking the FK error.
			var exists bool
			if err := tx.q.QueryRow(ctx, `SELECT EXISTS (SELECT 1 FROM verdicts WHERE id = $1)`, int64(through)).Scan(&exists); err != nil {
				return fmt.Errorf("check verdict %d: %w", through, err)
			}
			if !exists {
				return listings.Invalidf("through_verdict_id %d does not exist (verdicts were reset?)", through)
			}
			id := int64(through)
			throughArg = &id
		}
		rows, err := tx.q.Query(ctx, insert, content, throughArg)
		if err != nil {
			return fmt.Errorf("insert rubric: %w", err)
		}
		row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[rubricRow])
		if err != nil {
			return fmt.Errorf("scan rubric: %w", err)
		}
		out = row.toDomain()
		return nil
	})
	if err != nil {
		return domain.Rubric{}, err
	}
	return out, nil
}

// latestRubric returns nil when no rubric has been recorded.
func (s *Store) latestRubric(ctx context.Context) (*domain.Rubric, error) {
	const query = `
SELECT id, content, through_verdict_id, created_at
FROM rubric_versions
ORDER BY id DESC
LIMIT 1`

	rows, err := s.q.Query(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query latest rubric: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[rubricRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scan latest rubric: %w", err)
	}
	rubric := row.toDomain()
	return &rubric, nil
}

// GetProfile returns the zero profile (sale, unconstrained) when none is set.
func (s *Store) GetProfile(ctx context.Context) (domain.Profile, error) {
	const query = `SELECT ` + profileColumns + ` FROM profile WHERE id`

	rows, err := s.q.Query(ctx, query)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("query profile: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[profileRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.Profile{ListingType: domain.ListingSale}, nil
	}
	if err != nil {
		return domain.Profile{}, fmt.Errorf("scan profile: %w", err)
	}
	return row.toDomain(), nil
}

// UpdateProfile applies merge to the current profile under a lock, so two
// field-level updates that overlap cannot drop each other's fields.
func (s *Store) UpdateProfile(ctx context.Context, merge func(*domain.Profile) error) (domain.Profile, error) {
	var saved domain.Profile
	err := s.InTx(ctx, func(tx *Store) error {
		if _, err := tx.q.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('profile'))`); err != nil {
			return fmt.Errorf("lock profile: %w", err)
		}
		next, err := tx.GetProfile(ctx)
		if err != nil {
			return err
		}
		if err := merge(&next); err != nil {
			return err
		}
		saved, err = tx.SetProfile(ctx, next)
		return err
	})
	return saved, err
}

func (s *Store) SetProfile(ctx context.Context, p domain.Profile) (domain.Profile, error) {
	const query = `
INSERT INTO profile (
	id, max_price, min_beds, min_baths, max_monthly_carrying,
	listing_type, neighborhoods, property_types, updated_at)
VALUES (true, $1, $2, $3::float8, $4, $5, $6, $7, now())
ON CONFLICT (id) DO UPDATE SET
	max_price = excluded.max_price,
	min_beds = excluded.min_beds,
	min_baths = excluded.min_baths,
	max_monthly_carrying = excluded.max_monthly_carrying,
	listing_type = excluded.listing_type,
	neighborhoods = excluded.neighborhoods,
	property_types = excluded.property_types,
	updated_at = excluded.updated_at
RETURNING ` + profileColumns

	listingType := string(p.ListingType)
	if listingType == "" {
		listingType = string(domain.ListingSale)
	}
	rows, err := s.q.Query(ctx, query,
		numPtr[int32](p.MaxPrice), numPtr[int16](p.MinBeds), p.MinBaths,
		numPtr[int32](p.MaxMonthlyCarrying), listingType,
		p.Neighborhoods, stringsFromPropertyTypes(p.PropertyTypes),
	)
	if err != nil {
		return domain.Profile{}, fmt.Errorf("upsert profile: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[profileRow])
	if err != nil {
		return domain.Profile{}, fmt.Errorf("scan profile: %w", err)
	}
	return row.toDomain(), nil
}

// State is the one read backing get_state: profile, latest rubric (nil when
// none), whether that rubric trails the newest verdict, and verdict history.
func (s *Store) State(ctx context.Context) (domain.Profile, *domain.Rubric, bool, []domain.Verdict, error) {
	var (
		profile  domain.Profile
		rubric   *domain.Rubric
		verdicts []domain.Verdict
	)
	// One snapshot so the staleness bit cannot be computed from a torn read
	// (READ COMMITTED would give each SELECT its own).
	err := s.InTxOptions(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead}, func(tx *Store) error {
		var err error
		if profile, err = tx.GetProfile(ctx); err != nil {
			return err
		}
		if rubric, err = tx.latestRubric(ctx); err != nil {
			return err
		}
		verdicts, err = tx.Verdicts(ctx)
		return err
	})
	if err != nil {
		return domain.Profile{}, nil, false, nil, err
	}

	stale := false
	if len(verdicts) > 0 {
		latest := verdicts[len(verdicts)-1].ID
		stale = rubric == nil || latest > rubric.ThroughVerdictID
	}
	return profile, rubric, stale, verdicts, nil
}

func (s *Store) latestVerdictID(ctx context.Context) (domain.VerdictID, error) {
	var latest int64
	if err := s.q.QueryRow(ctx, `SELECT coalesce(max(id), 0) FROM verdicts`).Scan(&latest); err != nil {
		return 0, fmt.Errorf("query latest verdict: %w", err)
	}
	return domain.VerdictID(latest), nil
}
