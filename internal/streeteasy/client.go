// Package streeteasy is the StreetEasy provider adapter: a GraphQL search client,
// detail-page enrichment, optional Webshare proxying, and conversion of wire
// types into ingest.SourceProperty. Nothing outside this package sees its wire shapes.
package streeteasy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"iter"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

const (
	ProviderName = "streeteasy"

	defaultAPIURL       = "https://api-v6.streeteasy.com/"
	defaultSiteURL      = "https://streeteasy.com"
	defaultUserAgent    = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"
	defaultDelay        = 1500 * time.Millisecond
	defaultPerPage      = 500
	defaultResultCap    = 1000
	defaultMaxAttempts  = 3
	defaultRetryBackoff = 2 * time.Second

	maxPages         = 50
	maxErrorBodySize = 2 << 10
	maxPageBodySize  = 32 << 20
)

type Config struct {
	APIURL    string
	SiteURL   string
	UserAgent string

	// Delay is the minimum spacing between outbound requests; concurrency is 1.
	Delay time.Duration

	PerPage int
	// ResultCap is the most rows the API answers per query; a scope counting
	// more is split into price bands.
	ResultCap    int
	MaxAttempts  int
	RetryBackoff time.Duration

	// Makes EnrichDetail a no-op: description and days-on-market stay unset.
	DisableDetail bool
}

func (c Config) withDefaults() Config {
	if c.APIURL == "" {
		c.APIURL = defaultAPIURL
	}
	if c.SiteURL == "" {
		c.SiteURL = defaultSiteURL
	}
	c.SiteURL = strings.TrimSuffix(c.SiteURL, "/")
	if c.UserAgent == "" {
		c.UserAgent = defaultUserAgent
	}
	if c.Delay <= 0 {
		c.Delay = defaultDelay
	}
	if c.PerPage <= 0 {
		c.PerPage = defaultPerPage
	}
	if c.ResultCap <= 0 {
		c.ResultCap = defaultResultCap
	}
	if c.MaxAttempts <= 0 {
		c.MaxAttempts = defaultMaxAttempts
	}
	if c.RetryBackoff <= 0 {
		c.RetryBackoff = defaultRetryBackoff
	}
	return c
}

type Client struct {
	http *http.Client
	cfg  Config

	mu          sync.Mutex
	nextAllowed time.Time
}

var _ ingest.Source = (*Client)(nil)

func NewClient(httpClient *http.Client, cfg Config) *Client {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Client{http: httpClient, cfg: cfg.withDefaults()}
}

func (c *Client) Name() string { return ProviderName }

// A yielded error with a zero SourceProperty is provider-level and ends the
// iterator; one alongside a populated SourceProperty is item-scoped partial data.
func (c *Client) Search(ctx context.Context, q ingest.SearchQuery) iter.Seq2[ingest.SourceProperty, error] {
	return func(yield func(ingest.SourceProperty, error) bool) {
		var zero ingest.SourceProperty

		listingType := q.ListingType
		if listingType == "" {
			listingType = domain.ListingSale
		}
		var fetch func(context.Context, []int, ingest.SearchQuery, priceBand, int) (*searchOutput, error)
		switch listingType {
		case domain.ListingSale:
			fetch = c.salesPage
		case domain.ListingRent:
			fetch = c.rentalsPage
		default:
			yield(zero, fmt.Errorf("streeteasy search: %w: %w: %q", ingest.ErrSearchAborted, ErrUnsupportedListingType, q.ListingType))
			return
		}
		areas, err := parseAreas(q.Areas)
		if err != nil {
			yield(zero, fmt.Errorf("streeteasy search: %w: %w", ingest.ErrSearchAborted, err))
			return
		}

		seen := make(map[string]struct{})
		emit := func(edge searchEdge) bool {
			if len(edge.Node) == 0 || string(edge.Node) == "null" {
				return true
			}
			var node searchListing
			if err := json.Unmarshal(edge.Node, &node); err != nil {
				// A field of the wrong type still decodes the id; report the listing
				// as seen-but-unusable so it is not aged toward delisting.
				var typeErr *json.UnmarshalTypeError
				if errors.As(err, &typeErr) && node.ID != "" {
					seen[node.ID] = struct{}{}
					return yield(ingest.SourceProperty{ProviderID: node.ID}, fmt.Errorf("streeteasy decode listing %s: %w: %w", node.ID, ingest.ErrUnusableListing, err))
				}
				return yield(zero, fmt.Errorf("streeteasy decode listing: %w", err))
			}
			if node.ID == "" {
				return yield(zero, errors.New("streeteasy decode listing: missing id"))
			}
			if _, dup := seen[node.ID]; dup {
				return true
			}
			seen[node.ID] = struct{}{}
			sp, itemErr := c.sourceProperty(ctx, node, edge.Node, listingType)
			return yield(sp, itemErr)
		}

		// The API answers at most ~1000 rows per query however many pages are
		// asked for (and counts pages as if it did not), so a band counting more
		// is split at its median price; every abort below keeps the run incomplete.
		var walk func(band priceBand) bool
		walk = func(band priceBand) bool {
			for page := 1; page <= maxPages; page++ {
				out, err := fetch(ctx, areas, q, band, page)
				if err != nil {
					yield(zero, fmt.Errorf("streeteasy search page %d: %w: %w", page, ingest.ErrSearchAborted, err))
					return false
				}
				if page == 1 && out.TotalCount > c.cfg.ResultCap {
					lower, upper, ok := band.split(out.Edges)
					if !ok {
						yield(zero, fmt.Errorf("streeteasy search: %w: %d listings priced %s exceed the provider's result cap", ingest.ErrSearchAborted, out.TotalCount, band))
						return false
					}
					return walk(lower) && walk(upper)
				}
				for _, edge := range out.Edges {
					if !emit(edge) {
						return false
					}
				}
				if !out.PageInfo.HasNextPage {
					return true
				}
				if len(out.Edges) == 0 {
					yield(zero, fmt.Errorf("streeteasy search page %d: %w: empty page while more pages were claimed", page, ingest.ErrSearchAborted))
					return false
				}
			}
			yield(zero, fmt.Errorf("streeteasy search: %w: more than %d pages; scope too broad to crawl completely", ingest.ErrSearchAborted, maxPages))
			return false
		}
		var root priceBand
		if q.MaxPrice > 0 {
			root.hi = ptr.To(int(q.MaxPrice))
		}
		walk(root)
	}
}

// priceBand is an inclusive price range; a nil end is open.
type priceBand struct{ lo, hi *int }

func (b priceBand) String() string {
	s := func(p *int) string {
		if p == nil {
			return "*"
		}
		return strconv.Itoa(*p)
	}
	return s(b.lo) + ".." + s(b.hi)
}

func (b priceBand) bounds() *boundsInput {
	if b.lo == nil && b.hi == nil {
		return nil
	}
	return &boundsInput{LowerBound: b.lo, UpperBound: b.hi}
}

// split halves the band at the median price of a sample of its listings; ok is
// false when the band cannot shrink any further.
func (b priceBand) split(sample []searchEdge) (lower, upper priceBand, ok bool) {
	var prices []int
	for _, edge := range sample {
		var node struct {
			Price num `json:"price"`
		}
		if json.Unmarshal(edge.Node, &node) == nil && node.Price.v != nil {
			prices = append(prices, int(*node.Price.v))
		}
	}
	if len(prices) == 0 {
		return b, b, false
	}
	slices.Sort(prices)
	pivot := prices[len(prices)/2]
	if b.hi != nil && pivot >= *b.hi {
		pivot = *b.hi - 1
	}
	if b.lo != nil && pivot < *b.lo {
		return b, b, false
	}
	return priceBand{lo: b.lo, hi: ptr.To(pivot)}, priceBand{lo: ptr.To(pivot + 1), hi: b.hi}, true
}

// EnrichDetail fetches one search-only listing's detail page and merges its
// fields (description, carrying costs, days on market, full photos) in. On
// failure the caller keeps the search-only listing and retries next crawl.
func (c *Client) EnrichDetail(ctx context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) {
	if c.cfg.DisableDetail {
		return sp, nil
	}
	var prov rawProvenance
	if err := json.Unmarshal(sp.Raw, &prov); err != nil {
		return sp, fmt.Errorf("streeteasy decode provenance %s: %w", sp.ProviderID, err)
	}
	var node searchListing
	if err := json.Unmarshal(prov.SearchNode, &node); err != nil {
		return sp, fmt.Errorf("streeteasy decode search node %s: %w", sp.ProviderID, err)
	}
	if node.URLPath == "" {
		return sp, nil
	}
	d, rawDetail, err := c.fetchDetail(ctx, node.URLPath)
	if err != nil {
		return sp, fmt.Errorf("streeteasy detail %s: %w", sp.ProviderID, err)
	}
	// A unit page shows whichever listing is current for the unit (a relist, or
	// the rental twin of a sale), so never merge another listing's fields.
	if d.ID != node.ID {
		return sp, fmt.Errorf("streeteasy detail %s: page shows listing %q", sp.ProviderID, d.ID)
	}
	raw, err := json.Marshal(rawProvenance{SearchNode: prov.SearchNode, DetailListing: rawDetail})
	if err != nil {
		return sp, fmt.Errorf("streeteasy encode provenance %s: %w", sp.ProviderID, err)
	}
	return toSourceProperty(node, d, raw, c.cfg.SiteURL, sp.Property.ListingType), nil
}

// sourceProperty builds the search-only listing; detail enrichment is a
// separate, per-listing EnrichDetail call so the crawl can skip it for
// unchanged listings.
func (c *Client) sourceProperty(_ context.Context, node searchListing, rawNode json.RawMessage, listingType domain.ListingType) (ingest.SourceProperty, error) {
	raw, err := json.Marshal(rawProvenance{SearchNode: rawNode})
	if err != nil {
		return ingest.SourceProperty{}, fmt.Errorf("streeteasy encode provenance %s: %w", node.ID, err)
	}
	return toSourceProperty(node, nil, raw, c.cfg.SiteURL, listingType), nil
}

func (c *Client) salesPage(ctx context.Context, areas []int, q ingest.SearchQuery, band priceBand, page int) (*searchOutput, error) {
	resp, err := c.searchPage(ctx, salesQuery, saleFiltersInput{
		Areas: areas, SaleStatus: "ACTIVE", Price: band.bounds(), Bedrooms: bedsBound(q),
	}, page)
	if err != nil {
		return nil, err
	}
	if resp.Data.SearchSales == nil {
		return nil, errors.New("response missing searchSales")
	}
	return resp.Data.SearchSales, nil
}

func (c *Client) rentalsPage(ctx context.Context, areas []int, q ingest.SearchQuery, band priceBand, page int) (*searchOutput, error) {
	resp, err := c.searchPage(ctx, rentalsQuery, rentalFiltersInput{
		Areas: areas, RentalStatus: "ACTIVE", Price: band.bounds(), Bedrooms: bedsBound(q),
	}, page)
	if err != nil {
		return nil, err
	}
	if resp.Data.SearchRentals == nil {
		return nil, errors.New("response missing searchRentals")
	}
	return resp.Data.SearchRentals, nil
}

func bedsBound(q ingest.SearchQuery) *boundsInput {
	if q.MinBeds > 0 {
		return &boundsInput{LowerBound: ptr.To(q.MinBeds)}
	}
	return nil
}

func (c *Client) searchPage(ctx context.Context, query string, filters any, page int) (*graphQLResponse, error) {
	body, err := json.Marshal(graphQLRequest{
		Query: query,
		Variables: searchVarsDTO{Input: searchInput{
			Filters:    filters,
			Page:       page,
			PerPage:    c.cfg.PerPage,
			Sorting:    sortingInput{Attribute: "RECOMMENDED", Direction: "DESCENDING"},
			AdStrategy: "NONE",
		}},
	})
	if err != nil {
		return nil, fmt.Errorf("encode request: %w", err)
	}

	raw, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.cfg.APIURL, bytes.NewReader(body))
		if err != nil {
			return nil, err
		}
		req.Header.Set("accept", "application/json")
		req.Header.Set("content-type", "application/json")
		req.Header.Set("origin", "https://streeteasy.com")
		req.Header.Set("os", "web")
		req.Header.Set("referer", "https://streeteasy.com/")
		req.Header.Set("user-agent", c.cfg.UserAgent)
		req.Header.Set("x-forwarded-proto", "https")
		return req, nil
	})
	if err != nil {
		return nil, err
	}

	var resp graphQLResponse
	if err := json.Unmarshal(raw, &resp); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	if len(resp.Errors) > 0 {
		msgs := make([]string, 0, len(resp.Errors))
		for _, e := range resp.Errors {
			msgs = append(msgs, e.Message)
		}
		return nil, &GraphQLError{Messages: msgs}
	}
	return &resp, nil
}

func (c *Client) do(ctx context.Context, build func() (*http.Request, error)) ([]byte, error) {
	var lastErr error
	for attempt := 1; attempt <= c.cfg.MaxAttempts; attempt++ {
		if err := c.wait(ctx); err != nil {
			return nil, err
		}
		req, err := build()
		if err != nil {
			return nil, fmt.Errorf("build request: %w", err)
		}
		body, err := c.roundTrip(req)
		if err == nil {
			return body, nil
		}
		lastErr = err
		if !isRetryable(ctx, err) || attempt == c.cfg.MaxAttempts {
			break
		}
		if err := sleepCtx(ctx, c.cfg.RetryBackoff<<(attempt-1)); err != nil {
			return nil, err
		}
	}
	return nil, lastErr
}

func (c *Client) roundTrip(req *http.Request) ([]byte, error) {
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request %s: %w", req.URL.Path, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodySize))
		return nil, &HTTPError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(snippet))}
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPageBodySize))
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}
	return body, nil
}

func (c *Client) wait(ctx context.Context) error {
	c.mu.Lock()
	d := time.Until(c.nextAllowed)
	c.nextAllowed = time.Now().Add(c.cfg.Delay)
	if d > 0 {
		c.nextAllowed = c.nextAllowed.Add(d)
	}
	c.mu.Unlock()
	if d <= 0 {
		return nil
	}
	return sleepCtx(ctx, d)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// isRetryable treats every transport failure (including the per-request
// http.Client timeout, which also reports DeadlineExceeded) as retryable;
// only a dead crawl context or a non-retryable HTTP status ends the attempts.
func isRetryable(ctx context.Context, err error) bool {
	if ctx.Err() != nil || errors.Is(err, ErrProxiesBenched) {
		return false
	}
	if he, ok := errors.AsType[*HTTPError](err); ok {
		return he.retryable()
	}
	return true
}

func parseAreas(areas []string) ([]int, error) {
	if len(areas) == 0 {
		return nil, ErrNoAreas
	}
	out := make([]int, 0, len(areas))
	for _, a := range areas {
		n, err := strconv.Atoi(strings.TrimSpace(a))
		if err != nil || n <= 0 {
			return nil, fmt.Errorf("%w: %q", ErrInvalidArea, a)
		}
		out = append(out, n)
	}
	return out, nil
}
