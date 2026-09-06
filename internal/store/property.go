package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

// Query/result types and read-limit policy owned by the consumer package,
// re-exported here for the store's own callers.
const (
	DefaultReadLimit = listings.DefaultReadLimit
	MaxReadLimit     = listings.MaxReadLimit
	MaxBulkLimit     = listings.MaxBulkLimit
)

type (
	SearchFilter      = listings.SearchFilter
	ProvenanceSummary = listings.ProvenanceSummary
)

const propertyColumns = `
	l.id, l.canonical_address, l.unit, l.neighborhood, l.zip, l.lat, l.lng,
	l.listing_type, l.property_type, l.status,
	l.first_seen, l.last_seen, l.material_changed_at,
	c.price, c.currency, c.beds, c.baths::float8 AS baths, c.sqft,
	c.maintenance, c.common_charges, c.taxes_monthly, c.dom, c.description,
	src.url`

// v0 has one source per listing; pick the earliest deterministically for the
// shareable row URL.
const sourceJoin = `
LEFT JOIN LATERAL (
	SELECT ls.url FROM listing_sources ls
	WHERE ls.listing_id = l.id
	ORDER BY ls.first_seen_at, ls.provider, ls.provider_id
	LIMIT 1
) src ON true`

// Deterministic v0 ordering; tests assert it exactly.
const candidateOrder = `ORDER BY l.material_changed_at DESC, l.first_seen DESC, l.id`

// searchWhere: shared filter predicates for SearchProperties and
// CountProperties; $7 is include_inactive (false keeps the active-only rule).
const searchWhere = `
WHERE ($7::boolean OR l.status = 'active')
  AND ($1::integer IS NULL OR (c.price IS NOT NULL AND c.price <= $1))
  AND ($2::smallint IS NULL OR $2 <= 0 OR (c.beds IS NOT NULL AND c.beds >= $2))
  AND ($3::float8 IS NULL OR $3 <= 0 OR (c.baths IS NOT NULL AND c.baths::float8 >= $3))
  AND ($4::text[] IS NULL OR lower(l.neighborhood) = ANY($4))
  AND ($5::text[] IS NULL OR l.property_type = ANY($5))
  AND ($6::text IS NULL OR l.listing_type = $6)`

// lowerAll keeps neighborhood matching case-insensitive (the provider's casing is not the caller's); empty becomes nil so the SQL NULL check disables the filter.
func lowerAll(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	out := make([]string, len(in))
	for i, s := range in {
		out[i] = strings.ToLower(strings.TrimSpace(s))
	}
	return out
}

func searchWhereArgs(f SearchFilter) []any {
	return []any{
		numPtr[int32](f.MaxPrice),
		numPtr[int16](f.MinBeds),
		f.MinBaths,
		lowerAll(f.Neighborhoods),
		stringsFromPropertyTypes(f.PropertyTypes),
		nullText(string(f.ListingType)),
		f.IncludeInactive,
	}
}

func (s *Store) SearchProperties(ctx context.Context, f SearchFilter) ([]domain.Property, error) {
	const query = `
SELECT ` + propertyColumns + `
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id` + sourceJoin + searchWhere + `
` + candidateOrder + `
LIMIT $8 OFFSET $9`

	args := append(searchWhereArgs(f), clampBulkLimit(f.Limit), clampOffset(f.Offset))
	rows, err := s.q.Query(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("query listings: %w", err)
	}
	return collectProperties(rows)
}

// CountProperties is the full filtered size ignoring limit/offset.
func (s *Store) CountProperties(ctx context.Context, f SearchFilter) (int, error) {
	const query = `
SELECT count(*)
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id` + searchWhere

	var n int
	if err := s.q.QueryRow(ctx, query, searchWhereArgs(f)...).Scan(&n); err != nil {
		return 0, fmt.Errorf("count listings: %w", err)
	}
	return n, nil
}

// Candidates skips the carrying-cost filter when maintenance, common charges,
// and taxes are all unknown: that data is routinely absent on the sale side.
func (s *Store) Candidates(ctx context.Context, p domain.Profile, limit int) ([]domain.Property, error) {
	const query = `
SELECT ` + propertyColumns + `
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id
LEFT JOIN shown s ON s.listing_id = l.id` + sourceJoin + `
WHERE l.status = 'active'
  AND ($1::integer IS NULL OR (c.price IS NOT NULL AND c.price <= $1))
  AND ($2::smallint IS NULL OR $2 <= 0 OR (c.beds IS NOT NULL AND c.beds >= $2))
  AND ($3::float8 IS NULL OR $3 <= 0 OR (c.baths IS NOT NULL AND c.baths::float8 >= $3))
  AND ($4::integer IS NULL
       OR (c.maintenance IS NULL AND c.common_charges IS NULL AND c.taxes_monthly IS NULL)
       OR (COALESCE(c.maintenance, 0) + COALESCE(c.common_charges, 0) + COALESCE(c.taxes_monthly, 0)) <= $4)
  AND ($5::text IS NULL OR l.listing_type = $5)
  AND ($6::text[] IS NULL OR lower(l.neighborhood) = ANY($6))
  AND ($7::text[] IS NULL OR l.property_type = ANY($7))
  AND (s.listing_id IS NULL OR s.material_changed_at_seen < l.material_changed_at)
  AND NOT EXISTS (SELECT 1 FROM verdicts v WHERE v.listing_id = l.id)
` + candidateOrder + `
LIMIT $8`

	rows, err := s.q.Query(ctx, query,
		numPtr[int32](p.MaxPrice),
		numPtr[int16](p.MinBeds),
		p.MinBaths,
		numPtr[int32](p.MaxMonthlyCarrying),
		nullText(string(p.ListingType)),
		lowerAll(p.Neighborhoods),
		stringsFromPropertyTypes(p.PropertyTypes),
		clampLimit(limit),
	)
	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	return collectProperties(rows)
}

// GetProperty returns the listing with price history (oldest first) and photos
// (display order).
func (s *Store) GetProperty(ctx context.Context, id domain.PropertyID) (domain.Property, []domain.PriceEvent, []domain.Photo, ProvenanceSummary, error) {
	const query = `
SELECT ` + propertyColumns + `
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id` + sourceJoin + `
WHERE l.id = $1`

	var (
		zeroProp domain.Property
		zeroProv ProvenanceSummary
	)
	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return zeroProp, nil, nil, zeroProv, fmt.Errorf("query listing %d: %w", id, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[propertyRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return zeroProp, nil, nil, zeroProv, fmt.Errorf("get listing %d: %w", id, ErrPropertyNotFound)
	}
	if err != nil {
		return zeroProp, nil, nil, zeroProv, fmt.Errorf("scan listing %d: %w", id, err)
	}

	events, err := s.priceEvents(ctx, id)
	if err != nil {
		return zeroProp, nil, nil, zeroProv, err
	}
	photos, err := s.photos(ctx, id)
	if err != nil {
		return zeroProp, nil, nil, zeroProv, err
	}
	prov, err := s.provenance(ctx, id)
	if err != nil {
		return zeroProp, nil, nil, zeroProv, err
	}
	return row.toDomain(), events, photos, prov, nil
}

// PriceDrops reports, per property, whether the newest price event is lower than
// the one before it. Ids with fewer than two events are absent.
func (s *Store) PriceDrops(ctx context.Context, ids []domain.PropertyID) (map[domain.PropertyID]bool, error) {
	if len(ids) == 0 {
		return map[domain.PropertyID]bool{}, nil
	}
	const query = `
SELECT e.listing_id, e.price < e.previous_price AS dropped
FROM (
	SELECT listing_id, price,
		lag(price) OVER (PARTITION BY listing_id ORDER BY observed_at, id) AS previous_price,
		row_number() OVER (PARTITION BY listing_id ORDER BY observed_at DESC, id DESC) AS recency
	FROM price_events
	WHERE listing_id = ANY($1)
) e
WHERE e.recency = 1 AND e.previous_price IS NOT NULL`

	rows, err := s.q.Query(ctx, query, int64IDs(ids))
	if err != nil {
		return nil, fmt.Errorf("query price drops: %w", err)
	}
	drops, err := pgx.CollectRows(rows, pgx.RowToStructByName[priceDropRow])
	if err != nil {
		return nil, fmt.Errorf("scan price drops: %w", err)
	}
	out := make(map[domain.PropertyID]bool, len(drops))
	for _, d := range drops {
		out[domain.PropertyID(d.ListingID)] = d.Dropped
	}
	return out, nil
}

func (s *Store) priceEvents(ctx context.Context, id domain.PropertyID) ([]domain.PriceEvent, error) {
	const query = `
SELECT listing_id, price, observed_at
FROM price_events
WHERE listing_id = $1
ORDER BY observed_at, id`

	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return nil, fmt.Errorf("query price events for listing %d: %w", id, err)
	}
	out, err := collectMapped(rows, priceEventRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan price events for listing %d: %w", id, err)
	}
	return out, nil
}

func (s *Store) photos(ctx context.Context, id domain.PropertyID) ([]domain.Photo, error) {
	const query = `
SELECT position, source_url, cached_path, mime_type, width, height
FROM listing_photos
WHERE listing_id = $1
ORDER BY position`

	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return nil, fmt.Errorf("query photos for listing %d: %w", id, err)
	}
	out, err := collectMapped(rows, photoRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan photos for listing %d: %w", id, err)
	}
	return out, nil
}

// PhotoCounts returns, per listing, total photo slots and how many carry a
// cached thumbnail. Listings with no photo rows are absent.
func (s *Store) PhotoCounts(ctx context.Context, ids []domain.PropertyID) (map[domain.PropertyID]listings.PhotoCount, error) {
	if len(ids) == 0 {
		return map[domain.PropertyID]listings.PhotoCount{}, nil
	}
	const query = `
SELECT listing_id, count(*) AS total, count(cached_path) AS cached
FROM listing_photos
WHERE listing_id = ANY($1)
GROUP BY listing_id`

	rows, err := s.q.Query(ctx, query, int64IDs(ids))
	if err != nil {
		return nil, fmt.Errorf("query photo counts: %w", err)
	}
	counted, err := pgx.CollectRows(rows, pgx.RowToStructByName[photoCountRow])
	if err != nil {
		return nil, fmt.Errorf("scan photo counts: %w", err)
	}
	out := make(map[domain.PropertyID]listings.PhotoCount, len(counted))
	for _, c := range counted {
		out[domain.PropertyID(c.ListingID)] = listings.PhotoCount{Total: int(c.Total), Cached: int(c.Cached)}
	}
	return out, nil
}

// PhotoKeyAt returns the cached_path of the listing's n-th cached photo in
// position order (dense, so 0..cached-1 always resolve), backing GET
// /img/l/{id}/{n}. Reports ErrPropertyNotFound (caller answers 404) past the end.
func (s *Store) PhotoKeyAt(ctx context.Context, id domain.PropertyID, n int) (string, error) {
	if n < 0 {
		return "", fmt.Errorf("photo key for listing %d index %d: %w", id, n, ErrPropertyNotFound)
	}
	const query = `
SELECT cached_path
FROM listing_photos
WHERE listing_id = $1 AND cached_path IS NOT NULL
ORDER BY position
OFFSET $2 LIMIT 1`

	var cachedPath string
	err := s.q.QueryRow(ctx, query, int64(id), n).Scan(&cachedPath)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", fmt.Errorf("photo key for listing %d index %d: %w", id, n, ErrPropertyNotFound)
	}
	if err != nil {
		return "", fmt.Errorf("query photo key for listing %d index %d: %w", id, n, err)
	}
	return cachedPath, nil
}

func (s *Store) provenance(ctx context.Context, id domain.PropertyID) (ProvenanceSummary, error) {
	const query = `
SELECT provider, provider_id, url, source_status, first_seen_at, last_seen_at, fetched_at, missing_runs
FROM listing_sources
WHERE listing_id = $1
ORDER BY last_seen_at DESC
LIMIT 1`

	rows, err := s.q.Query(ctx, query, int64(id))
	if err != nil {
		return ProvenanceSummary{}, fmt.Errorf("query provenance for listing %d: %w", id, err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[sourceRow])
	if errors.Is(err, pgx.ErrNoRows) {
		// v0 always has a source, but an empty summary beats failing the read.
		return ProvenanceSummary{}, nil
	}
	if err != nil {
		return ProvenanceSummary{}, fmt.Errorf("scan provenance for listing %d: %w", id, err)
	}
	return row.toProvenance(), nil
}

// MarkShown snapshots each listing's material_changed_at so a later material
// change can resurface it; a non-nil asOf (when the caller read it) caps the
// snapshot so a change committed since still resurfaces. Unknown ids are ignored.
func (s *Store) MarkShown(ctx context.Context, ids []domain.PropertyID, asOf *time.Time) (int, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	const query = `
INSERT INTO shown (listing_id, shown_at, material_changed_at_seen)
SELECT l.id, now(), LEAST(l.material_changed_at, $2::timestamptz)
FROM listings l
WHERE l.id = ANY($1)
ON CONFLICT (listing_id) DO UPDATE SET
	shown_at = excluded.shown_at,
	material_changed_at_seen = excluded.material_changed_at_seen`

	tag, err := s.q.Exec(ctx, query, int64IDs(ids), asOf)
	if err != nil {
		return 0, fmt.Errorf("mark listings shown: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func (s *Store) ActiveCount(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRow(ctx, `SELECT count(*) FROM listings WHERE status = 'active'`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count active listings: %w", err)
	}
	return n, nil
}

func (s *Store) MissingSourceCount(ctx context.Context) (int, error) {
	var n int
	err := s.q.QueryRow(ctx, `SELECT count(*) FROM listing_sources WHERE missing_runs > 0`).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count missing sources: %w", err)
	}
	return n, nil
}

func collectProperties(rows pgx.Rows) ([]domain.Property, error) {
	out, err := collectMapped(rows, propertyRow.toDomain)
	if err != nil {
		return nil, fmt.Errorf("scan listings: %w", err)
	}
	return out, nil
}

func clampLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultReadLimit
	case limit > MaxReadLimit:
		return MaxReadLimit
	default:
		return limit
	}
}

func clampBulkLimit(limit int) int {
	switch {
	case limit <= 0:
		return DefaultReadLimit
	case limit > MaxBulkLimit:
		return MaxBulkLimit
	default:
		return limit
	}
}

func clampOffset(offset int) int {
	if offset < 0 {
		return 0
	}
	return offset
}

// PropertiesByIDs returns the listings for the given ids in unspecified order.
// Missing ids are absent.
func (s *Store) PropertiesByIDs(ctx context.Context, ids []domain.PropertyID) ([]domain.Property, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	const query = `
SELECT ` + propertyColumns + `
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id` + sourceJoin + `
WHERE l.id = ANY($1)`
	rows, err := s.q.Query(ctx, query, int64IDs(ids))
	if err != nil {
		return nil, fmt.Errorf("query listings by ids: %w", err)
	}
	return collectProperties(rows)
}
