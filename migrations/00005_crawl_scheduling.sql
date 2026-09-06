-- +goose Up
-- +goose StatementBegin

-- Scheduling state for the drain loop: when the target last ran at all, and
-- when it last ran a deep (full re-enrich) crawl. Standing targets run an
-- incremental crawl every drain and a deep one once ~20h have passed.
ALTER TABLE crawl_targets
    ADD COLUMN last_run_at timestamptz,
    ADD COLUMN last_deep_at timestamptz;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE crawl_targets
    DROP COLUMN IF EXISTS last_run_at,
    DROP COLUMN IF EXISTS last_deep_at;
-- +goose StatementEnd
