package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

// missingRunsToDelist is the v0 absence policy: delist after two consecutive
// complete, non-suspect runs without the source.
const missingRunsToDelist = 2

// ApplySourceProperty persists one observation atomically: provenance, listing,
// snapshot, field provenance, price history, and photo slots.
func (s *Store) ApplySourceProperty(ctx context.Context, runID domain.IngestRunID, sp ingest.SourceProperty, observedAt time.Time) (ingest.ApplyResult, error) {
	var res ingest.ApplyResult
	err := s.InTx(ctx, func(tx *Store) error {
		var err error
		res, err = tx.applySourceProperty(ctx, runID, sp, observedAt)
		return err
	})
	if err != nil {
		return ingest.ApplyResult{}, err
	}
	return res, nil
}

func (s *Store) applySourceProperty(ctx context.Context, runID domain.IngestRunID, sp ingest.SourceProperty, observedAt time.Time) (ingest.ApplyResult, error) {
	const findSource = `
SELECT s.listing_id, l.status, c.price
FROM listing_sources s
JOIN listings l ON l.id = s.listing_id
LEFT JOIN listing_current c ON c.listing_id = s.listing_id
WHERE s.provider = $1 AND s.provider_id = $2
FOR NO KEY UPDATE OF s, l`

	// FOR UPDATE cannot lock a row that does not exist yet: two drains seeing a
	// new listing at once would each insert one, orphaning the loser's row.
	const lockSource = `SELECT pg_advisory_xact_lock(hashtext($1 || '/' || $2))`
	if _, err := s.q.Exec(ctx, lockSource, sp.Provider, sp.ProviderID); err != nil {
		return ingest.ApplyResult{}, fmt.Errorf("lock source %s/%s: %w", sp.Provider, sp.ProviderID, err)
	}

	var (
		listingID  int64
		prevStatus string
		prevPrice  *int32
	)
	err := s.q.QueryRow(ctx, findSource, sp.Provider, sp.ProviderID).Scan(&listingID, &prevStatus, &prevPrice)
	created := errors.Is(err, pgx.ErrNoRows)
	if err != nil && !created {
		return ingest.ApplyResult{}, fmt.Errorf("look up source %s/%s: %w", sp.Provider, sp.ProviderID, err)
	}

	p := sp.Property
	// A blank status keeps what the detail page last said, except that being
	// seen at all reactivates a delisted listing.
	status := string(p.Status)
	if status == "" {
		status = prevStatus
		if created || prevStatus == string(domain.StatusDelisted) {
			status = string(domain.StatusActive)
		}
	}
	listingType := string(p.ListingType)
	if listingType == "" {
		listingType = string(domain.ListingSale)
	}

	newPrice := fitPtr[int32](p.Price)
	priceChanged := newPrice != nil && (prevPrice == nil || *prevPrice != *newPrice)
	statusChanged := !created && status != prevStatus
	// A first-ever price is history, not a change worth resurfacing.
	materialChanged := !created && (statusChanged || (prevPrice != nil && priceChanged))

	if created {
		const insertListing = `
INSERT INTO listings (
	canonical_address, unit, neighborhood, zip, lat, lng,
	listing_type, property_type, status, first_seen, last_seen, material_changed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $10, $10)
RETURNING id`

		row := s.q.QueryRow(ctx, insertListing,
			p.Address.Street, nullText(p.Address.Unit), nullText(p.Address.Neighborhood), nullText(p.Address.Zip),
			latOf(p.Geo), lngOf(p.Geo),
			listingType, nullText(string(p.PropertyType)), status, observedAt,
		)
		if err := row.Scan(&listingID); err != nil {
			return ingest.ApplyResult{}, fmt.Errorf("insert listing for %s/%s: %w", sp.Provider, sp.ProviderID, err)
		}
	} else {
		const updateListing = `
UPDATE listings SET
	canonical_address = $2,
	-- A search row omits what only the detail page reports; keep the last known.
	unit = COALESCE($3, unit),
	neighborhood = COALESCE($4, neighborhood),
	zip = COALESCE($5, zip),
	lat = COALESCE($6, lat),
	lng = COALESCE($7, lng),
	listing_type = $8,
	property_type = $9,
	status = $10,
	last_seen = $11,
	material_changed_at = CASE WHEN $12 THEN $11 ELSE material_changed_at END
WHERE id = $1`

		if _, err := s.q.Exec(ctx, updateListing, listingID,
			p.Address.Street, nullText(p.Address.Unit), nullText(p.Address.Neighborhood), nullText(p.Address.Zip),
			latOf(p.Geo), lngOf(p.Geo),
			listingType, nullText(string(p.PropertyType)), status, observedAt, materialChanged,
		); err != nil {
			return ingest.ApplyResult{}, fmt.Errorf("update listing %d: %w", listingID, err)
		}
	}

	if err := s.upsertSource(ctx, listingID, runID, sp, observedAt); err != nil {
		return ingest.ApplyResult{}, err
	}
	if err := s.upsertCurrent(ctx, listingID, p, observedAt); err != nil {
		return ingest.ApplyResult{}, err
	}
	if err := s.upsertFields(ctx, listingID, sp, status, observedAt); err != nil {
		return ingest.ApplyResult{}, err
	}
	if priceChanged {
		const insertEvent = `INSERT INTO price_events (listing_id, price, observed_at) VALUES ($1, $2, $3)`
		if _, err := s.q.Exec(ctx, insertEvent, listingID, *newPrice, observedAt); err != nil {
			return ingest.ApplyResult{}, fmt.Errorf("insert price event for listing %d: %w", listingID, err)
		}
	}
	evicted, err := s.replacePhotos(ctx, listingID, sp)
	if err != nil {
		return ingest.ApplyResult{}, err
	}

	return ingest.ApplyResult{
		PropertyID:      domain.PropertyID(listingID),
		EvictedKeys:     evicted,
		Created:         created,
		PriceChanged:    priceChanged,
		StatusChanged:   statusChanged,
		MaterialChanged: materialChanged,
	}, nil
}

func (s *Store) upsertSource(ctx context.Context, listingID int64, runID domain.IngestRunID, sp ingest.SourceProperty, observedAt time.Time) error {
	const query = `
INSERT INTO listing_sources (
	listing_id, provider, provider_id, url, source_status, raw,
	first_seen_at, last_seen_at, fetched_at, missing_runs, last_run_id, enriched_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $7, $7, 0, $8, CASE WHEN $9::boolean THEN $7::timestamptz END)
ON CONFLICT (provider, provider_id) DO UPDATE SET
	listing_id = excluded.listing_id,
	url = excluded.url,
	source_status = excluded.source_status,
	raw = excluded.raw,
	last_seen_at = excluded.last_seen_at,
	fetched_at = excluded.fetched_at,
	enriched_at = COALESCE(excluded.enriched_at, listing_sources.enriched_at),
	missing_runs = 0,
	-- Absence ownership: a one-off (or run-less) apply never takes it from the scope that holds it.
	last_run_id = CASE
		WHEN listing_sources.last_run_id IS NOT NULL
		 AND coalesce((SELECT one_off FROM ingest_runs WHERE id = excluded.last_run_id), true)
		THEN listing_sources.last_run_id
		ELSE excluded.last_run_id END`

	raw := []byte(sp.Raw)
	if len(raw) == 0 {
		raw = []byte("null")
	}
	var run *int64
	if runID != 0 {
		id := int64(runID)
		run = &id
	}
	if _, err := s.q.Exec(ctx, query, listingID, sp.Provider, sp.ProviderID, sp.URL,
		nullText(sp.SourceStatus), raw, observedAt, run, sp.Enriched); err != nil {
		return fmt.Errorf("upsert source %s/%s: %w", sp.Provider, sp.ProviderID, err)
	}
	return nil
}

func (s *Store) upsertCurrent(ctx context.Context, listingID int64, p domain.Property, observedAt time.Time) error {
	const query = `
INSERT INTO listing_current (
	listing_id, price, currency, beds, baths, sqft,
	maintenance, common_charges, taxes_monthly, dom, description, observed_at)
VALUES ($1, $2, $3, $4, $5::float8, $6, $7, $8, $9, $10, $11, $12)
ON CONFLICT (listing_id) DO UPDATE SET
	-- A page that omits the price keeps the last known one, so a drop that
	-- straddles the gap is still seen against it (and the row stays searchable).
	price = COALESCE(excluded.price, listing_current.price),
	currency = CASE WHEN excluded.price IS NULL THEN listing_current.currency ELSE excluded.currency END,
	-- A field the incoming apply lacks keeps its stored value, so an incremental
	-- (search-only, unenriched) pass never erases what a deep pass fetched.
	beds = COALESCE(excluded.beds, listing_current.beds),
	baths = COALESCE(excluded.baths, listing_current.baths),
	sqft = COALESCE(excluded.sqft, listing_current.sqft),
	maintenance = COALESCE(excluded.maintenance, listing_current.maintenance),
	common_charges = COALESCE(excluded.common_charges, listing_current.common_charges),
	taxes_monthly = COALESCE(excluded.taxes_monthly, listing_current.taxes_monthly),
	dom = COALESCE(excluded.dom, listing_current.dom),
	description = COALESCE(excluded.description, listing_current.description),
	observed_at = excluded.observed_at`

	if _, err := s.q.Exec(ctx, query, listingID,
		fitPtr[int32](p.Price), currencyToColumn(p.Currency), fitPtr[int16](p.Bedrooms), fitBaths(p.Bathrooms),
		fitPtr[int32](p.Sqft), fitPtr[int32](p.Maintenance), fitPtr[int32](p.CommonCharges),
		fitPtr[int32](p.TaxesMonthly), fitPtr[int32](p.DaysOnMarket),
		nullText(p.Description), observedAt,
	); err != nil {
		return fmt.Errorf("upsert current snapshot for listing %d: %w", listingID, err)
	}
	return nil
}

// upsertFields records per-field provenance. Unknown (nil) values are not
// written: absence of a row means "not reported".
func (s *Store) upsertFields(ctx context.Context, listingID int64, sp ingest.SourceProperty, status string, observedAt time.Time) error {
	const query = `
INSERT INTO listing_fields (listing_id, field, value, provider, provider_id, observed_at)
VALUES ($1, $2, $3, $4, $5, $6)
ON CONFLICT (listing_id, field) DO UPDATE SET
	value = excluded.value,
	provider = excluded.provider,
	provider_id = excluded.provider_id,
	observed_at = excluded.observed_at`

	p := sp.Property
	type field struct {
		name  string
		value any
	}
	fields := []field{
		{"price", p.Price},
		{"beds", p.Bedrooms},
		{"baths", fitBaths(p.Bathrooms)},
		{"sqft", p.Sqft},
		{"maintenance", p.Maintenance},
		{"common_charges", p.CommonCharges},
		{"taxes_monthly", p.TaxesMonthly},
		{"dom", p.DaysOnMarket},
		{"status", status},
		{"address", p.Address.String()},
	}
	for _, f := range fields {
		if isNilValue(f.value) {
			continue
		}
		encoded, err := json.Marshal(f.value)
		if err != nil {
			return fmt.Errorf("encode field %q for listing %d: %w", f.name, listingID, err)
		}
		if _, err := s.q.Exec(ctx, query, listingID, f.name, encoded, sp.Provider, sp.ProviderID, observedAt); err != nil {
			return fmt.Errorf("upsert field %q for listing %d: %w", f.name, listingID, err)
		}
	}
	return nil
}

// replacePhotos rewrites the photo slots, carrying cached thumbnail metadata with
// its source URL across positions (a re-ordered gallery is not re-downloaded).
// It returns the store keys the rewrite displaced and no other row references.
func (s *Store) replacePhotos(ctx context.Context, listingID int64, sp ingest.SourceProperty) ([]string, error) {
	const displaced = `
WITH displaced AS (
	SELECT p.listing_id, p.position, p.cached_path FROM listing_photos p
	WHERE p.listing_id = $1 AND p.cached_path IS NOT NULL AND NOT (p.source_url = ANY($2::text[]))
)
SELECT coalesce(array_agg(DISTINCT d.cached_path), '{}') FROM displaced d
WHERE NOT EXISTS (
	SELECT 1 FROM listing_photos o
	WHERE o.cached_path = d.cached_path
	  AND NOT EXISTS (SELECT 1 FROM displaced d2 WHERE d2.listing_id = o.listing_id AND d2.position = o.position))`
	urls := sp.PhotoURLs
	if urls == nil {
		urls = []string{}
	}
	if !sp.Enriched {
		// A search row may carry fewer photos than the detail page did; only a
		// deep pass (or a genuinely new photo) may shrink or evict the gallery.
		const known = `SELECT coalesce(array_agg(source_url), '{}') FROM listing_photos WHERE listing_id = $1`
		var existing []string
		if err := s.q.QueryRow(ctx, known, listingID).Scan(&existing); err != nil {
			return nil, fmt.Errorf("load photos for listing %d: %w", listingID, err)
		}
		if len(urls) < len(existing) && subset(urls, existing) {
			return nil, nil
		}
	}
	var evicted []string
	if err := s.q.QueryRow(ctx, displaced, listingID, urls).Scan(&evicted); err != nil {
		return nil, fmt.Errorf("find displaced photos for listing %d: %w", listingID, err)
	}

	const cachedByURL = `
SELECT source_url, cached_path, mime_type, width, height, refreshed_at
FROM listing_photos WHERE listing_id = $1 AND cached_path IS NOT NULL ORDER BY position`
	rows, err := s.q.Query(ctx, cachedByURL, listingID)
	if err != nil {
		return nil, fmt.Errorf("load cached photos for listing %d: %w", listingID, err)
	}
	cached, err := pgx.CollectRows(rows, pgx.RowToStructByPos[cachedPhotoRow])
	if err != nil {
		return nil, fmt.Errorf("scan cached photos for listing %d: %w", listingID, err)
	}
	byURL := make(map[string]cachedPhotoRow, len(cached))
	for _, c := range cached {
		if _, dup := byURL[c.SourceURL]; !dup {
			byURL[c.SourceURL] = c
		}
	}

	const upsert = `
INSERT INTO listing_photos (listing_id, position, provider, source_url, cached_path, mime_type, width, height, refreshed_at)
VALUES ($1, $2, $3, $4, $5, $6, $7, $8, COALESCE($9, now()))
ON CONFLICT (listing_id, position) DO UPDATE SET
	provider = excluded.provider,
	source_url = excluded.source_url,
	cached_path = excluded.cached_path,
	mime_type = excluded.mime_type,
	width = excluded.width,
	height = excluded.height,
	refreshed_at = excluded.refreshed_at`

	for i, url := range sp.PhotoURLs {
		c := byURL[url]
		if _, err := s.q.Exec(ctx, upsert, listingID, i, sp.Provider, url,
			c.CachedPath, c.MIMEType, c.Width, c.Height, c.RefreshedAt); err != nil {
			return nil, fmt.Errorf("upsert photo %d for listing %d: %w", i, listingID, err)
		}
	}
	const deleteTail = `DELETE FROM listing_photos WHERE listing_id = $1 AND position >= $2`
	if _, err := s.q.Exec(ctx, deleteTail, listingID, len(sp.PhotoURLs)); err != nil {
		return nil, fmt.Errorf("trim photos for listing %d: %w", listingID, err)
	}
	return evicted, nil
}

func subset(of, in []string) bool {
	set := make(map[string]struct{}, len(in))
	for _, u := range in {
		set[u] = struct{}{}
	}
	for _, u := range of {
		if _, ok := set[u]; !ok {
			return false
		}
	}
	return true
}

type cachedPhotoRow struct {
	SourceURL   string
	CachedPath  *string
	MIMEType    *string
	Width       *int32
	Height      *int32
	RefreshedAt *time.Time
}

func (s *Store) SetPhotoCache(ctx context.Context, id domain.PropertyID, position int, sourceURL, cachedPath, mimeType string, width, height int) error {
	// refreshed_at is the LRU touch: every crawl that (re)caches or re-verifies a
	// photo bumps it, so an actively-crawled listing never ages out of the cache.
	// Matched by URL, not position: a search-only sighting may index a kept
	// gallery differently, and the URL guard alone stops a stale thumb landing.
	const query = `
UPDATE listing_photos
SET cached_path = $3, mime_type = $4, width = $5, height = $6, refreshed_at = now()
WHERE listing_id = $1 AND source_url = $2`

	tag, err := s.q.Exec(ctx, query, int64(id), sourceURL, nullText(cachedPath), nullText(mimeType), width, height)
	if err != nil {
		return fmt.Errorf("set photo cache for listing %d position %d: %w", id, position, err)
	}
	if tag.RowsAffected() == 0 {
		return fmt.Errorf("set photo cache: listing %d position %d no longer holds %s", id, position, sourceURL)
	}
	return nil
}

// MarkMissingSources advances absence counting. The run-eligibility check here
// is a defensive backstop; the ingest service owns the actual decision.
func (s *Store) MarkMissingSources(ctx context.Context, provider string, seenProviderIDs []string, runID domain.IngestRunID) (ingest.MissingResult, error) {
	var eligible bool
	err := s.q.QueryRow(ctx,
		`SELECT complete AND NOT suspect FROM ingest_runs WHERE id = $1`, int64(runID)).Scan(&eligible)
	if errors.Is(err, pgx.ErrNoRows) {
		return ingest.MissingResult{}, fmt.Errorf("run %d: %w", runID, ErrRunNotEligible)
	}
	if err != nil {
		return ingest.MissingResult{}, fmt.Errorf("load run %d: %w", runID, err)
	}
	if !eligible {
		return ingest.MissingResult{}, fmt.Errorf("run %d: %w", runID, ErrRunNotEligible)
	}

	// A delisted listing's thumbnails are freed in the same transaction: the DB
	// forgets them (cached_path NULL, /img stops resolving) and the freed keys come
	// back so the caller can delete the S3 bytes — a rolling cache, not an archive.
	const query = `
WITH advanced AS (
	UPDATE listing_sources s
	SET missing_runs = s.missing_runs + 1
	FROM listings l
	WHERE l.id = s.listing_id
	  AND s.provider = $1
	  AND NOT EXISTS (SELECT 1 FROM unnest($2::text[]) seen(id) WHERE seen.id = s.provider_id)
	  AND l.status IN ('active', 'in_contract')
	  -- Absence is scope-scoped: only listings last seen by THIS run's scope, or
	  -- by a scope this standing run fully covers (same listing type, every area,
	  -- filters at least as wide), may be advanced. A narrower run never ages them.
	  AND EXISTS (
		SELECT 1 FROM ingest_runs prev, ingest_runs cur
		WHERE prev.id = s.last_run_id AND cur.id = $4
		  AND (prev.scope_hash = cur.scope_hash
		   OR (NOT cur.one_off AND prev.areas IS NOT NULL AND cur.areas IS NOT NULL
		       AND prev.areas <@ cur.areas
		       AND prev.listing_type = cur.listing_type
		       AND (cur.max_price IS NULL OR prev.max_price <= cur.max_price)
		       AND coalesce(cur.min_beds, 0) <= coalesce(prev.min_beds, 0)))
	  )
	RETURNING s.listing_id, s.missing_runs
), delisted AS (
	UPDATE listings
	SET status = 'delisted', material_changed_at = now()
	WHERE id IN (SELECT listing_id FROM advanced WHERE missing_runs >= $3)
	RETURNING id
), freed_target AS MATERIALIZED (
	-- Capture the keys BEFORE the update nulls them (RETURNING would give NULL).
	SELECT listing_id, position, cached_path FROM listing_photos
	WHERE listing_id IN (SELECT id FROM delisted) AND cached_path IS NOT NULL
), freed AS (
	UPDATE listing_photos p
	SET cached_path = NULL, mime_type = NULL, width = NULL, height = NULL
	FROM freed_target ft
	WHERE p.listing_id = ft.listing_id AND p.position = ft.position
	RETURNING 1
)
SELECT
	(SELECT count(*) FROM advanced),
	(SELECT count(*) FROM delisted),
	(SELECT coalesce(array_agg(DISTINCT ft.cached_path), '{}') FROM freed_target ft
	 WHERE NOT EXISTS (
		SELECT 1 FROM listing_photos o
		WHERE o.cached_path = ft.cached_path
		  AND NOT EXISTS (SELECT 1 FROM freed_target f2
		                  WHERE f2.listing_id = o.listing_id AND f2.position = o.position)))`

	if seenProviderIDs == nil {
		seenProviderIDs = []string{}
	}
	var res ingest.MissingResult
	if err := s.q.QueryRow(ctx, query, provider, seenProviderIDs, missingRunsToDelist, int64(runID)).
		Scan(&res.Advanced, &res.Delisted, &res.EvictedKeys); err != nil {
		return ingest.MissingResult{}, fmt.Errorf("advance missing sources for %s: %w", provider, err)
	}
	return res, nil
}

// FreeExpiredCachedPhotos is the TTL/LRU sweep: it NULLs cached_path for up to
// limit cached photos older than cutoff (oldest first) and returns the freed
// store keys plus the rows-freed count (for batching; keys de-duplicate).
func (s *Store) FreeExpiredCachedPhotos(ctx context.Context, cutoff time.Time, limit int) ([]string, int, error) {
	if limit <= 0 {
		return nil, 0, nil
	}
	const query = `
WITH expired AS MATERIALIZED (
	-- Capture the keys BEFORE the update nulls them (RETURNING would give NULL).
	SELECT listing_id, position, cached_path FROM listing_photos
	WHERE cached_path IS NOT NULL AND refreshed_at < $1
	ORDER BY refreshed_at
	LIMIT $2
), freed AS (
	UPDATE listing_photos p
	SET cached_path = NULL, mime_type = NULL, width = NULL, height = NULL
	FROM expired e
	WHERE p.listing_id = e.listing_id AND p.position = e.position
	RETURNING 1
)
SELECT
	(SELECT coalesce(array_agg(DISTINCT e.cached_path), '{}') FROM expired e
	 WHERE NOT EXISTS (
		SELECT 1 FROM listing_photos o
		WHERE o.cached_path = e.cached_path
		  AND NOT EXISTS (SELECT 1 FROM expired e2
		                  WHERE e2.listing_id = o.listing_id AND e2.position = o.position))),
	(SELECT count(*) FROM expired)`

	var (
		keys  []string
		freed int
	)
	if err := s.q.QueryRow(ctx, query, cutoff, limit).Scan(&keys, &freed); err != nil {
		return nil, 0, fmt.Errorf("free expired cached photos: %w", err)
	}
	return keys, freed, nil
}

// ForgetPhotoKeys clears any row still pointing at one of keys, whose bytes the
// caller just deleted; a concurrent drain may have re-pointed a row at them in
// between. Returns the number of rows cleared.
func (s *Store) ForgetPhotoKeys(ctx context.Context, keys []string) (int, error) {
	if len(keys) == 0 {
		return 0, nil
	}
	const query = `
UPDATE listing_photos
SET cached_path = NULL, mime_type = NULL, width = NULL, height = NULL
WHERE cached_path = ANY($1)`
	tag, err := s.q.Exec(ctx, query, keys)
	if err != nil {
		return 0, fmt.Errorf("forget photo keys: %w", err)
	}
	return int(tag.RowsAffected()), nil
}

func latOf(g *domain.GeoPoint) *float64 {
	if g == nil {
		return nil
	}
	return &g.Latitude
}

func lngOf(g *domain.GeoPoint) *float64 {
	if g == nil {
		return nil
	}
	return &g.Longitude
}

// fitPtr converts a nullable integer for a narrower column, mapping a value the
// column cannot hold to NULL instead of letting it wrap.
// fitBaths drops a value baths numeric(3,1) cannot hold (NaN, Inf, <0, >=100)
// instead of letting it abort the whole apply.
func fitBaths(p *float64) *float64 {
	if p == nil || math.IsNaN(*p) || math.IsInf(*p, 0) || *p < 0 || *p >= 99.95 {
		return nil
	}
	return p
}

func fitPtr[U, T integer](p *T) *U {
	if p == nil {
		return nil
	}
	v := U(*p)
	if T(v) != *p {
		return nil
	}
	return &v
}

func isNilValue(v any) bool {
	switch t := v.(type) {
	case nil:
		return true
	case *domain.Money:
		return t == nil
	case *int:
		return t == nil
	case *float64:
		return t == nil
	case string:
		return t == ""
	default:
		return false
	}
}
