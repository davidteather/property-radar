package mcp_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

type manageCrawlTargetResult struct {
	ID      int64  `json:"id"`
	Action  string `json:"action"`
	Deleted bool   `json:"deleted"`
	Message string `json:"message"`
}

// manage_crawl_target drives the whole lifecycle of a scope: promote a one-off
// to recurring, pause it, resume it, and delete it.
func TestManageCrawlTargetLifecycle(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()

	once, err := h.store.CreateCrawlTarget(ctx, domain.CrawlTarget{
		Kind: domain.TargetOnce, Areas: []string{"305"},
		ListingType: domain.ListingSale, Status: domain.TargetDone,
	})
	if err != nil {
		t.Fatalf("seed crawl target: %v", err)
	}

	out := decodeResult[manageCrawlTargetResult](t, h.mustCall("manage_crawl_target", map[string]any{
		"id": int64(once.ID), "action": "make_recurring",
	}))
	if out.Deleted {
		t.Fatalf("make_recurring reported deleted: %+v", out)
	}
	if got, _ := h.store.GetCrawlTarget(ctx, once.ID); got.Kind != domain.TargetStanding || !got.Enabled {
		t.Fatalf("after make_recurring = kind %s enabled %v, want standing+enabled", got.Kind, got.Enabled)
	}

	h.mustCall("manage_crawl_target", map[string]any{"id": int64(once.ID), "action": "pause"})
	if got, _ := h.store.GetCrawlTarget(ctx, once.ID); got.Enabled {
		t.Fatal("scope still enabled after pause")
	}

	h.mustCall("manage_crawl_target", map[string]any{"id": int64(once.ID), "action": "resume"})
	if got, _ := h.store.GetCrawlTarget(ctx, once.ID); !got.Enabled {
		t.Fatal("scope not enabled after resume")
	}

	del := decodeResult[manageCrawlTargetResult](t, h.mustCall("manage_crawl_target", map[string]any{
		"id": int64(once.ID), "action": "delete",
	}))
	if !del.Deleted {
		t.Fatalf("delete did not report deleted: %+v", del)
	}
	if _, err := h.store.GetCrawlTarget(ctx, once.ID); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("scope lookup after delete = %v, want not-found", err)
	}
}

func TestManageCrawlTargetUnknownAction(t *testing.T) {
	h := newHarness(t)
	msg := h.mustFail("manage_crawl_target", map[string]any{"id": int64(1), "action": "explode"})
	// The schema enum rejects it before the handler and lists the valid actions.
	if !strings.Contains(msg, "explode") || !strings.Contains(msg, "make_recurring") {
		t.Fatalf("error = %q, want it to name the bad action and the valid ones", msg)
	}
}

func TestManageCrawlTargetRejectsPausingAOneOff(t *testing.T) {
	h := newHarness(t)

	once := decodeResult[requestCrawlResult](t, h.mustCall("request_crawl", map[string]any{
		"areas": []string{"305"}, "listing_type": "sale", "one_off": true,
	}))
	for _, action := range []string{"pause", "resume"} {
		msg := h.mustFail("manage_crawl_target", map[string]any{"id": once.JobID, "action": action})
		if !strings.Contains(msg, "one-off") || !strings.Contains(msg, "make_recurring") {
			t.Fatalf("%s one-off error = %q, want it to explain delete/make_recurring", action, msg)
		}
	}
	// Still pending: the rejected calls changed nothing.
	if got := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": once.JobID})); got.Status != "pending" {
		t.Fatalf("status after rejected pause = %q, want pending", got.Status)
	}

	// Once promoted, the same id can be paused and resumed, and promoting again is refused.
	h.mustCall("manage_crawl_target", map[string]any{"id": once.JobID, "action": "make_recurring"})
	if msg := h.mustFail("manage_crawl_target", map[string]any{"id": once.JobID, "action": "make_recurring"}); !strings.Contains(msg, "already standing") {
		t.Fatalf("second make_recurring = %q, want already standing", msg)
	}
	paused := decodeResult[manageCrawlTargetResult](t, h.mustCall("manage_crawl_target", map[string]any{"id": once.JobID, "action": "pause"}))
	if !strings.Contains(paused.Message, "Paused") {
		t.Fatalf("pause message = %q", paused.Message)
	}
	if got := decodeResult[crawlStatusResult](t, h.mustCall("get_crawl_status", map[string]any{"job_id": once.JobID})); got.Target.Enabled {
		t.Fatal("promoted target still enabled after pause")
	}
}

func TestCrawlTargetErrorsSayWhereIDsComeFrom(t *testing.T) {
	h := newHarness(t)

	if msg := h.mustFail("get_crawl_status", map[string]any{"job_id": 424242}); !strings.Contains(msg, "424242 does not exist") || !strings.Contains(msg, "list_crawl_targets") {
		t.Fatalf("get_crawl_status unknown = %q", msg)
	}
	for _, action := range []string{"pause", "resume", "make_recurring", "delete"} {
		msg := h.mustFail("manage_crawl_target", map[string]any{"id": 424242, "action": action})
		if !strings.Contains(msg, "424242 does not exist") || !strings.Contains(msg, "request_crawl") {
			t.Fatalf("%s unknown = %q", action, msg)
		}
	}
}

func TestCorpusStatsListsNeighborhoodSpellings(t *testing.T) {
	h := newHarness(t)

	for i, hood := range []string{"Park Slope", "Park Slope", "Cobble Hill"} {
		sp := newSource("hood" + string(rune('a'+i)))
		sp.Property.Address.Neighborhood = hood
		h.apply(sp, baseTime)
	}
	type hoodCount struct {
		Name   string `json:"name"`
		Active int    `json:"active"`
	}
	stats := decodeResult[struct {
		Neighborhoods []hoodCount `json:"neighborhoods"`
	}](t, h.mustCall("get_corpus_stats", map[string]any{}))
	want := []hoodCount{{"Park Slope", 2}, {"Cobble Hill", 1}}
	if len(stats.Neighborhoods) != 2 || stats.Neighborhoods[0] != want[0] || stats.Neighborhoods[1] != want[1] {
		t.Fatalf("neighborhoods = %+v, want %+v", stats.Neighborhoods, want)
	}

	// The spellings round-trip into the search filter verbatim.
	rows := decodeResult[searchResult](t, h.mustCall("search_listings", map[string]any{"neighborhoods": []string{"Cobble Hill"}}))
	if rows.Count != 1 {
		t.Fatalf("search by listed neighborhood returned %d rows, want 1", rows.Count)
	}
}
