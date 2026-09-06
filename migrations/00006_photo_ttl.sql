-- +goose Up
-- +goose StatementBegin

-- Photo cache is a rolling cache, not an archive: refreshed_at records when a
-- listing's thumbnail was last (re)written or re-verified by a crawl. A crawl
-- "touches" it each time it re-caches the photo, so an actively-crawled listing
-- stays fresh; one that stops being seen ages out and the LRU/TTL sweep evicts
-- its bytes (default 30 days, PHOTO_TTL_DAYS). Existing rows start fresh (now())
-- so a first deploy of this migration does not evict everything at once.
ALTER TABLE listing_photos
    ADD COLUMN refreshed_at timestamptz NOT NULL DEFAULT now();

-- Drives the TTL sweep: only cached rows matter, ordered by staleness.
CREATE INDEX listing_photos_refreshed_idx
    ON listing_photos (refreshed_at)
    WHERE cached_path IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS listing_photos_refreshed_idx;
ALTER TABLE listing_photos DROP COLUMN IF EXISTS refreshed_at;
-- +goose StatementEnd
