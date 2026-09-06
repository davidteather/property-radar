// Command console is a small web UI over the Property Radar REST API. It renders
// server-side (html/template + htmx) and keeps the bearer token in an httpOnly
// cookie; writes (lists, crawl scopes, reset) are on unless CONSOLE_READONLY=true.
package main

import (
	"context"
	"embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/davidteather/property-radar/internal/publicurl"
	"github.com/davidteather/property-radar/internal/shared/logging"
)

//go:embed static/*
var static embed.FS

//go:embed templates/*.html
var templatesFS embed.FS

const cookieName = "pr_console"

type server struct {
	api   string // API base URL, no trailing slash
	http  *http.Client
	tmpl  *template.Template
	log   *slog.Logger
	write bool // create/manage lists, favorite, reset; on unless CONSOLE_READONLY=true

	logins loginLimiter
}

// loginLimiter blunts token guessing through /login: after maxLoginFailures
// rejected tokens within loginWindow, every attempt is refused until it passes.
type loginLimiter struct {
	mu       sync.Mutex
	failures int
	window   time.Time
	now      func() time.Time
}

const (
	maxLoginFailures = 10
	loginWindow      = time.Minute
)

func (l *loginLimiter) clock() time.Time {
	if l.now != nil {
		return l.now()
	}
	return time.Now()
}

// allow spends one attempt up front, so a parallel burst cannot slip past the
// budget while its probes are still in flight; a success hands the budget back.
func (l *loginLimiter) allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if now := l.clock(); now.After(l.window) {
		l.failures, l.window = 0, now.Add(loginWindow)
	}
	if l.failures >= maxLoginFailures {
		return false
	}
	l.failures++
	return true
}

func (l *loginLimiter) succeeded() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = 0
}

func addrFlagSet() bool {
	set := false
	flag.Visit(func(f *flag.Flag) { set = set || f.Name == "addr" })
	return set
}

func main() {
	log := logging.New(false)
	addr := flag.String("addr", ":8080", "listen address (defaults to $PORT when set)")
	flag.Parse()

	api := strings.TrimRight(strings.TrimSpace(os.Getenv("API_BASE_URL")), "/")
	if err := checkAPIBase(api); err != nil {
		log.Error("API_BASE_URL is required (the Property Radar mcpd base URL)", "err", err)
		os.Exit(1)
	}
	// Same precedence as mcpd/api: an explicit -addr wins over $PORT.
	if p := strings.TrimSpace(os.Getenv("PORT")); p != "" && !addrFlagSet() {
		*addr = ":" + p
	}

	tmpl, err := template.New("").Funcs(funcs).ParseFS(templatesFS, "templates/*.html")
	if err != nil {
		log.Error("parse templates", "err", err)
		os.Exit(1)
	}
	// Writes on by default; set CONSOLE_READONLY=true to make it view-only.
	write := !strings.EqualFold(strings.TrimSpace(os.Getenv("CONSOLE_READONLY")), "true")
	s := &server{api: api, http: &http.Client{Timeout: 20 * time.Second}, tmpl: tmpl, log: log, write: write}

	mux := routes(s, write)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal during shutdown kills, not waits
	log.Info("console starting", "addr", *addr, "api", api, "write", write)
	if err := serve(ctx, *addr, mux); err != nil {
		log.Error("serve", "err", err)
		os.Exit(1)
	}
}

// routes builds the console handler: every POST is refused from another origin
// (Sec-Fetch-Site/Origin), the defence-in-depth behind the SameSite cookie.
func routes(s *server, write bool) http.Handler {
	mux := http.NewServeMux()
	mux.Handle("GET /static/", noIndex(http.FileServer(http.FS(static))))
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = io.WriteString(w, "ok") })
	mux.HandleFunc("GET /login", s.loginPage)
	mux.HandleFunc("POST /login", s.doLogin)
	mux.HandleFunc("POST /logout", s.logout)
	mux.HandleFunc("GET /{$}", s.auth(s.dashboard))
	mux.HandleFunc("GET /listings", s.auth(s.listings))
	mux.HandleFunc("GET /listings/{id}", s.auth(s.listing))
	mux.HandleFunc("GET /lists", s.auth(s.listsPage))
	mux.HandleFunc("GET /lists/{id}", s.auth(s.listDetail))
	mux.HandleFunc("GET /verdicts", s.auth(s.verdictsPage))
	mux.HandleFunc("GET /crawls", s.auth(s.crawls))
	if write {
		mux.HandleFunc("POST /lists", s.auth(s.createList))
		mux.HandleFunc("POST /lists/{id}/delete", s.auth(s.deleteList))
		mux.HandleFunc("POST /listings/{id}/lists", s.auth(s.addToList))
		mux.HandleFunc("POST /listings/{id}/lists/{listID}/remove", s.auth(s.removeFromList))
		mux.HandleFunc("POST /crawls", s.auth(s.queueCrawl))
		mux.HandleFunc("POST /crawls/{id}/enabled", s.auth(s.setCrawlEnabled))
		mux.HandleFunc("POST /crawls/{id}/recurring", s.auth(s.makeCrawlRecurring))
		mux.HandleFunc("POST /crawls/{id}/delete", s.auth(s.deleteCrawl))
		mux.HandleFunc("GET /settings", s.auth(s.settings))
		mux.HandleFunc("POST /reset", s.auth(s.resetData))
	}
	guard := http.NewCrossOriginProtection()
	return hardenHeaders(guard.Handler(mux))
}

// hardenHeaders refuses framing (every write is a one-click POST) and keeps
// authenticated pages out of shared caches; only /static/ may be cached.
func hardenHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hdr := w.Header()
		hdr.Set("X-Frame-Options", "DENY")
		hdr.Set("Content-Security-Policy", "frame-ancestors 'none'")
		hdr.Set("X-Content-Type-Options", "nosniff")
		hdr.Set("Referrer-Policy", "same-origin")
		if strings.HasPrefix(r.URL.Path, "/static/") {
			hdr.Set("Cache-Control", "public, max-age=3600")
		} else {
			hdr.Set("Cache-Control", "no-store")
		}
		h.ServeHTTP(w, r)
	})
}

// noIndex hides the directory listing FileServer would render for /static/.
func noIndex(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/") {
			http.NotFound(w, r)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// serve runs the console until SIGINT/SIGTERM, then drains in-flight requests.
func serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      2 * time.Minute,
		IdleTimeout:       2 * time.Minute,
	}
	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.ListenAndServe() }()
	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 15*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return err
	}
	<-serveErr
	return nil
}

// ---- auth ----

// The cookie carries the bearer base64url-encoded: net/http silently drops
// bytes a cookie value cannot hold, which would turn an odd token into a login loop.
func (s *server) token(r *http.Request) string {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return ""
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value)
	if err != nil {
		return ""
	}
	return string(raw)
}

func (s *server) auth(h func(http.ResponseWriter, *http.Request, string)) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		tok := s.token(r)
		if tok == "" {
			toLogin(w, r)
			return
		}
		h(w, r, tok)
	}
}

// fragmentRequest is an htmx swap into part of the page; a history restore
// replaces <body> and follows redirects like a plain navigation.
func fragmentRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true" && r.Header.Get("HX-History-Restore-Request") != "true"
}

// toLogin sends the browser to the login page. htmx follows a 303 inside its
// fetch and would swap the login form into the fragment, so it gets HX-Redirect.
func toLogin(w http.ResponseWriter, r *http.Request) {
	if fragmentRequest(r) {
		w.Header().Set("HX-Redirect", "/login")
		w.WriteHeader(http.StatusOK)
		return
	}
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

func (s *server) loginPage(w http.ResponseWriter, r *http.Request) {
	if s.token(r) != "" {
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
	s.render(w, "login.html", map[string]any{"Nav": false})
}

func (s *server) doLogin(w http.ResponseWriter, r *http.Request) {
	// PostFormValue: a token in the query string would land in access logs.
	tok := strings.TrimSpace(r.PostFormValue("token"))
	if tok == "" {
		s.render(w, "login.html", map[string]any{"Nav": false, "Error": "Enter your bearer token."})
		return
	}
	if !s.logins.allow() {
		w.Header().Set("Retry-After", "60")
		w.WriteHeader(http.StatusTooManyRequests)
		s.render(w, "login.html", map[string]any{"Nav": false, "Error": "Too many failed logins; try again in a minute."})
		return
	}
	// Validate the token against a cheap read before trusting it.
	_, status, err := s.get(r.Context(), tok, "/v1/state")
	switch {
	case err != nil:
		s.log.Error("login probe", "err", err)
		s.render(w, "login.html", map[string]any{"Nav": false, "Error": "Could not reach the API to check the token; see the console logs."})
		return
	case status == http.StatusUnauthorized:
		s.render(w, "login.html", map[string]any{"Nav": false, "Error": "That token was rejected by the API."})
		return
	case status != http.StatusOK:
		s.render(w, "login.html", map[string]any{"Nav": false, "Error": fmt.Sprintf("The API answered %d %s while checking the token.", status, http.StatusText(status))})
		return
	}
	s.logins.succeeded()
	http.SetCookie(w, &http.Cookie{
		Name: cookieName, Value: base64.RawURLEncoding.EncodeToString([]byte(tok)), Path: "/", HttpOnly: true,
		Secure: publicurl.Secure(r), SameSite: http.SameSiteLaxMode, MaxAge: 12 * 3600,
	})
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
	http.Redirect(w, r, "/login", http.StatusSeeOther)
}

// ---- API client ----

func (s *server) get(ctx context.Context, token, path string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.api+path, nil)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	resp, err := s.http.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = resp.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return body, resp.StatusCode, err
}

// getJSON fetches and decodes; a 401 becomes errUnauthorized so handlers bounce
// to login, any other non-200 an apiError so a 404 renders as one, not a 502.
func (s *server) getJSON(ctx context.Context, token, path string, out any) error {
	body, status, err := s.get(ctx, token, path)
	if err != nil {
		return err
	}
	if status == http.StatusUnauthorized {
		return errUnauthorized
	}
	if status != http.StatusOK {
		msg := problemMessage(body)
		if msg == "" {
			msg = http.StatusText(status)
		}
		return &apiError{status: status, msg: fmt.Sprintf("api GET %s: %s", path, msg)}
	}
	return json.Unmarshal(body, out)
}

var errUnauthorized = errors.New("unauthorized")

// ---- pages ----

func (s *server) dashboard(w http.ResponseWriter, r *http.Request, tok string) {
	var st stateResp
	if err := s.getJSON(r.Context(), tok, "/v1/state", &st); err != nil {
		s.fail(w, r, err)
		return
	}
	var search listResp
	if err := s.getJSON(r.Context(), tok, "/v1/listings?limit=6", &search); err != nil {
		s.fail(w, r, err)
		return
	}
	// The search defaults to sale; a rent-only corpus is not "empty".
	total := search.Total
	var corpus corpusStats
	if err := s.getJSON(r.Context(), tok, "/v1/corpus", &corpus); err == nil {
		total = corpus.Listings
		if len(search.Listings) == 0 && corpus.ActiveRent > 0 {
			_ = s.getJSON(r.Context(), tok, "/v1/listings?limit=6&listing_type=rent", &search)
		}
	}
	var targets targetsResp
	_ = s.getJSON(r.Context(), tok, "/v1/crawl-targets", &targets)
	s.render(w, "dashboard.html", map[string]any{
		"Nav": "home", "Write": s.write, "State": st, "Total": total,
		"Recent": search.Listings, "Targets": targets.list(),
	})
}

func (s *server) listings(w http.ResponseWriter, r *http.Request, tok string) {
	q := r.URL.Query()
	params := url.Values{}
	params.Set("limit", "24")
	listingType := q.Get("listing_type")
	if listingType != "rent" {
		listingType = "sale"
	}
	params.Set("listing_type", listingType)
	if v := strings.TrimSpace(q.Get("min_beds")); v != "" {
		params.Set("min_beds", v)
	}
	if v := strings.TrimSpace(q.Get("max_price")); v != "" {
		params.Set("max_price", v)
	}
	off := offsetInt(q.Get("offset")) // pager links only; junk becomes page one
	if off > 0 {
		params.Set("offset", strconv.Itoa(off))
	}
	if q.Get("include_inactive") == "on" {
		params.Set("include_inactive", "true")
	}
	var res listResp
	if err := s.getJSON(r.Context(), tok, "/v1/listings?"+params.Encode(), &res); err != nil {
		s.fail(w, r, err)
		return
	}
	pageCount := res.Count // API rows before the in-page text filter
	// The API has no text search, so filter this page's rows by address/neighborhood here.
	if needle := strings.ToLower(strings.TrimSpace(q.Get("q"))); needle != "" {
		res.Listings = filterRows(res.Listings, needle)
	}
	// Build complete pager URLs here; a template-concatenated filter would be query-escaped and break the link.
	persist := url.Values{}
	for _, k := range []string{"q", "min_beds", "max_price"} {
		if v := strings.TrimSpace(q.Get(k)); v != "" {
			persist.Set(k, v)
		}
	}
	if listingType == "rent" {
		persist.Set("listing_type", "rent")
	}
	if q.Get("include_inactive") == "on" {
		persist.Set("include_inactive", "on")
	}
	pageURL := func(o int) string {
		p := url.Values{}
		maps.Copy(p, persist)
		if o > 0 {
			p.Set("offset", strconv.Itoa(o))
		}
		return "/listings?" + p.Encode()
	}
	data := map[string]any{
		"Nav": "listings", "Write": s.write, "Res": res, "Q": q.Get("q"),
		"MinBeds": q.Get("min_beds"), "MaxPrice": q.Get("max_price"),
		"ListingType": listingType,
		"Inactive":    q.Get("include_inactive") == "on",
		"Offset":      off,
		"HasPrev":     off > 0, "PrevURL": pageURL(off - 24),
		"HasNext": off+pageCount < res.Total, "NextURL": pageURL(off + 24),
	}
	// A history-restore request swaps the response into <body>, so it needs the full page.
	w.Header().Add("Vary", "HX-Request")
	if fragmentRequest(r) {
		s.render(w, "listings_rows.html", data)
		return
	}
	s.render(w, "listings.html", data)
}

func (s *server) listing(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.Atoi(id); err != nil {
		http.NotFound(w, r)
		return
	}
	var d detailResp
	if err := s.getJSON(r.Context(), tok, "/v1/listings/"+id, &d); err != nil {
		s.fail(w, r, err)
		return
	}
	data := map[string]any{"Nav": "listings", "Write": s.write, "L": d}
	if s.write { // all lists for the add-to-list picker, minus ones it's already in
		var p listsPayload
		if err := s.getJSON(r.Context(), tok, "/v1/lists", &p); err == nil {
			in := map[int64]bool{}
			for _, l := range d.Lists {
				in[l.ID] = true
			}
			var addable []consoleList
			for _, l := range p.Lists {
				if !in[l.ID] {
					addable = append(addable, l)
				}
			}
			data["AddableLists"] = addable
		}
	}
	s.render(w, "listing.html", data)
}

func (s *server) crawls(w http.ResponseWriter, r *http.Request, tok string) {
	var targets targetsResp
	if err := s.getJSON(r.Context(), tok, "/v1/crawl-targets", &targets); err != nil {
		s.fail(w, r, err)
		return
	}
	var corpus corpusStats
	_ = s.getJSON(r.Context(), tok, "/v1/corpus", &corpus) // header only; a failure just omits it

	list := targets.list()
	refresh := corpus.PendingCrawls > 0
	for _, t := range list {
		if t.InFlight() {
			refresh = true
		}
	}
	s.render(w, "crawls.html", map[string]any{
		"Nav": "crawls", "Write": s.write, "Targets": list, "Corpus": corpus,
		"Refresh": refresh, "Queued": r.URL.Query().Get("queued"), "Err": r.URL.Query().Get("err"),
	})
}

// ---- helpers ----

func (s *server) render(w http.ResponseWriter, name string, data any) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	if err := s.tmpl.ExecuteTemplate(w, name, data); err != nil {
		s.log.Error("render", "tmpl", name, "err", err)
	}
}

func (s *server) fail(w http.ResponseWriter, r *http.Request, err error) {
	if errors.Is(err, errUnauthorized) {
		http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, MaxAge: -1})
		toLogin(w, r)
		return
	}
	status := http.StatusBadGateway
	// Transport errors name the internal API host; the page gets a generic line.
	msg := "The console could not reach the API; see the console logs."
	var api *apiError
	if errors.As(err, &api) && api.status < 500 {
		status = api.status
		msg = err.Error()
		s.log.Warn("api rejected request", "path", r.URL.Path, "err", err)
	} else {
		s.log.Error("page failed", "path", r.URL.Path, "err", err)
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)
	if fragmentRequest(r) {
		s.render(w, "error_fragment.html", map[string]any{"Message": msg})
		return
	}
	s.render(w, "error.html", map[string]any{"Nav": false, "Message": msg})
}

// isHTTPS is true behind TLS or a proxy that says so; a Secure cookie set over
// plain http (a LAN box) is refused by the browser and looks like a login loop.
func checkAPIBase(api string) error {
	if api == "" {
		return errors.New("empty")
	}
	u, err := url.Parse(api)
	if err != nil {
		return err
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%q must be an http(s) URL with a host", api)
	}
	return nil
}

func offsetInt(s string) int {
	n, _ := strconv.Atoi(s)
	if n < 0 {
		return 0
	}
	return n
}

func filterRows(rows []listingRow, needle string) []listingRow {
	out := rows[:0]
	for _, r := range rows {
		if strings.Contains(strings.ToLower(r.Address), needle) || strings.Contains(strings.ToLower(r.Neighborhood), needle) {
			out = append(out, r)
		}
	}
	return out
}
