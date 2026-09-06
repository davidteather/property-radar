package mcp

import "time"

// Transport-owned DTOs. JSON tags exist only in this package; domain types carry none.

type profileDTO struct {
	MaxPrice           *int64   `json:"max_price" jsonschema:"maximum asking price in whole dollars; null means no cap"`
	MinBeds            *int     `json:"min_beds" jsonschema:"minimum bedrooms; null or 0 means no minimum"`
	MinBaths           *float64 `json:"min_baths" jsonschema:"minimum bathrooms in half steps; null or 0 means no minimum"`
	MaxMonthlyCarrying *int64   `json:"max_monthly_carrying" jsonschema:"maximum monthly maintenance + common charges + taxes; null means no cap"`
	ListingType        string   `json:"listing_type" jsonschema:"sale or rent: which side of the corpus get_candidates draws from"`
	Neighborhoods      []string `json:"neighborhoods" jsonschema:"canonical neighborhood names the candidates are restricted to; empty means anywhere"`
	PropertyTypes      []string `json:"property_types" jsonschema:"allowed property types (coop, condo, townhouse, house, other); empty means any"`
	UpdatedAt          *string  `json:"updated_at" jsonschema:"when the profile was last saved (RFC 3339); null when it has never been set"`
}

type rubricDTO struct {
	Content          string `json:"content"`
	ThroughVerdictID int64  `json:"through_verdict_id"`
	CreatedAt        string `json:"created_at"`
}

type verdictDTO struct {
	ID        int64  `json:"id"`
	ListingID int64  `json:"listing_id"`
	Verdict   string `json:"verdict"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

type listingRowDTO struct {
	ID                 int64    `json:"id"`
	Address            string   `json:"address"`
	Neighborhood       string   `json:"neighborhood"`
	Price              *int64   `json:"price"`
	Beds               *int     `json:"beds"`
	Baths              *float64 `json:"baths"`
	Sqft               *int     `json:"sqft"`
	PropertyType       string   `json:"property_type"`
	ListingType        string   `json:"listing_type" jsonschema:"sale or rent; price is the asking price for a sale and the monthly rent for a rental"`
	MonthlyCarrying    *int64   `json:"monthly_carrying"`
	DOM                *int     `json:"dom"`
	DescriptionExcerpt string   `json:"description_preview" jsonschema:"first ~200 characters of the description, truncated; get the full text and the photos from get_listing"`
	PriceDrop          bool     `json:"price_drop"`
	Status             string   `json:"status" jsonschema:"active, in_contract, sold, rented, or delisted; only active rows are returned unless include_inactive is set"`
	URL                string   `json:"url"`
	PhotoCount         int      `json:"photo_count" jsonschema:"photos the listing has on the provider"`
	PhotosCached       int      `json:"photos_cached" jsonschema:"how many of them are serveable from this server: /img/l/{id}/{n} loads for n in 0..photos_cached-1"`
}

type priceEventDTO struct {
	Price      int64  `json:"price"`
	ObservedAt string `json:"observed_at"`
}

type provenanceDTO struct {
	Provider     string `json:"provider"`
	ProviderID   string `json:"provider_id"`
	URL          string `json:"url"`
	SourceStatus string `json:"source_status"`
	FirstSeenAt  string `json:"first_seen_at"`
	LastSeenAt   string `json:"last_seen_at"`
	FetchedAt    string `json:"fetched_at"`
	MissingRuns  int    `json:"missing_runs"`
}

type listingDetailDTO struct {
	ID                int64    `json:"id"`
	Address           string   `json:"address"`
	Street            string   `json:"street"`
	Unit              string   `json:"unit"`
	Neighborhood      string   `json:"neighborhood"`
	Zip               string   `json:"zip"`
	Latitude          *float64 `json:"latitude"`
	Longitude         *float64 `json:"longitude"`
	ListingType       string   `json:"listing_type"`
	PropertyType      string   `json:"property_type"`
	Status            string   `json:"status" jsonschema:"active, in_contract, sold, rented, or delisted"`
	Price             *int64   `json:"price"`
	Currency          string   `json:"currency"`
	Beds              *int     `json:"beds"`
	Baths             *float64 `json:"baths"`
	Sqft              *int     `json:"sqft"`
	Maintenance       *int64   `json:"maintenance"`
	CommonCharges     *int64   `json:"common_charges"`
	TaxesMonthly      *int64   `json:"taxes_monthly"`
	MonthlyCarrying   *int64   `json:"monthly_carrying"`
	DOM               *int     `json:"dom"`
	Description       string   `json:"description"`
	URL               string   `json:"url"`
	FirstSeen         string   `json:"first_seen"`
	LastSeen          string   `json:"last_seen"`
	MaterialChangedAt string   `json:"material_changed_at"`

	PriceHistory []priceEventDTO `json:"price_history"`
	Provenance   provenanceDTO   `json:"provenance"`

	PhotoCount     int             `json:"photo_count"`
	PhotosCached   int             `json:"photos_cached"`
	PhotosReturned int             `json:"photos_returned"`
	PhotoMissing   int             `json:"photo_missing"`
	PhotoLayout    string          `json:"photo_layout,omitempty" jsonschema:"contact_sheet or individual; set only when images were attached inline"`
	Sheets         int             `json:"sheets"`
	PhotoSheets    []photoSheetDTO `json:"photo_sheets"`
}

type photoSheetDTO struct {
	Sheet     int   `json:"sheet"`
	Positions []int `json:"positions" jsonschema:"[first, last] listing photo position on this sheet, row-major; these are raw listing positions, not /img/l/{id}/{n} cached indices"`
	Photos    int   `json:"photos"`
}

type effectiveFiltersDTO struct {
	ListingType     string   `json:"listing_type"`
	MaxPrice        *int64   `json:"max_price"`
	MinBeds         *int     `json:"min_beds"`
	MinBaths        *float64 `json:"min_baths"`
	Neighborhoods   []string `json:"neighborhoods"`
	PropertyTypes   []string `json:"property_types"`
	IncludeInactive bool     `json:"include_inactive"`
	Limit           int      `json:"limit"`
}

type getStateInput struct {
	VerdictsLimit int `json:"verdicts_limit,omitempty" jsonschema:"how many of the most recent verdicts to return (still oldest first); defaults to 300 and is capped at 2000. verdicts_total in the response says how many exist"`
}

type getStateOutput struct {
	Profile         profileDTO   `json:"profile"`
	Rubric          *rubricDTO   `json:"rubric"`
	RubricStale     bool         `json:"rubric_stale" jsonschema:"true when verdicts newer than the rubric's through_verdict_id exist; rewrite the rubric and save it with update_rubric"`
	Verdicts        []verdictDTO `json:"verdicts" jsonschema:"the most recent verdicts_limit verdicts, oldest first"`
	VerdictsTotal   int          `json:"verdicts_total" jsonschema:"how many verdicts exist in all; larger than len(verdicts) when the history was truncated"`
	LatestVerdictID int64        `json:"latest_verdict_id" jsonschema:"id of the newest verdict (0 when none): the value to pass update_rubric as through_verdict_id. Verdict ids are not listing ids"`
	ImageBaseURL    string       `json:"image_base_url,omitempty" jsonschema:"origin to prefix constructed /img/l/{id}/{n} photo URLs with; absent when this server cannot tell its own public origin"`
	// ImageAccessKey is present only when this instance gates its photo proxy. Append it as ?k=<key> to any {base}/img/... URL you construct so it loads; get_listings_photos already includes it. Absent means the proxy is open.
	ImageAccessKey string `json:"image_access_key,omitempty"`
}

type searchListingsInput struct {
	MaxPrice        *int64   `json:"max_price,omitempty" jsonschema:"maximum asking price in whole dollars (positive; omit for no cap); listings with no known price are excluded when set"`
	MinBeds         *int     `json:"min_beds,omitempty" jsonschema:"minimum bedroom count"`
	MinBaths        *float64 `json:"min_baths,omitempty" jsonschema:"minimum bathroom count; half baths are .5"`
	Neighborhoods   []string `json:"neighborhoods,omitempty" jsonschema:"restrict to these canonical neighborhood names (matched case-insensitively; get_corpus_stats lists the spellings the corpus carries)"`
	PropertyType    *string  `json:"property_type,omitempty" jsonschema:"one of coop, condo, townhouse, house, other"`
	ListingType     string   `json:"listing_type,omitempty" jsonschema:"sale (default) or rent; the corpus holds both and this picks which to search"`
	Limit           int      `json:"limit,omitempty" jsonschema:"rows per page; defaults to 40 and is capped at 500"`
	Offset          int      `json:"offset,omitempty" jsonschema:"rows to skip for paging; page with offset until offset+count reaches total"`
	IncludeInactive bool     `json:"include_inactive,omitempty" jsonschema:"when true, also return in_contract, sold, rented, and delisted listings instead of active-only"`
}

type searchListingsOutput struct {
	EffectiveFilters effectiveFiltersDTO `json:"effective_filters"`
	Count            int                 `json:"count"`
	Total            int                 `json:"total"`
	Offset           int                 `json:"offset"`
	// QueriedAt is when these rows were read (full precision, so a change that
	// landed sub-second before the read is not mistaken for a later one).
	QueriedAt string          `json:"queried_at" jsonschema:"when these rows were read (RFC 3339, sub-second); pass it to mark_shown as as_of"`
	Listings  []listingRowDTO `json:"listings"`
	// UnmatchedNeighborhoods flags filter names no listing carries (see get_corpus_stats for spellings).
	UnmatchedNeighborhoods []string `json:"unmatched_neighborhoods,omitempty" jsonschema:"neighborhood filters no listing in the corpus carries, so they matched nothing; check get_corpus_stats for the spelling the corpus uses"`
}

type getCandidatesInput struct {
	Limit int `json:"limit,omitempty" jsonschema:"maximum rows to return; defaults to 40 and is capped at 60 (larger values are clamped, not rejected)"`
}

type getCandidatesOutput struct {
	Count     int             `json:"count"`
	QueriedAt string          `json:"queried_at" jsonschema:"when these rows were read (RFC 3339, sub-second); pass it to mark_shown as as_of"`
	Listings  []listingRowDTO `json:"listings"`
}

type getListingInput struct {
	ID int64 `json:"id" jsonschema:"the listing id from search_listings or get_candidates"`
	// Photos defaults to an inline contact sheet so the naive call lets the model
	// SEE the listing; only "links" and "none" skip inline images.
	Photos    string `json:"photos,omitempty" jsonschema:"how to attach the photos, so YOU can judge look and condition. Omit for the default contact_sheet (one inline image grid you can see). Options: contact_sheet (default, inline grid), individual (one inline image per photo, for close inspection), links (resource-link URLs only — shown to the user but NOT visible to you; never use these to judge), none (data only, no images)"`
	MaxPhotos int    `json:"max_photos,omitempty" jsonschema:"max photos to attach; contact_sheet defaults to 12 (cap 30), individual defaults to 6 (cap 10), links default to 6 (cap 30); 0 or less means the default"`
}

type getListingsPhotosInput struct {
	IDs []int64 `json:"ids" jsonschema:"listing ids to build gallery photo URLs for; at most 30, extra ids are ignored"`
}

// listingPhotosDTO is one listing's bulk gallery entry: photo accounting plus the public image URLs, one per cached photo.
type listingPhotosDTO struct {
	ID           int64    `json:"id"`
	PhotoCount   int      `json:"photo_count"`
	PhotosCached int      `json:"photos_cached"`
	ImageURIs    []string `json:"image_uris"`
}

type getListingsPhotosOutput struct {
	Count    int                `json:"count"`
	Listings []listingPhotosDTO `json:"listings"`
}

type markShownInput struct {
	IDs []int64 `json:"ids" jsonschema:"listing ids that were actually presented to the user"`
	// AsOf caps the material-change snapshot so a change since the read resurfaces.
	AsOf string `json:"as_of,omitempty" jsonschema:"the queried_at value from the get_candidates or search_listings response these ids came from (RFC 3339); omit to snapshot now"`
}

type markShownOutput struct {
	Marked int `json:"marked" jsonschema:"how many of the submitted ids exist and were marked; unknown ids are skipped"`
}

type recordVerdictInput struct {
	ListingID int64  `json:"listing_id" jsonschema:"the listing the user gave feedback on"`
	Verdict   string `json:"verdict" jsonschema:"one of love, maybe, dislike"`
	Note      string `json:"note,omitempty" jsonschema:"the user's own words about why, kept verbatim as taste evidence"`
}

type recordVerdictOutput struct {
	Verdict verdictDTO `json:"verdict"`
	// InspectPhotos is set when this listing has cached photos: a reminder to
	// judge from the images, not specs, before finalizing a recommendation.
	InspectPhotos string `json:"inspect_photos,omitempty"`
}

type recordVerdictItemInput struct {
	ListingID int64  `json:"listing_id" jsonschema:"the listing the user gave feedback on"`
	Verdict   string `json:"verdict" jsonschema:"one of love, maybe, dislike"`
	Note      string `json:"note,omitempty" jsonschema:"the user's own words about why, kept verbatim as taste evidence"`
}

type recordVerdictsInput struct {
	Verdicts []recordVerdictItemInput `json:"verdicts" jsonschema:"one entry per listing to react to"`
}

type verdictFailureDTO struct {
	Index     int    `json:"index"`
	ListingID int64  `json:"listing_id"`
	Error     string `json:"error"`
}

type recordVerdictsOutput struct {
	Recorded []verdictDTO `json:"recorded"`
	Count    int          `json:"count"`
	// Failed lists the items that were not saved (unknown ids); always check it.
	Failed      []verdictFailureDTO `json:"failed"`
	FailedCount int                 `json:"failed_count"`
	// InspectPhotos is set when any recorded listing has cached photos: a
	// reminder to look at them (get_listing) before finalizing, since specs and
	// rows alone are unreliable for taste.
	InspectPhotos string `json:"inspect_photos,omitempty"`
}

type setProfileInput struct {
	MaxPrice           *int64   `json:"max_price,omitempty" jsonschema:"maximum asking price in whole dollars; null clears the constraint"`
	MinBeds            *int     `json:"min_beds,omitempty" jsonschema:"minimum bedroom count; null clears the constraint"`
	MinBaths           *float64 `json:"min_baths,omitempty" jsonschema:"minimum bathroom count; null clears the constraint"`
	MaxMonthlyCarrying *int64   `json:"max_monthly_carrying,omitempty" jsonschema:"maximum monthly maintenance plus common charges plus taxes; null clears the constraint"`
	ListingType        *string  `json:"listing_type,omitempty" jsonschema:"sale or rent; null resets to sale"`
	Neighborhoods      []string `json:"neighborhoods,omitempty" jsonschema:"canonical neighborhood names (matched case-insensitively; see get_corpus_stats); an empty array or null clears the constraint"`
	PropertyTypes      []string `json:"property_types,omitempty" jsonschema:"any of coop, condo, townhouse, house, other; an empty array or null clears the constraint"`
}

type setProfileOutput struct {
	Profile                profileDTO `json:"profile"`
	UnmatchedNeighborhoods []string   `json:"unmatched_neighborhoods,omitempty" jsonschema:"saved neighborhood names no listing in the corpus carries; get_candidates will return nothing for them until you fix the spelling (see get_corpus_stats)"`
}

type updateRubricInput struct {
	Content          string `json:"content" jsonschema:"the full rubric text; this replaces nothing, it is appended as a new version"`
	ThroughVerdictID int64  `json:"through_verdict_id" jsonschema:"id of the newest verdict this rubric summarizes, from get_state; 0 only when no verdicts exist yet"`
}

type updateRubricOutput struct {
	Rubric rubricDTO `json:"rubric"`
}

type resetStateInput struct {
	Confirm bool `json:"confirm,omitempty" jsonschema:"must be true to actually erase saved taste memory (verdicts, rubric, shown history, profile), saved lists, and crawl scopes; when false or omitted the call is a no-op that deletes nothing"`
}

type resetCountsDTO struct {
	Verdicts     int `json:"verdicts"`
	Rubrics      int `json:"rubrics"`
	Shown        int `json:"shown"`
	Profile      int `json:"profile"`
	ListItems    int `json:"list_items"`
	CrawlTargets int `json:"crawl_targets"`
}

type resetStateOutput struct {
	// Reset is true only when confirm was true and the wipe ran. Cleared is the per-table row counts removed (all zero on a no-op).
	Reset   bool           `json:"reset"`
	Cleared resetCountsDTO `json:"cleared"`
	Message string         `json:"message"`
}

type crawlTargetDTO struct {
	ID          int64    `json:"id"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Enabled     bool     `json:"enabled"`
	Areas       []string `json:"areas"`
	ListingType string   `json:"listing_type"`
	MaxPrice    *int64   `json:"max_price"`
	MinBeds     *int     `json:"min_beds"`
	Note        string   `json:"note"`
	CreatedAt   string   `json:"created_at"`
	LastRunID   *int64   `json:"last_run_id"`
	LastRunAt   string   `json:"last_run_at,omitempty"`
	LastError   string   `json:"last_error"`
}

type requestCrawlInput struct {
	Areas       []string `json:"areas" jsonschema:"places to crawl: plain-English neighborhood/borough/city names (e.g. \"Upper West Side\", \"all of Manhattan\") and/or numeric area ids; names are resolved automatically. 1 to 20 entries"`
	ListingType string   `json:"listing_type" jsonschema:"sale or rent"`
	MaxPrice    *int64   `json:"max_price,omitempty" jsonschema:"maximum asking price in whole dollars; omit for no cap"`
	MinBeds     *int     `json:"min_beds,omitempty" jsonschema:"minimum bedroom count; omit for no minimum"`
	Note        string   `json:"note,omitempty" jsonschema:"one line on why this scope was requested, for the operator"`
	OneOff      bool     `json:"one_off,omitempty" jsonschema:"leave false (the default) to create a STANDING scope the crawler keeps fresh — the right choice whenever the user is interested in or looking for something in an area. Set true ONLY for a one-time snapshot the user does not want tracked going forward (e.g. \"just show me what's in X right now\")"`
}

type requestCrawlOutput struct {
	JobID           int64          `json:"job_id" jsonschema:"poll get_crawl_status with this; it is the same value as target.id"`
	Target          crawlTargetDTO `json:"target"`
	AlreadyQueued   bool           `json:"already_queued" jsonschema:"true when an identical scope already existed and was returned instead of a new one; target then reflects its current state, which may be a pass that already finished"`
	PendingRequests int            `json:"pending_requests" jsonschema:"one-off jobs still waiting to run (standing scopes excluded); this is the count the request budget applies to, unlike get_corpus_stats.pending_crawls"`
	Message         string         `json:"message"`
}

type manageCrawlTargetInput struct {
	ID     int64  `json:"id" jsonschema:"the crawl target id from list_crawl_targets (also the job_id request_crawl returned)"`
	Action string `json:"action" jsonschema:"what to do with the scope. One of: pause (stop crawling a standing scope but keep it), resume (re-enable a paused standing scope), make_recurring (turn a one-off scope into a standing one the crawler keeps fresh), delete (remove the scope entirely so it is never crawled again)"`
}

type manageCrawlTargetOutput struct {
	ID      int64  `json:"id"`
	Action  string `json:"action"`
	Deleted bool   `json:"deleted,omitempty"`
	Message string `json:"message"`
}

type getCrawlStatusInput struct {
	JobID int64 `json:"job_id" jsonschema:"the job_id returned by request_crawl (also target.id in list_crawl_targets)"`
}

type crawlRunDTO struct {
	ID            int64  `json:"id"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at,omitempty"`
	Complete      bool   `json:"complete"`
	ListingsSeen  int    `json:"listings_seen"`
	Created       int    `json:"created"`
	Updated       int    `json:"updated"`
	PhotoFailures int    `json:"photo_failures"`
	Suspect       bool   `json:"suspect"`
}

type getCrawlStatusOutput struct {
	JobID  int64          `json:"job_id"`
	Status string         `json:"status" jsonschema:"pending (queued), running, done, or failed (see target.last_error); a standing scope reports its most recent pass"`
	Target crawlTargetDTO `json:"target"`
	Run    *crawlRunDTO   `json:"run,omitempty" jsonschema:"metrics of the most recent pass once one has run; absent while pending or running. On a standing scope check finished_at to see how old the pass is"`
}

type getCorpusStatsInput struct{}

type corpusStatsOutput struct {
	Listings       int    `json:"listings"`
	ActiveListings int    `json:"active_listings"`
	ActiveSale     int    `json:"active_sale"`
	ActiveRent     int    `json:"active_rent"`
	PhotosCached   int    `json:"photos_cached"`
	Verdicts       int    `json:"verdicts"`
	Lists          int    `json:"lists"`
	PendingCrawls  int    `json:"pending_crawls" jsonschema:"crawl targets pending or running, standing scopes included; not the request budget"`
	StandingScopes int    `json:"standing_scopes"`
	LastCrawlAt    string `json:"last_crawl_at,omitempty"`
	// Neighborhoods are the exact strings search_listings/set_profile accept.
	Neighborhoods []neighborhoodCountDTO `json:"neighborhoods"`
}

type neighborhoodCountDTO struct {
	Name   string `json:"name"`
	Active int    `json:"active"`
}

type listCrawlTargetsInput struct{}

type listCrawlTargetsOutput struct {
	Count   int              `json:"count"`
	Targets []crawlTargetDTO `json:"targets"`
}

func timestamp(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// ---- Lists ----

type listDTO struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Emoji     string `json:"emoji"`
	IsDefault bool   `json:"is_default"`
	Count     int    `json:"count"`
}

type getListsOutput struct {
	Lists []listDTO `json:"lists"`
	Count int       `json:"count"`
}

type createListInput struct {
	Name  string `json:"name" jsonschema:"display name for the list; the slug is derived from it"`
	Emoji string `json:"emoji,omitempty" jsonschema:"optional emoji label, e.g. a window emoji for a big-windows list"`
}

type createListOutput struct {
	List          listDTO `json:"list"`
	AlreadyExists bool    `json:"already_exists" jsonschema:"true when a list with this slug already existed and was returned unchanged"`
}

type addToListInput struct {
	List      string `json:"list" jsonschema:"the target list, by name or slug (e.g. favorites, big-windows); it must already exist - create it first with create_list"`
	ListingID int64  `json:"listing_id" jsonschema:"the listing to add"`
	Note      string `json:"note,omitempty" jsonschema:"optional note on why this listing is in the list"`
}

type addToListOutput struct {
	List  listDTO `json:"list"`
	Added bool    `json:"added" jsonschema:"true when the listing was newly added; false when it was already on the list (a note still updates)"`
}

type removeFromListInput struct {
	List      string `json:"list" jsonschema:"the list, by name or slug"`
	ListingID int64  `json:"listing_id" jsonschema:"the listing to remove"`
}

type getListInput struct {
	List string `json:"list" jsonschema:"the list, by name or slug"`
}

type getListOutput struct {
	List     listDTO         `json:"list"`
	Count    int             `json:"count"`
	Listings []listingRowDTO `json:"listings"`
}

type removeFromListOutput struct {
	OK bool `json:"ok"`
	// Removed is false when the listing was not on the list (still ok: nothing to do).
	Removed bool `json:"removed"`
}

type consoleURLOutput struct {
	URL       string `json:"url"`
	Available bool   `json:"available" jsonschema:"false when no web console is configured for this deployment"`
	Message   string `json:"message"`
}

type getListsInput struct{}
type getConsoleURLInput struct{}

// ---- Areas ----

type resolveAreasInput struct {
	Query string `json:"query" jsonschema:"a neighborhood, borough, or city phrase in plain English, e.g. \"Upper West Side\", \"all of Manhattan\", or \"the whole city\""`
}

type resolvedAreaDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Borough string `json:"borough"`
	Level   int    `json:"level" jsonschema:"how broad the area is: 0 city, 1 borough, 2 neighborhood, deeper for sub-neighborhoods"`
}

type resolveAreasOutput struct {
	Query    string            `json:"query"`
	CityWide bool              `json:"city_wide"`
	Areas    []resolvedAreaDTO `json:"areas"`
	AreaIDs  []string          `json:"area_ids" jsonschema:"the provider area ids to pass to request_crawl for this place"`
}
