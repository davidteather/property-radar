package store_test

import (
	"context"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

func TestResetTasteStateClearsTasteAndAreaPreferences(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// Corpus + ingest history that must survive.
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)
	// An area preference (crawl scope) that must be WIPED — a reset must never
	// keep looking for the old areas.
	if _, err := s.CreateCrawlTarget(ctx, domain.CrawlTarget{
		Kind:        domain.TargetOnce,
		Areas:       []string{"305"},
		ListingType: domain.ListingSale,
		Status:      domain.TargetPending,
		Note:        "drop me",
	}); err != nil {
		t.Fatalf("create crawl target: %v", err)
	}

	// Taste state that must be wiped.
	v1, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictLove, "great light")
	if err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if _, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictMaybe, "on the fence"); err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if _, err := s.AppendRubric(ctx, "likes light", v1.ID); err != nil {
		t.Fatalf("append rubric: %v", err)
	}
	if _, err := s.MarkShown(ctx, []domain.PropertyID{created.PropertyID}, nil); err != nil {
		t.Fatalf("mark shown: %v", err)
	}
	if _, err := s.SetProfile(ctx, domain.Profile{MinBeds: ptr.To(2), ListingType: domain.ListingSale}); err != nil {
		t.Fatalf("set profile: %v", err)
	}

	counts, err := s.ResetTasteState(ctx)
	if err != nil {
		t.Fatalf("reset taste state: %v", err)
	}
	if counts.Verdicts != 2 || counts.Rubrics != 1 || counts.Shown != 1 || counts.Profile != 1 || counts.CrawlTargets != 1 {
		t.Fatalf("reset counts = %+v, want {Verdicts:2 Rubrics:1 Shown:1 Profile:1 CrawlTargets:1}", counts)
	}

	// The taste tables are empty and get_state is back to zero.
	profile, rubric, stale, verdicts, err := s.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if rubric != nil || stale || len(verdicts) != 0 {
		t.Fatalf("post-reset state = rubric %v stale %v verdicts %d, want zero", rubric, stale, len(verdicts))
	}
	if profile.MinBeds != nil || profile.ListingType != domain.ListingSale {
		t.Fatalf("post-reset profile = %+v, want the zero profile", profile)
	}

	// The corpus survived and the formerly-verdicted, formerly-shown listing is a
	// fresh candidate again.
	if n, err := s.ActiveCount(ctx); err != nil || n != 1 {
		t.Fatalf("active count = %d err %v, want the listing kept", n, err)
	}
	if _, _, _, _, err := s.GetProperty(ctx, created.PropertyID); err != nil {
		t.Fatalf("get property after reset: %v", err)
	}
	cands := candidateIDs(t, s, domain.Profile{ListingType: domain.ListingSale})
	if !equalIDs(cands, []domain.PropertyID{created.PropertyID}) {
		t.Fatalf("candidates after reset = %v, want the listing to reappear", cands)
	}

	// crawl_targets (area preferences) were wiped — the crawler no longer knows
	// about the old areas.
	targets, err := s.ListCrawlTargets(ctx)
	if err != nil {
		t.Fatalf("list crawl targets: %v", err)
	}
	if len(targets) != 0 {
		t.Fatalf("crawl targets after reset = %+v, want none", targets)
	}
}

func TestResetTasteStateEmptyIsZeroCounts(t *testing.T) {
	s := newStore(t)
	counts, err := s.ResetTasteState(context.Background())
	if err != nil {
		t.Fatalf("reset taste state: %v", err)
	}
	if (counts.Verdicts | counts.Rubrics | counts.Shown | counts.Profile | counts.CrawlTargets) != 0 {
		t.Fatalf("reset on empty db = %+v, want all zero", counts)
	}
}
