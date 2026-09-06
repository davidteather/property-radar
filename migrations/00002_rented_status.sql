-- +goose Up
-- +goose StatementBegin

ALTER TABLE listings DROP CONSTRAINT listings_status_check;
ALTER TABLE listings ADD CONSTRAINT listings_status_check
    CHECK (status IN ('active','in_contract','sold','delisted','rented'));

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin

-- 'rented' is a terminal off-market state, so the closest pre-migration value is
-- 'delisted'; material_changed_at is left alone because nothing materially changed.
UPDATE listings SET status = 'delisted' WHERE status = 'rented';
UPDATE listing_fields SET value = '"delisted"'::jsonb
    WHERE field = 'status' AND value = '"rented"'::jsonb;

ALTER TABLE listings DROP CONSTRAINT listings_status_check;
ALTER TABLE listings ADD CONSTRAINT listings_status_check
    CHECK (status IN ('active','in_contract','sold','delisted'));

-- +goose StatementEnd
