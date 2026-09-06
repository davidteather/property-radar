-- +goose Up
-- +goose StatementBegin

-- Thumbnail keys hash the source URL, so two listings can share one object.
-- Eviction only deletes bytes no other row still references; this index makes
-- that reference check cheap.
CREATE INDEX listing_photos_cached_path_idx
    ON listing_photos (cached_path)
    WHERE cached_path IS NOT NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS listing_photos_cached_path_idx;
-- +goose StatementEnd
