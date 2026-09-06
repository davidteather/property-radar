// Package restapi is the REST transport adapter over the shared internal/listings.Service: decode -> service call -> convert -> encode, with no ranking or heuristics. It owns its wire DTOs and serves an OpenAPI docs UI.
package restapi

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humago"

	"github.com/davidteather/property-radar/internal/bearerauth"
	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/publicurl"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

const (
	apiTitle   = "Property Radar API"
	apiVersion = "0.1.0"
)

// handler binds the operations to the shared Service. bearerToken and imgToken gate the public /img proxy (imgToken is the ?k= capability, bearerToken is also accepted via the Authorization header) and imgToken is appended to every minted image URL. An empty imgToken leaves the proxy public.
type handler struct {
	svc         *listings.Service
	bearerToken string
	imgToken    string
}

// NewHandler builds the standalone REST transport (cmd/api): Register on a fresh mux, wrapped in the public-base-URL middleware and the request logger. publicBase is an optional PUBLIC_BASE_URL override; empty derives the origin per request. imgToken gates the /img proxy; empty leaves it public.
func NewHandler(svc *listings.Service, token, imgToken, publicBase string) http.Handler {
	mux := http.NewServeMux()
	Register(mux, svc, token, imgToken)
	return bearerauth.LogRequests(nil)(publicurl.Middleware(publicBase)(mux))
}

// RegisterImages mounts only the photo proxy, so an MCP-only server can still
// serve the /img links its tools mint. imgToken gates it (the operator bearer
// token also passes); empty leaves it public.
func RegisterImages(mux *http.ServeMux, svc *listings.Service, token, imgToken string) {
	h := &handler{svc: svc, bearerToken: token, imgToken: imgToken}
	// Unauthenticated: the public content-addressed photo proxy. More specific than "/", so a combined server still gives MCP the root.
	mux.HandleFunc("GET /img/{key...}", h.image)
	// Unauthenticated: the public constructable photo resolver mapping (listing id, 0-based position) to the cached thumbnail. More specific than /img/{key...}, so it wins the route.
	mux.HandleFunc("GET /img/l/{id}/{n}", h.imageByListing)
}

// Register mounts the REST routes onto mux: GET /healthz, the Scalar docs UI, the OpenAPI spec and /schemas refs (all unauthenticated), and every /v1 data route behind the bearer token. It does not claim "/", so a caller can mount another handler (the MCP stream) at the root. token must be non-empty. imgToken gates the /img proxy; empty leaves it public.
func Register(mux *http.ServeMux, svc *listings.Service, token, imgToken string) {
	h := &handler{svc: svc, bearerToken: token, imgToken: imgToken}

	// apiMux carries the huma-registered routes: /openapi.json (+ .yaml), /schemas/*, and every /v1 operation.
	apiMux := http.NewServeMux()

	config := huma.DefaultConfig(apiTitle, apiVersion)
	// Disable huma's built-in docs route; we serve our own branded Scalar shell at /docs.
	config.DocsPath = ""
	// Advertise the static bearer token as the default security scheme so Scalar shows an Authorize button and every operation is gated in the spec.
	config.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer": {
			Type:        "http",
			Scheme:      "bearer",
			Description: "Static operator bearer token (MCP_BEARER_TOKEN); shared with the MCP server.",
		},
	}
	config.Security = []map[string][]string{{"bearer": {}}}

	api := humago.New(apiMux, config)
	h.register(api)

	// Unauthenticated: liveness probe needs no secret.
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	// Unauthenticated: docs UI + embedded script.
	registerDocs(mux)
	RegisterImages(mux, svc, token, imgToken)
	// Unauthenticated at their exact paths so Scalar loads the spec without a token; "/" is left free for a combined server to give MCP the root.
	mux.Handle("/openapi.json", apiMux)
	mux.Handle("/openapi.yaml", apiMux)
	mux.Handle("/schemas/", apiMux)
	// Authenticated: every data route.
	mux.Handle("/v1/", bearerauth.Middleware(token)(apiMux))
}

func (h *handler) register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-state",
		Method:      http.MethodGet,
		Path:        "/v1/state",
		Summary:     "Get saved state",
		Description: "Returns the hard-filter profile, the latest taste rubric and whether it is stale, and the full verdict history.",
		Tags:        []string{"State"},
	}, h.getState)

	huma.Register(api, huma.Operation{
		OperationID: "search-listings",
		Method:      http.MethodGet,
		Path:        "/v1/listings",
		Summary:     "Search listings",
		Description: "Searches listings with ad-hoc hard filters, ignoring the saved profile. Returns compact rows (never photos) plus the filters actually applied, and total/offset/limit for paging. To sweep the whole corpus, page with offset (limit up to 500) until offset+count reaches total; set include_inactive=true to also return sold/in-contract/delisted listings.",
		Tags:        []string{"Listings"},
	}, h.searchListings)

	huma.Register(api, huma.Operation{
		OperationID: "get-candidates",
		Method:      http.MethodGet,
		Path:        "/v1/candidates",
		Summary:     "Get profile candidates",
		Description: "Returns profile-matching listings that are unseen or materially changed and not yet given a verdict. Compact rows only; never photos.",
		Tags:        []string{"Listings"},
	}, h.getCandidates)

	huma.Register(api, huma.Operation{
		OperationID: "get-listing",
		Method:      http.MethodGet,
		Path:        "/v1/listings/{id}",
		Summary:     "Get listing detail",
		Description: "Returns one listing in full: canonical fields, the complete description, provenance, price history, photo accounting, and photos[] as fetchable image URLs. To actually judge a listing you must look at those photos — rows and specs alone are unreliable (a spec-match often looks dated or faces a wall). This JSON returns no image bytes; render photos[] or fetch the per-photo / contact-sheet routes.",
		Tags:        []string{"Listings"},
	}, h.getListing)

	huma.Register(api, huma.Operation{
		OperationID: "get-listing-photo",
		Method:      http.MethodGet,
		Path:        "/v1/listings/{id}/photos/{n}",
		Summary:     "Get one listing photo",
		Description: "Returns the nth cached thumbnail (zero-based, dense over cached photos like /img/l/{id}/{n}) as raw JPEG bytes. 404 when the index is out of range or the thumbnail is missing.",
		Tags:        []string{"Photos"},
		Responses:   imageResponses("A cached JPEG thumbnail."),
	}, h.getListingPhoto)

	huma.Register(api, huma.Operation{
		OperationID: "get-listing-contact-sheet",
		Method:      http.MethodGet,
		Path:        "/v1/listings/{id}/contact-sheet",
		Summary:     "Get a contact sheet",
		Description: "Stitches the listing's cached thumbnails into 3x4 grid sheets and returns the selected sheet as raw JPEG bytes. `sheet` is 1-based and matches the detail's photo_sheets (default 1); 404 names the planned count when it is out of range.",
		Tags:        []string{"Photos"},
		Responses:   imageResponses("A stitched contact-sheet JPEG."),
	}, h.getContactSheet)

	huma.Register(api, huma.Operation{
		OperationID: "get-listings-photos",
		Method:      http.MethodPost,
		Path:        "/v1/listings/photos",
		Summary:     "Bulk photo URLs for many listings",
		Description: "Returns, for each requested listing id (cap 30), photo_count, photos_cached, and image_uris: absolute JPEG URLs of the form {base}/img/l/{id}/{n} — one per cached photo, embeddable directly as <img src>, each already carrying the ?k= photo access key when the instance gates its proxy. Pure database read: no image bytes, and no get_listing call per listing. Use this to build a photo gallery, website, or profile page for many listings in a single call, instead of calling GET /v1/listings/{id} once per listing just to learn its photo URLs.",
		Tags:        []string{"Photos"},
	}, h.getListingsPhotos)

	huma.Register(api, huma.Operation{
		OperationID: "mark-shown",
		Method:      http.MethodPost,
		Path:        "/v1/shown",
		Summary:     "Mark listings shown",
		Description: "Records that listings were presented to the user so they are not offered again until something material changes.",
		Tags:        []string{"Feedback"},
	}, h.markShown)

	huma.Register(api, huma.Operation{
		OperationID: "record-verdict",
		Method:      http.MethodPost,
		Path:        "/v1/verdicts",
		Summary:     "Record a verdict",
		Description: "Appends an immutable love/maybe/dislike verdict with an optional free-text note.",
		Tags:        []string{"Feedback"},
	}, h.recordVerdict)

	huma.Register(api, huma.Operation{
		OperationID: "record-verdicts",
		Method:      http.MethodPost,
		Path:        "/v1/verdicts/batch",
		Summary:     "Record verdicts in bulk",
		Description: "Appends many immutable verdicts in one call, for reacting to a batch of listings at once. Best-effort per item, not transactional: valid ids are recorded even if another id is unknown. The call returns 200 with recorded (what was saved) and failed (each unknown id with its batch index); check failed_count.",
		Tags:        []string{"Feedback"},
	}, h.recordVerdicts)

	huma.Register(api, huma.Operation{
		OperationID:   "set-profile",
		Method:        http.MethodPatch,
		Path:          "/v1/profile",
		Summary:       "Update the profile",
		Description:   "Partially updates the hard-filter profile. Each field is optional: omit it to leave it unchanged, send an explicit null (or [] for arrays) to clear the constraint, or send a value to set it.",
		Tags:          []string{"State"},
		DefaultStatus: http.StatusOK,
	}, h.setProfile)

	huma.Register(api, huma.Operation{
		OperationID: "update-rubric",
		Method:      http.MethodPost,
		Path:        "/v1/rubric",
		Summary:     "Append a rubric version",
		Description: "Appends a new taste-rubric version summarizing verdicts through the given verdict id. 422 when through_verdict_id is ahead of the latest verdict.",
		Tags:        []string{"State"},
	}, h.updateRubric)

	huma.Register(api, huma.Operation{
		OperationID: "request-crawl",
		Method:      http.MethodPost,
		Path:        "/v1/crawl-requests",
		Summary:     "Queue a crawl",
		Description: "Queues a crawl scope and returns a job_id immediately; the crawl runs out-of-band on the operator's crawler (poll GET /v1/crawl-requests/{id} for pending/running/done + counts). By default the scope is STANDING — the crawler keeps it fresh; pass one_off=true for a single snapshot that is not tracked afterward. Scope tightly (fewer areas + max_price/min_beds) to finish in minutes rather than a long borough-wide crawl. Manage scopes via PATCH/DELETE /v1/crawl-targets/{id}. 429 when the one_off pending-request queue is full.",
		Tags:        []string{"Crawl"},
	}, h.requestCrawl)

	huma.Register(api, huma.Operation{
		OperationID: "list-crawl-targets",
		Method:      http.MethodGet,
		Path:        "/v1/crawl-targets",
		Summary:     "List crawl targets",
		Description: "Lists every stored crawl target and its status, so a caller can see whether a queued scope has run yet.",
		Tags:        []string{"Crawl"},
	}, h.crawlTargets)

	huma.Register(api, huma.Operation{
		OperationID: "get-crawl-status",
		Method:      http.MethodGet,
		Path:        "/v1/crawl-requests/{id}",
		Summary:     "Check an async crawl job",
		Description: "Returns one crawl request's status (pending, running, done, failed) and, once run, its crawl metrics (listings_seen, created, updated). The id is the job_id returned by POST /v1/crawl-requests. Settled requests are pruned after about a week.",
		Tags:        []string{"Crawl"},
	}, h.crawlStatus)

	huma.Register(api, huma.Operation{
		OperationID:   "update-crawl-target",
		Method:        http.MethodPatch,
		Path:          "/v1/crawl-targets/{id}",
		Summary:       "Pause, resume, or make a crawl scope recurring",
		Description:   "Manage an existing crawl target. enabled=false pauses a standing scope (the worker stops crawling it) without deleting it; enabled=true resumes it. recurring=true promotes a one-off scope to a recurring standing one the crawler keeps re-crawling (and enables it), and takes precedence over enabled. 404 for an unknown id; 422 when pausing a one-off or promoting a scope that is already standing.",
		Tags:          []string{"Crawl"},
		DefaultStatus: http.StatusOK,
	}, h.updateCrawlTarget)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-crawl-target",
		Method:        http.MethodDelete,
		Path:          "/v1/crawl-targets/{id}",
		Summary:       "Delete a crawl scope",
		Description:   "Removes a crawl target (standing scope or one-off request). 404 for an unknown id.",
		Tags:          []string{"Crawl"},
		DefaultStatus: http.StatusOK,
	}, h.deleteCrawlTarget)

	huma.Register(api, huma.Operation{
		OperationID: "get-corpus-stats",
		Method:      http.MethodGet,
		Path:        "/v1/corpus",
		Summary:     "Corpus-wide totals and freshness",
		Description: "One snapshot of the whole corpus: total/active listings (split sale/rent), cached photos, verdicts, lists, queued crawl requests, standing scopes, and when the last complete crawl finished. Zero listings means a fresh install whose first crawl has not run.",
		Tags:        []string{"Crawl"},
	}, h.corpusStats)

	huma.Register(api, huma.Operation{
		OperationID:   "reset-state",
		Method:        http.MethodPost,
		Path:          "/v1/reset",
		Summary:       "Reset saved taste state",
		Description:   "Destructive: erases all saved taste memory - every verdict, the rubric, the shown history, and the profile - every saved list's contents (favorites emptied, custom lists deleted), AND the area preferences (standing crawl scopes and queued crawl jobs), starting the search over completely. The listings corpus is kept, so a later re-crawl only adds fresh data. Requires confirm=true in the body; without it the call is a no-op that deletes nothing and returns reset=false.",
		Tags:          []string{"State"},
		DefaultStatus: http.StatusOK,
	}, h.reset)

	h.registerLists(api)
	h.registerAreas(api)
}

func (h *handler) reset(ctx context.Context, in *resetInput) (*resetOutput, error) {
	if in.Body == nil || !in.Body.Confirm {
		return &resetOutput{Body: resetResponse{
			Reset:   false,
			Message: "No changes made. Send confirm=true to erase all saved taste memory (every verdict, the rubric, the shown history, and the profile) and the area preferences (standing crawl scopes and queued crawl jobs). The listings corpus is always kept.",
		}}, nil
	}
	res, err := h.svc.ResetState(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	c := res.Cleared
	return &resetOutput{Body: resetResponse{
		Reset:   true,
		Cleared: resetClearedDTO{Verdicts: c.Verdicts, Rubrics: c.Rubrics, Shown: c.Shown, Profile: c.Profile, ListItems: c.ListItems, CrawlTargets: c.CrawlTargets},
		Message: fmt.Sprintf("Cleared %d verdicts, the rubric, %d shown markers, the profile, %d saved list item(s) (custom lists deleted), and %d crawl scope(s); the listings corpus was kept.", c.Verdicts, c.Shown, c.ListItems, c.CrawlTargets),
	}}, nil
}

func (h *handler) getState(ctx context.Context, _ *stateInput) (*stateOutput, error) {
	state, err := h.svc.State(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &stateOutput{Body: toState(state)}, nil
}

func (h *handler) searchListings(ctx context.Context, in *searchInput) (*searchOutput, error) {
	q := listings.SearchQuery{
		Neighborhoods:   in.Neighborhoods,
		Limit:           in.Limit,
		Offset:          in.Offset,
		IncludeInactive: in.IncludeInactive,
	}
	if in.MaxPrice > 0 {
		q.MaxPrice = ptr.To(domain.Money(in.MaxPrice))
	}
	if in.MinBeds > 0 {
		q.MinBeds = ptr.To(in.MinBeds)
	}
	if in.MinBaths > 0 {
		q.MinBaths = ptr.To(in.MinBaths)
	}
	if pt := strings.TrimSpace(in.PropertyType); pt != "" {
		parsed, err := domain.ParsePropertyType(pt)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		q.PropertyTypes = []domain.PropertyType{parsed}
	}
	if lt := strings.TrimSpace(in.ListingType); lt != "" {
		parsed, err := domain.ParseListingType(lt)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		q.ListingType = parsed
	}
	queriedAt := time.Now()
	res, err := h.svc.Search(ctx, q)
	if err != nil {
		return nil, mapServiceError(err)
	}
	rows := toListingRows(res.Rows)
	return &searchOutput{Body: searchResponse{
		EffectiveFilters:       toEffectiveFilters(res.Filters, res.Limit),
		Count:                  len(rows),
		Total:                  res.Total,
		Offset:                 res.Offset,
		Limit:                  res.Limit,
		QueriedAt:              queriedAt.UTC(),
		Listings:               rows,
		UnmatchedNeighborhoods: res.UnmatchedNeighborhoods,
	}}, nil
}

func (h *handler) getCandidates(ctx context.Context, in *candidatesInput) (*candidatesOutput, error) {
	queriedAt := time.Now()
	rows, err := h.svc.Candidates(ctx, in.Limit)
	if err != nil {
		return nil, mapServiceError(err)
	}
	out := toListingRows(rows)
	return &candidatesOutput{Body: candidatesResponse{Count: len(out), QueriedAt: queriedAt.UTC(), Listings: out}}, nil
}

func (h *handler) getListing(ctx context.Context, in *listingInput) (*listingOutput, error) {
	// Link every cached thumbnail as a public image URL; read no bytes.
	detail, err := h.svc.Listing(ctx, domain.PropertyID(in.ID), listings.PhotoRequest{
		Links:     true,
		Count:     true,
		MaxPhotos: listings.MaxSheetPhotos,
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	dto := toListingDetail(detail)
	dto.Photos = toPhotoRefs(publicurl.FromContext(ctx), h.imgToken, detail.Photos.Links)
	dto.PhotoSheets = photoSheets(listings.PlanSheets(detail.Photos.Links))
	ls, err := h.svc.ListsForProperty(ctx, domain.PropertyID(in.ID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	dto.Lists = toLists(ls)
	vs, err := h.svc.VerdictsFor(ctx, domain.PropertyID(in.ID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	dto.Verdicts = nonNil(reverseVerdicts(vs)) // newest first
	return &listingOutput{Body: dto}, nil
}

// reverseVerdicts returns the verdicts newest-first (the store yields oldest-first).
func reverseVerdicts(vs []domain.Verdict) []verdictDTO {
	out := make([]verdictDTO, len(vs))
	for i, v := range vs {
		out[len(vs)-1-i] = toVerdict(v)
	}
	return out
}

func (h *handler) getListingPhoto(ctx context.Context, in *photoInput) (*imageOutput, error) {
	// Resolve the one key and read only its bytes, the same dense index /img/l/{id}/{n} uses.
	key, err := h.svc.PhotoKeyAt(ctx, domain.PropertyID(in.ID), in.N)
	if err != nil {
		if errors.Is(err, listings.ErrNotFound) {
			return nil, huma.Error404NotFound("no cached photo at that index")
		}
		return nil, mapServiceError(err)
	}
	data, err := h.svc.PhotoObject(ctx, key)
	if errors.Is(err, listings.ErrNotFound) {
		return nil, huma.Error404NotFound("cached thumbnail is missing")
	}
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &imageOutput{ContentType: "image/jpeg", ContentLength: len(data), Body: data}, nil
}

func (h *handler) getContactSheet(ctx context.Context, in *contactSheetInput) (*imageOutput, error) {
	// Same budget the detail's photo_sheets plan assumes, so its indices resolve here.
	if in.MaxPhotos <= 0 {
		in.MaxPhotos = listings.MaxSheetPhotos
	}
	detail, err := h.svc.Listing(ctx, domain.PropertyID(in.ID), listings.PhotoRequest{
		Include:   true,
		MaxPhotos: in.MaxPhotos,
		Layout:    listings.LayoutContactSheet,
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	sheets := detail.Photos.Sheets
	idx := max(in.Sheet, 1) - 1
	if idx >= len(sheets) {
		return nil, huma.Error404NotFound(fmt.Sprintf("no contact sheet %d; this listing plans %d", idx+1, len(sheets)))
	}
	return &imageOutput{ContentType: listings.SheetMIME, ContentLength: len(sheets[idx].Data), Body: sheets[idx].Data}, nil
}

func (h *handler) getListingsPhotos(ctx context.Context, in *listingsPhotosInput) (*listingsPhotosOutput, error) {
	summaries, err := h.svc.PhotosForListings(ctx, propertyIDs(in.Body.IDs))
	if err != nil {
		return nil, mapServiceError(err)
	}
	base := publicurl.FromContext(ctx)
	rows := make([]listingPhotosDTO, 0, len(summaries))
	for _, s := range summaries {
		rows = append(rows, toListingPhotos(base, h.imgToken, s))
	}
	return &listingsPhotosOutput{Body: listingsPhotosResponse{Count: len(rows), Listings: rows}}, nil
}

func (h *handler) markShown(ctx context.Context, in *shownInput) (*markShownOutput, error) {
	var asOf time.Time
	if in.Body.AsOf != nil {
		asOf = *in.Body.AsOf
	}
	marked, err := h.svc.MarkShown(ctx, propertyIDs(in.Body.IDs), asOf)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &markShownOutput{Body: markShownResponse{Marked: marked}}, nil
}

func (h *handler) recordVerdict(ctx context.Context, in *recordVerdictInput) (*recordVerdictOutput, error) {
	kind, err := domain.ParseVerdictKind(in.Body.Verdict)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	v, err := h.svc.RecordVerdict(ctx, domain.PropertyID(in.Body.ListingID), kind, in.Body.Note)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &recordVerdictOutput{Body: recordVerdictResponse{Verdict: toVerdict(v)}}, nil
}

func (h *handler) recordVerdicts(ctx context.Context, in *recordVerdictsInput) (*recordVerdictsOutput, error) {
	inputs := make([]listings.VerdictInput, 0, len(in.Body.Verdicts))
	for _, item := range in.Body.Verdicts {
		kind, err := domain.ParseVerdictKind(item.Verdict)
		if err != nil {
			return nil, huma.Error422UnprocessableEntity(err.Error())
		}
		inputs = append(inputs, listings.VerdictInput{
			PropertyID: domain.PropertyID(item.ListingID),
			Kind:       kind,
			Note:       item.Note,
		})
	}
	batch, err := h.svc.RecordVerdicts(ctx, inputs)
	if err != nil {
		return nil, mapServiceError(err)
	}
	rows := make([]verdictDTO, 0, len(batch.Recorded))
	for _, v := range batch.Recorded {
		rows = append(rows, toVerdict(v))
	}
	failed := make([]verdictFailureDTO, 0, len(batch.Failed))
	for _, f := range batch.Failed {
		failed = append(failed, verdictFailureDTO{Index: f.Index, ListingID: int64(f.PropertyID), Error: f.Message})
	}
	return &recordVerdictsOutput{Body: recordVerdictsResponse{Recorded: rows, Count: len(rows), Failed: failed, FailedCount: len(failed)}}, nil
}

func (h *handler) setProfile(ctx context.Context, in *profilePatchInput) (*setProfileOutput, error) {
	b := in.Body
	var up listings.ProfileUpdate
	if b.MaxPrice.Set {
		up.MaxPrice = listings.Opt[*domain.Money]{Set: true, Val: moneyFromPatch(b.MaxPrice)}
	}
	if b.MinBeds.Set {
		up.MinBeds = listings.Opt[*int]{Set: true, Val: intFromPatch(b.MinBeds)}
	}
	if b.MinBaths.Set {
		up.MinBaths = listings.Opt[*float64]{Set: true, Val: floatFromPatch(b.MinBaths)}
	}
	if b.MaxMonthlyCarrying.Set {
		up.MaxMonthlyCarrying = listings.Opt[*domain.Money]{Set: true, Val: moneyFromPatch(b.MaxMonthlyCarrying)}
	}
	if b.ListingType.Set {
		lt := domain.ListingSale
		if !b.ListingType.Null && strings.TrimSpace(b.ListingType.Val) != "" {
			parsed, err := domain.ParseListingType(b.ListingType.Val)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			lt = parsed
		}
		up.ListingType = listings.Opt[domain.ListingType]{Set: true, Val: lt}
	}
	if b.Neighborhoods.Set {
		up.Neighborhoods = listings.Opt[[]string]{Set: true, Val: b.Neighborhoods.Val}
	}
	if b.PropertyTypes.Set {
		types := make([]domain.PropertyType, 0, len(b.PropertyTypes.Val))
		for _, t := range b.PropertyTypes.Val {
			pt, err := domain.ParsePropertyType(t)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			types = append(types, pt)
		}
		up.PropertyTypes = listings.Opt[[]domain.PropertyType]{Set: true, Val: types}
	}

	saved, err := h.svc.SetProfile(ctx, up)
	if err != nil {
		return nil, mapServiceError(err)
	}
	unmatched, err := h.svc.UnmatchedNeighborhoods(ctx, saved.Neighborhoods)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &setProfileOutput{Body: setProfileResponse{Profile: toProfile(saved), UnmatchedNeighborhoods: unmatched}}, nil
}

func (h *handler) updateRubric(ctx context.Context, in *updateRubricInput) (*updateRubricOutput, error) {
	if strings.TrimSpace(in.Body.Content) == "" {
		return nil, huma.Error422UnprocessableEntity("content is empty")
	}
	if in.Body.ThroughVerdictID < 0 {
		return nil, huma.Error422UnprocessableEntity("through_verdict_id is negative")
	}
	rubric, err := h.svc.UpdateRubric(ctx, in.Body.Content, domain.VerdictID(in.Body.ThroughVerdictID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &updateRubricOutput{Body: updateRubricResponse{Rubric: *toRubric(&rubric)}}, nil
}

func (h *handler) requestCrawl(ctx context.Context, in *requestCrawlInput) (*requestCrawlOutput, error) {
	lt, err := domain.ParseListingType(in.Body.ListingType)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	// Resolve place names to area ids, then validate in the transport so client mistakes are 422s not 500s; the Service re-validates defensively.
	if len(in.Body.Areas) > listings.MaxRequestAreas {
		return nil, huma.Error422UnprocessableEntity(fmt.Sprintf("%d areas requested; at most %d fit in one crawl request", len(in.Body.Areas), listings.MaxRequestAreas))
	}
	areaIDs, unresolved := h.svc.ResolveAreaList(in.Body.Areas)
	if len(unresolved) > 0 {
		return nil, huma.Error422UnprocessableEntity("could not resolve area name(s): " + strings.Join(unresolved, ", ") + " — use GET /v1/areas?q= to find them, or pass numeric area ids")
	}
	if _, err := listings.ParseAreaIDs(areaIDs); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if in.Body.MaxPrice != nil && *in.Body.MaxPrice <= 0 {
		return nil, huma.Error422UnprocessableEntity("max_price must be positive; omit it for no cap")
	}
	if in.Body.MinBeds != nil && *in.Body.MinBeds < 0 {
		return nil, huma.Error422UnprocessableEntity("min_beds must not be negative")
	}
	result, err := h.svc.RequestCrawl(ctx, listings.CrawlRequest{
		Areas:       areaIDs,
		ListingType: lt,
		MaxPrice:    moneyPtr(in.Body.MaxPrice),
		MinBeds:     in.Body.MinBeds,
		Note:        in.Body.Note,
		Recurring:   !in.Body.OneOff,
	})
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &requestCrawlOutput{Body: requestCrawlResponse{
		JobID:           int64(result.Target.ID),
		Target:          toCrawlTarget(result.Target),
		AlreadyQueued:   result.AlreadyQueued,
		PendingRequests: result.PendingRequests,
	}}, nil
}

func (h *handler) crawlStatus(ctx context.Context, in *crawlStatusInput) (*crawlStatusOutput, error) {
	status, err := h.svc.CrawlStatus(ctx, domain.CrawlTargetID(in.ID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &crawlStatusOutput{Body: crawlStatusResponse{
		JobID:  int64(status.Target.ID),
		Status: string(status.Target.Status),
		Target: toCrawlTarget(status.Target),
		Run:    toCrawlRun(status.Run),
	}}, nil
}

func (h *handler) updateCrawlTarget(ctx context.Context, in *updateCrawlTargetInput) (*crawlTargetActionOutput, error) {
	if in.Body.Recurring != nil && *in.Body.Recurring {
		if err := h.svc.MakeCrawlTargetRecurring(ctx, domain.CrawlTargetID(in.ID)); err != nil {
			return nil, mapServiceError(err)
		}
		return &crawlTargetActionOutput{Body: crawlTargetActionResponse{ID: in.ID, Recurring: true, Enabled: true}}, nil
	}
	if in.Body.Enabled == nil {
		return nil, huma.Error422UnprocessableEntity("set enabled (true/false) or recurring (true)")
	}
	if err := h.svc.SetCrawlTargetEnabled(ctx, domain.CrawlTargetID(in.ID), *in.Body.Enabled); err != nil {
		return nil, mapServiceError(err)
	}
	return &crawlTargetActionOutput{Body: crawlTargetActionResponse{ID: in.ID, Enabled: *in.Body.Enabled}}, nil
}

func (h *handler) deleteCrawlTarget(ctx context.Context, in *deleteCrawlTargetInput) (*crawlTargetActionOutput, error) {
	if err := h.svc.DeleteCrawlTarget(ctx, domain.CrawlTargetID(in.ID)); err != nil {
		return nil, mapServiceError(err)
	}
	return &crawlTargetActionOutput{Body: crawlTargetActionResponse{ID: in.ID, Deleted: true}}, nil
}

func (h *handler) corpusStats(ctx context.Context, _ *corpusStatsInput) (*corpusStatsOutput, error) {
	stats, err := h.svc.CorpusStats(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &corpusStatsOutput{Body: toCorpusStats(stats)}, nil
}

func (h *handler) crawlTargets(ctx context.Context, _ *crawlTargetsInput) (*crawlTargetsOutput, error) {
	targets, err := h.svc.CrawlTargets(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	rows := make([]crawlTargetDTO, 0, len(targets))
	for _, t := range targets {
		rows = append(rows, toCrawlTarget(t))
	}
	return &crawlTargetsOutput{Body: crawlTargetsResponse{Count: len(rows), Targets: rows}}, nil
}

// mapServiceError maps the listings sentinels to HTTP status errors; anything else is logged and hidden behind a 500.
func mapServiceError(err error) error {
	switch {
	case errors.Is(err, listings.ErrInvalidInput):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, listings.ErrNotFound):
		return huma.Error404NotFound("listing not found")
	case errors.Is(err, listings.ErrFutureThroughVerdict):
		return huma.Error422UnprocessableEntity("through_verdict_id is ahead of the latest verdict; re-read GET /v1/state and rebuild the rubric from the verdicts it returns")
	case errors.Is(err, listings.ErrQueueFull):
		return huma.Error429TooManyRequests(err.Error())
	case errors.Is(err, listings.ErrCrawlTargetNotFound):
		return huma.Error404NotFound("crawl request not found (settled requests are pruned after about a week)")
	case errors.Is(err, listings.ErrListNotFound):
		return huma.Error404NotFound("list not found")
	case errors.Is(err, listings.ErrDefaultList):
		return huma.Error422UnprocessableEntity(err.Error())
	default:
		slog.Error("rest: unexpected service error", "err", err)
		return huma.Error500InternalServerError("internal server error")
	}
}

// imageResponses declares an image/jpeg raw-bytes 200 response so the OpenAPI spec documents the media type; huma writes the []byte body straight to the wire.
func imageResponses(desc string) map[string]*huma.Response {
	return map[string]*huma.Response{
		"200": {
			Description: desc,
			Content: map[string]*huma.MediaType{
				"image/jpeg": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
			},
		},
	}
}

func intFromPatch(p patchable[int]) *int {
	if p.Null {
		return nil
	}
	v := p.Val
	return &v
}

func floatFromPatch(p patchable[float64]) *float64 {
	if p.Null {
		return nil
	}
	v := p.Val
	return &v
}
