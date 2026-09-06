package store

import "time"

// Row structs mirror the schema (nullable columns are pointers, integer widths
// match) and never leave this package.

type propertyRow struct {
	ID                int64     `db:"id"`
	CanonicalAddress  string    `db:"canonical_address"`
	Unit              *string   `db:"unit"`
	Neighborhood      *string   `db:"neighborhood"`
	Zip               *string   `db:"zip"`
	Lat               *float64  `db:"lat"`
	Lng               *float64  `db:"lng"`
	ListingType       string    `db:"listing_type"`
	PropertyType      *string   `db:"property_type"`
	Status            string    `db:"status"`
	FirstSeen         time.Time `db:"first_seen"`
	LastSeen          time.Time `db:"last_seen"`
	MaterialChangedAt time.Time `db:"material_changed_at"`

	Price *int32 `db:"price"`
	// Currency is nullable here only because listing_current is LEFT JOINed.
	Currency      *string  `db:"currency"`
	Beds          *int16   `db:"beds"`
	Baths         *float64 `db:"baths"`
	Sqft          *int32   `db:"sqft"`
	Maintenance   *int32   `db:"maintenance"`
	CommonCharges *int32   `db:"common_charges"`
	TaxesMonthly  *int32   `db:"taxes_monthly"`
	DOM           *int32   `db:"dom"`
	Description   *string  `db:"description"`
	// URL is the shareable provider link, LEFT JOINed from listing_sources.
	URL *string `db:"url"`
}

type photoRow struct {
	Position   int32   `db:"position"`
	SourceURL  string  `db:"source_url"`
	CachedPath *string `db:"cached_path"`
	MIMEType   *string `db:"mime_type"`
	Width      *int32  `db:"width"`
	Height     *int32  `db:"height"`
}

type priceEventRow struct {
	ListingID  int64     `db:"listing_id"`
	Price      int32     `db:"price"`
	ObservedAt time.Time `db:"observed_at"`
}

type priceDropRow struct {
	ListingID int64 `db:"listing_id"`
	Dropped   bool  `db:"dropped"`
}

type photoCountRow struct {
	ListingID int64 `db:"listing_id"`
	Total     int64 `db:"total"`
	Cached    int64 `db:"cached"`
}

type verdictRow struct {
	ID        int64     `db:"id"`
	ListingID int64     `db:"listing_id"`
	Verdict   string    `db:"verdict"`
	Note      *string   `db:"note"`
	CreatedAt time.Time `db:"created_at"`
}

type rubricRow struct {
	ID               int64     `db:"id"`
	Content          string    `db:"content"`
	ThroughVerdictID *int64    `db:"through_verdict_id"`
	CreatedAt        time.Time `db:"created_at"`
}

type profileRow struct {
	MaxPrice           *int32    `db:"max_price"`
	MinBeds            *int16    `db:"min_beds"`
	MinBaths           *float64  `db:"min_baths"`
	MaxMonthlyCarrying *int32    `db:"max_monthly_carrying"`
	ListingType        string    `db:"listing_type"`
	Neighborhoods      []string  `db:"neighborhoods"`
	PropertyTypes      []string  `db:"property_types"`
	UpdatedAt          time.Time `db:"updated_at"`
}

type runRow struct {
	ID            int64      `db:"id"`
	Provider      string     `db:"provider"`
	ScopeHash     string     `db:"scope_hash"`
	StartedAt     time.Time  `db:"started_at"`
	FinishedAt    *time.Time `db:"finished_at"`
	Complete      bool       `db:"complete"`
	ListingsSeen  *int32     `db:"listings_seen"`
	Created       *int32     `db:"created"`
	Updated       *int32     `db:"updated"`
	PhotoFailures *int32     `db:"photo_failures"`
	Errors        []byte     `db:"errors"`
	Suspect       bool       `db:"suspect"`
}

// runErrorJSON is the persisted shape of ingest_runs.errors; domain types carry
// no serialization tags, so the store owns it.
type runErrorJSON struct {
	ProviderID string `json:"provider_id"`
	Message    string `json:"message"`
}

type crawlTargetRow struct {
	ID          int64      `db:"id"`
	Kind        string     `db:"kind"`
	Areas       []string   `db:"areas"`
	ListingType string     `db:"listing_type"`
	MaxPrice    *int32     `db:"max_price"`
	MinBeds     *int16     `db:"min_beds"`
	Enabled     bool       `db:"enabled"`
	Status      string     `db:"status"`
	Note        *string    `db:"note"`
	CreatedAt   time.Time  `db:"created_at"`
	LastRunID   *int64     `db:"last_run_id"`
	LastError   *string    `db:"last_error"`
	LastRunAt   *time.Time `db:"last_run_at"`
	LastDeepAt  *time.Time `db:"last_deep_at"`
}

type sourceRow struct {
	Provider     string    `db:"provider"`
	ProviderID   string    `db:"provider_id"`
	URL          string    `db:"url"`
	SourceStatus *string   `db:"source_status"`
	FirstSeenAt  time.Time `db:"first_seen_at"`
	LastSeenAt   time.Time `db:"last_seen_at"`
	FetchedAt    time.Time `db:"fetched_at"`
	MissingRuns  int32     `db:"missing_runs"`
}
