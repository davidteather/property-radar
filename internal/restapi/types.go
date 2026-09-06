package restapi

import (
	"bytes"
	"encoding/json"
	"reflect"
	"time"

	"github.com/danielgtaylor/huma/v2"
)

// Transport-owned DTOs. JSON + huma tags live only in this package; the listings and domain types carry none. These may diverge from the MCP transport's DTOs.

type profileDTO struct {
	MaxPrice           *int64   `json:"max_price" doc:"maximum asking price in whole dollars; null when unconstrained"`
	MinBeds            *int     `json:"min_beds" doc:"minimum bedroom count; null when unconstrained"`
	MinBaths           *float64 `json:"min_baths" doc:"minimum bathroom count; null when unconstrained"`
	MaxMonthlyCarrying *int64   `json:"max_monthly_carrying" doc:"maximum monthly maintenance + common charges + taxes; null when unconstrained"`
	ListingType        string   `json:"listing_type" doc:"sale or rent"`
	Neighborhoods      []string `json:"neighborhoods" doc:"canonical neighborhood names the profile is restricted to"`
	PropertyTypes      []string `json:"property_types" doc:"property types the profile is restricted to"`
	UpdatedAt          *string  `json:"updated_at" doc:"RFC3339 timestamp of the last profile edit"`
}

type rubricDTO struct {
	Content          string `json:"content"`
	ThroughVerdictID int64  `json:"through_verdict_id" doc:"id of the newest verdict this rubric summarizes"`
	CreatedAt        string `json:"created_at"`
}

type verdictDTO struct {
	ID        int64  `json:"id"`
	ListingID int64  `json:"listing_id"`
	Verdict   string `json:"verdict" doc:"love, maybe, or dislike"`
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
	ListingType        string   `json:"listing_type" doc:"sale or rent; price is the asking price for a sale and the monthly rent for a rental"`
	MonthlyCarrying    *int64   `json:"monthly_carrying"`
	DOM                *int     `json:"dom" doc:"days on market"`
	DescriptionExcerpt string   `json:"description_preview" doc:"first ~200 characters of the description, truncated; get the full text from GET /v1/listings/{id}"`
	PriceDrop          bool     `json:"price_drop"`
	Status             string   `json:"status"`
	URL                string   `json:"url" doc:"shareable provider listing link"`
	PhotoCount         int      `json:"photo_count" doc:"total photos on the listing"`
	PhotosCached       int      `json:"photos_cached" doc:"cached photos serveable as constructable image URLs; build them as {base}/img/l/{id}/{n} for n in 0..photos_cached-1 (append ?k=<PUBLIC_IMG_TOKEN> when the instance gates its photo proxy), no get_listing needed"`
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

type photoSheetDTO struct {
	Sheet     int   `json:"sheet" doc:"1-based sheet index; fetch its bytes from the contact-sheet endpoint"`
	Positions []int `json:"positions" doc:"[first, last] listing photo position on this sheet, row-major; raw listing positions, not /img/l/{id}/{n} cached indices"`
	Photos    int   `json:"photos"`
}

type listingDetailDTO struct {
	ID                int64    `json:"id"`
	URL               string   `json:"url" doc:"the provider listing page (same as provenance.url; rows carry it too)"`
	Address           string   `json:"address"`
	Street            string   `json:"street"`
	Unit              string   `json:"unit"`
	Neighborhood      string   `json:"neighborhood"`
	Zip               string   `json:"zip"`
	Latitude          *float64 `json:"latitude"`
	Longitude         *float64 `json:"longitude"`
	ListingType       string   `json:"listing_type"`
	PropertyType      string   `json:"property_type"`
	Status            string   `json:"status"`
	Price             *int64   `json:"price"`
	Currency          string   `json:"currency"`
	Beds              *int     `json:"beds"`
	Baths             *float64 `json:"baths"`
	Sqft              *int     `json:"sqft"`
	Maintenance       *int64   `json:"maintenance"`
	CommonCharges     *int64   `json:"common_charges"`
	TaxesMonthly      *int64   `json:"taxes_monthly"`
	MonthlyCarrying   *int64   `json:"monthly_carrying"`
	DOM               *int     `json:"dom" doc:"days on market"`
	Description       string   `json:"description"`
	FirstSeen         string   `json:"first_seen"`
	LastSeen          string   `json:"last_seen"`
	MaterialChangedAt string   `json:"material_changed_at"`

	PriceHistory []priceEventDTO `json:"price_history"`
	Provenance   provenanceDTO   `json:"provenance"`

	// Photo accounting plus fetchable public URLs. This endpoint returns no image bytes: use the photos[] links (public, content-addressed) or the bearer /v1/listings/{id}/photos/{n} endpoint.
	PhotoCount   int             `json:"photo_count" doc:"total photos on the listing"`
	PhotosCached int             `json:"photos_cached" doc:"cached thumbnails that read back cleanly"`
	PhotoMissing int             `json:"photo_missing" doc:"cached thumbnails that failed to read"`
	Photos       []photoRefDTO   `json:"photos" doc:"readable cached thumbnails as fetchable public image URLs"`
	PhotoSheets  []photoSheetDTO `json:"photo_sheets" doc:"contact sheets the contact-sheet endpoint renders at max_photos=30: 12 cached photos per sheet, in position order"`
	Lists        []listDTO       `json:"lists" doc:"the user-curated lists this listing is a member of"`
	Verdicts     []verdictDTO    `json:"verdicts" doc:"this listing's verdict history, newest first"`
}

// listingPhotosDTO is one listing's bulk gallery entry: photo accounting plus the public image URLs, one per cached photo.
type listingPhotosDTO struct {
	ID           int64    `json:"id"`
	PhotoCount   int      `json:"photo_count" doc:"total photos on the listing"`
	PhotosCached int      `json:"photos_cached" doc:"cached photos, equal to len(image_uris)"`
	ImageURIs    []string `json:"image_uris" doc:"absolute JPEG URLs ({base}/img/l/{id}/{n}), one per cached photo (n is dense over cached photos, so 0..photos_cached-1 all load), embeddable directly as <img src>; each already carries the ?k= photo access key when the instance sets one"`
}

type photoRefDTO struct {
	Position       int    `json:"position" doc:"zero-based photo position on the listing"`
	HostedImageURI string `json:"hosted_image_uri" doc:"absolute public image URL; unauthenticated and content-addressed"`
	MIME           string `json:"mime" doc:"image media type, always image/jpeg"`
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
	LastRunAt   string   `json:"last_run_at,omitempty" doc:"when this scope last ran; empty if never"`
	LastError   string   `json:"last_error"`
}

type crawlRunDTO struct {
	ID            int64  `json:"id"`
	StartedAt     string `json:"started_at"`
	FinishedAt    string `json:"finished_at,omitempty"`
	Complete      bool   `json:"complete"`
	ListingsSeen  int    `json:"listings_seen"`
	Created       int    `json:"created" doc:"listings new to the corpus"`
	Updated       int    `json:"updated"`
	PhotoFailures int    `json:"photo_failures"`
	Suspect       bool   `json:"suspect"`
}

type crawlStatusResponse struct {
	JobID  int64          `json:"job_id"`
	Status string         `json:"status" doc:"pending (queued), running, done, or failed"`
	Target crawlTargetDTO `json:"target"`
	Run    *crawlRunDTO   `json:"run" doc:"crawl metrics once the job has run; null while pending/running"`
}

type corpusStatsResponse struct {
	Listings       int                    `json:"listings"`
	ActiveListings int                    `json:"active_listings"`
	ActiveSale     int                    `json:"active_sale"`
	ActiveRent     int                    `json:"active_rent"`
	PhotosCached   int                    `json:"photos_cached"`
	Verdicts       int                    `json:"verdicts"`
	Lists          int                    `json:"lists"`
	PendingCrawls  int                    `json:"pending_crawls" doc:"once targets still pending or running"`
	StandingScopes int                    `json:"standing_scopes"`
	LastCrawlAt    string                 `json:"last_crawl_at,omitempty" doc:"when the last complete crawl finished; empty on a fresh install"`
	Neighborhoods  []neighborhoodCountDTO `json:"neighborhoods" doc:"neighborhood names active listings carry, busiest first; the spellings the neighborhoods filters match (case-insensitively)"`
}

type neighborhoodCountDTO struct {
	Name   string `json:"name"`
	Active int    `json:"active" doc:"active listings in that neighborhood"`
}

// ---- Response bodies (the Body field of each huma output) ----

type stateResponse struct {
	Profile     profileDTO   `json:"profile"`
	Rubric      *rubricDTO   `json:"rubric"`
	RubricStale bool         `json:"rubric_stale"`
	Verdicts    []verdictDTO `json:"verdicts"`
}

type searchResponse struct {
	EffectiveFilters       effectiveFiltersDTO `json:"effective_filters"`
	Count                  int                 `json:"count" doc:"rows in this page"`
	Total                  int                 `json:"total" doc:"full filtered count ignoring limit/offset; page until offset+count reaches total"`
	Offset                 int                 `json:"offset" doc:"rows skipped for this page"`
	Limit                  int                 `json:"limit" doc:"effective rows-per-page cap actually applied"`
	QueriedAt              time.Time           `json:"queried_at" doc:"when these rows were read; pass as as_of to POST /v1/shown"`
	Listings               []listingRowDTO     `json:"listings"`
	UnmatchedNeighborhoods []string            `json:"unmatched_neighborhoods,omitempty" doc:"neighborhood filters no listing carries, so they matched nothing; GET /v1/corpus lists the spellings in use"`
}

type candidatesResponse struct {
	Count     int             `json:"count"`
	QueriedAt time.Time       `json:"queried_at" doc:"when these rows were read; pass as as_of to POST /v1/shown"`
	Listings  []listingRowDTO `json:"listings"`
}

type listingsPhotosResponse struct {
	Count    int                `json:"count"`
	Listings []listingPhotosDTO `json:"listings"`
}

type markShownResponse struct {
	Marked int `json:"marked" doc:"distinct ids in the request that exist (re-marking an already-shown id counts it again); unknown ids are skipped"`
}

type recordVerdictResponse struct {
	Verdict verdictDTO `json:"verdict"`
}

type verdictFailureDTO struct {
	Index     int    `json:"index" doc:"position of the item in the request batch"`
	ListingID int64  `json:"listing_id"`
	Error     string `json:"error"`
}

type recordVerdictsResponse struct {
	Recorded    []verdictDTO        `json:"recorded"`
	Count       int                 `json:"count"`
	Failed      []verdictFailureDTO `json:"failed" doc:"items that were not saved (unknown listing ids); the call is still 200"`
	FailedCount int                 `json:"failed_count"`
}

type setProfileResponse struct {
	Profile                profileDTO `json:"profile"`
	UnmatchedNeighborhoods []string   `json:"unmatched_neighborhoods,omitempty" doc:"saved neighborhood names no listing carries; candidates will be empty for them until the spelling is fixed"`
}

type updateRubricResponse struct {
	Rubric rubricDTO `json:"rubric"`
}

type requestCrawlResponse struct {
	// JobID is the target id, for GET /v1/crawl-requests/{id} polling.
	JobID  int64          `json:"job_id"`
	Target crawlTargetDTO `json:"target"`
	// True when an identical request was already queued; no new row was added.
	AlreadyQueued   bool `json:"already_queued"`
	PendingRequests int  `json:"pending_requests"`
}

type crawlTargetsResponse struct {
	Count   int              `json:"count"`
	Targets []crawlTargetDTO `json:"targets"`
}

type resetClearedDTO struct {
	Verdicts     int `json:"verdicts" doc:"verdict rows deleted"`
	Rubrics      int `json:"rubrics" doc:"rubric versions deleted"`
	Shown        int `json:"shown" doc:"shown markers deleted"`
	Profile      int `json:"profile" doc:"profile rows deleted (0 or 1)"`
	ListItems    int `json:"list_items" doc:"saved list entries deleted across all lists; custom lists themselves are deleted too, favorites is kept empty"`
	CrawlTargets int `json:"crawl_targets" doc:"crawl scopes + queued jobs deleted (area preferences)"`
}

type resetResponse struct {
	Reset   bool            `json:"reset" doc:"true when saved taste memory was actually erased; false for a no-op"`
	Cleared resetClearedDTO `json:"cleared" doc:"per-table row counts removed; all zero on a no-op"`
	Message string          `json:"message" doc:"human-readable summary of what was or was not cleared"`
}

// ---- Operation inputs and outputs ----

type stateInput struct{}

type stateOutput struct {
	Body stateResponse
}

// Query params cannot be pointers in huma, so an omitted numeric filter arrives as its zero value; the handler treats a zero max_price/min_beds/min_baths as "unconstrained" and huma rejects negatives.
type searchInput struct {
	MaxPrice        int64    `query:"max_price" minimum:"0" maximum:"2147483647" doc:"maximum asking price in whole dollars; listings with no known price are excluded when set. 0 (or omitted) means no cap"`
	MinBeds         int      `query:"min_beds" minimum:"0" maximum:"32767" doc:"minimum bedroom count; 0 (or omitted) means no minimum"`
	MinBaths        float64  `query:"min_baths" minimum:"0" maximum:"99.5" doc:"minimum bathroom count; half baths are .5. 0 (or omitted) means no minimum"`
	Neighborhoods   []string `query:"neighborhoods,explode" doc:"restrict to these exact neighborhood names (repeat the parameter per name; GET /v1/corpus lists the spellings)"`
	PropertyType    string   `query:"property_type" enum:"coop,condo,townhouse,house,other" doc:"one of coop, condo, townhouse, house, other"`
	ListingType     string   `query:"listing_type" enum:"sale,rent" doc:"sale (default) or rent"`
	Limit           int      `query:"limit" doc:"rows per page; defaults to 40, capped at 500"`
	Offset          int      `query:"offset" doc:"rows to skip for paging; defaults to 0. Page until offset+count reaches total"`
	IncludeInactive bool     `query:"include_inactive" doc:"when true, also return sold/in-contract/delisted listings instead of active-only"`
}

type searchOutput struct {
	Body searchResponse
}

type candidatesInput struct {
	Limit int `query:"limit" doc:"maximum rows to return; defaults to 40, capped at 60"`
}

type candidatesOutput struct {
	Body candidatesResponse
}

type listingInput struct {
	ID int64 `path:"id" doc:"listing id from a search or candidates row"`
}

type listingOutput struct {
	Body listingDetailDTO
}

type photoInput struct {
	ID int64 `path:"id" doc:"listing id"`
	N  int   `path:"n" minimum:"0" doc:"zero-based index into the listing's cached thumbnails, dense: every n in 0..photos_cached-1 resolves"`
}

// imageOutput is huma's raw-bytes response: a []byte Body is written straight to the wire (no JSON encoding) and the Content-Type header field labels it.
type imageOutput struct {
	ContentType   string `header:"Content-Type"`
	ContentLength int    `header:"Content-Length"`
	Body          []byte
}

type contactSheetInput struct {
	ID        int64 `path:"id" doc:"listing id"`
	Sheet     int   `query:"sheet" minimum:"1" doc:"1-based sheet index as listed in the detail's photo_sheets; defaults to 1"`
	MaxPhotos int   `query:"max_photos" minimum:"0" doc:"maximum photos to stitch across all sheets; defaults to 30 (the cap), which is what photo_sheets plans"`
}

type listingsPhotosInput struct {
	Body struct {
		IDs []int64 `json:"ids" doc:"listing ids to fetch gallery photo URLs for; at most 30, extras are ignored"`
	}
}

type listingsPhotosOutput struct {
	Body listingsPhotosResponse
}

type shownInput struct {
	Body struct {
		IDs  []int64    `json:"ids" doc:"listing ids that were actually presented to the user"`
		AsOf *time.Time `json:"as_of,omitempty" doc:"queried_at from the search or candidates response these ids came from; a material change since then still resurfaces them. Omit to snapshot now"`
	}
}

type markShownOutput struct {
	Body markShownResponse
}

type recordVerdictInput struct {
	Body struct {
		ListingID int64  `json:"listing_id" doc:"the listing the user gave feedback on"`
		Verdict   string `json:"verdict" enum:"love,maybe,dislike" doc:"one of love, maybe, dislike"`
		Note      string `json:"note,omitempty" doc:"the user's own words about why, kept verbatim as taste evidence"`
	}
}

type recordVerdictOutput struct {
	Body recordVerdictResponse
}

type recordVerdictsInput struct {
	Body struct {
		Verdicts []struct {
			ListingID int64  `json:"listing_id" doc:"the listing the user gave feedback on"`
			Verdict   string `json:"verdict" enum:"love,maybe,dislike" doc:"one of love, maybe, dislike"`
			Note      string `json:"note,omitempty" doc:"the user's own words about why, kept verbatim as taste evidence"`
		} `json:"verdicts" doc:"one entry per listing to react to"`
	}
}

type recordVerdictsOutput struct {
	Body recordVerdictsResponse
}

// profilePatchInput uses presence-aware patchable fields so an omitted key leaves the constraint unchanged, while an explicit null (or [] for arrays) clears it.
type profilePatchInput struct {
	Body profilePatchBody
}

type profilePatchBody struct {
	MaxPrice           patchable[int64]    `json:"max_price,omitempty" doc:"maximum asking price in whole dollars; null clears the constraint, omit to leave unchanged"`
	MinBeds            patchable[int]      `json:"min_beds,omitempty" doc:"minimum bedroom count; null clears, omit to leave unchanged"`
	MinBaths           patchable[float64]  `json:"min_baths,omitempty" doc:"minimum bathroom count; null clears, omit to leave unchanged"`
	MaxMonthlyCarrying patchable[int64]    `json:"max_monthly_carrying,omitempty" doc:"maximum monthly carrying cost; null clears, omit to leave unchanged"`
	ListingType        patchable[string]   `json:"listing_type,omitempty" doc:"sale or rent; null resets to sale, omit to leave unchanged"`
	Neighborhoods      patchable[[]string] `json:"neighborhoods,omitempty" doc:"canonical neighborhood names, matched case-insensitively; [] or null clears, omit to leave unchanged"`
	PropertyTypes      patchable[[]string] `json:"property_types,omitempty" doc:"any of coop, condo, townhouse, house, other; [] or null clears, omit to leave unchanged"`
}

type setProfileOutput struct {
	Body setProfileResponse
}

type updateRubricInput struct {
	Body struct {
		Content          string `json:"content" doc:"the full rubric text; appended as a new version, replacing nothing"`
		ThroughVerdictID int64  `json:"through_verdict_id" doc:"id of the newest verdict this rubric summarizes, from GET /v1/state"`
	}
}

type updateRubricOutput struct {
	Body updateRubricResponse
}

type requestCrawlInput struct {
	Body struct {
		Areas       []string `json:"areas" doc:"places to crawl: neighborhood/borough/city names and/or numeric area ids (names are resolved; GET /v1/areas previews the match), 1 to 20 entries"`
		ListingType string   `json:"listing_type" enum:"sale,rent" doc:"sale or rent"`
		MaxPrice    *int64   `json:"max_price,omitempty" minimum:"1" maximum:"2147483647" doc:"maximum asking price in whole dollars; omit for no cap"`
		MinBeds     *int     `json:"min_beds,omitempty" minimum:"0" maximum:"32767" doc:"minimum bedroom count; omit for no minimum"`
		Note        string   `json:"note,omitempty" doc:"one line on why this scope was requested, for the operator"`
		OneOff      bool     `json:"one_off,omitempty" doc:"false (the default) creates a standing scope the crawler keeps re-crawling to stay fresh; true crawls the scope once and does not track it afterward"`
	}
}

type requestCrawlOutput struct {
	Body requestCrawlResponse
}

type crawlStatusInput struct {
	ID int64 `path:"id" doc:"the job_id returned by POST /v1/crawl-requests"`
}

type crawlStatusOutput struct {
	Body crawlStatusResponse
}

type updateCrawlTargetInput struct {
	ID   int64 `path:"id"`
	Body struct {
		Enabled   *bool `json:"enabled,omitempty" required:"false" doc:"false pauses a standing scope; true resumes it"`
		Recurring *bool `json:"recurring,omitempty" doc:"true promotes a one-off scope to a recurring standing one the crawler keeps re-crawling (and enables it); takes precedence over enabled"`
	}
}

type deleteCrawlTargetInput struct {
	ID int64 `path:"id"`
}

type crawlTargetActionResponse struct {
	ID        int64 `json:"id"`
	Enabled   bool  `json:"enabled"`
	Recurring bool  `json:"recurring"`
	Deleted   bool  `json:"deleted"`
}

type crawlTargetActionOutput struct {
	Body crawlTargetActionResponse
}

type corpusStatsInput struct{}

type corpusStatsOutput struct {
	Body corpusStatsResponse
}

type crawlTargetsInput struct{}

type crawlTargetsOutput struct {
	Body crawlTargetsResponse
}

// resetInput's body is a pointer so an empty POST is the documented no-op, not a 400.
type resetInput struct {
	Body *struct {
		Confirm bool `json:"confirm,omitempty" doc:"must be true to actually erase saved taste memory (verdicts, rubric, shown history, profile), saved lists, and crawl scopes; false, omitted, or an empty body makes the call a no-op that deletes nothing"`
	}
}

type resetOutput struct {
	Body resetResponse
}

// patchable[T] carries JSON presence for PATCH bodies: UnmarshalJSON runs only when the key is present, so Set distinguishes "omitted, leave unchanged" from an explicit value, and Null marks an explicit null (clear). It implements huma.SchemaProvider so the OpenAPI schema stays the underlying type, made nullable so an explicit null validates.
type patchable[T any] struct {
	Set  bool
	Null bool
	Val  T
}

func (p *patchable[T]) UnmarshalJSON(b []byte) error {
	p.Set = true
	if string(bytes.TrimSpace(b)) == "null" {
		p.Null = true
		var zero T
		p.Val = zero
		return nil
	}
	return json.Unmarshal(b, &p.Val)
}

func (patchable[T]) Schema(r huma.Registry) *huma.Schema {
	s := r.Schema(reflect.TypeFor[T](), false, "")
	s.Nullable = true
	return s
}

// ---- Lists ----

type listDTO struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug" doc:"stable identifier derived from the name; use it as the list argument to the MCP add_to_list tool"`
	Name      string `json:"name"`
	Emoji     string `json:"emoji" doc:"optional emoji label for the list, e.g. a window emoji for a big-windows list"`
	IsDefault bool   `json:"is_default" doc:"true for the built-in favorites list, which cannot be deleted"`
	Count     int    `json:"count" doc:"number of listings currently in the list"`
}

type listsInput struct{}

type listsResponse struct {
	Count int       `json:"count"`
	Lists []listDTO `json:"lists"`
}

type listsOutput struct{ Body listsResponse }

type createListInput struct {
	Body struct {
		Name  string `json:"name" minLength:"1" doc:"display name; the slug is derived from it (lowercased, hyphenated)"`
		Emoji string `json:"emoji,omitempty" doc:"optional emoji label"`
	}
}

type createListResponse struct {
	List          listDTO `json:"list"`
	AlreadyExists bool    `json:"already_exists" doc:"true when a list with this slug already existed and was returned unchanged"`
}

type createListOutput struct{ Body createListResponse }

type listIDInput struct {
	ID int64 `path:"id" doc:"list id"`
}

type listDetailResponse struct {
	List     listDTO         `json:"list"`
	Count    int             `json:"count" doc:"listings in the list"`
	Listings []listingRowDTO `json:"listings"`
}

type listDetailOutput struct{ Body listDetailResponse }

type addToListInput struct {
	ID   int64 `path:"id" doc:"list id"`
	Body struct {
		ListingID int64  `json:"listing_id"`
		Note      string `json:"note,omitempty" doc:"optional note on why this listing is in the list"`
	}
}

type removeFromListInput struct {
	ID        int64 `path:"id" doc:"list id"`
	ListingID int64 `path:"listing_id" doc:"listing id to remove from the list"`
}

type listOKResponse struct {
	OK bool `json:"ok"`
}

type listOKOutput struct{ Body listOKResponse }

type addToListResponse struct {
	OK    bool `json:"ok"`
	Added bool `json:"added" doc:"true when the listing was newly added; false when it was already on the list (a note still updates)"`
}

type addToListOutput struct{ Body addToListResponse }

type removeFromListResponse struct {
	OK      bool `json:"ok"`
	Removed bool `json:"removed" doc:"false when the listing was not on the list (not an error)"`
}

type removeFromListOutput struct{ Body removeFromListResponse }

// ---- Verdicts / rated ----

type ratedDTO struct {
	Listing listingRowDTO `json:"listing"`
	Verdict string        `json:"verdict" doc:"the current (latest) verdict for this listing"`
	Note    string        `json:"note" doc:"the note attached to the latest verdict"`
	History []verdictDTO  `json:"history" doc:"every verdict for this listing, newest first"`
}

type verdictsInput struct{}

type verdictsResponse struct {
	Rubric      *rubricDTO `json:"rubric"`
	RubricStale bool       `json:"rubric_stale"`
	Count       int        `json:"count" doc:"number of rated listings"`
	Rated       []ratedDTO `json:"rated"`
}

type verdictsOutput struct{ Body verdictsResponse }

// ---- Areas ----

type areasInput struct {
	Q string `query:"q" doc:"a place phrase to resolve, e.g. 'Upper West Side', 'all of Manhattan', or 'the whole city'"`
}

type areaDTO struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Borough string `json:"borough"`
	Level   int    `json:"level"`
}

type areasResponse struct {
	Query    string    `json:"query"`
	CityWide bool      `json:"city_wide"`
	Areas    []areaDTO `json:"areas"`
	AreaIDs  []string  `json:"area_ids" doc:"the area ids to pass to POST /v1/crawl-requests"`
}

type areasOutput struct{ Body areasResponse }
