-- +goose Up
-- +goose StatementBegin

-- Canonical listing identity and lifecycle. One row per canonical property.
CREATE TABLE listings (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    canonical_address text NOT NULL,
    unit text,
    neighborhood text,
    zip text,
    lat double precision,
    lng double precision,
    listing_type text NOT NULL CHECK (listing_type IN ('sale','rent')),
    property_type text CHECK (property_type IN ('coop','condo','townhouse','house','other')),
    status text NOT NULL DEFAULT 'active'
        CHECK (status IN ('active','in_contract','sold','delisted')),
    first_seen timestamptz NOT NULL DEFAULT now(),
    last_seen timestamptz NOT NULL DEFAULT now(),
    material_changed_at timestamptz NOT NULL DEFAULT now()
);

-- Hot query/read model. One typed current snapshot per canonical listing.
CREATE TABLE listing_current (
    listing_id bigint PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    price integer,
    currency text NOT NULL DEFAULT 'USD',
    beds smallint,
    baths numeric(3,1),
    sqft integer,
    maintenance integer,
    common_charges integer,
    taxes_monthly integer,
    dom integer,
    description text,
    observed_at timestamptz NOT NULL
);

CREATE TABLE ingest_runs (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    provider text NOT NULL,
    scope_hash text NOT NULL,
    started_at timestamptz NOT NULL,
    finished_at timestamptz,
    complete boolean NOT NULL DEFAULT false,
    listings_seen integer,
    created integer,
    updated integer,
    photo_failures integer,
    errors jsonb,
    suspect boolean NOT NULL DEFAULT false
);

-- Raw provider provenance. V0 canonical identity is (provider, provider_id).
-- last_run_id is a deviation from the spec: it records which ingest run last
-- observed this source, so raw provenance can be tied back to a run record.
CREATE TABLE listing_sources (
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    provider text NOT NULL,
    provider_id text NOT NULL,
    url text NOT NULL,
    source_status text,
    raw jsonb NOT NULL,
    first_seen_at timestamptz NOT NULL,
    last_seen_at timestamptz NOT NULL,
    fetched_at timestamptz NOT NULL,
    missing_runs integer NOT NULL DEFAULT 0,
    last_run_id bigint REFERENCES ingest_runs(id) ON DELETE SET NULL,
    PRIMARY KEY (provider, provider_id)
);

-- Selected canonical value + provenance. Raw source payload remains authoritative.
CREATE TABLE listing_fields (
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    field text NOT NULL,
    value jsonb NOT NULL,
    provider text NOT NULL,
    provider_id text NOT NULL,
    observed_at timestamptz NOT NULL,
    PRIMARY KEY (listing_id, field)
);

-- Append only, but only insert on an actual canonical price change.
CREATE TABLE price_events (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    price integer NOT NULL,
    observed_at timestamptz NOT NULL
);

CREATE TABLE listing_photos (
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    position integer NOT NULL,
    provider text NOT NULL,
    source_url text NOT NULL,
    cached_path text,
    mime_type text,
    width integer,
    height integer,
    PRIMARY KEY (listing_id, position)
);

-- Immutable; current opinion for a listing is the latest row.
CREATE TABLE verdicts (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    listing_id bigint NOT NULL REFERENCES listings(id) ON DELETE CASCADE,
    verdict text NOT NULL CHECK (verdict IN ('love','maybe','dislike')),
    note text,
    created_at timestamptz NOT NULL DEFAULT now()
);

-- Append-only. through_verdict_id makes rubric freshness measurable.
CREATE TABLE rubric_versions (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    content text NOT NULL,
    through_verdict_id bigint REFERENCES verdicts(id),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE profile (
    id boolean PRIMARY KEY DEFAULT true CHECK (id),
    max_price integer,
    min_beds smallint,
    min_baths numeric(3,1),
    max_monthly_carrying integer,
    listing_type text NOT NULL DEFAULT 'sale'
        CHECK (listing_type IN ('sale','rent')),
    neighborhoods text[],
    property_types text[],
    updated_at timestamptz NOT NULL DEFAULT now()
);

-- Stores the listing version the user/model actually saw.
-- A later material change makes the listing eligible to resurface.
CREATE TABLE shown (
    listing_id bigint PRIMARY KEY REFERENCES listings(id) ON DELETE CASCADE,
    shown_at timestamptz NOT NULL DEFAULT now(),
    material_changed_at_seen timestamptz NOT NULL
);

CREATE INDEX listings_active_idx
    ON listings (listing_type, status, material_changed_at DESC);
CREATE INDEX listings_neighborhood_idx ON listings (neighborhood);
CREATE INDEX listing_current_price_beds_idx ON listing_current (price, beds);
CREATE INDEX verdicts_listing_created_idx ON verdicts (listing_id, created_at DESC);
CREATE INDEX price_events_listing_observed_idx ON price_events (listing_id, observed_at DESC);

-- Not in the spec: listing_sources is keyed by (provider, provider_id) but the
-- detail read path and lifecycle updates look sources up by listing_id.
CREATE INDEX listing_sources_listing_idx ON listing_sources (listing_id);

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DROP TABLE IF EXISTS shown;
DROP TABLE IF EXISTS profile;
DROP TABLE IF EXISTS rubric_versions;
DROP TABLE IF EXISTS verdicts;
DROP TABLE IF EXISTS listing_photos;
DROP TABLE IF EXISTS price_events;
DROP TABLE IF EXISTS listing_fields;
DROP TABLE IF EXISTS listing_sources;
DROP TABLE IF EXISTS ingest_runs;
DROP TABLE IF EXISTS listing_current;
DROP TABLE IF EXISTS listings;
-- +goose StatementEnd
