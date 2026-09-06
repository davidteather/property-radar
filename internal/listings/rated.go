package listings

import (
	"context"
	"fmt"

	"github.com/davidteather/property-radar/internal/domain"
)

// RatedListing is one listing the user has judged: its compact row, the current (latest) verdict, and the full verdict history for it, newest first.
type RatedListing struct {
	Row     Row
	Verdict domain.VerdictKind
	Note    string
	History []domain.Verdict // newest first, so History[0].CreatedAt is when it was last judged
}

// RatedState is the taste-review view: the rubric plus every rated listing, ordered by most recently judged.
type RatedState struct {
	Rubric *domain.Rubric
	Stale  bool
	Rated  []RatedListing
}

// RatedListings returns the rubric and every listing that has a verdict, newest verdict first. A read over stored state; no ranking or interpretation.
func (s *Service) RatedListings(ctx context.Context) (RatedState, error) {
	profile, rubric, stale, verdicts, err := s.store.State(ctx)
	_ = profile
	if err != nil {
		return RatedState{}, fmt.Errorf("load state: %w", err)
	}
	// Group verdicts by listing; the store returns them oldest-first.
	history := map[domain.PropertyID][]domain.Verdict{}
	order := []domain.PropertyID{} // listings by first appearance (oldest verdict)
	for _, v := range verdicts {
		if _, seen := history[v.PropertyID]; !seen {
			order = append(order, v.PropertyID)
		}
		history[v.PropertyID] = append(history[v.PropertyID], v)
	}
	if len(order) == 0 {
		return RatedState{Rubric: rubric, Stale: stale}, nil
	}

	props, err := s.store.PropertiesByIDs(ctx, order)
	if err != nil {
		return RatedState{}, fmt.Errorf("load rated listings: %w", err)
	}
	rows, err := s.rows(ctx, props)
	if err != nil {
		return RatedState{}, err
	}
	byID := make(map[domain.PropertyID]Row, len(rows))
	for _, r := range rows {
		byID[r.ID] = r
	}

	// Emit most-recently-judged first: sort by latest verdict, since a listing re-rated later should move up.
	rated := make([]RatedListing, 0, len(order))
	for _, id := range order {
		hist := history[id]
		latest := hist[len(hist)-1]
		reversed := make([]domain.Verdict, len(hist))
		for i, v := range hist {
			reversed[len(hist)-1-i] = v
		}
		row, ok := byID[id]
		if !ok {
			continue // listing gone from the corpus; skip
		}
		rated = append(rated, RatedListing{
			Row: row, Verdict: latest.Kind, Note: latest.Note, History: reversed,
		})
	}
	sortRatedByLatest(rated, history)
	return RatedState{Rubric: rubric, Stale: stale, Rated: rated}, nil
}

func sortRatedByLatest(rated []RatedListing, history map[domain.PropertyID][]domain.Verdict) {
	latestAt := func(r RatedListing) domain.Verdict {
		h := history[r.Row.ID]
		return h[len(h)-1]
	}
	// Simple insertion sort by latest verdict CreatedAt desc; the rated set is small.
	for i := 1; i < len(rated); i++ {
		for j := i; j > 0 && latestAt(rated[j]).CreatedAt.After(latestAt(rated[j-1]).CreatedAt); j-- {
			rated[j], rated[j-1] = rated[j-1], rated[j]
		}
	}
}
