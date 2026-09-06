package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

func createTarget(t *testing.T, s *store.Store, target domain.CrawlTarget) domain.CrawlTarget {
	t.Helper()
	out, err := s.CreateCrawlTarget(context.Background(), target)
	if err != nil {
		t.Fatalf("create crawl target: %v", err)
	}
	return out
}

func onceTarget(areas ...string) domain.CrawlTarget {
	return domain.CrawlTarget{Kind: domain.TargetOnce, Areas: areas, ListingType: domain.ListingSale}
}

func standingTarget(enabled bool, areas ...string) domain.CrawlTarget {
	return domain.CrawlTarget{Kind: domain.TargetStanding, Areas: areas, ListingType: domain.ListingSale, Enabled: enabled}
}

func targetIDs(targets []domain.CrawlTarget) []int64 {
	out := make([]int64, 0, len(targets))
	for _, t := range targets {
		out = append(out, int64(t.ID))
	}
	return out
}

func TestCreateCrawlTargetRoundTrip(t *testing.T) {
	s := newStore(t)

	created := createTarget(t, s, domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{" 319 ", "305", "", "305"},
		ListingType: domain.ListingRent,
		MaxPrice:    money(4500),
		MinBeds:     ptr.To(2),
		Note:        "user asked about Fort Greene rentals",
	})

	if created.ID == 0 || created.Status != domain.TargetPending || created.Kind != domain.TargetOnce {
		t.Fatalf("created target = %+v", created)
	}
	if !slices.Equal(created.Areas, []string{"305", "319"}) {
		t.Fatalf("areas = %v, want normalized [305 319]", created.Areas)
	}
	if created.ListingType != domain.ListingRent || created.MaxPrice == nil || *created.MaxPrice != 4500 {
		t.Fatalf("scope round-trip = %+v", created)
	}
	if created.MinBeds == nil || *created.MinBeds != 2 || created.Note != "user asked about Fort Greene rentals" {
		t.Fatalf("filters round-trip = %+v", created)
	}
	if created.LastRunID != nil || created.LastError != "" || created.CreatedAt.IsZero() {
		t.Fatalf("fresh target carries run state: %+v", created)
	}

	listed, err := s.ListCrawlTargets(context.Background())
	if err != nil {
		t.Fatalf("list crawl targets: %v", err)
	}
	if len(listed) != 1 || listed[0].ID != created.ID {
		t.Fatalf("listed = %+v", listed)
	}

	if _, err := s.CreateCrawlTarget(context.Background(), onceTarget(" ", "")); err == nil {
		t.Fatal("empty areas were accepted")
	}
}

func TestCrawlTargetOrdering(t *testing.T) {
	s := newStore(t)

	standingOn := createTarget(t, s, standingTarget(true, "305"))
	standingOff := createTarget(t, s, standingTarget(false, "306"))
	pendingOnce := createTarget(t, s, onceTarget("307"))
	ranOnce := createTarget(t, s, onceTarget("308"))
	standingLater := createTarget(t, s, standingTarget(true, "309"))

	runID := completeRun(t, s, 10)
	if err := s.MarkTargetRun(context.Background(), ranOnce.ID, runID, false, nil); err != nil {
		t.Fatalf("mark target run: %v", err)
	}

	listed, err := s.ListCrawlTargets(context.Background())
	if err != nil {
		t.Fatalf("list crawl targets: %v", err)
	}
	want := []int64{int64(standingOn.ID), int64(standingLater.ID), int64(pendingOnce.ID), int64(standingOff.ID), int64(ranOnce.ID)}
	if got := targetIDs(listed); !slices.Equal(got, want) {
		t.Fatalf("list order = %v, want %v (enabled standing, pending once, then the rest)", got, want)
	}

	due, err := s.DueCrawlTargets(context.Background(), 0)
	if err != nil {
		t.Fatalf("due crawl targets: %v", err)
	}
	// The drain runs pending one-offs (explicit requests) before standing
	// background scopes, so a queued request is never stuck behind a big crawl.
	wantDue := []int64{int64(pendingOnce.ID), int64(standingOn.ID), int64(standingLater.ID)}
	if got := targetIDs(due); !slices.Equal(got, wantDue) {
		t.Fatalf("due targets = %v, want %v (pending once first, then standing)", got, wantDue)
	}
}

func TestCreateCrawlTargetDedupesPendingOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	first := createTarget(t, s, domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{"305", "319"},
		ListingType: domain.ListingSale,
		MaxPrice:    money(900_000),
	})

	same := domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{"319", " 305 ", "319"},
		ListingType: domain.ListingSale,
		MaxPrice:    money(900_000),
		Note:        "retry",
	}
	again, err := s.CreateCrawlTarget(ctx, same)
	if !errors.Is(err, store.ErrDuplicateCrawlTarget) {
		t.Fatalf("duplicate request error = %v, want ErrDuplicateCrawlTarget", err)
	}
	if again.ID != first.ID || again.Note != first.Note {
		t.Fatalf("duplicate returned %+v, want the existing row %+v", again, first)
	}

	// A different filter is a different scope.
	other, err := s.CreateCrawlTarget(ctx, domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{"305", "319"},
		ListingType: domain.ListingSale,
		MaxPrice:    money(1_200_000),
	})
	if err != nil || other.ID == first.ID {
		t.Fatalf("differing max_price deduped: %+v, %v", other, err)
	}
	// So is a standing target over the same scope.
	if _, err := s.CreateCrawlTarget(ctx, standingTarget(true, "305", "319")); err != nil {
		t.Fatalf("standing target over a pending scope: %v", err)
	}

	// Once the request has run, the same scope may be requested again.
	runID := completeRun(t, s, 10)
	if err := s.MarkTargetRun(ctx, first.ID, runID, false, nil); err != nil {
		t.Fatalf("mark target run: %v", err)
	}
	requeued, err := s.CreateCrawlTarget(ctx, same)
	if err != nil {
		t.Fatalf("requeue after completion: %v", err)
	}
	if requeued.ID == first.ID {
		t.Fatal("requeue returned the completed row instead of a new pending one")
	}
}

func TestMarkTargetRunOutcomes(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	once := createTarget(t, s, onceTarget("305"))
	failing := createTarget(t, s, onceTarget("306"))
	standing := createTarget(t, s, standingTarget(true, "307"))
	runID := completeRun(t, s, 10)

	if err := s.MarkTargetRun(ctx, once.ID, runID, true, nil); err != nil {
		t.Fatalf("mark once success: %v", err)
	}
	if err := s.MarkTargetRun(ctx, failing.ID, runID, false, errors.New("ingest run suspect: 2 listings")); err != nil {
		t.Fatalf("mark once failure: %v", err)
	}
	if err := s.MarkTargetRun(ctx, standing.ID, runID, false, errors.New("provider search: 403")); err != nil {
		t.Fatalf("mark standing failure: %v", err)
	}

	byID := map[domain.CrawlTargetID]domain.CrawlTarget{}
	listed, err := s.ListCrawlTargets(ctx)
	if err != nil {
		t.Fatalf("list crawl targets: %v", err)
	}
	for _, target := range listed {
		byID[target.ID] = target
	}

	if got := byID[once.ID]; got.Status != domain.TargetDone || got.LastError != "" || got.LastRunID == nil || *got.LastRunID != runID {
		t.Fatalf("successful once target = %+v", got)
	}
	if got := byID[failing.ID]; got.Status != domain.TargetFailed || got.LastError == "" {
		t.Fatalf("failed once target = %+v", got)
	}
	if got := byID[standing.ID]; got.Status != domain.TargetFailed || got.LastError == "" || got.LastRunID == nil {
		t.Fatalf("standing target = %+v, want its latest pass failed with the error recorded", got)
	}

	if err := s.MarkTargetRun(ctx, domain.CrawlTargetID(9999), runID, false, nil); err == nil {
		t.Fatal("marking an unknown target succeeded")
	}
	// A crawl that never got a run row records no run id.
	if err := s.MarkTargetRun(ctx, standing.ID, 0, false, errors.New("create ingest run: down")); err != nil {
		t.Fatalf("mark without a run id: %v", err)
	}
	listed, err = s.ListCrawlTargets(ctx)
	if err != nil {
		t.Fatalf("list crawl targets: %v", err)
	}
	for _, target := range listed {
		if target.ID == standing.ID && target.LastRunID != nil {
			t.Fatalf("last_run_id = %v, want it cleared when no run was created", *target.LastRunID)
		}
	}
}
