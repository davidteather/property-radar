package streeteasy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

const brooklynAreas = "319"

func testConfig(srv *httptest.Server) Config {
	return Config{
		APIURL:  srv.URL + "/graphql",
		SiteURL: srv.URL,
		// Non-zero: withDefaults treats 0 as "use the 1.5s production delay".
		Delay:        time.Microsecond,
		PerPage:      2,
		MaxAttempts:  3,
		RetryBackoff: time.Millisecond,
	}
}

func saleQuery() ingest.SearchQuery {
	return ingest.SearchQuery{ListingType: domain.ListingSale, Areas: []string{brooklynAreas}}
}

func rentQuery() ingest.SearchQuery {
	return ingest.SearchQuery{ListingType: domain.ListingRent, Areas: []string{brooklynAreas}}
}

// stubServer answers /graphql from pages and any other path from detailPage.
type stubServer struct {
	mu          sync.Mutex
	pages       []string
	detailPage  []byte
	detailFail  map[string]int
	apiCalls    int
	detailCalls int
	lastBody    map[string]any
}

func (s *stubServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/graphql" {
			s.mu.Lock()
			body, _ := io.ReadAll(r.Body)
			var parsed map[string]any
			_ = json.Unmarshal(body, &parsed)
			s.lastBody = parsed
			idx := s.apiCalls
			s.apiCalls++
			s.mu.Unlock()
			if idx >= len(s.pages) {
				http.Error(w, "no more pages", http.StatusInternalServerError)
				return
			}
			w.Header().Set("content-type", "application/json")
			_, _ = io.WriteString(w, s.pages[idx])
			return
		}

		s.mu.Lock()
		s.detailCalls++
		status, fail := s.detailFail[r.URL.Path]
		s.mu.Unlock()
		if fail {
			http.Error(w, "blocked", status)
			return
		}
		w.Header().Set("content-type", "text/html")
		_, _ = w.Write(s.detailPage)
	})
}

func searchPageJSON(hasNext bool, nodes ...string) string {
	return pageJSON("searchSales", "OrganicSaleEdge", hasNext, nodes...)
}

func rentalPageJSON(hasNext bool, nodes ...string) string {
	return pageJSON("searchRentals", "OrganicRentalEdge", hasNext, nodes...)
}

func pageJSON(root, edgeType string, hasNext bool, nodes ...string) string {
	edges := make([]string, 0, len(nodes))
	for _, n := range nodes {
		edges = append(edges, fmt.Sprintf(`{"__typename":%q,"node":%s}`, edgeType, n))
	}
	return fmt.Sprintf(
		`{"data":{%q:{"search":{"criteria":"area:319|status:open"},"totalCount":9,"pageInfo":{"currentPage":1,"hasNextPage":%t,"hasPreviousPage":false,"totalPages":5},"edges":[%s]}}}`,
		root, hasNext, strings.Join(edges, ","))
}

func node(id, path, buildingType string) string {
	return fmt.Sprintf(
		`{"id":%q,"areaName":"Park Slope","street":"1 Main St","unit":"2A","displayUnit":"#2A","zipCode":"11215","state":"NY","urlPath":%q,"status":"ACTIVE","buildingType":%q,"price":900000,"monthlyMaintenance":1200,"monthlyTaxes":0,"monthlyCommonCharges":null,"bedroomCount":2,"fullBathroomCount":1,"halfBathroomCount":1,"livingAreaSize":950,"geoPoint":{"latitude":40.67,"longitude":-73.98},"photos":[{"key":"abc"}]}`,
		id, path, buildingType)
}

func rentalNode(id, path, status string) string {
	return fmt.Sprintf(
		`{"id":%q,"areaName":"Park Slope","street":"1 Main St","unit":"2A","displayUnit":"#2A","zipCode":"11215","state":"NY","urlPath":%q,"status":%q,"buildingType":"RENTAL","price":4200,"totalMonthlyPrice":null,"noFee":true,"leaseTermMonths":12,"availableAt":"2026-09-01","bedroomCount":2,"fullBathroomCount":1,"halfBathroomCount":0,"livingAreaSize":0,"geoPoint":{"latitude":40.67,"longitude":-73.98},"photos":[{"key":"abc"}],"leadMedia":{"photo":{"key":"abc"}}}`,
		id, path, status)
}

func collect(t *testing.T, seq func(func(ingest.SourceProperty, error) bool)) ([]ingest.SourceProperty, []error) {
	t.Helper()
	var props []ingest.SourceProperty
	var errs []error
	for sp, err := range seq {
		props = append(props, sp)
		errs = append(errs, err)
	}
	return props, errs
}

func TestSearchPaginates(t *testing.T) {
	stub := &stubServer{
		pages: []string{
			searchPageJSON(true, node("1", "/a", "CO_OP"), node("2", "/b", "CONDO")),
			searchPageJSON(false, node("3", "/c", "TOWNHOUSE")),
		},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 3 {
		t.Fatalf("got %d properties, want 3", len(props))
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("item %d error: %v", i, err)
		}
	}
	if stub.apiCalls != 2 {
		t.Errorf("api calls = %d, want 2", stub.apiCalls)
	}
	if props[0].ProviderID != "1" || props[2].ProviderID != "3" {
		t.Errorf("ids = %q %q %q", props[0].ProviderID, props[1].ProviderID, props[2].ProviderID)
	}
	if props[2].Property.PropertyType != domain.PropertyTownhouse {
		t.Errorf("page-2 conversion wrong: %+v", props[2].Property)
	}
	if props[0].URL != srv.URL+"/a" {
		t.Errorf("url = %q", props[0].URL)
	}
}

func TestSearchSendsExpectedRequest(t *testing.T) {
	stub := &stubServer{pages: []string{searchPageJSON(false, node("1", "/a", "CONDO"))}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	q := saleQuery()
	q.Areas = []string{"319", "305"}
	q.MaxPrice = domain.Money(2000000)
	q.MinBeds = 2
	for range c.Search(t.Context(), q) {
	}

	input := stub.lastBody["variables"].(map[string]any)["input"].(map[string]any)
	filters := input["filters"].(map[string]any)
	if got := filters["saleStatus"]; got != "ACTIVE" {
		t.Errorf("saleStatus = %v", got)
	}
	areas := filters["areas"].([]any)
	if len(areas) != 2 || areas[0].(float64) != 319 || areas[1].(float64) != 305 {
		t.Errorf("areas = %v", areas)
	}
	if got := filters["price"].(map[string]any)["upperBound"].(float64); got != 2000000 {
		t.Errorf("price upperBound = %v", got)
	}
	if got := filters["bedrooms"].(map[string]any)["lowerBound"].(float64); got != 2 {
		t.Errorf("bedrooms lowerBound = %v", got)
	}
	if got := input["adStrategy"]; got != "NONE" {
		t.Errorf("adStrategy = %v", got)
	}
	if got := input["perPage"].(float64); got != 2 {
		t.Errorf("perPage = %v", got)
	}
	sorting := input["sorting"].(map[string]any)
	if sorting["attribute"] != "RECOMMENDED" || sorting["direction"] != "DESCENDING" {
		t.Errorf("sorting = %v", sorting)
	}
	if _, ok := input["userSearchToken"]; ok {
		t.Error("userSearchToken should be omitted when unset")
	}
}

func TestEnrichDetailMergesTheListingPage(t *testing.T) {
	stub := &stubServer{
		pages:      []string{searchPageJSON(false, node("1777453", "/building/185-hall-street-brooklyn/614", "CO_OP"))},
		detailPage: readFixture(t, "detail_page_coop_1777453.html"),
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := NewClient(srv.Client(), testConfig(srv))
	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 1 || errs[0] != nil {
		t.Fatalf("props=%d err=%v", len(props), errs[0])
	}
	// Search is search-only by design: no detail request, no description yet.
	if stub.detailCalls != 0 {
		t.Errorf("detail calls during search = %d, want 0", stub.detailCalls)
	}
	if props[0].Property.Description != "" {
		t.Errorf("search-only description = %q, want empty", props[0].Property.Description)
	}

	enriched, err := c.EnrichDetail(t.Context(), props[0])
	if err != nil {
		t.Fatalf("enrich: %v", err)
	}
	if stub.detailCalls != 1 {
		t.Errorf("detail calls = %d, want 1", stub.detailCalls)
	}
	p := enriched.Property
	if !strings.HasPrefix(p.Description, "Sunny one-bedroom co-op") {
		t.Errorf("description = %q", p.Description)
	}
	assertInt(t, "daysOnMarket", p.DaysOnMarket, 426)

	var raw rawProvenance
	if err := json.Unmarshal(enriched.Raw, &raw); err != nil {
		t.Fatalf("raw: %v", err)
	}
	if len(raw.SearchNode) == 0 || len(raw.DetailListing) == 0 {
		t.Error("raw provenance should carry both payloads")
	}
	if !json.Valid(raw.SearchNode) || !json.Valid(raw.DetailListing) {
		t.Error("raw provenance members must be valid json")
	}
}

func TestEnrichDetailFailureLeavesTheSearchListingUsable(t *testing.T) {
	stub := &stubServer{
		pages: []string{searchPageJSON(false,
			node("1", "/blocked", "CO_OP"),
			node("1777453", "/ok", "CONDO"))},
		detailPage: readFixture(t, "detail_page_coop_1777453.html"),
		detailFail: map[string]int{"/blocked": http.StatusNotFound},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := NewClient(srv.Client(), testConfig(srv))
	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 2 || errs[0] != nil || errs[1] != nil {
		t.Fatalf("search props=%d errs=%v", len(props), errs)
	}

	blocked, err := c.EnrichDetail(t.Context(), props[0])
	if err == nil {
		t.Fatal("expected an error for the blocked detail page")
	}
	// The caller keeps the search-derived listing on failure.
	if blocked.ProviderID != "1" || blocked.Property.Price == nil {
		t.Error("failed enrichment must return the search-derived property")
	}
	if blocked.Property.Description != "" {
		t.Error("failed enrichment should leave description empty")
	}

	ok, err := c.EnrichDetail(t.Context(), props[1])
	if err != nil {
		t.Fatalf("second item should succeed: %v", err)
	}
	if ok.Property.Description == "" {
		t.Error("second item should be enriched")
	}
}

func TestEnrichDetailRejectsAPageForAnotherListing(t *testing.T) {
	stub := &stubServer{
		pages:      []string{searchPageJSON(false, node("2", "/ok", "CONDO"))},
		detailPage: readFixture(t, "detail_page_coop_1777453.html"),
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	c := NewClient(srv.Client(), testConfig(srv))
	props, _ := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 1 {
		t.Fatalf("props = %d", len(props))
	}
	// The unit page now serves a different (relisted) listing: keep the search
	// fields, do not merge the other listing's detail.
	sp, err := c.EnrichDetail(t.Context(), props[0])
	if err == nil || !strings.Contains(err.Error(), `page shows listing "1777453"`) {
		t.Fatalf("err = %v", err)
	}
	if sp.ProviderID != "2" || sp.Property.Description != "" {
		t.Errorf("mismatched page must not be merged: %+v", sp.Property)
	}
}

func TestSearchWrongTypedFieldKeepsTheListingSeen(t *testing.T) {
	stub := &stubServer{
		pages: []string{fmt.Sprintf(
			`{"data":{"searchSales":{"totalCount":2,"pageInfo":{"hasNextPage":false},"edges":[`+
				`{"__typename":"OrganicSaleEdge","node":{"id":"7","photos":"not-a-list"}},`+
				`{"__typename":"OrganicSaleEdge","node":%s}]}}}`, node("9", "/x", "CONDO"))},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 2 {
		t.Fatalf("got %d yields, want 2", len(props))
	}
	// The id decoded, so the listing is reported as present-but-unusable rather
	// than vanishing from the run and being aged toward delisting.
	if props[0].ProviderID != "7" || !errors.Is(errs[0], ingest.ErrUnusableListing) {
		t.Errorf("wrong-typed node: props=%+v err=%v", props[0], errs[0])
	}
	if errs[1] != nil || props[1].ProviderID != "9" {
		t.Errorf("good node not yielded: %v %+v", errs[1], props[1])
	}
}

func TestSearchPageCapIsAnIncompleteRun(t *testing.T) {
	pages := make([]string, 0, maxPages+1)
	for i := range maxPages + 1 {
		pages = append(pages, searchPageJSON(true, node(fmt.Sprint(i), "/p", "CONDO")))
	}
	stub := &stubServer{pages: pages}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != maxPages+1 || stub.apiCalls != maxPages {
		t.Fatalf("yields=%d apiCalls=%d", len(props), stub.apiCalls)
	}
	// The trailing provider-level error stops the run from counting as complete.
	last := errs[len(errs)-1]
	if last == nil || props[len(props)-1].ProviderID != "" || !strings.Contains(last.Error(), "more than 50 pages") {
		t.Errorf("last yield = %+v %v", props[len(props)-1], last)
	}
	if !errors.Is(last, ingest.ErrSearchAborted) {
		t.Errorf("page cap error = %v, want it to wrap ErrSearchAborted", last)
	}
}

// An empty page that still claims hasNextPage is not the end of the results;
// it aborts the run so nothing unseen is aged toward delisting.
func TestSearchEmptyPageWithMoreClaimedAbortsTheRun(t *testing.T) {
	stub := &stubServer{pages: []string{
		searchPageJSON(true, node("1", "/a", "CONDO")),
		searchPageJSON(true),
		searchPageJSON(false, node("3", "/c", "CONDO")),
	}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 2 || stub.apiCalls != 2 {
		t.Fatalf("yields=%d apiCalls=%d, want the listing plus one abort after two pages", len(props), stub.apiCalls)
	}
	last := errs[len(errs)-1]
	if !errors.Is(last, ingest.ErrSearchAborted) || !strings.Contains(last.Error(), "empty page") {
		t.Fatalf("last yield = %v, want ErrSearchAborted naming the empty page", last)
	}
}

func TestSearchMalformedListingIsItemScoped(t *testing.T) {
	stub := &stubServer{
		pages: []string{fmt.Sprintf(
			`{"data":{"searchSales":{"totalCount":3,"pageInfo":{"hasNextPage":false},"edges":[`+
				`{"__typename":"OrganicSaleEdge","node":{"id":123}},`+
				`{"__typename":"OrganicSaleEdge","node":{"id":""}},`+
				`{"__typename":"SponsoredSaleEdge","node":null},`+
				`{"__typename":"OrganicSaleEdge","node":%s}]}}}`, node("9", "/x", "CONDO"))},
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 3 {
		t.Fatalf("got %d yields, want 3 (2 item errors + 1 good)", len(props))
	}
	if errs[0] == nil || errs[1] == nil {
		t.Errorf("malformed nodes should yield errors: %v %v", errs[0], errs[1])
	}
	// Item-scoped: the crawl must not treat a bad edge as a provider failure.
	if errors.Is(errs[0], ingest.ErrSearchAborted) || errors.Is(errs[1], ingest.ErrSearchAborted) {
		t.Errorf("malformed nodes must not abort the search: %v %v", errs[0], errs[1])
	}
	if errs[2] != nil || props[2].ProviderID != "9" {
		t.Errorf("good node not yielded: %v %+v", errs[2], props[2])
	}
}

func TestSearchDeduplicatesRepeatedIDs(t *testing.T) {
	stub := &stubServer{pages: []string{
		searchPageJSON(true, node("1", "/a", "CONDO")),
		searchPageJSON(false, node("1", "/a", "CONDO"), node("2", "/b", "CONDO")),
	}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, _ := collect(t, c.Search(t.Context(), saleQuery()))
	if len(props) != 2 {
		t.Fatalf("got %d, want 2 after dedupe", len(props))
	}
}

func TestSearchProviderFailureTerminatesIterator(t *testing.T) {
	t.Run("page error", func(t *testing.T) {
		stub := &stubServer{pages: []string{
			searchPageJSON(true, node("1", "/a", "CONDO")),
			// second call falls off the end of pages and 500s on every retry
		}}
		srv := httptest.NewServer(stub.handler())
		defer srv.Close()

		cfg := testConfig(srv)
		cfg.DisableDetail = true
		c := NewClient(srv.Client(), cfg)

		props, errs := collect(t, c.Search(t.Context(), saleQuery()))
		if len(props) != 2 {
			t.Fatalf("got %d yields, want 1 property + 1 terminal error", len(props))
		}
		if errs[0] != nil {
			t.Errorf("first item should succeed: %v", errs[0])
		}
		last := errs[1]
		if last == nil {
			t.Fatal("expected terminal provider error")
		}
		if !errors.Is(last, ingest.ErrSearchAborted) {
			t.Errorf("page error = %v, want it to wrap ErrSearchAborted", last)
		}
		if props[1].ProviderID != "" {
			t.Error("provider-level error must be paired with a zero SourceProperty")
		}
		var he *HTTPError
		if !errors.As(last, &he) || he.StatusCode != http.StatusInternalServerError {
			t.Errorf("err = %v, want HTTPError 500", last)
		}
	})

	t.Run("graphql error", func(t *testing.T) {
		stub := &stubServer{pages: []string{
			`{"errors":[{"message":"Cannot query field \"nope\" on type \"SearchSaleListing\"."}]}`,
		}}
		srv := httptest.NewServer(stub.handler())
		defer srv.Close()

		c := NewClient(srv.Client(), testConfig(srv))
		props, errs := collect(t, c.Search(t.Context(), saleQuery()))
		if len(props) != 1 || errs[0] == nil {
			t.Fatalf("expected a single terminal error, got %d yields", len(props))
		}
		if _, ok := errors.AsType[*GraphQLError](errs[0]); !ok {
			t.Fatalf("err = %v, want GraphQLError", errs[0])
		}
		if stub.apiCalls != 1 {
			t.Errorf("graphql errors must not be retried, got %d calls", stub.apiCalls)
		}
	})
}

func TestSearchRejectsBadQuery(t *testing.T) {
	c := NewClient(nil, Config{})

	_, errs := collect(t, c.Search(t.Context(), ingest.SearchQuery{ListingType: "timeshare", Areas: []string{"319"}}))
	if len(errs) != 1 || !errors.Is(errs[0], ErrUnsupportedListingType) || !errors.Is(errs[0], ingest.ErrSearchAborted) {
		t.Errorf("unknown listing type err = %v", errs)
	}

	_, errs = collect(t, c.Search(t.Context(), ingest.SearchQuery{ListingType: domain.ListingSale}))
	if len(errs) != 1 || !errors.Is(errs[0], ErrNoAreas) {
		t.Errorf("no-areas err = %v", errs)
	}

	_, errs = collect(t, c.Search(t.Context(), ingest.SearchQuery{ListingType: domain.ListingSale, Areas: []string{"park-slope"}}))
	if len(errs) != 1 || !errors.Is(errs[0], ErrInvalidArea) {
		t.Errorf("bad-area err = %v", errs)
	}
}

func TestSearchStopsWhenConsumerBreaks(t *testing.T) {
	stub := &stubServer{pages: []string{
		searchPageJSON(true, node("1", "/a", "CONDO"), node("2", "/b", "CONDO")),
		searchPageJSON(false, node("3", "/c", "CONDO")),
	}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	count := 0
	for range c.Search(t.Context(), saleQuery()) {
		count++
		break
	}
	if count != 1 {
		t.Fatalf("yielded %d after break", count)
	}
	if stub.apiCalls != 1 {
		t.Errorf("api calls = %d; breaking must not fetch further pages", stub.apiCalls)
	}
}

type flakyTransport struct {
	mu       sync.Mutex
	attempts int
	failWith int
	failures int
	body     string
}

func (f *flakyTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.attempts++
	if f.attempts <= f.failures {
		return &http.Response{
			StatusCode: f.failWith,
			Body:       io.NopCloser(strings.NewReader("rate limited")),
			Header:     make(http.Header),
			Request:    r,
		}, nil
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(f.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    r,
	}, nil
}

func TestRetriesTransientFailures(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusForbidden, http.StatusBadGateway} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			ft := &flakyTransport{failWith: status, failures: 2, body: searchPageJSON(false, node("1", "/a", "CONDO"))}
			c := NewClient(&http.Client{Transport: ft}, Config{
				APIURL: "https://example.invalid/", SiteURL: "https://example.invalid",
				Delay: time.Microsecond, MaxAttempts: 3, RetryBackoff: time.Millisecond,
				DisableDetail: true,
			})
			props, errs := collect(t, c.Search(t.Context(), saleQuery()))
			if len(props) != 1 || errs[0] != nil {
				t.Fatalf("props=%d err=%v", len(props), errs[0])
			}
			if ft.attempts != 3 {
				t.Errorf("attempts = %d, want 3", ft.attempts)
			}
		})
	}
}

func TestGivesUpAfterMaxAttempts(t *testing.T) {
	ft := &flakyTransport{failWith: http.StatusServiceUnavailable, failures: 99}
	c := NewClient(&http.Client{Transport: ft}, Config{
		APIURL: "https://example.invalid/", SiteURL: "https://example.invalid",
		Delay: time.Microsecond, MaxAttempts: 2, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})
	_, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(errs) != 1 || errs[0] == nil {
		t.Fatalf("expected one terminal error, got %v", errs)
	}
	if ft.attempts != 2 {
		t.Errorf("attempts = %d, want 2", ft.attempts)
	}
}

func TestDoesNotRetryBadRequest(t *testing.T) {
	ft := &flakyTransport{failWith: http.StatusBadRequest, failures: 99}
	c := NewClient(&http.Client{Transport: ft}, Config{
		APIURL: "https://example.invalid/", SiteURL: "https://example.invalid",
		Delay: time.Microsecond, MaxAttempts: 3, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})
	collect(t, c.Search(t.Context(), saleQuery()))
	if ft.attempts != 1 {
		t.Errorf("attempts = %d, want 1 (400 is not transient)", ft.attempts)
	}
}

type timestampTransport struct {
	pages []string
	at    []time.Time
}

func (tr *timestampTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.at = append(tr.at, time.Now())
	i := len(tr.at) - 1
	if i >= len(tr.pages) {
		return nil, errors.New("unexpected extra request")
	}
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(tr.pages[i])),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    r,
	}, nil
}

func TestPolitenessDelayBetweenRequests(t *testing.T) {
	const delay = 60 * time.Millisecond
	tr := &timestampTransport{pages: []string{
		searchPageJSON(true, node("1", "/a", "CONDO")),
		searchPageJSON(false, node("2", "/b", "CONDO")),
	}}
	c := NewClient(&http.Client{Transport: tr}, Config{
		APIURL: "https://example.invalid/", SiteURL: "https://example.invalid",
		Delay: delay, MaxAttempts: 3, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})

	start := time.Now()
	props, _ := collect(t, c.Search(t.Context(), saleQuery()))
	elapsed := time.Since(start)

	if len(props) != 2 {
		t.Fatalf("got %d properties", len(props))
	}
	if len(tr.at) != 2 {
		t.Fatalf("requests = %d", len(tr.at))
	}
	// The second request cannot start before start+delay, so total elapsed is a
	// hard lower bound; the per-request gap is checked with slack for scheduler jitter.
	if elapsed < delay {
		t.Errorf("elapsed = %v, want >= %v", elapsed, delay)
	}
	if gap := tr.at[1].Sub(tr.at[0]); gap < delay*9/10 {
		t.Errorf("gap between requests = %v, want ~>= %v", gap, delay)
	}
}

func TestDefaultConfig(t *testing.T) {
	c := NewClient(nil, Config{SiteURL: "https://example.test/"})
	if c.cfg.APIURL != defaultAPIURL {
		t.Errorf("apiURL = %q", c.cfg.APIURL)
	}
	if c.cfg.SiteURL != "https://example.test" {
		t.Errorf("siteURL should have its trailing slash trimmed, got %q", c.cfg.SiteURL)
	}
	if c.cfg.Delay != defaultDelay || c.cfg.PerPage != defaultPerPage || c.cfg.MaxAttempts != defaultMaxAttempts {
		t.Errorf("defaults not applied: %+v", c.cfg)
	}
	if c.http == nil {
		t.Error("nil http client should fall back to http.DefaultClient")
	}
	if c.Name() != "streeteasy" {
		t.Errorf("Name() = %q", c.Name())
	}
}

// cancelAfterFirstTransport serves one page then cancels, so the politeness wait
// before page 2 is interrupted without depending on wall-clock timing.
type cancelAfterFirstTransport struct {
	body   string
	cancel context.CancelFunc
	calls  int
}

func (tr *cancelAfterFirstTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	tr.calls++
	if tr.calls > 1 {
		return nil, errors.New("transport should not be reached after cancellation")
	}
	tr.cancel()
	return &http.Response{
		StatusCode: http.StatusOK,
		Body:       io.NopCloser(strings.NewReader(tr.body)),
		Header:     http.Header{"Content-Type": []string{"application/json"}},
		Request:    r,
	}, nil
}

func TestSearchHonoursContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	tr := &cancelAfterFirstTransport{
		body:   searchPageJSON(true, node("1", "/a", "CONDO")),
		cancel: cancel,
	}
	c := NewClient(&http.Client{Transport: tr}, Config{
		APIURL: "https://example.invalid/", SiteURL: "https://example.invalid",
		Delay: time.Hour, MaxAttempts: 3, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})

	props, errs := collect(t, c.Search(ctx, saleQuery()))
	if len(props) != 2 {
		t.Fatalf("got %d yields, want 1 property + 1 terminal error", len(props))
	}
	if errs[0] != nil || props[0].ProviderID != "1" {
		t.Errorf("first page should have been yielded: %v %+v", errs[0], props[0])
	}
	if !errors.Is(errs[1], context.Canceled) {
		t.Errorf("terminal err = %v, want context.Canceled", errs[1])
	}
	if props[1].ProviderID != "" {
		t.Error("provider-level error must be paired with a zero SourceProperty")
	}
	if tr.calls != 1 {
		t.Errorf("transport calls = %d, want 1", tr.calls)
	}
}

// A per-request http.Client timeout reads as DeadlineExceeded too, but it is a
// transport failure: every attempt is used and the run fails rather than
// looking like a cancelled crawl.
func TestClientTimeoutIsRetriedAndFailsTheRun(t *testing.T) {
	var attempts atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		select {
		case <-time.After(200 * time.Millisecond):
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()
	c := NewClient(&http.Client{Timeout: 20 * time.Millisecond}, Config{
		APIURL: srv.URL + "/", SiteURL: srv.URL,
		Delay: time.Microsecond, MaxAttempts: 3, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})

	_, errs := collect(t, c.Search(t.Context(), saleQuery()))
	if len(errs) != 1 || errs[0] == nil || !errors.Is(errs[0], ingest.ErrSearchAborted) {
		t.Fatalf("expected one terminal provider error, got %v", errs)
	}
	if got := attempts.Load(); got != 3 {
		t.Errorf("attempts = %d, want 3 (client timeouts are retryable)", got)
	}
	if errors.Is(errs[0], context.Canceled) {
		t.Errorf("provider timeout surfaced as a cancelled crawl: %v", errs[0])
	}
}

// A cancelled crawl context, by contrast, ends the attempts at once.
func TestCancelledContextIsNotRetried(t *testing.T) {
	var attempts atomic.Int32
	ctx, cancel := context.WithCancel(t.Context())
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		cancel()
		<-release
	}))
	defer srv.Close()
	c := NewClient(srv.Client(), Config{
		APIURL: srv.URL + "/", SiteURL: srv.URL,
		Delay: time.Microsecond, MaxAttempts: 3, RetryBackoff: time.Millisecond,
		DisableDetail: true,
	})

	collect(t, c.Search(ctx, saleQuery()))
	close(release)
	if got := attempts.Load(); got != 1 {
		t.Errorf("attempts = %d, want 1 (a dead crawl context is never retried)", got)
	}
}

// cappedServer mimics the provider's result cap: a query answers at most
// resultCap rows across its pages, however many exist, filtered by price bounds.
type cappedServer struct {
	prices    map[string]int
	perPage   int
	resultCap int
	mu        sync.Mutex
	bounds    []string
}

func (s *cappedServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		var req struct {
			Variables struct {
				Input struct {
					Page    int `json:"page"`
					Filters struct {
						Price *struct{ LowerBound, UpperBound *int } `json:"price"`
					} `json:"filters"`
				} `json:"input"`
			} `json:"variables"`
		}
		_ = json.Unmarshal(body, &req)
		in := req.Variables.Input
		lo, hi := 0, int(^uint(0)>>1)
		if in.Filters.Price != nil {
			if in.Filters.Price.LowerBound != nil {
				lo = *in.Filters.Price.LowerBound
			}
			if in.Filters.Price.UpperBound != nil {
				hi = *in.Filters.Price.UpperBound
			}
		}
		s.mu.Lock()
		s.bounds = append(s.bounds, fmt.Sprintf("%d..%d/%d", lo, hi, in.Page))
		s.mu.Unlock()

		var ids []string
		for id, p := range s.prices {
			if p >= lo && p <= hi {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		total := len(ids)
		pages := (min(total, s.resultCap) + s.perPage - 1) / s.perPage
		start := min((in.Page-1)*s.perPage, len(ids))
		end := min(start+s.perPage, min(total, s.resultCap))
		var edges []string
		for _, id := range ids[start:end] {
			edges = append(edges, fmt.Sprintf(`{"__typename":"OrganicSaleEdge","node":%s}`,
				strings.Replace(node(id, "/"+id, "CONDO"), `"price":900000`, fmt.Sprintf(`"price":%d`, s.prices[id]), 1)))
		}
		w.Header().Set("content-type", "application/json")
		_, _ = fmt.Fprintf(w, `{"data":{"searchSales":{"search":{"criteria":""},"totalCount":%d,"pageInfo":{"currentPage":%d,"hasNextPage":%t,"hasPreviousPage":false,"totalPages":%d},"edges":[%s]}}}`,
			total, in.Page, in.Page < pages, pages, strings.Join(edges, ","))
	})
}

// A scope the provider caps is walked in price bands until every band fits,
// so a broad standing scope sees every listing instead of the first thousand.
func TestSearchSplitsACappedScopeByPrice(t *testing.T) {
	stub := &cappedServer{prices: map[string]int{}, perPage: 2, resultCap: 4}
	for i := range 23 {
		stub.prices[fmt.Sprintf("l%02d", i)] = 100000 * (i + 1)
	}
	stub.prices["l06"] = 700000 // two listings at one price straddle a pivot
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	for i, err := range errs {
		if err != nil {
			t.Errorf("item %d: %v", i, err)
		}
	}
	got := map[string]bool{}
	for _, p := range props {
		got[p.ProviderID] = true
	}
	if len(props) != len(stub.prices) || len(got) != len(stub.prices) {
		t.Fatalf("yielded %d (%d distinct), want all %d listings once; requests: %v", len(props), len(got), len(stub.prices), stub.bounds)
	}
	if stub.bounds[0] != "0..9223372036854775807/1" {
		t.Errorf("first request should be unbounded, got %s", stub.bounds[0])
	}
}

func TestSearchSplitStaysUnderTheQueryCeiling(t *testing.T) {
	stub := &cappedServer{prices: map[string]int{}, perPage: 2, resultCap: 4}
	for i := range 12 {
		stub.prices[fmt.Sprintf("l%02d", i)] = 100000 * (i + 1)
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	q := saleQuery()
	q.MaxPrice = 800000
	props, _ := collect(t, c.Search(t.Context(), q))
	if len(props) != 8 {
		t.Fatalf("yielded %d, want the 8 listings at or under the ceiling; requests: %v", len(props), stub.bounds)
	}
	for _, b := range stub.bounds {
		var lo, hi, page int
		if _, err := fmt.Sscanf(b, "%d..%d/%d", &lo, &hi, &page); err != nil || hi > 800000 {
			t.Errorf("request %s escaped the ceiling", b)
		}
	}
}

// Listings that share one price cannot be split apart; the run aborts rather
// than counting itself complete on a truncated sweep.
func TestSearchAbortsWhenOnePriceExceedsTheCap(t *testing.T) {
	stub := &cappedServer{prices: map[string]int{}, perPage: 2, resultCap: 4}
	for i := range 6 {
		stub.prices[fmt.Sprintf("l%02d", i)] = 500000
	}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), saleQuery()))
	last := errs[len(errs)-1]
	if !errors.Is(last, ingest.ErrSearchAborted) || !strings.Contains(last.Error(), "result cap") {
		t.Fatalf("last yield = %v, want ErrSearchAborted naming the result cap", last)
	}
	for i, p := range props[:len(props)-1] {
		if p.ProviderID == "" || errs[i] != nil {
			t.Errorf("item %d = %+v %v", i, p, errs[i])
		}
	}
}
