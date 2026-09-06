// Package mcp is the MCP transport adapter: decode -> service call -> convert -> encode, with no ranking or heuristics. It depends only on internal/listings and internal/domain (plus the MCP SDK).
package mcp

import (
	"context"
	"fmt"
	"strings"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/shared/brand"
)

const (
	serverName    = "property-radar"
	serverVersion = "0.1.0"
)

type Server struct {
	svc *listings.Service
	mcp *mcpsdk.Server
	// publicBase is the fallback origin for public /img links minted by get_listing when the request carries no derived base URL (stdio, or a PUBLIC_BASE_URL override). Over HTTP the per-request base wins. Empty yields relative /img links as a last resort.
	publicBase string
	// consoleURL is the hosted read-only web console, surfaced by get_console_url. Empty means none is configured.
	consoleURL string
	// publicImgToken, when set, is appended as ?k= to every minted image URL and exposed via get_state (image_access_key). Empty means the /img proxy is public and URLs carry no key.
	publicImgToken string
}

// Option configures a Server at construction.
type Option func(*Server)

// WithPublicBaseURL sets the fallback origin for public /img links when a request does not carry one (stdio, or a PUBLIC_BASE_URL override).
func WithPublicBaseURL(base string) Option {
	return func(s *Server) { s.publicBase = strings.TrimRight(strings.TrimSpace(base), "/") }
}

// WithConsoleURL sets the hosted web-console URL that get_console_url points to.
func WithConsoleURL(url string) Option {
	return func(s *Server) { s.consoleURL = strings.TrimRight(strings.TrimSpace(url), "/") }
}

// WithPublicImageToken sets the PUBLIC_IMG_TOKEN appended to minted image URLs and reported by get_state. Empty leaves the /img proxy public and URLs keyless.
func WithPublicImageToken(token string) Option {
	return func(s *Server) { s.publicImgToken = strings.TrimSpace(token) }
}

const instructions = `Property Radar remembers one operator's home search: hard filters, every explicit verdict, and a taste rubric you write yourself.

Typical loop: call get_state first to load the profile, the current rubric, and the verdict history (most recent verdicts_limit of them, oldest first; verdicts_total says how many exist). When the user states or changes a hard constraint (budget, bedrooms, neighborhoods, sale vs rent), save it with set_profile so get_candidates honors it. Use get_candidates for fresh profile-matching listings, or search_listings to explore outside the profile. Both return compact text rows and never photos. For every listing you are seriously considering, call get_listing and LOOK at the photos before forming an opinion (see below). After you present listings to the user, call mark_shown so they are not offered again until something material changes. Turn the user's reaction into record_verdict calls (record_verdicts for a whole batch), and when get_state reports rubric_stale, rewrite the rubric from the verdict history and store it with update_rubric using latest_verdict_id as through_verdict_id.

**You must look at photos before judging any listing.** Compact rows and data fields carry NO visual information, and specs mislead: a listing that matches on beds/price/sqft often looks dated inside or faces a wall, while a plainer one photographs beautifully. So for each listing on your shortlist, call get_listing(id) — by default it returns an inline contact-sheet image you can actually see, plus the full description in the text block. Do this from the FIRST round, not only at the end; a "maybe" formed from rows alone is a guess. Do not use photos="links" to judge for yourself — those images are shown to the user but never reach your context; they are only for when the human is the one looking.

BUILDING A GALLERY, website, or profile page for MANY listings is different: never loop get_listing to collect URLs. Every compact row carries photo_count and photos_cached, and the photo URLs are CONSTRUCTABLE: for photos_cached=k, its images are {base}/img/l/{id}/{n} for n in 0..k-1 — JPEG URLs you embed directly as <img src>. When get_state returns an image_access_key, append ?k=<image_access_key> so they load. Build those URLs from the rows, or call get_listings_photos(ids) once for up to 30 listings (URLs come back with the key already appended). base is this server's own origin, which get_state also returns as image_base_url when it is known.

If get_candidates or search_listings come back empty, call get_corpus_stats before concluding anything: listings=0 means a fresh install whose first crawl has not run, and a stale last_crawl_at explains missing fresh data. Do not just report zero results: proactively call request_crawl to queue the relevant scope — it returns a job_id immediately and the crawl runs out-of-band (a continuously-running worker picks it up within minutes; a scheduled cron on its cadence). By default request_crawl creates a STANDING scope the crawler keeps fresh, which is what you want whenever the user is interested in an area; pass one_off=true only for a single snapshot they do not want tracked. Manage those scopes with manage_crawl_target (pause, resume, make_recurring, or delete) and see them all with list_crawl_targets — honor "stop crawling X" or "keep an eye on Y" with it. Tell the user their request is queued and roughly when to expect listings; on an empty corpus, ask them to check back after the first crawl lands rather than improvising results. Poll get_crawl_status(job_id) to report progress and, once done, exactly what arrived (listings_seen, created). You do not need to know area ids — request_crawl accepts plain place names ("Upper West Side", "all of Manhattan", "the whole city") and resolves them (listing_type is still required: sale or rent); resolve_areas is only for previewing which areas a phrase covers.

When you present or summarize any listing to the user, always include its listing URL (the url field) as a link so they can open it. For subjective or "find the weirdest/most unusual" style requests, listing descriptions carry strong signal: sweep the whole corpus with search_listings (page by offset, include_inactive if useful), read the description previews to shortlist, then call get_listing for the full description and photos to confirm.

Lists: the user keeps curated lists of listings — a built-in "favorites" plus any named feature buckets they create (e.g. "big windows", "great yards"), each with an optional emoji. get_lists shows them with their counts; get_list(list) returns a list's listings. YOU do the triage, the server only stores membership and never classifies: when the user reacts strongly to a listing (a love verdict, "add this to favorites", "I'd rate this a 9/10"), add it to favorites with add_to_list. When they praise a specific FEATURE that matches an existing list's name — they love the huge windows and a "big windows" list exists — add the listing to that list too. Use create_list to make a new bucket (name plus optional emoji) when the user names a feature they want to track and no matching list exists yet, then add_to_list to it. A list argument accepts the name or the slug.

If the user asks to see this in a web console, site, dashboard, or browser view, call get_console_url and give them the link — it is a web view of this same data. Do not invent a URL; if get_console_url reports none is available, say so.

If the user explicitly asks to reset or start the search over, reset_state erases all saved taste memory (profile, verdicts, rubric, shown history, lists other than the empty favorites) and every crawl scope, while keeping the listings corpus; warn the user of that full scope first, and it only wipes when called with confirm=true.

This server does no ranking, scoring, or interpretation. Every result is a deterministic query over stored state; the judgment is yours.`

// NewServer builds the stdio/HTTP MCP adapter, constructing the Service from the store and photo interfaces.
func NewServer(st listings.Store, photos listings.PhotoStore, opts ...Option) *Server {
	return NewServerFromService(listings.NewService(st, photos), opts...)
}

// NewServerFromService builds the adapter over an already-constructed Service so a combined server can share one Service between MCP and REST.
func NewServerFromService(svc *listings.Service, opts ...Option) *Server {
	s := &Server{svc: svc}
	for _, opt := range opts {
		opt(s)
	}
	s.mcp = mcpsdk.NewServer(
		&mcpsdk.Implementation{
			Name:    serverName,
			Title:   "Property Radar",
			Version: serverVersion,
			// A data-URI icon so a host (e.g. Claude) shows the project's mark
			// instead of falling back to the host's default; works over stdio too.
			Icons: []mcpsdk.Icon{{
				Source:   brand.IconDataURI(),
				MIMEType: "image/svg+xml",
				Sizes:    []string{"any"},
			}},
		},
		&mcpsdk.ServerOptions{Instructions: instructions},
	)
	s.registerTools()
	return s
}

func (s *Server) MCP() *mcpsdk.Server { return s.mcp }

func (s *Server) RunStdio(ctx context.Context) error {
	if err := s.mcp.Run(ctx, &mcpsdk.StdioTransport{}); err != nil {
		return fmt.Errorf("run mcp stdio server: %w", err)
	}
	return nil
}
