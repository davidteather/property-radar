package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/publicurl"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

func (s *Server) registerTools() {
	addTool(s, toolSpec{
		name:  "get_state",
		title: "Get saved search state",
		description: `Load everything Property Radar remembers about this user: the hard-filter profile, the latest taste rubric (null if none was ever written), whether that rubric is stale, and the complete verdict history oldest first.

Call this first in any conversation about listings. rubric_stale is true when verdicts have been recorded since the rubric's through_verdict_id; when it is true, rewrite the rubric from the verdict history and save it with update_rubric, passing latest_verdict_id as through_verdict_id. verdicts holds the most recent verdicts_limit entries (default 300, oldest first) and verdicts_total says how many exist; raise verdicts_limit (up to 2000) when you need the whole history. image_base_url, when present, is the origin to prefix constructed /img photo URLs with.`,
		effect: readOnly,
	}, s.getState)

	addTool(s, toolSpec{
		name:  "search_listings",
		title: "Search active listings",
		description: `Search listings (sale by default; listing_type=rent for rentals) with ad-hoc hard filters, ignoring the saved profile. Use this to explore outside the profile ("what if we went to 1.4M?"), to check a specific neighborhood, or to sweep the entire corpus.

Returns compact rows only, never photo bytes: id, address, price, listing_type (sale or rent — price is monthly rent for rentals), beds, baths, sqft, property type, monthly carrying cost (maintenance + common charges + taxes, summed over whatever is known), days on market, a ~200-character description_preview (truncated — the full text and the photos are in get_listing), a price_drop flag, status, and photo counts (photo_count total, photos_cached serveable). effective_filters echoes exactly what was applied. Omitted filters are unconstrained; a filter on price, beds, or baths excludes listings whose value is unknown. Results are ordered by most recent material change. limit defaults to 40 and is capped at 500.

Each row's photos_cached tells you how many constructable image URLs the listing has: {base}/img/l/{id}/{n} for n in 0..photos_cached-1 (when get_state returns an image_access_key, append ?k=<that key> so the URL loads). To build a gallery for many rows, use those URLs (or get_listings_photos, which includes the key) rather than get_listing per row.

To scan the ENTIRE corpus (not just the first page), page with offset, using limit up to 500, until you have seen total rows: total is the full filtered count, and each response echoes its offset. Pages are read live, so a crawl landing mid-scan can shift rows between pages; dedupe by id and re-check total at the end. Set include_inactive=true to also include sold, in-contract, and delisted listings; by default only active listings are returned. Rows are compact text with no photos, so to find "the weirdest-LOOKING apartment" you narrow by text here first, then call get_listing (default contact_sheet) on the shortlist to actually see them.

For candidates that respect the saved profile and exclude what the user has already seen or judged, use get_candidates instead. To see photos or the full description of a specific row, call get_listing.`,
		effect: readOnly,
		enums:  map[string][]string{"listing_type": {"sale", "rent"}, "property_type": {"coop", "condo", "townhouse", "house", "other"}},
	}, s.searchListings)

	addTool(s, toolSpec{
		name:  "get_candidates",
		title: "Get profile-matched candidates",
		description: `Return active listings that match the saved hard-filter profile and are worth showing: never shown before, or materially changed (price or status) since they were last shown, and with no verdict recorded yet.

Read-only. It does not mark anything as shown, so an interrupted conversation loses nothing. Call mark_shown yourself once you have actually presented listings to the user.

Row shape is identical to search_listings and contains no photo bytes; each row carries photo_count and photos_cached, so gallery image URLs are constructable as {base}/img/l/{id}/{n} for n in 0..photos_cached-1 (append ?k=<image_access_key from get_state> when the instance sets one), or fetch them in bulk with get_listings_photos. limit defaults to 40 and is capped at 60.`,
		effect: readOnly,
	}, s.getCandidates)

	addTool(s, toolSpec{
		name:  "get_listing",
		title: "Get one listing in full",
		description: `Full detail for one listing you are judging — complete description, all carrying-cost components, price history, provenance — and, by default, its photos rendered INLINE so you can actually see it. The full data is in structuredContent; the text block carries the facts AND the complete description.

**Rows and specs are not enough — you must look at the photos.** A listing that matches on beds, price, and sqft frequently looks dated inside, or its "view" faces a wall; a plainer listing photographs beautifully. You cannot judge taste, condition, light, or view from compact rows or from data fields alone. So for every listing you are seriously considering, call get_listing and look at the images before forming any opinion or recommendation.

photos controls how the images come back (default is contact_sheet, so the plain call just works):
- contact_sheet (default): inline 12-photo grids (one per sheet, up to 30 photos) — the whole place at a glance. Use this to judge.
- individual: one inline image per photo, for close inspection of a finalist or two.
- links: resource-link URLs only. These are shown to the USER but do NOT put pixels in your context — you will not see them. Use only when the human will look and you are not the one deciding; never use links to judge for yourself.
- none: data only, no images.

For building a gallery or website across MANY listings, do NOT loop this tool — call get_listings_photos(ids) for URLs. get_listing is for the handful you are visually inspecting. max_photos: contact_sheet defaults to 12 (cap 30), individual defaults to 6 (cap 10).

Photo counts: photo_count total on the listing, photos_cached how many have a readable cached thumbnail (normally lower, not an error), photos_returned how many this call attached, photo_missing thumbnails that failed to read (skipped, not fatal).`,
		effect: readOnly,
		enums:  map[string][]string{"photos": {"", "contact_sheet", "individual", "links", "none"}},
	}, s.getListing)

	addTool(s, toolSpec{
		name:  "get_listings_photos",
		title: "Bulk photo URLs for many listings",
		description: `Get gallery photo URLs for many listings in ONE call. Pass ids (up to 30) and get back, per listing, photo_count, photos_cached, and image_uris: JPEG URLs of the form {base}/img/l/{id}/{n}, embeddable directly as <img src> (absolute when this server knows its origin; on a stdio server without PUBLIC_BASE_URL they are relative /img paths, so prefix get_state.image_base_url or tell the user to set it) (they keep working across re-crawls; a re-crawl may change which photo a given n shows). Each already carries this instance's photo access key when one is set, so they load as-is.

Use this to build a photo gallery, website, or profile page for a batch of listings. Do NOT call get_listing per listing just to collect photo URLs — that is for visually judging a shortlist and does not scale to dozens of listings. This tool is a pure database read: it returns no image bytes and makes no per-listing detail call, so it stays cheap for large batches.

The URLs are the same ones you can construct yourself from any compact row: {base}/img/l/{id}/{n} for n in 0..photos_cached-1, plus ?k=<image_access_key from get_state> when the instance sets one. This tool just resolves them for you, with the key already appended. n indexes cached photos densely, so every n in 0..photos_cached-1 loads. base is this server's own origin; over HTTP it is the URL the request came in on. Unknown ids come back with zero counts and no URLs rather than failing the call.`,
		effect: readOnly,
	}, s.getListingsPhotos)

	addTool(s, toolSpec{
		name:  "mark_shown",
		title: "Mark listings as shown",
		description: `Record that these listings were actually presented to the user, so get_candidates stops offering them until their price or status materially changes.

Call this after presenting listings, not before: it is deliberately separate from get_candidates so an abandoned recommendation run does not silently burn candidates. Pass the queried_at from the response the listings came from as as_of, so a price or status change that landed while you were presenting them still resurfaces them. Unknown ids are skipped; marked is the number of submitted ids that exist.`,
		effect:     additive,
		idempotent: true,
	}, s.markShown)

	addTool(s, toolSpec{
		name:  "record_verdict",
		title: "Record explicit feedback",
		description: `Append one piece of explicit user feedback: love, maybe, or dislike, plus an optional note.

Verdicts are immutable and append-only. If the user changes their mind, record another verdict; the newest one for a listing is their current opinion and the history stays as evidence. Put the user's own reasoning in note, in their words — that free text is the raw material for the taste rubric, so it matters more than the verdict label. Record a verdict only for feedback the user actually gave; never infer one. When the listing has photos, the response returns inspect_photos: a reminder to base your own read on the images (get_listing), not specs.`,
		effect: additive,
		enums:  map[string][]string{"verdict": {"love", "maybe", "dislike"}},
	}, s.recordVerdict)

	addTool(s, toolSpec{
		name:  "record_verdicts",
		title: "Record explicit feedback in bulk",
		description: `Append many verdicts at once: one entry per listing, each love/maybe/dislike with an optional note. Use this to react to a whole batch of listings in a single call instead of one record_verdict per listing.

Same rules as record_verdict: verdicts are immutable and append-only, and note should hold the user's own words since it is the raw material for the taste rubric. Recording is best-effort and per-item, not one transaction: valid ids are saved even if another id in the batch is unknown. recorded lists what was saved and failed lists each item that was not (with its index and listing_id); always check failed_count, since the call succeeds even when some items did not. Record verdicts only for feedback the user actually gave; never infer them.`,
		effect: additive,
		enums:  map[string][]string{"verdicts.verdict": {"love", "maybe", "dislike"}},
	}, s.recordVerdicts)

	addTool(s, toolSpec{
		name:  "set_profile",
		title: "Update hard filters",
		description: `Update the single hard-filter profile. Hard constraints only — budget, bedrooms, bathrooms, carrying cost, neighborhoods, property types. Never encode taste here; taste belongs in verdicts and the rubric.

Partial update: fields you omit keep their current value. To remove a constraint, send the field explicitly as null (or as an empty array for neighborhoods and property_types). Returns the resulting full profile.`,
		effect:     additive,
		idempotent: true,
		enums:      map[string][]string{"listing_type": {"sale", "rent"}, "property_types": {"coop", "condo", "townhouse", "house", "other"}},
	}, s.setProfile)

	addTool(s, toolSpec{
		name:  "update_rubric",
		title: "Save a taste rubric",
		description: `Append a new version of the taste rubric: your own prose summary of what this user likes and dislikes, derived from the verdict history.

Rubrics are append-only summaries, never ground truth; the raw verdicts stay authoritative. Set through_verdict_id to the id of the newest verdict your text accounts for, taken from get_state. An id newer than the newest recorded verdict is rejected — re-read get_state and rebuild if that happens.`,
		effect: additive,
	}, s.updateRubric)

	addTool(s, toolSpec{
		name:  "request_crawl",
		title: "Queue a crawl of a new scope",
		description: `Queue one new crawl scope (areas plus optional price and bedroom filters) for the operator's crawler. Use it when the corpus has nothing for a neighborhood or price band the user cares about — check get_corpus_stats when unsure whether the corpus is simply empty.

**Scope it tightly — it makes the crawl far faster.** The crawler fetches every matching listing's detail page, so a whole borough is thousands of listings and can take a long time, while a few neighborhoods plus a max_price / min_beds filter finishes in minutes. Narrow to what the user actually asked for (fewer areas, a price cap, a bedroom minimum); you can always queue more later. When unsure exactly what a phrase covers, call resolve_areas first, or check get_corpus_stats to see whether the corpus is simply empty before crawling.

**This is an async job that may take a while — do NOT wait inline.** The call returns a job_id immediately and the crawl runs out-of-band (a continuously-running worker starts within moments; a scheduled cron on its cadence). So: tell the user the request is queued and give a rough ETA (a few minutes for a tight scope, longer for a big one), then either ask them to check back shortly, or — if your host can schedule follow-ups — set up an occasional poll of get_crawl_status(job_id). When you next act (a later turn, or the user re-prompting), poll get_crawl_status(job_id): it moves pending → running → done and reports listings_seen and created, so you can tell the user exactly what arrived and then surface the new listings. Meanwhile, keep working with whatever listings already exist.

areas accepts plain-English place names OR numeric area ids, given as strings. You can pass neighborhood, borough, or city phrases directly — "Upper West Side", "all of Manhattan", "the whole city" — and they are resolved to the provider's area ids automatically (the resolved ids come back in the target). Prefer names; call resolve_areas first if you want to preview or confirm exactly which areas a phrase maps to. A name that matches nothing fails the call with the unresolved text, and so does a bare fragment several areas merely contain ("East"); fix the spelling or try resolve_areas rather than guessing an id.

By default a request creates a STANDING scope: the crawler keeps it fresh, re-crawling it about hourly and deep-refreshing it about daily, so the corpus stays current for an area the user cares about. This is what you want almost always — "I'm interested in / looking for / show me 2BR co-ops in Park Slope" all imply ongoing interest, so leave it standing. Set one_off=true ONLY when the user explicitly wants a single snapshot they are not tracking ("just check what's in X right now, don't keep crawling it"). A standing request that matches an existing standing scope is idempotent (re-enables it if paused and returns it with already_queued=true). Manage or remove standing scopes later with manage_crawl_target, and see them all with list_crawl_targets.

An identical one_off request that is still pending or running is not queued twice — you get the existing row back with already_queued=true, so retrying is safe. The one_off queue is small on purpose and refuses new one_off requests once ten are waiting; check list_crawl_targets before asking for more.`,
		effect:     additive,
		idempotent: true,
		enums:      map[string][]string{"listing_type": {"sale", "rent"}},
	}, s.requestCrawl)

	addTool(s, toolSpec{
		name:  "list_crawl_targets",
		title: "List crawl scopes and queued requests",
		description: `List every crawl scope the operator's crawler knows about: the standing scopes it crawls on every pass, and the one-off requests made through request_crawl.

Use it to check whether a request_crawl you made earlier has run yet — status is pending (still queued), running, done, or failed, with last_run_id and last_error once it has been attempted. For a standing scope, status describes its latest pass (pending until the first one). A pending request means the crawler has not drained the queue since; that is normal and not an error.

Each row's kind is "standing" (a recurring scope the crawler keeps fresh — enabled true/false shows whether it is active or paused) or "once" (a one-off snapshot). Manage any of them with manage_crawl_target: pause/resume a standing scope, make a one-off recurring, or delete a scope entirely.

The areas listed here are also the working vocabulary of area ids for request_crawl.`,
		effect: readOnly,
	}, s.listCrawlTargets)

	addTool(s, toolSpec{
		name:  "manage_crawl_target",
		title: "Pause, resume, make recurring, or delete a crawl scope",
		description: `Manage one existing crawl scope from list_crawl_targets by id. action is one of:
- pause — stop crawling a standing scope but keep it (resume later).
- resume — re-enable a paused standing scope.
- make_recurring — promote a one-off scope into a standing one the crawler keeps fresh (use when the user decides they want an area tracked after a one-off look).
- delete — remove the scope entirely so it is never crawled again. This only stops crawling and drops the scope; it never deletes any listings already in the corpus.

Use this to honor "stop crawling X", "pause the Brooklyn scope", "keep an eye on X from now on", or "you can drop that search". Unknown id returns an error; call list_crawl_targets to see valid ids.`,
		effect:     destructive,
		idempotent: true,
		enums:      map[string][]string{"action": {"pause", "resume", "make_recurring", "delete"}},
	}, s.manageCrawlTarget)

	addTool(s, toolSpec{
		name:  "get_crawl_status",
		title: "Check an async crawl job",
		description: `Check one crawl request by the job_id request_crawl returned. status is pending (queued, the crawler has not picked it up), running, done, or failed (see target.last_error). A standing scope (the request_crawl default) reports its latest pass: done means the first crawl has landed and the crawler will keep it fresh from there.

Once the job has run, run carries the crawl's metrics — listings_seen, created (new to the corpus), updated — so you can tell the user exactly what arrived ("crawled 320 listings, 47 new"). Settled jobs are pruned after about a week; an unknown job_id on an old request just means it aged out.`,
		effect: readOnly,
	}, s.getCrawlStatus)

	addTool(s, toolSpec{
		name:  "get_corpus_stats",
		title: "Corpus-wide totals and freshness",
		description: `One snapshot of the whole corpus: total and active listings (split sale/rent), cached photos, verdicts, lists, queued crawl requests, standing crawl scopes, when the last complete crawl finished, and the exact neighborhood names active listings carry (busiest first) — the spellings search_listings and set_profile match verbatim.

Call it when results seem thin or empty before concluding anything: listings=0 means a fresh install whose first crawl has not run — queue what the user cares about with request_crawl and set the expectation that the corpus is filling. A stale last_crawl_at explains missing fresh listings. Cheap, read-only, safe to call at the start of a conversation.`,
		effect: readOnly,
	}, s.getCorpusStats)

	addTool(s, toolSpec{
		name:  "resolve_areas",
		title: "Resolve a place name to crawl area ids",
		description: `Turn a plain-English place phrase into the provider's numeric area ids for crawling. Pass a neighborhood, borough, or city phrase — "Upper West Side", "West Village and Chelsea" (call once per phrase), "all of Manhattan", "the whole city" — and get back the matching areas (id, name, borough) plus a ready-to-use area_ids list.

Use it to translate what the user said into ids before request_crawl, or to confirm which areas a phrase covers. "all of Manhattan" resolves to the borough; "the whole city" resolves to all five NYC boroughs. request_crawl also accepts names directly, so resolve_areas is mainly for previewing/confirming; a bare numeric id (from list_crawl_targets) resolves to its own name. If nothing matches, area_ids is empty — tell the user you could not find that place rather than guessing.`,
		effect: readOnly,
	}, s.resolveAreas)

	addTool(s, toolSpec{
		name:  "reset_state",
		title: "Reset saved taste memory",
		description: `Destructive: erases all saved taste memory (verdicts, rubric, shown history, profile), every saved list's contents (favorites are emptied, custom lists deleted), AND the area preferences (standing crawl scopes and queued crawl jobs), starting the search over completely. The listings corpus is kept. Only call this when the user explicitly asks to reset / start over, and only with confirm=true.

With confirm=false (the default), nothing is deleted: the call returns reset=false and explains that confirm=true is required. With confirm=true it wipes the taste, list, and area-preference tables in one transaction and returns per-table counts. get_state afterward shows the zero profile, no rubric, and no verdicts; get_lists shows only an empty favorites list; every active listing becomes a fresh candidate again; and the crawler no longer refreshes the old areas until a new scope is requested (the corpus itself is kept, so a re-crawl only adds fresh data).`,
		effect:     destructive,
		idempotent: true,
	}, s.resetState)

	addTool(s, toolSpec{
		name:        "get_lists",
		title:       "List the user's curated lists",
		description: `Return every curated list the user keeps: the built-in "favorites" plus any named feature buckets they created (e.g. "big windows"), each with an emoji and a count. Call this to see what buckets exist before triaging a listing with add_to_list, or to answer "what's on my favorites / big-windows list" (then use get_list for the listings themselves).`,
		effect:      readOnly,
	}, s.getLists)

	addTool(s, toolSpec{
		name:        "create_list",
		title:       "Create a curated list",
		description: `Create a new list bucket from a display name plus an optional emoji. Use it when the user names a feature they want to track (large windows, great yards, quiet block) and get_lists shows no matching list yet; then add listings to it with add_to_list. Idempotent: if a list with the same derived slug already exists, it is returned unchanged with already_exists=true. Do not create lists speculatively — only when the user has expressed a bucket they care about.`,
		effect:      additive,
		idempotent:  true,
	}, s.createList)

	addTool(s, toolSpec{
		name:        "add_to_list",
		title:       "Add a listing to a list",
		description: `Add a listing to one of the user's lists (by name or slug). The list must already exist — create it first with create_list. YOU decide membership from what the user says; the server just stores it. Add to "favorites" when the user strongly approves of a listing (a love verdict, an explicit "favorite this", a high rating). Also add to a feature list when the user praises a feature that matches that list's name (they love the windows and a "big windows" list exists). Adding is idempotent (added=false reports it was already there) and separate from recording a verdict — do both when the user both reacts and wants it saved.`,
		effect:      additive,
		idempotent:  true,
	}, s.addToList)

	addTool(s, toolSpec{
		name:        "remove_from_list",
		title:       "Remove a listing from a list",
		description: `Remove a listing from one of the user's lists (by name or slug). Use it when the user changes their mind about a listing belonging in a bucket. Removing a listing that is not on the list is a harmless no-op.`,
		effect:      destructive,
		idempotent:  true,
	}, s.removeFromList)

	addTool(s, toolSpec{
		name:        "get_list",
		title:       "Get the listings in a list",
		description: `Return one list (by name or slug) and its member listings as compact rows, most-recently-added first. Use it to show the user what is on their favorites or a feature list. Rows carry the same photo_count / photos_cached as search rows, so you can build a gallery of the list with constructable image URLs.`,
		effect:      readOnly,
	}, s.getList)

	addTool(s, toolSpec{
		name:        "get_console_url",
		title:       "Get the web console link",
		description: `Return the URL of the hosted web console — a browser view of this corpus and taste state. Call it only when the user asks to see this in a website, console, dashboard, or browser, and then give them the link. If no console is configured, available is false; tell the user there is no web view rather than inventing a URL.`,
		effect:      readOnly,
	}, s.getConsoleURL)
}

func (s *Server) resetState(ctx context.Context, _ *mcpsdk.CallToolRequest, in resetStateInput) (*mcpsdk.CallToolResult, resetStateOutput, error) {
	if !in.Confirm {
		return nil, resetStateOutput{
			Reset:   false,
			Message: "No changes made. Set confirm=true to erase all saved taste memory (every verdict, the rubric, the shown history, the profile, and every saved list) and the area preferences (standing crawl scopes and queued crawl jobs). The listings corpus is always kept.",
		}, nil
	}
	res, err := s.svc.ResetState(ctx)
	if err != nil {
		return nil, resetStateOutput{}, err
	}
	c := res.Cleared
	return nil, resetStateOutput{
		Reset:   true,
		Cleared: resetCountsDTO{Verdicts: c.Verdicts, Rubrics: c.Rubrics, Shown: c.Shown, Profile: c.Profile, ListItems: c.ListItems, CrawlTargets: c.CrawlTargets},
		Message: fmt.Sprintf("Cleared %d verdicts, the rubric, %d shown markers, the profile, %d saved list item(s) (custom lists deleted), and %d crawl scope(s); the listings corpus was kept.", c.Verdicts, c.Shown, c.ListItems, c.CrawlTargets),
	}, nil
}

const (
	defaultVerdictsLimit = 300
	maxVerdictsLimit     = 2000
)

func (s *Server) getState(ctx context.Context, req *mcpsdk.CallToolRequest, in getStateInput) (*mcpsdk.CallToolResult, getStateOutput, error) {
	state, err := s.svc.State(ctx)
	if err != nil {
		return nil, getStateOutput{}, err
	}
	limit := in.VerdictsLimit
	if limit <= 0 {
		limit = defaultVerdictsLimit
	}
	limit = min(limit, maxVerdictsLimit)
	verdicts := state.Verdicts
	if len(verdicts) > limit {
		verdicts = verdicts[len(verdicts)-limit:] // oldest first, so the tail is the most recent
	}
	out := getStateOutput{
		Profile:        toProfile(state.Profile),
		Rubric:         toRubric(state.Rubric),
		RubricStale:    state.Stale,
		Verdicts:       nonNil(xslices.Map(verdicts, toVerdict)),
		VerdictsTotal:  len(state.Verdicts),
		ImageBaseURL:   s.baseURL(req),
		ImageAccessKey: s.publicImgToken,
	}
	if n := len(state.Verdicts); n > 0 {
		out.LatestVerdictID = int64(state.Verdicts[n-1].ID)
	}
	return nil, out, nil
}

func (s *Server) searchListings(ctx context.Context, _ *mcpsdk.CallToolRequest, in searchListingsInput) (*mcpsdk.CallToolResult, searchListingsOutput, error) {
	query := listings.SearchQuery{
		MaxPrice:        moneyPtr(in.MaxPrice),
		MinBeds:         ptr.Clone(in.MinBeds),
		MinBaths:        ptr.Clone(in.MinBaths),
		Neighborhoods:   in.Neighborhoods,
		Limit:           in.Limit,
		Offset:          in.Offset,
		IncludeInactive: in.IncludeInactive,
	}
	if in.PropertyType != nil && *in.PropertyType != "" {
		pt, err := domain.ParsePropertyType(*in.PropertyType)
		if err != nil {
			return nil, searchListingsOutput{}, err
		}
		query.PropertyTypes = []domain.PropertyType{pt}
	}
	if lt := strings.TrimSpace(in.ListingType); lt != "" {
		parsed, err := domain.ParseListingType(lt)
		if err != nil {
			return nil, searchListingsOutput{}, err
		}
		query.ListingType = parsed
	}
	queriedAt := time.Now()
	result, err := s.svc.Search(ctx, query)
	if err != nil {
		return nil, searchListingsOutput{}, err
	}
	return nil, searchListingsOutput{
		EffectiveFilters:       toEffectiveFilters(result.Filters, result.Limit),
		Count:                  len(result.Rows),
		Total:                  result.Total,
		Offset:                 result.Offset,
		QueriedAt:              queriedAt.UTC().Format(time.RFC3339Nano),
		Listings:               toListingRows(result.Rows),
		UnmatchedNeighborhoods: result.UnmatchedNeighborhoods,
	}, nil
}

func (s *Server) getCandidates(ctx context.Context, _ *mcpsdk.CallToolRequest, in getCandidatesInput) (*mcpsdk.CallToolResult, getCandidatesOutput, error) {
	queriedAt := time.Now()
	rows, err := s.svc.Candidates(ctx, in.Limit)
	if err != nil {
		return nil, getCandidatesOutput{}, err
	}
	return nil, getCandidatesOutput{Count: len(rows), QueriedAt: queriedAt.UTC().Format(time.RFC3339Nano), Listings: toListingRows(rows)}, nil
}

func (s *Server) getListing(ctx context.Context, req *mcpsdk.CallToolRequest, in getListingInput) (*mcpsdk.CallToolResult, listingDetailDTO, error) {
	// Default: an inline contact sheet, so the naive call lets the model SEE the
	// listing. "links" shows the user only; "none" is data-only.
	mode := strings.ToLower(strings.TrimSpace(in.Photos))
	if mode == "" {
		mode = listings.LayoutContactSheet
	}
	pr := listings.PhotoRequest{Count: true, MaxPhotos: in.MaxPhotos}
	switch mode {
	case "none":
	case "links":
		pr.Links = true
	case listings.LayoutIndividual:
		pr.Include, pr.Layout = true, listings.LayoutIndividual
	case listings.LayoutContactSheet:
		pr.Include, pr.Layout = true, listings.LayoutContactSheet
		if pr.MaxPhotos <= 0 {
			pr.MaxPhotos = 12
		}
	default:
		return nil, listingDetailDTO{}, fmt.Errorf("photos must be contact_sheet, individual, links, or none, got %q", in.Photos)
	}

	detail, err := s.svc.Listing(ctx, domain.PropertyID(in.ID), pr)
	if errors.Is(err, listings.ErrNotFound) {
		return nil, listingDetailDTO{}, fmt.Errorf("listing %d does not exist; ids come from search_listings or get_candidates", in.ID)
	}
	if err != nil {
		return nil, listingDetailDTO{}, err
	}

	dto := toListingDetail(detail)
	// The text block carries the facts AND the full description, so a model that
	// only reads text still has the copy; images ride alongside for inline modes.
	content := []mcpsdk.Content{&mcpsdk.TextContent{Text: listingText(dto, detail.Photos, mode)}}
	switch {
	case pr.Include:
		content = append(content, imageBlocks(detail.Photos)...)
	case pr.Links:
		content = append(content, photoLinks(s.baseURL(req), s.publicImgToken, dto.Address, detail.Photos.Links)...)
	}
	return &mcpsdk.CallToolResult{Content: content}, dto, nil
}

// baseURL is the origin for public /img links: the per-request base the publicurl middleware derives (over HTTP), else the configured fallback.
func (s *Server) baseURL(req *mcpsdk.CallToolRequest) string {
	if ex := req.GetExtra(); ex != nil {
		if base := publicurl.FromHeader(ex.Header); base != "" {
			return base
		}
	}
	return s.publicBase
}

// photoLinks turns readable cached thumbnails into resource_link blocks pointing at the public /img proxy, marked for the user so a host shows them alongside the reply without spending base64 tokens.
func photoLinks(base, token, address string, links []listings.PhotoLink) []mcpsdk.Content {
	out := make([]mcpsdk.Content, 0, len(links))
	for _, link := range links {
		out = append(out, &mcpsdk.ResourceLink{
			URI:         publicurl.ImageURL(base, "/img/"+link.Key, token),
			Name:        fmt.Sprintf("%s — photo %d", address, link.Position+1),
			MIMEType:    link.MIME,
			Annotations: &mcpsdk.Annotations{Audience: []mcpsdk.Role{"user"}, Priority: 0.6},
		})
	}
	return out
}

// listingSummary is a compact one-line description of a listing for the text content block, deliberately not the full JSON (that is structuredContent).
func listingText(d listingDetailDTO, photos listings.PhotoResult, mode string) string {
	parts := []string{fmt.Sprintf("#%d %s", d.ID, d.Address)}
	if d.Price != nil {
		price := "$" + withThousands(*d.Price)
		if d.ListingType == string(domain.ListingRent) {
			price += "/mo"
		}
		parts = append(parts, price)
	}
	var bb strings.Builder
	if d.Beds != nil {
		fmt.Fprintf(&bb, "%d bd", *d.Beds)
	}
	if d.Baths != nil {
		if bb.Len() > 0 {
			bb.WriteString(" / ")
		}
		fmt.Fprintf(&bb, "%s ba", strconv.FormatFloat(*d.Baths, 'f', -1, 64))
	}
	if bb.Len() > 0 {
		parts = append(parts, bb.String())
	}
	if d.Sqft != nil {
		parts = append(parts, withThousands(int64(*d.Sqft))+" sqft")
	}
	if d.MonthlyCarrying != nil {
		parts = append(parts, "$"+withThousands(*d.MonthlyCarrying)+"/mo carrying")
	}
	if d.PropertyType != "" {
		parts = append(parts, d.PropertyType)
	}
	if d.Status != "" {
		parts = append(parts, d.Status)
	}
	if d.DOM != nil {
		parts = append(parts, fmt.Sprintf("%d days on market", *d.DOM))
	}
	summary := strings.Join(parts, " · ")

	if note := photoModeNote(photos, mode); note != "" {
		summary += ". " + note
	}
	// The full description follows the facts line so a model reading only the
	// text block still has the listing copy (rows carry a preview only).
	if d.Description != "" {
		summary += "\n\n" + d.Description
	}
	if d.URL != "" {
		summary += "\n\n" + d.URL
	}
	return summary
}

// photoNote describes how the response carries photos so a host that ignores the non-text blocks can still tell the user (and the model) what was attached.
func photoModeNote(photos listings.PhotoResult, mode string) string {
	switch {
	case len(photos.Sheets) > 0:
		return fmt.Sprintf("%d contact-sheet image(s) attached inline (%d photos) — look at them.", len(photos.Sheets), photos.Counts.Returned)
	case len(photos.Images) > 0:
		return fmt.Sprintf("%d photo(s) attached inline — look at them.", len(photos.Images))
	case len(photos.Links) > 0:
		return fmt.Sprintf("%d photo(s) attached as links (shown to the user, NOT visible to you); call again with photos=contact_sheet to judge the look yourself.", len(photos.Links))
	case mode == "none" && photos.Counts.Total > 0:
		return fmt.Sprintf("%d photo(s) on the listing but none requested — you have not seen it; call again with photos=contact_sheet before judging.", photos.Counts.Total)
	case photos.Counts.Total > 0:
		return fmt.Sprintf("%d photo(s) on the listing but none are cached yet: the crawler has not fetched them, so nothing can be shown until its next pass.", photos.Counts.Total)
	default:
		return ""
	}
}

// withThousands formats a whole-number value with comma grouping, without a formatting dependency.
func withThousands(v int64) string {
	s := strconv.FormatInt(v, 10)
	neg := strings.HasPrefix(s, "-")
	if neg {
		s = s[1:]
	}
	n := len(s)
	if n <= 3 {
		if neg {
			return "-" + s
		}
		return s
	}
	var b strings.Builder
	lead := n % 3
	if lead == 0 {
		lead = 3
	}
	b.WriteString(s[:lead])
	for i := lead; i < n; i += 3 {
		b.WriteByte(',')
		b.WriteString(s[i : i+3])
	}
	if neg {
		return "-" + b.String()
	}
	return b.String()
}

// imageBlocks encodes the Service's photo bytes as MCP image content: one block per photo for the individual layout, one per stitched sheet otherwise.
func imageBlocks(r listings.PhotoResult) []mcpsdk.Content {
	var blocks []mcpsdk.Content
	if r.Layout == listings.LayoutContactSheet {
		for _, sheet := range r.Sheets {
			blocks = append(blocks, &mcpsdk.ImageContent{Data: sheet.Data, MIMEType: listings.SheetMIME})
		}
		return blocks
	}
	for _, img := range r.Images {
		blocks = append(blocks, &mcpsdk.ImageContent{Data: img.Data, MIMEType: img.MIME})
	}
	return blocks
}

func (s *Server) getListingsPhotos(ctx context.Context, req *mcpsdk.CallToolRequest, in getListingsPhotosInput) (*mcpsdk.CallToolResult, getListingsPhotosOutput, error) {
	summaries, err := s.svc.PhotosForListings(ctx, propertyIDs(in.IDs))
	if err != nil {
		return nil, getListingsPhotosOutput{}, err
	}
	base := s.baseURL(req)
	rows := make([]listingPhotosDTO, 0, len(summaries))
	for _, summary := range summaries {
		rows = append(rows, toListingPhotos(base, s.publicImgToken, summary))
	}
	return nil, getListingsPhotosOutput{Count: len(rows), Listings: rows}, nil
}

func (s *Server) markShown(ctx context.Context, _ *mcpsdk.CallToolRequest, in markShownInput) (*mcpsdk.CallToolResult, markShownOutput, error) {
	asOf, err := parseAsOf(in.AsOf)
	if err != nil {
		return nil, markShownOutput{}, err
	}
	marked, err := s.svc.MarkShown(ctx, propertyIDs(in.IDs), asOf)
	if err != nil {
		return nil, markShownOutput{}, err
	}
	return nil, markShownOutput{Marked: marked}, nil
}

func (s *Server) recordVerdict(ctx context.Context, _ *mcpsdk.CallToolRequest, in recordVerdictInput) (*mcpsdk.CallToolResult, recordVerdictOutput, error) {
	kind, err := domain.ParseVerdictKind(in.Verdict)
	if err != nil {
		return nil, recordVerdictOutput{}, err
	}
	v, err := s.svc.RecordVerdict(ctx, domain.PropertyID(in.ListingID), kind, in.Note)
	if errors.Is(err, listings.ErrNotFound) {
		return nil, recordVerdictOutput{}, fmt.Errorf("listing %d does not exist; ids come from search_listings or get_candidates", in.ListingID)
	}
	if err != nil {
		return nil, recordVerdictOutput{}, err
	}
	out := recordVerdictOutput{Verdict: toVerdict(v)}
	if s.anyHavePhotos(ctx, []domain.PropertyID{v.PropertyID}) {
		out.InspectPhotos = inspectPhotosNote
	}
	return nil, out, nil
}

// inspectPhotosNote is the forcing reminder returned with a verdict when the
// listing has photos: judgment from specs alone is unreliable.
const inspectPhotosNote = "At least one of these listings has cached photos. Do not finalize a recommendation from specs or rows alone — call get_listing (photos default to an inline contact sheet) and look. Listings that match on specs frequently look dated or face a wall."

// anyHavePhotos reports whether any of the listings has a cached, serveable photo.
func (s *Server) anyHavePhotos(ctx context.Context, ids []domain.PropertyID) bool {
	sums, err := s.svc.PhotosForListings(ctx, ids)
	if err != nil {
		return false
	}
	for _, sm := range sums {
		if sm.Cached > 0 {
			return true
		}
	}
	return false
}

func (s *Server) recordVerdicts(ctx context.Context, _ *mcpsdk.CallToolRequest, in recordVerdictsInput) (*mcpsdk.CallToolResult, recordVerdictsOutput, error) {
	inputs := make([]listings.VerdictInput, 0, len(in.Verdicts))
	for i, item := range in.Verdicts {
		kind, err := domain.ParseVerdictKind(item.Verdict)
		if err != nil {
			return nil, recordVerdictsOutput{}, fmt.Errorf("verdicts[%d] (listing %d): %w", i, item.ListingID, err)
		}
		inputs = append(inputs, listings.VerdictInput{
			PropertyID: domain.PropertyID(item.ListingID),
			Kind:       kind,
			Note:       item.Note,
		})
	}
	batch, err := s.svc.RecordVerdicts(ctx, inputs)
	if err != nil {
		// A mid-batch store failure would be masked to "internal error"; the
		// model must still learn which verdicts landed so it resubmits only the rest.
		if n := len(batch.Recorded); n > 0 {
			slog.Error("mcp: record_verdicts failed mid-batch", "recorded", n, "of", len(inputs), "err", err)
			landed := xslices.Map(batch.Recorded, func(v domain.Verdict) int64 { return int64(v.PropertyID) })
			return nil, recordVerdictsOutput{}, fmt.Errorf("internal error after %d of %d verdicts were recorded (listing ids %v); resubmit only the remaining ones", n, len(inputs), landed)
		}
		return nil, recordVerdictsOutput{}, err
	}
	recorded := batch.Recorded
	rows := nonNil(xslices.Map(recorded, toVerdict))
	failed := make([]verdictFailureDTO, 0, len(batch.Failed))
	for _, f := range batch.Failed {
		failed = append(failed, verdictFailureDTO{Index: f.Index, ListingID: int64(f.PropertyID), Error: f.Message + "; ids come from search_listings or get_candidates"})
	}
	out := recordVerdictsOutput{Recorded: rows, Count: len(rows), Failed: failed, FailedCount: len(failed)}
	ids := make([]domain.PropertyID, 0, len(recorded))
	for _, v := range recorded {
		ids = append(ids, v.PropertyID)
	}
	if s.anyHavePhotos(ctx, ids) {
		out.InspectPhotos = inspectPhotosNote
	}
	return nil, out, nil
}

func (s *Server) setProfile(ctx context.Context, req *mcpsdk.CallToolRequest, in setProfileInput) (*mcpsdk.CallToolResult, setProfileOutput, error) {
	// Absent field means "leave unchanged"; explicit null (or []) clears, so presence has to come from the raw arguments.
	given, err := givenFields(req.Params.Arguments)
	if err != nil {
		return nil, setProfileOutput{}, err
	}

	var up listings.ProfileUpdate
	if given["max_price"] {
		up.MaxPrice = listings.Opt[*domain.Money]{Set: true, Val: moneyPtr(in.MaxPrice)}
	}
	if given["min_beds"] {
		up.MinBeds = listings.Opt[*int]{Set: true, Val: ptr.Clone(in.MinBeds)}
	}
	if given["min_baths"] {
		up.MinBaths = listings.Opt[*float64]{Set: true, Val: ptr.Clone(in.MinBaths)}
	}
	if given["max_monthly_carrying"] {
		up.MaxMonthlyCarrying = listings.Opt[*domain.Money]{Set: true, Val: moneyPtr(in.MaxMonthlyCarrying)}
	}
	if given["listing_type"] {
		listingType := domain.ListingSale
		if in.ListingType != nil && *in.ListingType != "" {
			parsed, err := domain.ParseListingType(*in.ListingType)
			if err != nil {
				return nil, setProfileOutput{}, err
			}
			listingType = parsed
		}
		up.ListingType = listings.Opt[domain.ListingType]{Set: true, Val: listingType}
	}
	if given["neighborhoods"] {
		up.Neighborhoods = listings.Opt[[]string]{Set: true, Val: in.Neighborhoods}
	}
	if given["property_types"] {
		types := make([]domain.PropertyType, 0, len(in.PropertyTypes))
		for _, t := range in.PropertyTypes {
			pt, err := domain.ParsePropertyType(t)
			if err != nil {
				return nil, setProfileOutput{}, err
			}
			types = append(types, pt)
		}
		up.PropertyTypes = listings.Opt[[]domain.PropertyType]{Set: true, Val: types}
	}

	saved, err := s.svc.SetProfile(ctx, up)
	if err != nil {
		return nil, setProfileOutput{}, err
	}
	unmatched, err := s.svc.UnmatchedNeighborhoods(ctx, saved.Neighborhoods)
	if err != nil {
		return nil, setProfileOutput{}, err
	}
	return nil, setProfileOutput{Profile: toProfile(saved), UnmatchedNeighborhoods: unmatched}, nil
}

func (s *Server) updateRubric(ctx context.Context, _ *mcpsdk.CallToolRequest, in updateRubricInput) (*mcpsdk.CallToolResult, updateRubricOutput, error) {
	rubric, err := s.svc.UpdateRubric(ctx, in.Content, domain.VerdictID(in.ThroughVerdictID))
	if errors.Is(err, listings.ErrFutureThroughVerdict) {
		return nil, updateRubricOutput{}, fmt.Errorf("through_verdict_id %d is newer than the newest recorded verdict; verdict ids come from get_state (verdicts[].id or latest_verdict_id), not listing ids, so re-read get_state and rebuild the rubric from the history it returns", in.ThroughVerdictID)
	}
	if err != nil {
		return nil, updateRubricOutput{}, err
	}
	return nil, updateRubricOutput{Rubric: *toRubric(&rubric)}, nil
}

func (s *Server) requestCrawl(ctx context.Context, _ *mcpsdk.CallToolRequest, in requestCrawlInput) (*mcpsdk.CallToolResult, requestCrawlOutput, error) {
	listingType, err := domain.ParseListingType(in.ListingType)
	if err != nil {
		return nil, requestCrawlOutput{}, err
	}
	if len(in.Areas) > listings.MaxRequestAreas {
		return nil, requestCrawlOutput{}, listings.Invalidf("%d areas requested; at most %d fit in one crawl request", len(in.Areas), listings.MaxRequestAreas)
	}
	areaIDs, unresolved := s.svc.ResolveAreaList(in.Areas)
	if len(unresolved) > 0 {
		return nil, requestCrawlOutput{}, fmt.Errorf("could not resolve area name(s): %s — call resolve_areas to find the right place, or pass numeric area ids", strings.Join(unresolved, ", "))
	}
	// An empty areaIDs falls through to the Service, which produces the canonical "areas is empty" validation error.
	result, err := s.svc.RequestCrawl(ctx, listings.CrawlRequest{
		Areas:       areaIDs,
		ListingType: listingType,
		MaxPrice:    moneyPtr(in.MaxPrice),
		MinBeds:     ptr.Clone(in.MinBeds),
		Note:        in.Note,
		Recurring:   !in.OneOff,
	})
	if err != nil {
		return nil, requestCrawlOutput{}, err
	}
	out := requestCrawlOutput{
		JobID:           int64(result.Target.ID),
		Target:          toCrawlTarget(result.Target),
		AlreadyQueued:   result.AlreadyQueued,
		PendingRequests: result.PendingRequests,
		Message:         fmt.Sprintf("Queued crawl %d; poll get_crawl_status(%d) for progress.", result.Target.ID, result.Target.ID),
	}
	if result.AlreadyQueued {
		out.Message = fmt.Sprintf("An identical scope already exists as crawl %d (status %s); nothing new was queued. get_crawl_status reports its most recent pass, so check run.finished_at before treating its numbers as fresh.", result.Target.ID, result.Target.Status)
	}
	return nil, out, nil
}

func (s *Server) manageCrawlTarget(ctx context.Context, _ *mcpsdk.CallToolRequest, in manageCrawlTargetInput) (*mcpsdk.CallToolResult, manageCrawlTargetOutput, error) {
	id := domain.CrawlTargetID(in.ID)
	out := manageCrawlTargetOutput{ID: in.ID, Action: in.Action}
	switch in.Action {
	case "pause":
		if err := s.svc.SetCrawlTargetEnabled(ctx, id, false); err != nil {
			return nil, manageCrawlTargetOutput{}, crawlTargetErr(in.ID, err)
		}
		out.Message = fmt.Sprintf("Paused crawl scope %d; the crawler will stop crawling it until you resume it.", in.ID)
	case "resume":
		if err := s.svc.SetCrawlTargetEnabled(ctx, id, true); err != nil {
			return nil, manageCrawlTargetOutput{}, crawlTargetErr(in.ID, err)
		}
		out.Message = fmt.Sprintf("Resumed crawl scope %d; the crawler will keep it fresh again.", in.ID)
	case "make_recurring":
		if err := s.svc.MakeCrawlTargetRecurring(ctx, id); err != nil {
			return nil, manageCrawlTargetOutput{}, crawlTargetErr(in.ID, err)
		}
		out.Message = fmt.Sprintf("Crawl scope %d is now standing; the crawler will keep re-crawling it (about hourly) to keep it fresh.", in.ID)
	case "delete":
		if err := s.svc.DeleteCrawlTarget(ctx, id); err != nil {
			return nil, manageCrawlTargetOutput{}, crawlTargetErr(in.ID, err)
		}
		out.Deleted = true
		out.Message = fmt.Sprintf("Deleted crawl scope %d; it will no longer be crawled. Listings already in the corpus are kept.", in.ID)
	default:
		return nil, manageCrawlTargetOutput{}, fmt.Errorf("unknown action %q: use pause, resume, make_recurring, or delete", in.Action)
	}
	return nil, out, nil
}

func (s *Server) listCrawlTargets(ctx context.Context, _ *mcpsdk.CallToolRequest, _ listCrawlTargetsInput) (*mcpsdk.CallToolResult, listCrawlTargetsOutput, error) {
	targets, err := s.svc.CrawlTargets(ctx)
	if err != nil {
		return nil, listCrawlTargetsOutput{}, err
	}
	rows := nonNil(xslices.Map(targets, toCrawlTarget))
	return nil, listCrawlTargetsOutput{Count: len(rows), Targets: rows}, nil
}

func givenFields(raw json.RawMessage) (map[string]bool, error) {
	if len(raw) == 0 {
		return map[string]bool{}, nil
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return nil, fmt.Errorf("decode tool arguments: %w", err)
	}
	given := make(map[string]bool, len(fields))
	for name := range fields {
		given[name] = true
	}
	return given, nil
}

// parseAsOf reads an optional RFC 3339 instant; empty means "now".
func parseAsOf(raw string) (time.Time, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return time.Time{}, nil
	}
	t, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return time.Time{}, listings.Invalidf("as_of must be an RFC 3339 timestamp such as 2026-09-02T18:04:05Z: %v", err)
	}
	return t, nil
}
