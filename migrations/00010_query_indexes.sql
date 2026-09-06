-- +goose Up
-- +goose StatementBegin

-- Every neighborhood filter compares lower(neighborhood), which the plain
-- column index cannot serve; index the expression the queries actually use.
CREATE INDEX listings_neighborhood_lower_idx ON listings (lower(neighborhood));
DROP INDEX IF EXISTS listings_neighborhood_idx;

-- Absence marking joins each source to the run that last saw it.
CREATE INDEX listing_sources_last_run_idx ON listing_sources (last_run_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP INDEX IF EXISTS listing_sources_last_run_idx;
CREATE INDEX listings_neighborhood_idx ON listings (neighborhood);
DROP INDEX IF EXISTS listings_neighborhood_lower_idx;
-- +goose StatementEnd
