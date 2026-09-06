-- +goose Up
-- +goose StatementBegin

-- A run remembers the scope it swept (not just its hash) so a standing scope
-- covering a one-off's areas and filters can age the listings that one-off first
-- saw. NULL for runs older than this migration and for preflight rows.
ALTER TABLE ingest_runs
    ADD COLUMN listing_type text,
    ADD COLUMN max_price integer,
    ADD COLUMN min_beds smallint,
    ADD COLUMN areas text[];

-- When the detail page was last merged in, so a deep pass interrupted midway
-- resumes with the listings it had not reached instead of starting over.
ALTER TABLE listing_sources ADD COLUMN enriched_at timestamptz;

-- When the current run claimed the target; drives stale-claim recovery for
-- standing rows, whose last_run_at must not move at claim time.
ALTER TABLE crawl_targets ADD COLUMN started_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE crawl_targets DROP COLUMN IF EXISTS started_at;
ALTER TABLE listing_sources DROP COLUMN IF EXISTS enriched_at;
ALTER TABLE ingest_runs
    DROP COLUMN IF EXISTS areas,
    DROP COLUMN IF EXISTS min_beds,
    DROP COLUMN IF EXISTS max_price,
    DROP COLUMN IF EXISTS listing_type;
-- +goose StatementEnd
