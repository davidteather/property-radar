package store_test

import (
	"context"
	"errors"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/store"
)

func TestListsCRUDAndMembership(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	if err := s.EnsureDefaultList(ctx); err != nil {
		t.Fatalf("ensure default list: %v", err)
	}
	a := apply(t, s, runID, newSource("2001"), baseTime).PropertyID
	b := apply(t, s, runID, newSource("2002"), baseTime).PropertyID

	lists, err := s.Lists(ctx)
	if err != nil {
		t.Fatalf("lists: %v", err)
	}
	if len(lists) != 1 || lists[0].Slug != "favorites" || !lists[0].IsDefault {
		t.Fatalf("seeded lists = %+v, want one default favorites", lists)
	}
	fav := lists[0]

	big, err := s.CreateList(ctx, "Big Windows", "🪟")
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	if big.Slug != "big-windows" || big.Emoji != "🪟" {
		t.Fatalf("created list = %+v, want slug big-windows with emoji", big)
	}

	// Duplicate slug returns the existing list with ErrDuplicateList.
	dup, err := s.CreateList(ctx, "big windows", "")
	if !errors.Is(err, store.ErrDuplicateList) || dup.ID != big.ID {
		t.Fatalf("duplicate create = (%+v, %v), want existing list + ErrDuplicateList", dup, err)
	}

	for _, id := range []domain.PropertyID{a, b} {
		if added, err := s.AddToList(ctx, big.ID, id, ""); err != nil || !added {
			t.Fatalf("add %d to big = (%v, %v), want newly added", id, added, err)
		}
	}
	if added, err := s.AddToList(ctx, big.ID, a, "second time"); err != nil || added {
		t.Fatalf("re-add a to big = (%v, %v), want already a member", added, err)
	}
	if _, err := s.AddToList(ctx, fav.ID, a, "love it"); err != nil {
		t.Fatalf("add to favorites: %v", err)
	}
	if _, err := s.AddToList(ctx, big.ID+1000, a, ""); !errors.Is(err, store.ErrListNotFound) {
		t.Fatalf("add to a missing list = %v, want ErrListNotFound", err)
	}

	members, err := s.ListMembers(ctx, big.ID)
	if err != nil {
		t.Fatalf("members: %v", err)
	}
	if len(members) != 2 {
		t.Fatalf("big-windows members = %d, want 2", len(members))
	}

	lists, _ = s.Lists(ctx)
	count := map[string]int{}
	for _, l := range lists {
		count[l.Slug] = l.Count
	}
	if count["big-windows"] != 2 || count["favorites"] != 1 {
		t.Fatalf("counts = %v, want big-windows 2, favorites 1", count)
	}

	forA, err := s.ListsForProperty(ctx, a)
	if err != nil {
		t.Fatalf("lists for property: %v", err)
	}
	if len(forA) != 2 {
		t.Fatalf("lists for a = %d, want 2", len(forA))
	}

	if removed, err := s.RemoveFromList(ctx, big.ID, a); err != nil || !removed {
		t.Fatalf("remove = %v, %v; want removed", removed, err)
	}
	if removed, err := s.RemoveFromList(ctx, big.ID, a); err != nil || removed {
		t.Fatalf("remove a non-member = %v, %v; want a reported no-op", removed, err)
	}
	if _, err := s.RemoveFromList(ctx, big.ID+1000, a); !errors.Is(err, store.ErrListNotFound) {
		t.Fatalf("remove from a missing list = %v, want ErrListNotFound", err)
	}
	if m, _ := s.ListMembers(ctx, big.ID); len(m) != 1 {
		t.Fatalf("after remove, members = %d, want 1", len(m))
	}

	if err := s.DeleteList(ctx, fav.ID); !errors.Is(err, store.ErrDefaultList) {
		t.Fatalf("delete default = %v, want ErrDefaultList", err)
	}
	if err := s.DeleteList(ctx, big.ID); err != nil {
		t.Fatalf("delete custom list: %v", err)
	}
	if _, err := s.GetList(ctx, big.ID); !errors.Is(err, store.ErrListNotFound) {
		t.Fatalf("get deleted list = %v, want ErrListNotFound", err)
	}
}

func TestSlugifyAcceptsAnyScript(t *testing.T) {
	for name, want := range map[string]string{
		"Big Windows ✨":  "big-windows",
		"お気に入り":          "お気に入り",
		"Избранное 2024": "избранное-2024",
		"   ":            "",
	} {
		if got := store.Slugify(name); got != want {
			t.Errorf("Slugify(%q) = %q, want %q", name, got, want)
		}
	}
	// Emoji-only names still get a stable, distinct slug.
	heart, star := store.Slugify("❤️"), store.Slugify("⭐")
	if heart == "" || star == "" || heart == star || heart != store.Slugify("❤️") {
		t.Fatalf("emoji slugs = %q, %q; want stable and distinct", heart, star)
	}
}

func TestDeleteListReportsMissing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	l, err := s.CreateList(ctx, "Temp", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteList(ctx, l.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteList(ctx, l.ID); !errors.Is(err, store.ErrListNotFound) {
		t.Fatalf("second delete = %v, want ErrListNotFound", err)
	}
}

func TestResetClearsListsButKeepsFavorites(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	if err := s.EnsureDefaultList(ctx); err != nil {
		t.Fatalf("ensure default list: %v", err)
	}
	a := apply(t, s, runID, newSource("2003"), baseTime).PropertyID
	custom, err := s.CreateList(ctx, "Great Yards", "🌳")
	if err != nil {
		t.Fatalf("create list: %v", err)
	}
	if _, err := s.AddToList(ctx, custom.ID, a, ""); err != nil {
		t.Fatalf("add: %v", err)
	}
	if lists, _ := s.Lists(ctx); len(lists) != 2 {
		t.Fatalf("pre-reset lists = %d, want 2", len(lists))
	}

	if _, err := s.ResetTasteState(ctx); err != nil {
		t.Fatalf("reset: %v", err)
	}

	after, _ := s.Lists(ctx)
	if len(after) != 1 || after[0].Slug != "favorites" || after[0].Count != 0 {
		t.Fatalf("post-reset lists = %+v, want only empty favorites", after)
	}
}
