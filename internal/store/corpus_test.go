package store_test

import (
	"context"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/pgtest"
)

func TestCorpusStatsListsActiveNeighborhoodsBusiestFirst(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	for i, hood := range []string{"Park Slope", "Park Slope", "Chelsea", "Astoria"} {
		sp := newSource("n" + string(rune('a'+i)))
		sp.Property.Address.Neighborhood = hood
		apply(t, s, runID, sp, baseTime)
	}
	// A sold listing does not count, and one without a neighborhood is skipped.
	sold := newSource("sold")
	sold.Property.Address.Neighborhood = "Astoria"
	sold.Property.Status = domain.StatusSold
	apply(t, s, runID, sold, baseTime)
	blank := newSource("blank")
	blank.Property.Address.Neighborhood = ""
	apply(t, s, runID, blank, baseTime)

	stats, err := s.CorpusStats(ctx)
	if err != nil {
		t.Fatalf("corpus stats: %v", err)
	}
	want := []domain.NeighborhoodCount{{Name: "Park Slope", Active: 2}, {Name: "Astoria", Active: 1}, {Name: "Chelsea", Active: 1}}
	if len(stats.Neighborhoods) != len(want) {
		t.Fatalf("neighborhoods = %+v, want %+v", stats.Neighborhoods, want)
	}
	for i := range want {
		if stats.Neighborhoods[i] != want[i] {
			t.Fatalf("neighborhoods[%d] = %+v, want %+v (all: %+v)", i, stats.Neighborhoods[i], want[i], stats.Neighborhoods)
		}
	}
}

func TestMatchNeighborhoodsIsCaseInsensitiveAndIgnoresStatus(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	sold := newSource("sold")
	sold.Property.Address.Neighborhood = "Astoria"
	sold.Property.Status = domain.StatusSold
	apply(t, s, runID, sold, baseTime)

	got, err := s.MatchNeighborhoods(context.Background(), []string{"ASTORIA", "Nowhere"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || got[0] != "astoria" {
		t.Fatalf("matched = %v, want [astoria] (lowercased, any status)", got)
	}
	if got, err = s.MatchNeighborhoods(context.Background(), nil); err != nil || got != nil {
		t.Fatalf("no names = %v, %v; want nil, nil", got, err)
	}
}

// The neighborhood filters compare lower(neighborhood); the index must match
// that expression or every filtered search scans the table.
func TestNeighborhoodIndexMatchesTheLowerPredicate(t *testing.T) {
	newStore(t)
	pool := pgtest.Pool(t)
	var def string
	err := pool.QueryRow(context.Background(),
		`SELECT indexdef FROM pg_indexes WHERE tablename = 'listings' AND indexdef LIKE '%lower(neighborhood)%'`).Scan(&def)
	if err != nil {
		t.Fatalf("no expression index on lower(neighborhood): %v", err)
	}
}
