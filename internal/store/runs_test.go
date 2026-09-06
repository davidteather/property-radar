package store_test

import (
	"context"
	"testing"

	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/store"
)

// runWithStats records one finished run for scope-a and returns nothing; the
// baseline is queried afterwards.
func runWithStats(t *testing.T, s *store.Store, stats ingest.RunStats) {
	t.Helper()
	r, err := s.CreateRun(context.Background(), testProvider, "scope-a", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	finishRun(t, s, r.ID, stats)
}

func baselineA(t *testing.T, s *store.Store) ingest.Baseline {
	t.Helper()
	b, err := s.BaselineVolume(context.Background(), testProvider, "scope-a")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	return b
}

// A genuine, lasting drop in volume (three consecutive suspect runs that agree
// with each other) becomes the new baseline; a single or unstable suspect run,
// or a stretch of empty pages, never does.
func TestBaselineAcceptsStableSuspectLevel(t *testing.T) {
	s := newStore(t)
	for range 4 {
		runWithStats(t, s, ingest.RunStats{Complete: true, ListingsSeen: 10})
	}
	suspect := func(seen int) ingest.RunStats {
		return ingest.RunStats{Complete: true, ListingsSeen: seen, Suspect: true}
	}

	runWithStats(t, s, suspect(2))
	runWithStats(t, s, suspect(2))
	if b := baselineA(t, s); b.Volume != 10 || b.Runs != 4 {
		t.Fatalf("baseline after two suspect runs = %+v, want the clean level 10 over 4 runs", b)
	}
	runWithStats(t, s, suspect(2))
	if b := baselineA(t, s); b.Volume != 2 || b.Runs != 3 {
		t.Fatalf("baseline after three stable suspect runs = %+v, want the accepted level 2 over 3 runs", b)
	}
	// The next run at the new level is clean and joins the accepted stretch;
	// the old 10s are no longer part of the picture.
	runWithStats(t, s, ingest.RunStats{Complete: true, ListingsSeen: 3})
	if b := baselineA(t, s); b.Volume != 2 || b.Runs != 4 {
		t.Fatalf("baseline after one clean run at the new level = %+v, want 2 over 4 runs", b)
	}
}

func TestBaselineIgnoresUnstableOrEmptySuspectRuns(t *testing.T) {
	s := newStore(t)
	for range 4 {
		runWithStats(t, s, ingest.RunStats{Complete: true, ListingsSeen: 10})
	}
	for _, seen := range []int{2, 5, 2} {
		runWithStats(t, s, ingest.RunStats{Complete: true, ListingsSeen: seen, Suspect: true})
	}
	if b := baselineA(t, s); b.Volume != 10 || b.Runs != 4 {
		t.Fatalf("baseline after an unstable suspect stretch = %+v, want 10 over 4 runs", b)
	}
	for range 3 {
		runWithStats(t, s, ingest.RunStats{Complete: true, ListingsSeen: 0, Suspect: true})
	}
	if b := baselineA(t, s); b.Volume != 10 || b.Runs != 4 {
		t.Fatalf("baseline after three empty suspect runs = %+v, want 10 over 4 runs (zero is never accepted)", b)
	}
}
