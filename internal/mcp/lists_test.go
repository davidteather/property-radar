package mcp_test

import (
	"context"
	"strings"
	"testing"
)

type listResult struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	IsDefault bool   `json:"is_default"`
	Count     int    `json:"count"`
}

type createListResult struct {
	List          listResult `json:"list"`
	AlreadyExists bool       `json:"already_exists"`
}

type removeResult struct {
	OK      bool `json:"ok"`
	Removed bool `json:"removed"`
}

type addToListResult struct {
	List  listResult `json:"list"`
	Added bool       `json:"added"`
}

type getListResult struct {
	List     listResult         `json:"list"`
	Count    int                `json:"count"`
	Listings []listingRowResult `json:"listings"`
}

type getListsResult struct {
	Lists []listResult `json:"lists"`
	Count int          `json:"count"`
}

// The list tools are idempotent as advertised: a second add reports
// added=false without double-counting, removing a non-member is a no-op, and
// only a missing list or listing is an error.
func TestListToolsAreIdempotent(t *testing.T) {
	h := newHarness(t)
	id := h.apply(newSource("1001"), baseTime)
	// The truncating harness drops the seeded favorites list; mcpd restores it at startup.
	if err := h.store.EnsureDefaultList(context.Background()); err != nil {
		t.Fatalf("ensure favorites: %v", err)
	}

	created := decodeResult[createListResult](t, h.mustCall("create_list", map[string]any{"name": "Big Windows"}))
	if created.AlreadyExists || created.List.Slug != "big-windows" {
		t.Fatalf("create_list = %+v", created)
	}
	again := decodeResult[createListResult](t, h.mustCall("create_list", map[string]any{"name": "big windows"}))
	if !again.AlreadyExists || again.List.ID != created.List.ID {
		t.Fatalf("second create_list = %+v, want already_exists for the same list", again)
	}

	add := decodeResult[addToListResult](t, h.mustCall("add_to_list", map[string]any{"list": "Big Windows", "listing_id": id}))
	if !add.Added || add.List.Count != 1 {
		t.Fatalf("first add_to_list = %+v, want added=true count=1", add)
	}
	add = decodeResult[addToListResult](t, h.mustCall("add_to_list", map[string]any{"list": "big-windows", "listing_id": id, "note": "corner unit"}))
	if add.Added || add.List.Count != 1 {
		t.Fatalf("repeat add_to_list = %+v, want added=false count=1", add)
	}

	got := decodeResult[getListResult](t, h.mustCall("get_list", map[string]any{"list": "big-windows"}))
	if got.Count != 1 || len(got.Listings) != 1 || got.Listings[0].ID != int64(id) {
		t.Fatalf("get_list = %+v, want the one listing", got)
	}

	if msg := h.mustFail("add_to_list", map[string]any{"list": "quiet block", "listing_id": id}); !strings.Contains(msg, "create_list") {
		t.Errorf("add to a missing list error = %q, want a pointer to create_list", msg)
	}
	if msg := h.mustFail("add_to_list", map[string]any{"list": "big-windows", "listing_id": 4242}); !strings.Contains(msg, "4242") {
		t.Errorf("add a missing listing error = %q", msg)
	}

	if out := decodeResult[removeResult](t, h.mustCall("remove_from_list", map[string]any{"list": "big-windows", "listing_id": id})); !out.OK || !out.Removed {
		t.Errorf("first remove = %+v, want ok+removed", out)
	}
	if out := decodeResult[removeResult](t, h.mustCall("remove_from_list", map[string]any{"list": "big-windows", "listing_id": id})); !out.OK || out.Removed {
		t.Errorf("second remove = %+v, want ok but not removed", out)
	}
	if msg := h.mustFail("remove_from_list", map[string]any{"list": "quiet block", "listing_id": id}); !strings.Contains(msg, "quiet block") {
		t.Errorf("remove from a missing list error = %q", msg)
	}

	lists := decodeResult[getListsResult](t, h.mustCall("get_lists", map[string]any{}))
	if lists.Count != 2 {
		t.Fatalf("get_lists = %+v, want favorites plus big-windows", lists)
	}
	for _, l := range lists.Lists {
		if l.Count != 0 {
			t.Errorf("list %s count = %d after removal, want 0", l.Slug, l.Count)
		}
	}
}
