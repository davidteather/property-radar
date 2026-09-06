package mcp_test

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

type crawlTargetResult struct {
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
	LastError   string   `json:"last_error"`
}

type requestCrawlResult struct {
	JobID           int64             `json:"job_id"`
	Target          crawlTargetResult `json:"target"`
	AlreadyQueued   bool              `json:"already_queued"`
	PendingRequests int               `json:"pending_requests"`
	Message         string            `json:"message"`
}

type crawlRunResult struct {
	ID           int64 `json:"id"`
	Complete     bool  `json:"complete"`
	ListingsSeen int   `json:"listings_seen"`
}

type crawlStatusResult struct {
	JobID  int64             `json:"job_id"`
	Status string            `json:"status"`
	Target crawlTargetResult `json:"target"`
	Run    *crawlRunResult   `json:"run"`
}

type corpusStatsResult struct {
	Listings       int    `json:"listings"`
	ActiveListings int    `json:"active_listings"`
	PendingCrawls  int    `json:"pending_crawls"`
	StandingScopes int    `json:"standing_scopes"`
	LastCrawlAt    string `json:"last_crawl_at"`
}

type listCrawlTargetsResult struct {
	Count   int                 `json:"count"`
	Targets []crawlTargetResult `json:"targets"`
}

func TestRequestCrawlQueuesATargetAndListsIt(t *testing.T) {
	h := newHarness(t)

	queued := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"326", "305"}, "listing_type": "rent", "one_off": true,
		"max_price": 4500, "min_beds": 1, "note": "user asked about Fort Greene",
	}))
	if queued.Target.ID == 0 || queued.Target.Kind != string(domain.TargetOnce) {
		t.Fatalf("queued target = %+v", queued.Target)
	}
	if queued.Target.Status != string(domain.TargetPending) || queued.AlreadyQueued || queued.PendingRequests != 1 {
		t.Fatalf("queue state = %+v", queued)
	}
	if !slices.Equal(queued.Target.Areas, []string{"305", "326"}) {
		t.Fatalf("areas = %v, want them normalized", queued.Target.Areas)
	}
	if queued.Target.ListingType != string(domain.ListingRent) || queued.Target.MaxPrice == nil || *queued.Target.MaxPrice != 4500 {
		t.Fatalf("scope = %+v", queued.Target)
	}
	if queued.Target.LastRunID != nil || queued.Target.LastError != "" {
		t.Fatalf("fresh request already carries run state: %+v", queued.Target)
	}

	listed := decodeResult[listCrawlTargetsResult](t, h.mustCall("list_crawl_targets", map[string]any{}))
	if listed.Count != 1 || listed.Targets[0].ID != queued.Target.ID {
		t.Fatalf("list_crawl_targets = %+v", listed)
	}
	if listed.Targets[0].Note != "user asked about Fort Greene" {
		t.Fatalf("note not round-tripped: %+v", listed.Targets[0])
	}
}

func TestRequestCrawlDefaultsToStandingScope(t *testing.T) {
	h := newHarness(t)

	queued := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"305"}, "listing_type": "sale",
	}))
	if queued.Target.Kind != string(domain.TargetStanding) || !queued.Target.Enabled {
		t.Fatalf("default request = kind %s enabled %v, want a standing enabled scope", queued.Target.Kind, queued.Target.Enabled)
	}
	// A standing scope is not a queued one-off, so the pending-request count stays 0.
	if queued.PendingRequests != 0 {
		t.Fatalf("standing scope counted as a pending request: %d", queued.PendingRequests)
	}

	// Re-requesting the same standing scope is idempotent.
	again := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"305"}, "listing_type": "sale",
	}))
	if !again.AlreadyQueued || again.Target.ID != queued.Target.ID || !strings.Contains(again.Message, "already exists") {
		t.Fatalf("re-request = %+v, want the existing standing scope, said so", again)
	}
}

func TestRequestCrawlIsIdempotentForPendingRequests(t *testing.T) {
	h := newHarness(t)

	first := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"305"}, "listing_type": "sale", "max_price": 900000, "one_off": true,
	}))
	again := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{" 305 "}, "listing_type": "sale", "max_price": 900000, "note": "retry", "one_off": true,
	}))
	if !again.AlreadyQueued || again.Target.ID != first.Target.ID || again.PendingRequests != 1 {
		t.Fatalf("retry = %+v, want the existing pending row", again)
	}

	// A different filter is a different scope.
	other := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"305"}, "listing_type": "sale", "one_off": true,
	}))
	if other.AlreadyQueued || other.Target.ID == first.Target.ID || other.PendingRequests != 2 {
		t.Fatalf("unfiltered request = %+v, want a new row", other)
	}

	listed := decodeResult[listCrawlTargetsResult](t, h.mustCall("list_crawl_targets", map[string]any{}))
	if listed.Count != 2 {
		t.Fatalf("queue holds %d targets, want 2", listed.Count)
	}
}

func TestRequestCrawlRefusesAFullQueue(t *testing.T) {
	h := newHarness(t)

	for i := range 10 {
		h.mustCall("request_crawl", map[string]any{
			"areas": []string{fmt.Sprint(400 + i)}, "listing_type": "sale", "one_off": true,
		})
	}
	msg := h.mustFail("request_crawl", map[string]any{"areas": []string{"500"}, "listing_type": "sale", "one_off": true})
	if !strings.Contains(msg, "list_crawl_targets") {
		t.Fatalf("queue-cap error = %q, want it to point at list_crawl_targets", msg)
	}

	listed := decodeResult[listCrawlTargetsResult](t, h.mustCall("list_crawl_targets", map[string]any{}))
	if listed.Count != 10 {
		t.Fatalf("queue holds %d targets, want the cap of 10", listed.Count)
	}

	// Standing scopes are not queued requests, so the cap blocks neither creating nor re-enabling one.
	standing := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{"areas": []string{"305"}, "listing_type": "sale"}))
	if standing.AlreadyQueued || standing.PendingRequests != 10 {
		t.Fatalf("standing scope under a full queue = %+v, want a new scope with 10 still pending", standing)
	}
	h.mustCall("manage_crawl_target", map[string]any{"id": standing.Target.ID, "action": "pause"})
	again := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{"areas": []string{"305"}, "listing_type": "sale"}))
	if !again.AlreadyQueued || again.Target.ID != standing.Target.ID || !again.Target.Enabled {
		t.Fatalf("re-enable under a full queue = %+v, want the existing scope re-enabled", again)
	}
}

func TestRequestCrawlValidatesInput(t *testing.T) {
	h := newHarness(t)

	tests := []struct {
		name string
		args map[string]any
		want string
	}{
		{"unresolvable name", map[string]any{"areas": []string{"Nowheresville"}, "listing_type": "sale"}, "could not resolve"},
		{"no areas", map[string]any{"areas": []string{" "}, "listing_type": "sale"}, "empty"},
		{"too many areas", map[string]any{"areas": tooManyAreas(21), "listing_type": "sale"}, "at most 20"},
		{"bad listing type", map[string]any{"areas": []string{"305"}, "listing_type": "lease"}, "[sale rent]"},
		{"max price beyond int32", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "max_price": 3_000_000_000}, "exceeds the maximum"},
		{"min beds beyond int16", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "min_beds": 40000}, "exceeds the maximum"},
		{"zero max price", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "max_price": 0}, "max_price"},
		{"negative beds", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "min_beds": -1}, "min_beds"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if msg := h.mustFail("request_crawl", tt.args); !strings.Contains(msg, tt.want) {
				t.Fatalf("error = %q, want it to mention %q", msg, tt.want)
			}
		})
	}

	listed := decodeResult[listCrawlTargetsResult](t, h.mustCall("list_crawl_targets", map[string]any{}))
	if listed.Count != 0 {
		t.Fatalf("rejected requests were queued anyway: %+v", listed)
	}
}

// With a catalog loaded, a numeric id must exist in it and leading zeros collapse.
func TestRequestCrawlRejectsUnknownNumericAreaIDs(t *testing.T) {
	h := newHarnessWithAreas(t, []domain.Area{
		{ID: "300", Name: "Brooklyn", Borough: "Brooklyn", Level: 1, ParentID: "1"},
		{ID: "305", Name: "Park Slope", Borough: "Brooklyn", Level: 3, ParentID: "300"},
	})
	msg := h.mustFail("request_crawl", map[string]any{"areas": []string{"999999"}, "listing_type": "sale"})
	if !strings.Contains(msg, "999999 (no such area id)") || !strings.Contains(msg, "resolve_areas") {
		t.Fatalf("error = %q, want the unknown id named and resolve_areas suggested", msg)
	}
	got := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"0305", "Brooklyn"}, "listing_type": "sale",
	}))
	if !slices.Equal(got.Target.Areas, []string{"300", "305"}) {
		t.Fatalf("areas = %v, want [300 305]", got.Target.Areas)
	}
}

func TestListCrawlTargetsShowsRunOutcome(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	standing, err := h.store.CreateCrawlTarget(ctx, domain.CrawlTarget{
		Kind: domain.TargetStanding, Areas: []string{"304"}, ListingType: domain.ListingSale, Enabled: true,
	})
	if err != nil {
		t.Fatalf("create standing target: %v", err)
	}
	queued := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"326"}, "listing_type": "sale", "one_off": true,
	}))
	if err := h.store.MarkTargetRun(ctx, domain.CrawlTargetID(queued.Target.ID), h.runID, false, errAlreadyRun); err != nil {
		t.Fatalf("mark target run: %v", err)
	}

	listed := decodeResult[listCrawlTargetsResult](t, h.mustCall("list_crawl_targets", map[string]any{}))
	if listed.Count != 2 || listed.Targets[0].ID != int64(standing.ID) {
		t.Fatalf("standing target should sort first: %+v", listed)
	}
	ran := listed.Targets[1]
	if ran.Status != string(domain.TargetFailed) || ran.LastError == "" {
		t.Fatalf("failed request = %+v", ran)
	}
	if ran.LastRunID == nil || *ran.LastRunID != int64(h.runID) {
		t.Fatalf("last_run_id = %v, want %d", ran.LastRunID, h.runID)
	}
	if listed.Targets[0].Enabled != true || listed.Targets[0].Kind != string(domain.TargetStanding) {
		t.Fatalf("standing target = %+v", listed.Targets[0])
	}
}

func TestCrawlStatusFollowsTheJobLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	queued := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"326"}, "listing_type": "sale", "one_off": true,
	}))
	if queued.JobID == 0 || queued.JobID != queued.Target.ID {
		t.Fatalf("job_id = %d, want it set and equal to target.id %d", queued.JobID, queued.Target.ID)
	}

	pending := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": queued.JobID}))
	if pending.Status != "pending" || pending.Run != nil {
		t.Fatalf("queued status = %+v, want pending with no run", pending)
	}

	if err := h.store.MarkTargetStarted(ctx, domain.CrawlTargetID(queued.JobID)); err != nil {
		t.Fatalf("mark started: %v", err)
	}
	running := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": queued.JobID}))
	if running.Status != "running" {
		t.Fatalf("running status = %q", running.Status)
	}

	if err := h.store.MarkTargetRun(ctx, domain.CrawlTargetID(queued.JobID), h.runID, true, nil); err != nil {
		t.Fatalf("mark run: %v", err)
	}
	done := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": queued.JobID}))
	if done.Status != "done" || done.Run == nil || done.Run.ID != int64(h.runID) {
		t.Fatalf("done status = %+v, want done with run %d attached", done, h.runID)
	}

	if msg := h.mustFail("get_crawl_status", map[string]any{"job_id": 999999}); !strings.Contains(msg, "does not exist") {
		t.Fatalf("unknown job error = %q, want it to say the id does not exist", msg)
	}

	// The default (standing) request follows the same lifecycle: a model
	// polling it must see done after the first pass, not pending forever.
	standing := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"319"}, "listing_type": "sale",
	}))
	if err := h.store.MarkTargetStarted(ctx, domain.CrawlTargetID(standing.JobID)); err != nil {
		t.Fatalf("mark standing started: %v", err)
	}
	if got := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": standing.JobID})); got.Status != "running" {
		t.Fatalf("standing running status = %q", got.Status)
	}
	if err := h.store.MarkTargetRun(ctx, domain.CrawlTargetID(standing.JobID), h.runID, true, nil); err != nil {
		t.Fatalf("mark standing run: %v", err)
	}
	got := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": standing.JobID}))
	if got.Status != "done" || got.Run == nil || got.Target.Kind != "standing" {
		t.Fatalf("standing after first pass = %+v, want done with run attached", got)
	}
}

func TestCorpusStatsCountsQueueAndFreshness(t *testing.T) {
	h := newHarness(t)

	before := decodeResult[corpusStatsResult](t, h.mustCall("get_corpus_stats", map[string]any{}))
	h.mustCall("request_crawl", map[string]any{"areas": []string{"305"}, "listing_type": "sale", "one_off": true})
	after := decodeResult[corpusStatsResult](t, h.mustCall("get_corpus_stats", map[string]any{}))

	if after.PendingCrawls != before.PendingCrawls+1 {
		t.Fatalf("pending crawls %d -> %d, want +1", before.PendingCrawls, after.PendingCrawls)
	}
	if after.Listings < after.ActiveListings {
		t.Fatalf("listings %d < active %d", after.Listings, after.ActiveListings)
	}
}

func tooManyAreas(n int) []string {
	areas := make([]string, 0, n)
	for i := range n {
		areas = append(areas, fmt.Sprint(600+i))
	}
	return areas
}

var errAlreadyRun = fmt.Errorf("ingest run suspect: %d listings against a baseline of %d", 2, 400)
