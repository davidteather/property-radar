-- +goose Up
-- +goose StatementBegin

-- User-curated lists of listings (favorites and named feature buckets like
-- "big windows"). Single-operator: no user scoping, matching profile/verdicts.
-- The seeded 'favorites' default list cannot be deleted.
CREATE TABLE lists (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    slug text NOT NULL UNIQUE,
    name text NOT NULL,
    emoji text,
    is_default boolean NOT NULL DEFAULT false,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- At most one default (favorites) list.
CREATE UNIQUE INDEX lists_one_default_idx ON lists (is_default) WHERE is_default;

CREATE TABLE list_items (
    list_id bigint NOT NULL REFERENCES lists(id) ON DELETE CASCADE,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    note text,
    added_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (list_id, listing_id)
);

CREATE INDEX list_items_listing_idx ON list_items (listing_id);

INSERT INTO lists (slug, name, emoji, is_default) VALUES ('favorites', 'Favorites', '⭐', true);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS list_items;
DROP TABLE IF EXISTS lists;
-- +goose StatementEnd
