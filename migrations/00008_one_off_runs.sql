-- +goose Up
-- +goose StatementBegin
-- A one-off crawl (once target) observes listings without taking absence
-- ownership, so a filtered request_crawl cannot make a standing scope's
-- listings un-delistable.
ALTER TABLE ingest_runs ADD COLUMN one_off boolean NOT NULL DEFAULT false;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE ingest_runs DROP COLUMN IF EXISTS one_off;
-- +goose StatementEnd
