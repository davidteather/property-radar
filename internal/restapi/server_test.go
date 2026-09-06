package restapi_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/mock"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/listings/mocks"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/restapi"
)

const token = "test-token-6f1c9d"

// fixture wires a Service over fresh mocks behind an httptest server.
type fixture struct {
	store  *mocks.MockStore
	photos *mocks.MockPhotoStore
	url    string
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	st := mocks.NewMockStore(t)
	ph := mocks.NewMockPhotoStore(t)
	svc := listings.NewService(st, ph)
	srv := httptest.NewServer(restapi.NewHandler(svc, token, "", ""))
	t.Cleanup(srv.Close)
	return &fixture{store: st, photos: ph, url: srv.URL}
}

func do(t *testing.T, method, url, auth string, body any) *http.Response {
	t.Helper()
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal body: %v", err)
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, url, r)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if auth != "" {
		req.Header.Set("Authorization", auth)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("%s %s: %v", method, url, err)
	}
	return resp
}

func decode(t *testing.T, resp *http.Response) map[string]any {
	t.Helper()
	defer func() { _ = resp.Body.Close() }()
	var out map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return out
}

func bearer() string { return "Bearer " + token }

// ---- auth gate ----

func TestBearerGate(t *testing.T) {
	f := newFixture(t)

	// No token: 401 before the handler runs (no store call expected).
	resp := do(t, http.MethodGet, f.url+"/v1/state", "", nil)
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}
	if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
		t.Fatalf("WWW-Authenticate = %q", got)
	}
	_ = resp.Body.Close()

	// Correct token: reaches the Service.
	f.store.EXPECT().State(mock.Anything).Return(domain.Profile{ListingType: domain.ListingSale}, nil, false, nil, nil)
	resp = do(t, http.MethodGet, f.url+"/v1/state", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("with token: status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if _, ok := body["profile"]; !ok {
		t.Fatalf("state body missing profile: %v", body)
	}
}

func TestHealthzOpen(t *testing.T) {
	f := newFixture(t)
	resp := do(t, http.MethodGet, f.url+"/healthz", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	b, _ := io.ReadAll(resp.Body)
	if strings.TrimSpace(string(b)) != "ok" {
		t.Fatalf("healthz body = %q", b)
	}
}

// ---- docs + openapi ----

func TestOpenAPIServedAndDescribesSecurity(t *testing.T) {
	f := newFixture(t)
	resp := do(t, http.MethodGet, f.url+"/openapi.json", "", nil) // unauthenticated
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	spec := decode(t, resp)

	comps, _ := spec["components"].(map[string]any)
	schemes, _ := comps["securitySchemes"].(map[string]any)
	bearerScheme, ok := schemes["bearer"].(map[string]any)
	if !ok {
		t.Fatalf("openapi missing bearer securityScheme: %v", schemes)
	}
	if bearerScheme["type"] != "http" || bearerScheme["scheme"] != "bearer" {
		t.Fatalf("bearer scheme = %v, want http/bearer", bearerScheme)
	}

	paths, _ := spec["paths"].(map[string]any)
	for _, want := range []string{"/v1/state", "/v1/listings", "/v1/listings/{id}", "/v1/listings/{id}/photos/{n}", "/v1/crawl-targets"} {
		if _, ok := paths[want]; !ok {
			t.Fatalf("openapi paths missing %q", want)
		}
	}
	// The contact-sheet op's prose must agree with its 1-based `sheet` parameter.
	sheetPath, _ := paths["/v1/listings/{id}/contact-sheet"].(map[string]any)
	sheetGet, _ := sheetPath["get"].(map[string]any)
	if desc, _ := sheetGet["description"].(string); !strings.Contains(desc, "1-based") || strings.Contains(desc, "zero-based") {
		t.Fatalf("contact-sheet description = %q, want it to say the index is 1-based", desc)
	}
	// The photo op must document image/jpeg.
	photoPath, _ := paths["/v1/listings/{id}/photos/{n}"].(map[string]any)
	get, _ := photoPath["get"].(map[string]any)
	responses, _ := get["responses"].(map[string]any)
	ok200, _ := responses["200"].(map[string]any)
	content, _ := ok200["content"].(map[string]any)
	if _, ok := content["image/jpeg"]; !ok {
		t.Fatalf("photo op 200 content = %v, want image/jpeg", content)
	}
}

func TestDocsServesShell(t *testing.T) {
	f := newFixture(t)

	resp := do(t, http.MethodGet, f.url+"/docs", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("docs status = %d, want 200", resp.StatusCode)
	}
	csp := resp.Header.Get("Content-Security-Policy")
	if !strings.Contains(csp, "cdn.jsdelivr.net") || strings.Contains(csp, "img-src 'self' data: https:") {
		t.Fatalf("docs CSP = %q, want the Scalar CDN allowed and no wildcard img-src (exfil beacon)", csp)
	}
	b, _ := io.ReadAll(resp.Body)
	html := string(b)
	// The bundle runs on the origin the bearer token is pasted into: pinned + SRI.
	for _, want := range []string{`id="api-reference"`, `data-url="/openapi.json"`,
		`cdn.jsdelivr.net/npm/@scalar/api-reference@1.`, `integrity="sha384-`, `crossorigin="anonymous"`} {
		if !strings.Contains(html, want) {
			t.Fatalf("docs shell missing %q:\n%s", want, html)
		}
	}
}

// ---- error mappings ----

func TestGetListingNotFound(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().GetProperty(mock.Anything, domain.PropertyID(999)).
		Return(domain.Property{}, nil, nil, listings.ProvenanceSummary{}, listings.ErrNotFound)

	resp := do(t, http.MethodGet, f.url+"/v1/listings/999", bearer(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestUpdateRubricFutureThroughVerdict(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().AppendRubric(mock.Anything, "taste notes", domain.VerdictID(42)).
		Return(domain.Rubric{}, listings.ErrFutureThroughVerdict)

	resp := do(t, http.MethodPost, f.url+"/v1/rubric", bearer(), map[string]any{
		"content": "taste notes", "through_verdict_id": 42,
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestRequestCrawlQueueFull(t *testing.T) {
	f := newFixture(t)
	full := make([]domain.CrawlTarget, listings.MaxPendingRequests)
	for i := range full {
		full[i] = domain.CrawlTarget{Kind: domain.TargetOnce, Status: domain.TargetPending}
	}
	f.store.EXPECT().ListCrawlTargets(mock.Anything).Return(full, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/crawl-requests", bearer(), map[string]any{
		"areas": []string{"305"}, "listing_type": "sale", "one_off": true,
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429", resp.StatusCode)
	}
}

func TestRequestCrawlDefaultsToStandingScope(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().ListCrawlTargets(mock.Anything).Return(nil, nil)
	f.store.EXPECT().CreateCrawlTarget(mock.Anything, mock.MatchedBy(func(ct domain.CrawlTarget) bool {
		return ct.Kind == domain.TargetStanding && ct.Enabled
	})).Return(domain.CrawlTarget{
		ID: 5, Kind: domain.TargetStanding, Enabled: true,
		Areas: []string{"305"}, ListingType: domain.ListingSale,
	}, nil)

	// No one_off flag: the default is a standing (recurring) scope.
	resp := do(t, http.MethodPost, f.url+"/v1/crawl-requests", bearer(), map[string]any{
		"areas": []string{"305"}, "listing_type": "sale",
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	tgt, _ := body["target"].(map[string]any)
	if tgt["kind"] != "standing" {
		t.Fatalf("target kind = %v, want standing", tgt["kind"])
	}
}

func TestRequestCrawlOneOffCreatesOnceScope(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().ListCrawlTargets(mock.Anything).Return(nil, nil)
	f.store.EXPECT().CreateCrawlTarget(mock.Anything, mock.MatchedBy(func(ct domain.CrawlTarget) bool {
		return ct.Kind == domain.TargetOnce
	})).Return(domain.CrawlTarget{
		ID: 6, Kind: domain.TargetOnce, Status: domain.TargetPending,
		Areas: []string{"305"}, ListingType: domain.ListingSale,
	}, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/crawl-requests", bearer(), map[string]any{
		"areas": []string{"305"}, "listing_type": "sale", "one_off": true,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	tgt, _ := body["target"].(map[string]any)
	if tgt["kind"] != "once" {
		t.Fatalf("target kind = %v, want once", tgt["kind"])
	}
}

func TestUpdateCrawlTargetRecurring(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().GetCrawlTarget(mock.Anything, domain.CrawlTargetID(7)).Return(domain.CrawlTarget{ID: 7, Kind: domain.TargetOnce}, nil).Once()
	f.store.EXPECT().PromoteCrawlTargetToStanding(mock.Anything, domain.CrawlTargetID(7)).Return(nil)

	resp := do(t, http.MethodPatch, f.url+"/v1/crawl-targets/7", bearer(), map[string]any{"recurring": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["recurring"] != true {
		t.Fatalf("recurring = %v, want true", body["recurring"])
	}

	// Already standing: 422, not a silent re-enable.
	f.store.EXPECT().GetCrawlTarget(mock.Anything, domain.CrawlTargetID(7)).Return(domain.CrawlTarget{ID: 7, Kind: domain.TargetStanding}, nil).Once()
	resp = do(t, http.MethodPatch, f.url+"/v1/crawl-targets/7", bearer(), map[string]any{"recurring": true})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("already standing: status = %d, want 422", resp.StatusCode)
	}
}

func TestRecordVerdictBadKind(t *testing.T) {
	f := newFixture(t) // no store call: rejected in the transport
	resp := do(t, http.MethodPost, f.url+"/v1/verdicts", bearer(), map[string]any{
		"listing_id": 1, "verdict": "smitten",
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

// ---- happy paths ----

func TestSearchListings(t *testing.T) {
	f := newFixture(t)
	props := []domain.Property{{ID: 7, Address: domain.Address{Street: "1 Main", Neighborhood: "Park Slope"}, Status: domain.StatusActive}}
	f.store.EXPECT().CountProperties(mock.Anything, mock.Anything).Return(1, nil)
	f.store.EXPECT().SearchProperties(mock.Anything, mock.Anything).Return(props, nil)
	f.store.EXPECT().PriceDrops(mock.Anything, mock.Anything).Return(map[domain.PropertyID]bool{}, nil)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{
		7: {Total: 8, Cached: 5},
	}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings?min_beds=2", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["count"].(float64) != 1 {
		t.Fatalf("count = %v, want 1", body["count"])
	}
	if _, ok := body["effective_filters"]; !ok {
		t.Fatalf("missing effective_filters: %v", body)
	}
	row := body["listings"].([]any)[0].(map[string]any)
	if row["photo_count"].(float64) != 8 || row["photos_cached"].(float64) != 5 {
		t.Fatalf("compact row photo counts = %v, want photo_count 8 photos_cached 5", row)
	}
}

func TestSearchListingsPagingParams(t *testing.T) {
	f := newFixture(t)
	props := []domain.Property{{ID: 7, Address: domain.Address{Street: "1 Main"}, Status: domain.StatusActive}}
	var gotFilter listings.SearchFilter
	capture := func(_ context.Context, filter listings.SearchFilter) {
		gotFilter = filter
	}
	f.store.EXPECT().CountProperties(mock.Anything, mock.Anything).Run(capture).Return(42, nil)
	f.store.EXPECT().SearchProperties(mock.Anything, mock.Anything).Run(capture).Return(props, nil)
	f.store.EXPECT().PriceDrops(mock.Anything, mock.Anything).Return(map[domain.PropertyID]bool{}, nil)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings?limit=200&offset=40&include_inactive=true", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["total"].(float64) != 42 {
		t.Fatalf("total = %v, want 42", body["total"])
	}
	if body["offset"].(float64) != 40 {
		t.Fatalf("offset = %v, want 40", body["offset"])
	}
	if body["limit"].(float64) != 200 {
		t.Fatalf("limit = %v, want 200", body["limit"])
	}
	if gotFilter.Offset != 40 || !gotFilter.IncludeInactive {
		t.Fatalf("filter passed to store = %+v, want offset 40 and include_inactive", gotFilter)
	}
}

func TestSearchListingsListingType(t *testing.T) {
	f := newFixture(t)
	var gotFilter listings.SearchFilter
	capture := func(_ context.Context, filter listings.SearchFilter) { gotFilter = filter }
	f.store.EXPECT().CountProperties(mock.Anything, mock.Anything).Run(capture).Return(0, nil)
	f.store.EXPECT().SearchProperties(mock.Anything, mock.Anything).Run(capture).Return(nil, nil)
	f.store.EXPECT().PriceDrops(mock.Anything, mock.Anything).Return(map[domain.PropertyID]bool{}, nil)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings?listing_type=rent", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if gotFilter.ListingType != domain.ListingRent {
		t.Fatalf("filter passed to store = %+v, want listing_type rent", gotFilter)
	}
	if resp := do(t, http.MethodGet, f.url+"/v1/listings?listing_type=timeshare", bearer(), nil); resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("bad listing_type status = %d, want 422", resp.StatusCode)
	}
}

func TestRecordVerdictsBatch(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(1), domain.VerdictLove, "bright").
		Return(domain.Verdict{ID: 10, PropertyID: 1, Kind: domain.VerdictLove, Note: "bright"}, nil)
	f.store.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(2), domain.VerdictDislike, "").
		Return(domain.Verdict{ID: 11, PropertyID: 2, Kind: domain.VerdictDislike}, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/verdicts/batch", bearer(), map[string]any{
		"verdicts": []map[string]any{
			{"listing_id": 1, "verdict": "love", "note": "bright"},
			{"listing_id": 2, "verdict": "dislike"},
		},
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["count"].(float64) != 2 {
		t.Fatalf("count = %v, want 2", body["count"])
	}
	if got := body["recorded"].([]any); len(got) != 2 {
		t.Fatalf("recorded = %v, want 2 entries", got)
	}
}

func TestRecordVerdictsBatchOneBadID(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(1), domain.VerdictMaybe, "").
		Return(domain.Verdict{ID: 12, PropertyID: 1, Kind: domain.VerdictMaybe}, nil)
	f.store.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(999), domain.VerdictLove, "").
		Return(domain.Verdict{}, listings.ErrNotFound)

	resp := do(t, http.MethodPost, f.url+"/v1/verdicts/batch", bearer(), map[string]any{
		"verdicts": []map[string]any{
			{"listing_id": 1, "verdict": "maybe"},
			{"listing_id": 999, "verdict": "love"},
		},
	})
	defer func() { _ = resp.Body.Close() }()
	// Best-effort: the good id is recorded and returned; the unknown id is reported in failed.
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["count"].(float64) != 1 || body["failed_count"].(float64) != 1 {
		t.Fatalf("count = %v failed_count = %v, want 1 and 1", body["count"], body["failed_count"])
	}
	failed := body["failed"].([]any)[0].(map[string]any)
	if failed["index"].(float64) != 1 || failed["listing_id"].(float64) != 999 {
		t.Fatalf("failed = %v, want index 1 listing 999", failed)
	}
}

func TestRecordVerdictsBatchStoreOutageIs500(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(1), domain.VerdictMaybe, "").
		Return(domain.Verdict{}, errors.New("connection reset"))

	resp := do(t, http.MethodPost, f.url+"/v1/verdicts/batch", bearer(), map[string]any{
		"verdicts": []map[string]any{{"listing_id": 1, "verdict": "maybe"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestRecordVerdictsBatchBadKind(t *testing.T) {
	f := newFixture(t) // no store call: rejected in the transport
	resp := do(t, http.MethodPost, f.url+"/v1/verdicts/batch", bearer(), map[string]any{
		"verdicts": []map[string]any{{"listing_id": 1, "verdict": "smitten"}},
	})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
}

func TestMarkShown(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().MarkShown(mock.Anything, []domain.PropertyID{1, 2}, (*time.Time)(nil)).Return(2, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/shown", bearer(), map[string]any{"ids": []int{1, 2}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["marked"].(float64) != 2 {
		t.Fatalf("marked = %v, want 2", body["marked"])
	}

	// as_of reaches the store; a future one is rejected.
	readAt := time.Date(2026, 9, 2, 18, 0, 0, 0, time.UTC)
	f.store.EXPECT().MarkShown(mock.Anything, []domain.PropertyID{3}, &readAt).Return(1, nil)
	resp = do(t, http.MethodPost, f.url+"/v1/shown", bearer(), map[string]any{"ids": []int{3}, "as_of": readAt.Format(time.RFC3339)})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status with as_of = %d, want 200", resp.StatusCode)
	}
	resp = do(t, http.MethodPost, f.url+"/v1/shown", bearer(), map[string]any{"ids": []int{3}, "as_of": time.Now().Add(time.Hour).Format(time.RFC3339)})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status with future as_of = %d, want 422", resp.StatusCode)
	}
}

func TestSetProfileClearsAndSets(t *testing.T) {
	f := newFixture(t)
	var saved domain.Profile
	f.store.EXPECT().UpdateProfile(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, merge func(*domain.Profile) error) (domain.Profile, error) {
			saved = domain.Profile{ListingType: domain.ListingSale}
			if err := merge(&saved); err != nil {
				return domain.Profile{}, err
			}
			saved.UpdatedAt = time.Now()
			return saved, nil
		})

	// max_price set to a value, neighborhoods explicitly cleared with null.
	resp := do(t, http.MethodPatch, f.url+"/v1/profile", bearer(), map[string]any{
		"max_price":     900000,
		"neighborhoods": nil,
	})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	_ = decode(t, resp)
	if saved.MaxPrice == nil || *saved.MaxPrice != 900000 {
		t.Fatalf("saved.MaxPrice = %v, want 900000", saved.MaxPrice)
	}
	if saved.Neighborhoods != nil {
		t.Fatalf("saved.Neighborhoods = %v, want nil (cleared)", saved.Neighborhoods)
	}
}

// ---- binary photo ----

func TestGetListingPhotoReturnsJPEG(t *testing.T) {
	f := newFixture(t)
	want := tinyJPEG(t)

	// Only the one key is resolved and read; no other thumbnail bytes are fetched.
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return("se/00/abc.jpg", nil)
	f.photos.EXPECT().Get(mock.Anything, "se/00/abc.jpg").Return(want, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings/5/photos/0", bearer(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, want) {
		t.Fatalf("photo bytes mismatch: got %d bytes, want %d", len(got), len(want))
	}
	if len(got) < 2 || got[0] != 0xFF || got[1] != 0xD8 {
		t.Fatalf("body is not JPEG (missing SOI marker)")
	}
}

// The doc promises `run: null` before the job has run; an omitted key reads as
// undefined to a client checking for null.
func TestCrawlStatusPendingHasExplicitNullRun(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().GetCrawlTarget(mock.Anything, domain.CrawlTargetID(9)).
		Return(domain.CrawlTarget{ID: 9, Kind: domain.TargetOnce, Status: domain.TargetPending, Areas: []string{"305"}, ListingType: domain.ListingSale}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/crawl-requests/9", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if run, ok := body["run"]; !ok || run != nil {
		t.Fatalf("run = %v (present=%v), want an explicit null", run, ok)
	}
}

func TestGetListingDetailIncludesHostedPhotoURLs(t *testing.T) {
	f := newFixture(t)
	prop := domain.Property{ID: 5, Address: domain.Address{Street: "5 Ave"}, Status: domain.StatusActive}
	photos := []domain.Photo{
		{Position: 0, CachedPath: "streeteasy/ab/one.jpg", MIMEType: "image/jpeg"},
		{Position: 1, CachedPath: "streeteasy/cd/two.jpg", MIMEType: "image/jpeg"},
	}
	f.store.EXPECT().GetProperty(mock.Anything, domain.PropertyID(5)).
		Return(prop, nil, photos, listings.ProvenanceSummary{URL: "https://streeteasy.com/sale/5"}, nil)
	f.store.EXPECT().ListsForProperty(mock.Anything, domain.PropertyID(5)).Return(nil, nil)
	f.store.EXPECT().State(mock.Anything).Return(domain.Profile{}, nil, false, nil, nil)
	f.photos.EXPECT().Stat(mock.Anything, "streeteasy/ab/one.jpg").Return(photostore.Info{Exists: true}, nil)
	f.photos.EXPECT().Stat(mock.Anything, "streeteasy/cd/two.jpg").Return(photostore.Info{Exists: true}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings/5", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	// Rows carry url; the detail must too, not only under provenance.
	if body["url"] != "https://streeteasy.com/sale/5" {
		t.Fatalf("detail url = %v, want the provider page", body["url"])
	}
	arr, ok := body["photos"].([]any)
	if !ok || len(arr) != 2 {
		t.Fatalf("photos = %v, want 2 entries", body["photos"])
	}
	first, _ := arr[0].(map[string]any)
	url, _ := first["hosted_image_uri"].(string)
	if !strings.HasPrefix(url, "http://") || !strings.HasSuffix(url, "/img/streeteasy/ab/one.jpg") {
		t.Fatalf("photo hosted_image_uri = %q, want an absolute /img/ URL", url)
	}
	if first["mime"] != "image/jpeg" || first["position"].(float64) != 0 {
		t.Fatalf("photo ref = %v, want position 0 image/jpeg", first)
	}
	// Two readable photos plan one 12-tile sheet covering positions 0..1.
	sheets, _ := body["photo_sheets"].([]any)
	if len(sheets) != 1 {
		t.Fatalf("photo_sheets = %v, want one planned sheet", body["photo_sheets"])
	}
	sheet := sheets[0].(map[string]any)
	if sheet["sheet"].(float64) != 1 || sheet["photos"].(float64) != 2 {
		t.Fatalf("sheet = %v, want sheet 1 with 2 photos", sheet)
	}
}

// validImageKey is a well-formed content-addressed thumbnail key for the /img proxy tests: provider/2-hex-shard/sha256.jpg.
func validImageKey() string {
	return "streeteasy/ab/" + strings.Repeat("ab", 32) + ".jpg"
}

func TestImageProxyServesBytesWithoutAuth(t *testing.T) {
	f := newFixture(t)
	key := validImageKey()
	want := tinyJPEG(t)
	f.photos.EXPECT().Get(mock.Anything, key).Return(want, nil)

	// No Authorization header at all: the content-addressed key is the capability.
	resp := do(t, http.MethodGet, f.url+"/img/"+key, "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	if cc := resp.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") {
		t.Fatalf("cache-control = %q, want an immutable long cache", cc)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, want) {
		t.Fatalf("image bytes mismatch: got %d, want %d", len(got), len(want))
	}
}

func TestImageProxyMissingObjectIs404(t *testing.T) {
	f := newFixture(t)
	key := validImageKey()
	f.photos.EXPECT().Get(mock.Anything, key).Return(nil, fmt.Errorf("photostore: get %s: %w", key, os.ErrNotExist)).Once()

	resp := do(t, http.MethodGet, f.url+"/img/"+key, "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	// A store outage is not a missing photo: 500 on both proxy shapes, so caches and hosts do not remember a 404.
	f.photos.EXPECT().Get(mock.Anything, key).Return(nil, errors.New("connection refused")).Twice()
	resp = do(t, http.MethodGet, f.url+"/img/"+key, "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("outage status = %d, want 500", resp.StatusCode)
	}
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil).Once()
	resp = do(t, http.MethodGet, f.url+"/img/l/5/0", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("resolver outage status = %d, want 500", resp.StatusCode)
	}
}

func TestImageProxyRejectsMalformedKeys(t *testing.T) {
	f := newFixture(t) // no store call: rejected on shape before the store is touched
	hex64 := strings.Repeat("ab", 32)
	// None of these is the content-addressed shape provider/shard/sha256.jpg, so the store is never touched (a traversal segment is also excluded by the shape and, before that, by ServeMux path cleaning and the store's own key guard).
	for _, key := range []string{
		"streeteasy/ab/" + hex64 + ".png",    // wrong extension
		"streeteasy/abc/" + hex64 + ".jpg",   // shard not 2 chars
		"streeteasy/ab/not-hex.jpg",          // filename not 64 hex
		"streeteasy/ab/cd/" + hex64 + ".jpg", // too many segments
		"StreetEasy/ab/" + hex64 + ".jpg",    // provider not lowercased
	} {
		resp := do(t, http.MethodGet, f.url+"/img/"+key, "", nil)
		status := resp.StatusCode
		_ = resp.Body.Close()
		if status != http.StatusForbidden {
			t.Fatalf("key %q: status = %d, want 403", key, status)
		}
	}
}

func TestGetListingPhotoOutOfRange(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 3).Return("", listings.ErrNotFound)

	resp := do(t, http.MethodGet, f.url+"/v1/listings/5/photos/3", bearer(), nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}

	// A store outage is a 500, not a misleading 404.
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return("", errors.New("connection reset"))
	resp2 := do(t, http.MethodGet, f.url+"/v1/listings/5/photos/0", bearer(), nil)
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusInternalServerError {
		t.Fatalf("outage status = %d, want 500", resp2.StatusCode)
	}
}

// ---- constructable resolver /img/l/{id}/{n} ----

func TestImageByListingServesBytesWithoutAuth(t *testing.T) {
	f := newFixture(t)
	key := validImageKey()
	want := tinyJPEG(t)
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil)
	f.photos.EXPECT().Get(mock.Anything, key).Return(want, nil)

	// No Authorization header: the resolver is public like /img/{key}.
	resp := do(t, http.MethodGet, f.url+"/img/l/5/0", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); ct != "image/jpeg" {
		t.Fatalf("content-type = %q, want image/jpeg", ct)
	}
	// The (id, n) -> key mapping changes on re-crawl, so unlike /img/{key} the
	// resolver is short-lived and revalidates on the key as ETag.
	if cc := resp.Header.Get("Cache-Control"); strings.Contains(cc, "immutable") || !strings.Contains(cc, "max-age=300") {
		t.Fatalf("cache-control = %q, want a short, non-immutable cache", cc)
	}
	etag := resp.Header.Get("ETag")
	if etag != `"`+key+`"` {
		t.Fatalf("etag = %q, want the quoted key", etag)
	}
	got, _ := io.ReadAll(resp.Body)
	if !bytes.Equal(got, want) {
		t.Fatalf("image bytes mismatch: got %d, want %d", len(got), len(want))
	}

	// A conditional revisit with the same key is 304 without touching the photo store.
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil)
	req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.url+"/img/l/5/0", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	req.Header.Set("If-None-Match", etag)
	resp2, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("conditional get: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	if resp2.StatusCode != http.StatusNotModified {
		t.Fatalf("conditional status = %d, want 304", resp2.StatusCode)
	}
}

func TestImageByListingOutOfRangeIs404(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 9).Return("", listings.ErrNotFound)

	resp := do(t, http.MethodGet, f.url+"/img/l/5/9", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", resp.StatusCode)
	}
}

func TestImageByListingStoreOutageIs500(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return("", errors.New("connection refused"))

	resp := do(t, http.MethodGet, f.url+"/img/l/5/0", "", nil)
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", resp.StatusCode)
	}
}

func TestImageByListingRejectsBadPathParams(t *testing.T) {
	f := newFixture(t) // no store call: rejected before the service is touched
	for _, path := range []string{"/img/l/0/0", "/img/l/-1/0", "/img/l/abc/0", "/img/l/5/-1", "/img/l/5/x"} {
		resp := do(t, http.MethodGet, f.url+path, "", nil)
		status := resp.StatusCode
		_ = resp.Body.Close()
		if status != http.StatusNotFound {
			t.Fatalf("path %q: status = %d, want 404", path, status)
		}
	}
}

// ---- bulk photo URLs POST /v1/listings/photos ----

func TestListingsPhotosBulkURLs(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{
		5: {Total: 3, Cached: 2},
		6: {Total: 1, Cached: 0},
	}, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/listings/photos", bearer(), map[string]any{"ids": []int64{5, 6}})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["count"].(float64) != 2 {
		t.Fatalf("count = %v, want 2", body["count"])
	}
	arr := body["listings"].([]any)
	first := arr[0].(map[string]any)
	if first["id"].(float64) != 5 || first["photo_count"].(float64) != 3 || first["photos_cached"].(float64) != 2 {
		t.Fatalf("first entry = %v, want id 5, photo_count 3, photos_cached 2", first)
	}
	uris := first["image_uris"].([]any)
	if len(uris) != 2 {
		t.Fatalf("image_uris = %v, want 2 URLs", uris)
	}
	if u, _ := uris[0].(string); !strings.HasSuffix(u, "/img/l/5/0") || !strings.HasPrefix(u, "http://") {
		t.Fatalf("first image uri = %q, want absolute .../img/l/5/0", uris[0])
	}
	if u, _ := uris[1].(string); !strings.HasSuffix(u, "/img/l/5/1") {
		t.Fatalf("second image uri = %q, want .../img/l/5/1", uris[1])
	}
	// A listing with no cached photos yields an empty URL list, not null.
	second := arr[1].(map[string]any)
	if second["photos_cached"].(float64) != 0 || len(second["image_uris"].([]any)) != 0 {
		t.Fatalf("second entry = %v, want photos_cached 0 and no URLs", second)
	}
}

// TestCombinedMuxPrecedence proves the routing the combined mcpd server relies on: Register's specific patterns win over a "/" catch-all, so REST keeps /healthz, /docs, /openapi.json, and /v1 while the root falls through to the mounted MCP stand-in.
func TestCombinedMuxPrecedence(t *testing.T) {
	st := mocks.NewMockStore(t)
	ph := mocks.NewMockPhotoStore(t)
	svc := listings.NewService(st, ph)

	mux := http.NewServeMux()
	restapi.Register(mux, svc, token, "")
	const rootMarker = "mcp-root-handler"
	mux.Handle("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(rootMarker))
	}))
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	cases := []struct {
		name, method, path, auth string
		wantStatus               int
		wantBody                 string
	}{
		{"healthz open", http.MethodGet, "/healthz", "", http.StatusOK, "ok"},
		{"openapi open", http.MethodGet, "/openapi.json", "", http.StatusOK, ""},
		{"docs open", http.MethodGet, "/docs", "", http.StatusOK, "api-reference"},
		{"v1 needs token", http.MethodGet, "/v1/state", "", http.StatusUnauthorized, ""},
		{"root falls through to mcp", http.MethodPost, "/", bearer(), http.StatusOK, rootMarker},
		{"unmatched path falls through to mcp", http.MethodGet, "/mcp", bearer(), http.StatusOK, rootMarker},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, tc.method, srv.URL+tc.path, tc.auth, nil)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.wantStatus {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.wantStatus)
			}
			if tc.wantBody != "" {
				b, _ := io.ReadAll(resp.Body)
				if !strings.Contains(string(b), tc.wantBody) {
					t.Fatalf("body = %q, want substring %q", b, tc.wantBody)
				}
			}
		})
	}
}

func tinyJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 4, 4))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

func TestResetStateNoOpWithoutConfirm(t *testing.T) {
	f := newFixture(t)

	// confirm omitted: no store call, reset=false, nothing deleted.
	resp := do(t, http.MethodPost, f.url+"/v1/reset", bearer(), map[string]any{})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["reset"] != false {
		t.Fatalf("reset = %v, want false for a no-op", body["reset"])
	}
	msg, _ := body["message"].(string)
	if !strings.Contains(msg, "confirm=true") {
		t.Fatalf("no-op message = %q, want it to require confirm=true", msg)
	}
}

func TestResetStateConfirmedClears(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().ResetTasteState(mock.Anything).
		Return(listings.ResetCounts{Verdicts: 3, Rubrics: 1, Shown: 2, Profile: 1, CrawlTargets: 4}, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/reset", bearer(), map[string]any{"confirm": true})
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	body := decode(t, resp)
	if body["reset"] != true {
		t.Fatalf("reset = %v, want true", body["reset"])
	}
	cleared, _ := body["cleared"].(map[string]any)
	if cleared["verdicts"] != float64(3) || cleared["shown"] != float64(2) || cleared["profile"] != float64(1) || cleared["crawl_targets"] != float64(4) {
		t.Fatalf("cleared = %v, want the store counts echoed", cleared)
	}
	if msg, _ := body["message"].(string); !strings.Contains(msg, "listings corpus was kept") {
		t.Fatalf("message = %q", msg)
	}
}

func TestResetStateBearerGate(t *testing.T) {
	f := newFixture(t)
	// No token: 401 before the handler runs (no store call expected).
	resp := do(t, http.MethodPost, f.url+"/v1/reset", "", map[string]any{"confirm": true})
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("no token: status = %d, want 401", resp.StatusCode)
	}
}

func TestResolveAreasEndpoint(t *testing.T) {
	st := mocks.NewMockStore(t)
	ph := mocks.NewMockPhotoStore(t)
	svc := listings.NewService(st, ph)
	svc.LoadAreas([]domain.Area{
		{ID: "100", Name: "Manhattan", Borough: "Manhattan", Level: 1, ParentID: "1"},
		{ID: "135", Name: "Upper West Side", Borough: "Manhattan", Level: 2, ParentID: "100"},
		{ID: "157", Name: "West Village", Borough: "Manhattan", Level: 3, ParentID: "100"},
		{ID: "300", Name: "Brooklyn", Borough: "Brooklyn", Level: 1, ParentID: "1"},
		{ID: "400", Name: "Queens", Borough: "Queens", Level: 1, ParentID: "1"},
		{ID: "500", Name: "Bronx", Borough: "Bronx", Level: 1, ParentID: "1"},
		{ID: "600", Name: "Staten Island", Borough: "Staten Island", Level: 1, ParentID: "1"},
	})
	srv := httptest.NewServer(restapi.NewHandler(svc, token, "", ""))
	t.Cleanup(srv.Close)

	resp := do(t, http.MethodGet, srv.URL+"/v1/areas?q=West+Village", bearer(), nil)
	body := decode(t, resp)
	ids, _ := body["area_ids"].([]any)
	if len(ids) != 1 || ids[0] != "157" {
		t.Fatalf("West Village area_ids = %v, want [157]", body["area_ids"])
	}

	resp = do(t, http.MethodGet, srv.URL+"/v1/areas?q=the%20whole%20city", bearer(), nil)
	body = decode(t, resp)
	if body["city_wide"] != true {
		t.Fatalf("whole-city city_wide = %v, want true", body["city_wide"])
	}
	if ids, _ := body["area_ids"].([]any); len(ids) != 5 {
		t.Fatalf("whole-city area_ids = %v, want 5 boroughs", body["area_ids"])
	}
}

// Service-side input validation must surface as 422, never as a masked 500.
func TestClientInputErrorsAre422(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().CreateList(mock.Anything, "   ", "").Return(domain.List{}, listings.Invalidf("list name is empty"))

	tests := []struct {
		name, method, path string
		body               any
	}{
		{"search max_price beyond int32", http.MethodGet, "/v1/listings?max_price=4294967296", nil},
		{"search min_beds beyond int16", http.MethodGet, "/v1/listings?min_beds=65536", nil},
		{"profile max_price beyond int32", http.MethodPatch, "/v1/profile", map[string]any{"max_price": 2147483648}},
		{"crawl min_beds beyond int16", http.MethodPost, "/v1/crawl-requests", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "min_beds": 40000}},
		{"blank list name", http.MethodPost, "/v1/lists", map[string]any{"name": "   "}},
		{"empty list name", http.MethodPost, "/v1/lists", map[string]any{"name": ""}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp := do(t, tt.method, f.url+tt.path, bearer(), tt.body)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnprocessableEntity {
				t.Fatalf("status = %d, want 422", resp.StatusCode)
			}
		})
	}
}

// A listing with more than ten cached photos links all of them (up to the
// 30-photo sheet cap), and every sheet index the detail plans resolves on the
// contact-sheet endpoint with default parameters.
func TestGetListingDetailLinksUpToThirtyPhotos(t *testing.T) {
	f := newFixture(t)
	prop := domain.Property{ID: 6, Address: domain.Address{Street: "6 Ave"}, Status: domain.StatusActive}
	var photos []domain.Photo
	for i := range 25 {
		key := fmt.Sprintf("streeteasy/ab/%02d.jpg", i)
		photos = append(photos, domain.Photo{Position: i, CachedPath: key, MIMEType: "image/jpeg"})
		f.photos.EXPECT().Stat(mock.Anything, key).Return(photostore.Info{Exists: true}, nil)
		f.photos.EXPECT().Get(mock.Anything, key).Return(tinyJPEG(t), nil).Maybe()
	}
	f.store.EXPECT().GetProperty(mock.Anything, domain.PropertyID(6)).
		Return(prop, nil, photos, listings.ProvenanceSummary{}, nil)
	f.store.EXPECT().ListsForProperty(mock.Anything, domain.PropertyID(6)).Return(nil, nil)
	f.store.EXPECT().State(mock.Anything).Return(domain.Profile{}, nil, false, nil, nil)

	body := decode(t, do(t, http.MethodGet, f.url+"/v1/listings/6", bearer(), nil))
	if arr, _ := body["photos"].([]any); len(arr) != 25 {
		t.Fatalf("photos = %d entries, want all 25", len(arr))
	}
	sheets, _ := body["photo_sheets"].([]any)
	if len(sheets) != 3 {
		t.Fatalf("photo_sheets = %v, want 3 planned sheets (12+12+1)", body["photo_sheets"])
	}
	for _, s := range sheets {
		idx := int(s.(map[string]any)["sheet"].(float64))
		resp := do(t, http.MethodGet, fmt.Sprintf("%s/v1/listings/6/contact-sheet?sheet=%d", f.url, idx), bearer(), nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("planned sheet %d: status = %d, want 200", idx, resp.StatusCode)
		}
	}
	for _, q := range []string{"", "?sheet=1"} {
		resp := do(t, http.MethodGet, f.url+"/v1/listings/6/contact-sheet"+q, bearer(), nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Fatalf("contact-sheet%q status = %d, want 200 (sheet 1)", q, resp.StatusCode)
		}
	}
	resp := do(t, http.MethodGet, f.url+"/v1/listings/6/contact-sheet?sheet=4", bearer(), nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("sheet 4 status = %d, want 404", resp.StatusCode)
	}
}

func TestSearchListingsRepeatedNeighborhoodParams(t *testing.T) {
	f := newFixture(t)
	var gotFilter listings.SearchFilter
	capture := func(_ context.Context, filter listings.SearchFilter) { gotFilter = filter }
	f.store.EXPECT().CountProperties(mock.Anything, mock.Anything).Run(capture).Return(0, nil)
	f.store.EXPECT().SearchProperties(mock.Anything, mock.Anything).Run(capture).Return(nil, nil)
	f.store.EXPECT().PriceDrops(mock.Anything, mock.Anything).Return(map[domain.PropertyID]bool{}, nil)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{}, nil)
	f.store.EXPECT().MatchNeighborhoods(mock.Anything, []string{"Upper West Side", "Chelsea"}).Return([]string{"chelsea"}, nil)

	resp := do(t, http.MethodGet, f.url+"/v1/listings?neighborhoods=Upper%20West%20Side&neighborhoods=Chelsea", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	var body struct {
		Unmatched []string `json:"unmatched_neighborhoods"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if len(body.Unmatched) != 1 || body.Unmatched[0] != "Upper West Side" {
		t.Fatalf("unmatched_neighborhoods = %v, want the one name the corpus lacks", body.Unmatched)
	}
	if len(gotFilter.Neighborhoods) != 2 || gotFilter.Neighborhoods[0] != "Upper West Side" || gotFilter.Neighborhoods[1] != "Chelsea" {
		t.Fatalf("neighborhoods passed to store = %v, want both repeated values", gotFilter.Neighborhoods)
	}
}

func TestResolveAreasNoMatchReturnsEmptyArrays(t *testing.T) {
	f := newFixture(t)
	body := decode(t, do(t, http.MethodGet, f.url+"/v1/areas?q=zzzz", bearer(), nil))
	if ids, ok := body["area_ids"].([]any); !ok || len(ids) != 0 {
		t.Fatalf("area_ids = %v (%T), want an empty array", body["area_ids"], body["area_ids"])
	}
	if areas, ok := body["areas"].([]any); !ok || len(areas) != 0 {
		t.Fatalf("areas = %v, want an empty array", body["areas"])
	}
}

func TestResetStateEmptyBodyIsANoOp(t *testing.T) {
	f := newFixture(t)
	resp := do(t, http.MethodPost, f.url+"/v1/reset", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200 for an empty body", resp.StatusCode)
	}
	if body := decode(t, resp); body["reset"] != false {
		t.Fatalf("reset = %v, want false", body["reset"])
	}
}

func TestQueryParamShapeErrorsAre422(t *testing.T) {
	f := newFixture(t)
	f.store.EXPECT().GetProperty(mock.Anything, mock.Anything).Return(domain.Property{}, nil, nil, listings.ProvenanceSummary{}, nil).Maybe()
	for _, path := range []string{
		"/v1/listings?property_type=castle",
		"/v1/listings/5/contact-sheet?sheet=0",
		"/v1/listings/5/contact-sheet?max_photos=-1",
	} {
		resp := do(t, http.MethodGet, f.url+path, bearer(), nil)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusUnprocessableEntity {
			t.Fatalf("%s status = %d, want 422", path, resp.StatusCode)
		}
	}
}

// The REST list writes carry the same signals the MCP tools do: a re-created
// list says already_exists, re-adding a member says added=false.
func TestListWritesReportIdempotentOutcomes(t *testing.T) {
	f := newFixture(t)
	fav := domain.List{ID: 1, Slug: "favorites", Name: "Favorites", IsDefault: true}
	f.store.EXPECT().CreateList(mock.Anything, "Favorites", "").Return(fav, listings.ErrDuplicateList).Once()
	f.store.EXPECT().AddToList(mock.Anything, domain.ListID(1), domain.PropertyID(7), "").Return(true, nil).Once()
	f.store.EXPECT().AddToList(mock.Anything, domain.ListID(1), domain.PropertyID(7), "").Return(false, nil).Once()

	body := decode(t, do(t, http.MethodPost, f.url+"/v1/lists", bearer(), map[string]any{"name": "Favorites"}))
	if l, _ := body["list"].(map[string]any); body["already_exists"] != true || l["slug"] != "favorites" {
		t.Fatalf("re-create = %v, want already_exists with the existing list", body)
	}
	for i, want := range []bool{true, false} {
		body = decode(t, do(t, http.MethodPost, f.url+"/v1/lists/1/items", bearer(), map[string]any{"listing_id": 7}))
		if body["ok"] != true || body["added"] != want {
			t.Fatalf("add #%d = %v, want added=%v", i+1, body, want)
		}
	}
}

// Intermediaries weaken ETags (W/) and clients send lists; both must still 304.
func TestImageByListingHonoursWeakAndListedETags(t *testing.T) {
	f := newFixture(t)
	key := validImageKey()
	for _, header := range []string{`W/"` + key + `"`, `"other", "` + key + `"`, "*"} {
		f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil).Once()
		req, err := http.NewRequestWithContext(context.Background(), http.MethodGet, f.url+"/img/l/5/0", nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		req.Header.Set("If-None-Match", header)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("conditional get: %v", err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusNotModified {
			t.Fatalf("If-None-Match %q: status = %d, want 304", header, resp.StatusCode)
		}
	}
}

// Image bodies are sent with their length so HEAD probes and download progress work.
func TestImageResponsesCarryContentLength(t *testing.T) {
	f := newFixture(t)
	key := validImageKey()
	want := bytes.Repeat(tinyJPEG(t), 40) // well past net/http's 2KB auto-length buffer
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil).Times(2)
	f.photos.EXPECT().Get(mock.Anything, key).Return(want, nil).Times(2)

	for _, path := range []string{"/img/l/5/0", "/v1/listings/5/photos/0"} {
		resp := do(t, http.MethodGet, f.url+path, bearer(), nil)
		got, _ := io.ReadAll(resp.Body)
		_ = resp.Body.Close()
		if resp.ContentLength != int64(len(want)) || len(got) != len(want) {
			t.Fatalf("%s: Content-Length = %d (body %d), want %d", path, resp.ContentLength, len(got), len(want))
		}
	}
}

// A run lookup outage must not read as "run pruned": the caller would treat a
// done job as one with no metrics.
func TestCrawlStatusRunOutageIs500(t *testing.T) {
	f := newFixture(t)
	runID := domain.IngestRunID(4)
	f.store.EXPECT().GetCrawlTarget(mock.Anything, domain.CrawlTargetID(9)).
		Return(domain.CrawlTarget{ID: 9, Kind: domain.TargetOnce, Status: domain.TargetDone, LastRunID: &runID, Areas: []string{"305"}, ListingType: domain.ListingSale}, nil).Times(2)
	f.store.EXPECT().RunByID(mock.Anything, runID).Return(domain.IngestRun{}, errors.New("connection reset")).Once()
	f.store.EXPECT().RunByID(mock.Anything, runID).Return(domain.IngestRun{}, listings.ErrCrawlTargetNotFound).Once()

	resp := do(t, http.MethodGet, f.url+"/v1/crawl-requests/9", bearer(), nil)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusInternalServerError {
		t.Fatalf("outage status = %d, want 500", resp.StatusCode)
	}
	resp = do(t, http.MethodGet, f.url+"/v1/crawl-requests/9", bearer(), nil)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pruned-run status = %d, want 200", resp.StatusCode)
	}
	if run, ok := decode(t, resp)["run"]; !ok || run != nil {
		t.Fatalf("pruned run = %v, want an explicit null", run)
	}
}

// The area cap is checked before any name is resolved, so an oversized list
// costs one comparison, not thousands of catalog scans.
func TestRequestCrawlCapsAreasBeforeResolving(t *testing.T) {
	f := newFixture(t)
	areas := make([]string, listings.MaxRequestAreas+1)
	for i := range areas {
		areas[i] = "Upper West Side"
	}
	areas[0] = "Nowhere Such Place"
	resp := do(t, http.MethodPost, f.url+"/v1/crawl-requests", bearer(), map[string]any{
		"areas": areas, "listing_type": "sale", "one_off": true,
	})
	if resp.StatusCode != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want 422", resp.StatusCode)
	}
	// The cap fires first; the unresolvable name is never even looked at.
	if msg, _ := decode(t, resp)["detail"].(string); !strings.Contains(msg, "at most") {
		t.Fatalf("detail = %q, want the area cap", msg)
	}
}
