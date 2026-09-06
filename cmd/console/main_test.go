package main

import (
	"html/template"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"
)

func testServer(t *testing.T) *server {
	t.Helper()
	tmpl, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		t.Fatalf("parse templates: %v", err)
	}
	return &server{api: "http://example.invalid", http: http.DefaultClient,
		tmpl: tmpl, log: slog.New(slog.NewTextHandler(io.Discard, nil))}
}

func TestLoginPageRendersWithTokenHint(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	s.loginPage(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "MCP_BEARER_TOKEN") {
		t.Errorf("login page missing the Railway token hint")
	}
}

// Templates only fail at execution, so exercise every page's render with
// representative data to catch a bad field reference before it reaches a user.
func TestPagesRender(t *testing.T) {
	s := testServer(t)
	row := listingRow{ID: 7, Address: "12 Main St #4B", Neighborhood: "Park Slope", Price: i64p(950_000), Beds: intp(2), Baths: f64p(1.5), Sqft: intp(900), PropertyType: "coop", DOM: intp(12), PriceDrop: true, Status: "active", URL: "https://example.com/7", PhotoCount: 9, PhotosCached: 6}
	detail := detailResp{ID: 7, Address: row.Address, Neighborhood: row.Neighborhood, Zip: "11215", ListingType: "sale", PropertyType: "coop", Status: "active", Price: row.Price, Beds: row.Beds, Baths: row.Baths, Sqft: row.Sqft, Maintenance: i64p(900), DOM: row.DOM, Description: "Sunny corner unit.", FirstSeen: "2026-08-01T00:00:00Z", LastSeen: "2026-08-30T00:00:00Z", PhotoCount: 9, PhotosCached: 6, PhotoMissing: 3,
		Photos: []photoRef{{Position: 0, HostedImageURI: "https://example.com/img/l/7/0"}}, PriceHistory: []priceEvent{{Price: 950_000, ObservedAt: "2026-08-30T00:00:00Z"}},
		Lists: []consoleList{{ID: 1, Slug: "favorites", Name: "Favorites", Emoji: "⭐", IsDefault: true, Count: 1}}, Verdicts: []verdict{{ID: 1, ListingID: 7, Verdict: "love", Note: "the windows", CreatedAt: "2026-08-30T00:00:00Z"}}}
	fav := consoleList{ID: 1, Slug: "favorites", Name: "Favorites", Emoji: "⭐", IsDefault: true, Count: 1}
	res := listResp{Total: 30, Count: 24, Offset: 0, Limit: 24, Listings: []listingRow{row}}
	state := stateResp{Profile: profile{MaxPrice: i64p(1_200_000), MinBeds: intp(2), ListingType: "sale", Neighborhoods: []string{"Park Slope"}}, Rubric: &rubric{Content: "Light matters.", CreatedAt: "2026-08-30T00:00:00Z"}, RubricStale: true, Verdicts: detail.Verdicts}
	rated := ratedPayload{Rubric: state.Rubric, Count: 1, Rated: []ratedEntry{{Listing: row, Verdict: "love", Note: "the windows", History: detail.Verdicts}}}
	pages := []struct {
		name string
		data map[string]any
	}{
		{"login.html", map[string]any{"Nav": false, "Error": "That token was rejected by the API."}},
		{"error.html", map[string]any{"Nav": false, "Message": "api GET /v1/state: boom"}},
		{"dashboard.html", map[string]any{"Nav": "home", "Write": true, "State": state, "Total": 30, "Recent": res.Listings, "Targets": []crawlTarget{{ID: 1, Kind: "standing", Status: "done", Enabled: true, Areas: []string{"100"}, ListingType: "sale"}}}},
		{"dashboard.html", map[string]any{"Nav": "home", "Write": false, "State": stateResp{}, "Total": 0, "Recent": []listingRow{}, "Targets": []crawlTarget{}}},
		{"listings.html", map[string]any{"Nav": "listings", "Write": true, "Res": res, "Q": "main", "MinBeds": "2", "MaxPrice": "1000000", "Inactive": true, "Offset": 0, "HasPrev": false, "PrevURL": "/listings", "HasNext": true, "NextURL": "/listings?offset=24"}},
		{"listings_rows.html", map[string]any{"Nav": "listings", "Write": false, "Res": listResp{}, "Offset": 24, "HasPrev": true, "PrevURL": "/listings", "HasNext": false, "NextURL": ""}},
		{"listing.html", map[string]any{"Nav": "listings", "Write": true, "L": detail, "AddableLists": []consoleList{{ID: 2, Slug: "big-windows", Name: "Big Windows"}}}},
		{"listing.html", map[string]any{"Nav": "listings", "Write": false, "L": detailResp{ID: 8, Address: "1 Bare St", Status: "delisted"}}},
		{"lists.html", map[string]any{"Nav": "lists", "Write": true, "Lists": []consoleList{fav}}},
		{"list_detail.html", map[string]any{"Nav": "lists", "Write": true, "List": fav, "Res": res, "HasPrev": false, "HasNext": false}},
		{"verdicts.html", map[string]any{"Nav": "verdicts", "Write": true, "Rated": rated}},
		{"verdicts.html", map[string]any{"Nav": "verdicts", "Write": false, "Rated": ratedPayload{}}},
		{"settings.html", map[string]any{"Nav": "settings", "Write": true}},
		{"crawls.html", map[string]any{
			"Nav": "crawls", "Write": true, "Refresh": true, "Queued": "Upper West Side", "Err": "",
			"Corpus": corpusStats{Listings: 120, ActiveListings: 100, ActiveSale: 80, ActiveRent: 20, PhotosCached: 900, LastCrawlAt: "2026-08-30T12:00:00Z"},
			"Targets": []crawlTarget{
				{ID: 1, Kind: "standing", Status: "pending", Enabled: true, Areas: []string{"100"}, ListingType: "sale", LastRunAt: "2026-08-30T12:00:00Z"},
				{ID: 2, Kind: "once", Status: "running", Areas: []string{"305"}, ListingType: "rent", MaxPrice: i64p(2000000), MinBeds: intp(2)},
				{ID: 3, Kind: "once", Status: "failed", Areas: []string{"x"}, ListingType: "sale", LastError: "could not resolve area name(s): x"},
			},
		}},
		{"crawls.html", map[string]any{"Nav": "crawls", "Write": false, "Corpus": corpusStats{}, "Targets": []crawlTarget{}}},
	}
	for _, p := range pages {
		rr := httptest.NewRecorder()
		s.render(rr, p.name, p.data)
		if b := rr.Body.String(); strings.Contains(b, "<no value>") || strings.TrimSpace(b) == "" {
			t.Fatalf("%s rendered empty or with <no value>", p.name)
		}
	}
}

func i64p(v int64) *int64     { return &v }
func intp(v int) *int         { return &v }
func f64p(v float64) *float64 { return &v }

// An expired session behind an htmx fragment request must send HX-Redirect,
// otherwise htmx swaps the login form into the results panel.
func TestSessionExpiryRedirectsHtmxViaHeader(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/lists", nil)
	req.Header.Set("HX-Request", "true")
	s.listsPage(rr, req, "expired")
	if rr.Code != http.StatusOK || rr.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx expiry = %d HX-Redirect=%q, want 200 with HX-Redirect: /login", rr.Code, rr.Header().Get("HX-Redirect"))
	}

	rr = httptest.NewRecorder()
	s.listsPage(rr, httptest.NewRequest(http.MethodGet, "/lists", nil), "expired")
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("plain expiry = %d -> %q, want 303 -> /login", rr.Code, rr.Header().Get("Location"))
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("HX-Request", "true")
	s.auth(func(http.ResponseWriter, *http.Request, string) { t.Fatal("handler ran without auth") })(rr, req)
	if rr.Header().Get("HX-Redirect") != "/login" {
		t.Fatalf("htmx request without a cookie got no HX-Redirect: %d", rr.Code)
	}
}

// Pager offsets come from links, so junk is treated as page one rather than
// forwarded to the API as a 422.
func TestListingsSanitizesOffset(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	rr := httptest.NewRecorder()
	s.listings(rr, httptest.NewRequest(http.MethodGet, "/listings?offset=abc", nil), "tok")
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rr.Code, rr.Body.String())
	}
}

// A rent-only corpus is not empty: the overview counts it and shows rentals
// with a monthly price; the listings page can ask for rentals.
func TestConsoleReachesRentals(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	rr := httptest.NewRecorder()
	s.dashboard(rr, httptest.NewRequest(http.MethodGet, "/", nil), "tok")
	body := rr.Body.String()
	if strings.Contains(body, "Nothing here yet") || !strings.Contains(body, "9 Rent St") || !strings.Contains(body, "$4,500<small") {
		t.Fatalf("dashboard on a rent-only corpus:\n%s", body)
	}
	rr = httptest.NewRecorder()
	s.listings(rr, httptest.NewRequest(http.MethodGet, "/listings?listing_type=rent&offset=24", nil), "tok")
	body = rr.Body.String()
	if !strings.Contains(body, "9 Rent St") || !strings.Contains(body, `value="rent" selected`) || !strings.Contains(body, "listing_type=rent") {
		t.Fatalf("rental listings page:\n%s", body)
	}
	rr = httptest.NewRecorder()
	s.listings(rr, httptest.NewRequest(http.MethodGet, "/listings", nil), "tok")
	if strings.Contains(rr.Body.String(), "9 Rent St") {
		t.Fatalf("default listings page returned rentals")
	}
}

func TestLoginDistinguishesUnreachableAPI(t *testing.T) {
	s := testServer(t)
	dead := httptest.NewServer(http.NotFoundHandler())
	dead.Close()
	s.api = dead.URL // connection refused: the probe cannot connect
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token=abc"))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	s.doLogin(rr, req)
	if b := rr.Body.String(); !strings.Contains(b, "Could not reach the API") || strings.Contains(b, "rejected") {
		t.Fatalf("unreachable API rendered as a rejected token: %s", b)
	}
}

func TestAuthRedirectsWithoutCookie(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	h := s.auth(func(http.ResponseWriter, *http.Request, string) { t.Fatal("handler ran without auth") })
	h(rr, httptest.NewRequest(http.MethodGet, "/", nil))
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("got %d -> %q, want 303 -> /login", rr.Code, rr.Header().Get("Location"))
	}
}

// A fake API: /v1/state accepts any bearer, /v1/listings/404 answers a problem 404.
func fakeAPI(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/state", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer wrong" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{}`)
	})
	mux.HandleFunc("GET /v1/listings/404", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = io.WriteString(w, `{"title":"Not Found","detail":"listing 404 not found"}`)
	})
	mux.HandleFunc("GET /v1/listings", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("offset") == "abc" {
			w.WriteHeader(http.StatusUnprocessableEntity)
			_, _ = io.WriteString(w, `{"title":"Unprocessable Entity","detail":"offset must be an integer"}`)
			return
		}
		if r.URL.Query().Get("listing_type") == "rent" {
			_, _ = io.WriteString(w, `{"listings":[{"id":9,"address":"9 Rent St","listing_type":"rent","price":4500,"status":"active"}],"total":1,"count":1}`)
			return
		}
		_, _ = io.WriteString(w, `{"listings":[],"total":0,"count":0}`)
	})
	mux.HandleFunc("GET /v1/corpus", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"listings":1,"active_listings":1,"active_sale":0,"active_rent":1}`)
	})
	mux.HandleFunc("GET /v1/crawl-targets", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, `{"targets":[]}`) })
	mux.HandleFunc("GET /v1/lists", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") == "Bearer expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = io.WriteString(w, `{"lists":[]}`)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestLoginCookieSecureFollowsTheScheme(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	login := func(proto string) *http.Cookie {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://homebox:8080/login", strings.NewReader("token=abc%3B%22def"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		if proto != "" {
			req.Header.Set("X-Forwarded-Proto", proto)
		}
		s.doLogin(rr, req)
		if rr.Code != http.StatusSeeOther {
			t.Fatalf("login = %d, want 303", rr.Code)
		}
		cookies := rr.Result().Cookies()
		if len(cookies) != 1 {
			t.Fatalf("cookies = %v, want one", cookies)
		}
		return cookies[0]
	}
	// Plain http on a LAN host: a Secure cookie would be dropped and loop the login.
	if c := login(""); c.Secure {
		t.Fatalf("cookie over plain http is Secure; the browser would refuse it")
	}
	c := login("https")
	if !c.Secure {
		t.Fatalf("cookie behind an https proxy is not Secure")
	}
	// Two chained proxies list both hops; the client-facing one is what counts.
	if c := login("https, http"); !c.Secure {
		t.Fatalf("cookie behind chained proxies (https, http) is not Secure")
	}
	// The token holds bytes a cookie cannot carry raw; it must round-trip intact.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(c)
	if got := s.token(req); got != `abc;"def` {
		t.Fatalf("token from cookie = %q, want the original", got)
	}
}

func TestAPI404RendersAs404NotBadGateway(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/listings/404", nil)
	req.SetPathValue("id", "404")
	s.listing(rr, req, "tok")
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 passed through from the API", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("content-type = %q, want text/html on the error page", ct)
	}
	if !strings.Contains(rr.Body.String(), "listing 404 not found") {
		t.Fatalf("error page lacks the API detail: %s", rr.Body.String())
	}
}

func TestTransportFailureHidesTheAPIOrigin(t *testing.T) {
	s := testServer(t) // api = http://example.invalid, which never resolves
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/listings/1", nil)
	req.SetPathValue("id", "1")
	s.listing(rr, req, "tok")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	if b := rr.Body.String(); strings.Contains(b, "example.invalid") || !strings.Contains(b, "could not reach the API") {
		t.Fatalf("error page leaks the internal API origin or lacks the generic line: %s", b)
	}
}

func TestLoginThrottlesAfterRepeatedRejections(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	now := time.Unix(1_700_000_000, 0)
	s.logins.now = func() time.Time { return now }
	attempt := func(tok string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/login", strings.NewReader("token="+tok))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		s.doLogin(rr, req)
		return rr.Code
	}
	for i := 0; i < maxLoginFailures; i++ {
		if code := attempt("wrong"); code != http.StatusOK {
			t.Fatalf("rejection %d = %d, want 200 with the login form", i+1, code)
		}
	}
	// Past the budget even the right token waits: the limiter is not an oracle.
	if code := attempt("right"); code != http.StatusTooManyRequests {
		t.Fatalf("attempt after %d failures = %d, want 429", maxLoginFailures, code)
	}
	now = now.Add(loginWindow + time.Second)
	if code := attempt("right"); code != http.StatusSeeOther {
		t.Fatalf("attempt after the window = %d, want 303", code)
	}
}

func TestLoginBudgetIsSpentOnAdmissionAndRefundedBySuccess(t *testing.T) {
	var l loginLimiter
	// A burst of attempts whose probes have not answered yet still shares one budget.
	for i := 0; i < maxLoginFailures; i++ {
		if !l.allow() {
			t.Fatalf("attempt %d refused before the budget was spent", i+1)
		}
	}
	if l.allow() {
		t.Fatal("attempt past the budget admitted while earlier probes were in flight")
	}
	l.succeeded()
	if !l.allow() {
		t.Fatal("a successful login should hand the budget back")
	}
}

func TestEveryPageIsUnframeableAndUncached(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	h := routes(s, true)
	get := func(target string) http.Header {
		rr := httptest.NewRecorder()
		h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, target, nil))
		return rr.Header()
	}
	for _, target := range []string{"/login", "/", "/healthz", "/static/console.css"} {
		hdr := get(target)
		if hdr.Get("X-Frame-Options") != "DENY" || hdr.Get("Content-Security-Policy") != "frame-ancestors 'none'" {
			t.Errorf("%s: framing headers = %q / %q", target, hdr.Get("X-Frame-Options"), hdr.Get("Content-Security-Policy"))
		}
		if hdr.Get("X-Content-Type-Options") != "nosniff" {
			t.Errorf("%s: nosniff missing", target)
		}
		wantCache := "no-store"
		if strings.HasPrefix(target, "/static/") {
			wantCache = "public, max-age=3600"
		}
		if got := hdr.Get("Cache-Control"); got != wantCache {
			t.Errorf("%s: Cache-Control = %q, want %q", target, got, wantCache)
		}
	}
}

func TestHistoryRestoreGetsTheFullPage(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	get := func(restore bool) string {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/listings?offset=24", nil)
		req.Header.Set("HX-Request", "true")
		if restore {
			req.Header.Set("HX-History-Restore-Request", "true")
		}
		s.listings(rr, req, "tok")
		if v := rr.Header().Get("Vary"); v != "HX-Request" {
			t.Fatalf("Vary = %q, want HX-Request", v)
		}
		return rr.Body.String()
	}
	if b := get(false); strings.Contains(b, "<nav") {
		t.Fatalf("plain htmx request returned the full page")
	}
	if b := get(true); !strings.Contains(b, "<nav") {
		t.Fatalf("history restore returned only the fragment; Back would lose the nav")
	}
}

func TestCheckAPIBase(t *testing.T) {
	for _, bad := range []string{"", "mcpd.railway.internal:8787", "ftp://x", "http://"} {
		if checkAPIBase(bad) == nil {
			t.Errorf("checkAPIBase(%q) accepted", bad)
		}
	}
	if err := checkAPIBase("https://mcpd.example.com"); err != nil {
		t.Errorf("checkAPIBase rejected a good URL: %v", err)
	}
}

// Every POST is refused from another site even with a valid session, /static/
// has no directory index, and a login token in the query string is ignored.
func TestRoutesRefuseCrossOriginWritesAndHideStaticIndex(t *testing.T) {
	s := testServer(t)
	s.api = fakeAPI(t).URL
	h := routes(s, true)

	login := func(target string) *httptest.ResponseRecorder {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, target, strings.NewReader("token=abc"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		h.ServeHTTP(rr, req)
		return rr
	}
	if rr := login("http://homebox:8080/login"); rr.Code != http.StatusSeeOther || len(rr.Result().Cookies()) != 1 {
		t.Fatalf("same-origin login = %d with %d cookies, want 303 + session", rr.Code, len(rr.Result().Cookies()))
	}
	cookie := login("http://homebox:8080/login").Result().Cookies()[0]

	post := func(site, origin string) int {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "http://homebox:8080/reset", strings.NewReader("confirm=RESET"))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.AddCookie(cookie)
		if site != "" {
			req.Header.Set("Sec-Fetch-Site", site)
		}
		if origin != "" {
			req.Header.Set("Origin", origin)
		}
		h.ServeHTTP(rr, req)
		return rr.Code
	}
	if got := post("cross-site", "https://evil.example"); got != http.StatusForbidden {
		t.Fatalf("cross-site POST with a session = %d, want 403", got)
	}
	if got := post("", "https://evil.example"); got != http.StatusForbidden {
		t.Fatalf("POST with a foreign Origin = %d, want 403", got)
	}
	if got := post("same-origin", ""); got == http.StatusForbidden {
		t.Fatal("same-origin POST was refused")
	}

	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "http://homebox:8080/static/", nil))
	if rr.Code != http.StatusNotFound {
		t.Fatalf("/static/ = %d, want 404 (no directory index)", rr.Code)
	}

	rr = httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "http://homebox:8080/login?token=abc", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	h.ServeHTTP(rr, req)
	if rr.Code == http.StatusSeeOther || !strings.Contains(rr.Body.String(), "Enter your bearer token") {
		t.Fatalf("query-string token logged in: %d %s", rr.Code, rr.Body.String())
	}
}

// "Park Slope, Brooklyn" is one place (the borough qualifies it); only an id
// list splits on commas, so the form cannot widen a neighborhood to a borough.
func TestSplitAreasKeepsQualifiedPhrases(t *testing.T) {
	cases := map[string][]string{
		"Park Slope, Brooklyn":    {"Park Slope, Brooklyn"},
		"305, 319":                {"305", "319"},
		"Harlem; Upper West Side": {"Harlem", "Upper West Side"},
		"Harlem\nall of Queens\n": {"Harlem", "all of Queens"},
		"  ":                      nil,
	}
	for in, want := range cases {
		if got := splitAreas(in); !slices.Equal(got, want) {
			t.Errorf("splitAreas(%q) = %q, want %q", in, got, want)
		}
	}
}

// htmx drops a full error document; a fragment request must get a swappable
// fragment, and a history-restore request must be redirected like a page load.
func TestHtmxErrorsAreFragmentsAndRestoresRedirect(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/listings?q=x", nil)
	req.Header.Set("HX-Request", "true")
	s.listings(rr, req, "tok")
	if rr.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", rr.Code)
	}
	if b := rr.Body.String(); strings.Contains(b, "<!doctype") || !strings.Contains(b, `class="err"`) {
		t.Fatalf("htmx error response is not a fragment:\n%s", b)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/listings", nil)
	req.Header.Set("HX-Request", "true")
	req.Header.Set("HX-History-Restore-Request", "true")
	s.auth(func(http.ResponseWriter, *http.Request, string) { t.Fatal("handler ran without auth") })(rr, req)
	if rr.Code != http.StatusSeeOther || rr.Header().Get("Location") != "/login" {
		t.Fatalf("history restore without a session = %d HX-Redirect=%q, want 303 -> /login", rr.Code, rr.Header().Get("HX-Redirect"))
	}
}

// The page tells htmx to swap error responses (its default drops them) and to
// reload rather than restore stale history snapshots.
func TestPageConfiguresHtmxResponseHandling(t *testing.T) {
	s := testServer(t)
	rr := httptest.NewRecorder()
	s.loginPage(rr, httptest.NewRequest(http.MethodGet, "/login", nil))
	b := rr.Body.String()
	for _, want := range []string{`name="htmx-config"`, `"code":"[45]..","swap":true`, `"refreshOnHistoryMiss":true`} {
		if !strings.Contains(b, want) {
			t.Errorf("page lacks htmx config %s", want)
		}
	}
}
