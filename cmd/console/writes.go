package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/davidteather/property-radar/internal/shared/xslices"
)

// apiError is a non-2xx answer from the REST API; fail maps its status back to
// the browser so a rejected input is a 4xx, not a 502.
type apiError struct {
	status int
	msg    string
}

func (e *apiError) Error() string { return e.msg }

// send performs a write against the REST API; a 401 becomes errUnauthorized and
// any other non-2xx an apiError carrying the API's problem detail, so handlers
// surface a rejected write instead of redirecting as if it succeeded.
func (s *server) send(ctx context.Context, token, method, path string, body any) (int, error) {
	status, msg, err := s.sendResult(ctx, token, method, path, body)
	switch {
	case err != nil:
		return status, err
	case status == http.StatusUnauthorized:
		return status, errUnauthorized
	case status >= 400:
		if msg == "" {
			msg = http.StatusText(status)
		}
		return status, &apiError{status: status, msg: fmt.Sprintf("api %s %s: %s", method, path, msg)}
	}
	return status, nil
}

// sendResult is send that also returns a human message: the API's problem
// "detail" on a 4xx (so the console can show why a crawl request was rejected),
// empty otherwise.
func (s *server) sendResult(ctx context.Context, token, method, path string, body any) (int, string, error) {
	var r io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, "", err
		}
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, s.api+path, r)
	if err != nil {
		return 0, "", err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := s.http.Do(req)
	if err != nil {
		return 0, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	msg := ""
	if resp.StatusCode >= 400 {
		msg = problemMessage(raw)
	}
	return resp.StatusCode, msg, nil
}

// problemMessage pulls the human line out of an RFC 9457 problem body.
func problemMessage(raw []byte) string {
	var problem struct {
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if json.Unmarshal(raw, &problem) != nil {
		return ""
	}
	if problem.Detail != "" {
		return problem.Detail
	}
	return problem.Title
}

// ---- read pages (available in both modes) ----

func (s *server) listsPage(w http.ResponseWriter, r *http.Request, tok string) {
	var p listsPayload
	if err := s.getJSON(r.Context(), tok, "/v1/lists", &p); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "lists.html", map[string]any{"Nav": "lists", "Write": s.write, "Lists": p.Lists})
}

func (s *server) listDetail(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.Atoi(id); err != nil {
		http.NotFound(w, r)
		return
	}
	var p listDetailPayload
	if err := s.getJSON(r.Context(), tok, "/v1/lists/"+id, &p); err != nil {
		s.fail(w, r, err)
		return
	}
	res := listResp{Listings: p.Listings, Total: len(p.Listings), Count: len(p.Listings)}
	s.render(w, "list_detail.html", map[string]any{
		"Nav": "lists", "Write": s.write, "List": p.List,
		"Res": res, "HasPrev": false, "HasNext": false,
	})
}

func (s *server) verdictsPage(w http.ResponseWriter, r *http.Request, tok string) {
	var p ratedPayload
	if err := s.getJSON(r.Context(), tok, "/v1/verdicts", &p); err != nil {
		s.fail(w, r, err)
		return
	}
	s.render(w, "verdicts.html", map[string]any{"Nav": "verdicts", "Write": s.write, "Rated": p})
}

func (s *server) settings(w http.ResponseWriter, r *http.Request, _ string) {
	s.render(w, "settings.html", map[string]any{"Nav": "settings", "Write": s.write})
}

// ---- write actions (registered only in write mode) ----

func (s *server) createList(w http.ResponseWriter, r *http.Request, tok string) {
	name := r.FormValue("name")
	body := map[string]string{"name": name, "emoji": r.FormValue("emoji")}
	if _, err := s.send(r.Context(), tok, http.MethodPost, "/v1/lists", body); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/lists", http.StatusSeeOther)
}

func (s *server) deleteList(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.send(r.Context(), tok, http.MethodDelete, "/v1/lists/"+id, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/lists", http.StatusSeeOther)
}

func (s *server) addToList(w http.ResponseWriter, r *http.Request, tok string) {
	listingID := r.PathValue("id")
	listID := r.FormValue("list_id")
	lid, err := strconv.ParseInt(listingID, 10, 64)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := strconv.ParseInt(listID, 10, 64); err != nil {
		http.Error(w, "list_id must be a number", http.StatusBadRequest)
		return
	}
	body := map[string]int64{"listing_id": lid}
	if _, err := s.send(r.Context(), tok, http.MethodPost, "/v1/lists/"+listID+"/items", body); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/listings/"+listingID, http.StatusSeeOther)
}

func (s *server) removeFromList(w http.ResponseWriter, r *http.Request, tok string) {
	listingID := r.PathValue("id")
	listID := r.PathValue("listID")
	if _, err := strconv.ParseInt(listingID, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := strconv.ParseInt(listID, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.send(r.Context(), tok, http.MethodDelete, "/v1/lists/"+listID+"/items/"+listingID, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/listings/"+listingID, http.StatusSeeOther)
}

// splitAreas keeps "Park Slope, Brooklyn" as one phrase (the resolver reads the
// borough as a qualifier); only id lists split on commas, names on ";" or newlines.
func splitAreas(v string) []string {
	if idList.MatchString(v) {
		return xslices.SplitCSV(v)
	}
	var out []string
	for _, part := range strings.FieldsFunc(v, func(r rune) bool { return r == ';' || r == '\n' || r == '\r' }) {
		if part = strings.TrimSpace(part); part != "" {
			out = append(out, part)
		}
	}
	return out
}

var idList = regexp.MustCompile(`^[\s\d,]+$`)

// queueCrawl posts a crawl request from the console's "areas of interest" form.
// Areas accept place names or numeric ids; the API resolves and validates, so a
// failure comes back as a message shown on the page.
func (s *server) queueCrawl(w http.ResponseWriter, r *http.Request, tok string) {
	areas := splitAreas(r.FormValue("areas"))
	if len(areas) == 0 {
		http.Redirect(w, r, "/crawls?err="+url.QueryEscape("Enter at least one neighborhood or area id."), http.StatusSeeOther)
		return
	}
	listingType := r.FormValue("listing_type")
	if listingType != "rent" {
		listingType = "sale"
	}
	body := map[string]any{"areas": areas, "listing_type": listingType}
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("max_price"))); err == nil && v > 0 {
		body["max_price"] = v
	}
	if v, err := strconv.Atoi(strings.TrimSpace(r.FormValue("min_beds"))); err == nil && v > 0 {
		body["min_beds"] = v
	}

	status, msg, err := s.sendResult(r.Context(), tok, http.MethodPost, "/v1/crawl-requests", body)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if status >= 400 {
		if msg == "" {
			msg = "Could not queue that crawl."
		}
		http.Redirect(w, r, "/crawls?err="+url.QueryEscape(msg), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/crawls?queued="+url.QueryEscape(strings.Join(areas, ", ")), http.StatusSeeOther)
}

// setCrawlEnabled pauses or resumes a standing scope from the console.
func (s *server) setCrawlEnabled(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	enabled := r.FormValue("enabled") == "true"
	if _, err := s.send(r.Context(), tok, http.MethodPatch, "/v1/crawl-targets/"+id, map[string]bool{"enabled": enabled}); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/crawls", http.StatusSeeOther)
}

// makeCrawlRecurring promotes a one-off scope to a recurring standing one so the
// crawler keeps re-crawling it, instead of it running exactly once.
func (s *server) makeCrawlRecurring(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.send(r.Context(), tok, http.MethodPatch, "/v1/crawl-targets/"+id, map[string]bool{"recurring": true}); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/crawls", http.StatusSeeOther)
}

// deleteCrawl removes a crawl scope from the console.
func (s *server) deleteCrawl(w http.ResponseWriter, r *http.Request, tok string) {
	id := r.PathValue("id")
	if _, err := strconv.ParseInt(id, 10, 64); err != nil {
		http.NotFound(w, r)
		return
	}
	if _, err := s.send(r.Context(), tok, http.MethodDelete, "/v1/crawl-targets/"+id, nil); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/crawls", http.StatusSeeOther)
}

func (s *server) resetData(w http.ResponseWriter, r *http.Request, tok string) {
	if r.FormValue("confirm") != "RESET" {
		http.Redirect(w, r, "/settings", http.StatusSeeOther)
		return
	}
	if _, err := s.send(r.Context(), tok, http.MethodPost, "/v1/reset", map[string]bool{"confirm": true}); err != nil {
		s.fail(w, r, err)
		return
	}
	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}
