-- +goose Up
-- +goose StatementBegin

-- Crawl scopes the ingest worker drains: 'standing' rows are the recurring
-- corpus scopes, 'once' rows are single agent-requested seeds.
CREATE TABLE crawl_targets (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    kind text NOT NULL CHECK (kind IN ('standing','once')),
    areas text[] NOT NULL,
    listing_type text NOT NULL CHECK (listing_type IN ('sale','rent')),
    max_price integer,
    min_beds smallint,
    -- enabled gates standing rows only; 'once' rows are gated by status.
    enabled boolean NOT NULL DEFAULT true,
    status text NOT NULL DEFAULT 'pending'
        CHECK (status IN ('pending','running','done','failed')),
    note text,
    created_at timestamptz NOT NULL DEFAULT now(),
    last_run_id bigint REFERENCES ingest_runs(id) ON DELETE SET NULL,
    last_error text
);

-- Dedupe guard for agent retry-spam: at most one pending 'once' target per
-- identical scope. Areas are normalized (sorted, deduped) before insert.
CREATE UNIQUE INDEX crawl_targets_pending_once_idx
    ON crawl_targets (listing_type, areas, coalesce(max_price, -1), coalesce(min_beds, -1))
    WHERE kind = 'once' AND status = 'pending';

CREATE INDEX crawl_targets_due_idx ON crawl_targets (kind, status, enabled, id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS crawl_targets;
-- +goose StatementEnd
