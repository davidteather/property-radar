package listings

import (
	"slices"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

func testAreaCatalog() []domain.Area {
	return []domain.Area{
		{Provider: "se", ID: "1", Name: "NYC and NJ", Level: 0},
		{Provider: "se", ID: "100", Name: "Manhattan", Borough: "Manhattan", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "135", Name: "All Upper West Side", Short: "upper-west-side", Borough: "Manhattan", Level: 2, ParentID: "100"},
		{Provider: "se", ID: "139", Name: "All Upper East Side", Borough: "Manhattan", Level: 2, ParentID: "100"},
		{Provider: "se", ID: "150", Name: "Harlem", Borough: "Manhattan", Level: 2, ParentID: "100"},
		{Provider: "se", ID: "154", Name: "Central Harlem", Borough: "Manhattan", Level: 3, ParentID: "150"},
		{Provider: "se", ID: "155", Name: "East Harlem", Borough: "Manhattan", Level: 3, ParentID: "150"},
		{Provider: "se", ID: "137", Name: "Upper West Side", Borough: "Manhattan", Level: 3, ParentID: "135"},
		{Provider: "se", ID: "157", Name: "West Village", Borough: "Manhattan", Level: 3, ParentID: "100"},
		{Provider: "se", ID: "300", Name: "Brooklyn", Borough: "Brooklyn", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "305", Name: "Park Slope", Borough: "Brooklyn", Level: 3, ParentID: "300"},
		{Provider: "se", ID: "400", Name: "Queens", Borough: "Queens", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "500", Name: "Bronx", Borough: "Bronx", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "600", Name: "Staten Island", Borough: "Staten Island", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "1000", Name: "New Jersey", Borough: "New Jersey", Level: 1, ParentID: "1"},
		{Provider: "se", ID: "1002100", Name: "West Side", Borough: "New Jersey", Level: 3, ParentID: "1000"},
	}
}

func TestResolveAreas(t *testing.T) {
	cat := testAreaCatalog()
	cases := []struct {
		query    string
		wantIDs  []string
		cityWide bool
	}{
		{"West Village", []string{"157"}, false},
		{"west village", []string{"157"}, false},
		{"Manhattan", []string{"100"}, false},
		{"all of Manhattan", []string{"100"}, false},
		{"Upper West Side", []string{"135"}, false}, // filler-stripped "All ..." region wins over descendant 137
		{"all of the upper west side", []string{"135"}, false},
		{"Park Slope", []string{"305"}, false},
		{"Park Slope, Brooklyn", []string{"305"}, false}, // phrase keeps the specific name, not the borough
		{"Upper West Side, Manhattan", []string{"135"}, false},
		{"Brooklyn and Queens", []string{"300", "400"}, false},
		{"Upper West Side and Upper East Side", []string{"135", "139"}, false}, // "and" must not partial-match "NYC and NJ"
		{"west side", []string{"1002100"}, false},
		{"Harlem", []string{"150"}, false},
		{"Harlem, Manhattan", []string{"150"}, false},    // borough qualifies; it does not widen to all of Manhattan
		{"West Side, Manhattan", []string{"100"}, false}, // qualifier excludes the NJ match; falls back to the borough
		{"the", nil, false},
		{"all", nil, false},
		{"", nil, false},
		{"park", []string{"305"}, false},
		{"the whole city", []string{"100", "300", "400", "500", "600"}, true},
		{"all of nyc", []string{"100", "300", "400", "500", "600"}, true},
		{"Narnia", nil, false},
		{"305", []string{"305"}, false}, // a bare id echoes its own catalog row
		{"999999", nil, false},
	}
	for _, c := range cases {
		got := resolveAreas(cat, c.query)
		ids := append([]string(nil), got.AreaIDs...)
		slices.Sort(ids)
		want := append([]string(nil), c.wantIDs...)
		slices.Sort(want)
		if !slices.Equal(ids, want) {
			t.Errorf("resolveAreas(%q) ids = %v, want %v", c.query, got.AreaIDs, c.wantIDs)
		}
		if got.CityWide != c.cityWide {
			t.Errorf("resolveAreas(%q) cityWide = %v, want %v", c.query, got.CityWide, c.cityWide)
		}
	}
}

func TestResolveAreaListMixesIDsAndNames(t *testing.T) {
	s := &Service{areas: testAreaCatalog()}
	ids, unresolved := s.ResolveAreaList([]string{"135", "West Village", "Narnia", "135"})
	slices.Sort(ids)
	if !slices.Equal(ids, []string{"135", "157"}) {
		t.Errorf("ids = %v, want [135 157] (deduped)", ids)
	}
	if !slices.Equal(unresolved, []string{"Narnia"}) {
		t.Errorf("unresolved = %v, want [Narnia]", unresolved)
	}
}

// A numeric id must exist in the catalog (a hallucinated one would become a
// scope that crawls nothing); leading zeros collapse; without a catalog ids pass.
func TestResolveAreaListRejectsUnknownNumericIDs(t *testing.T) {
	s := &Service{areas: testAreaCatalog()}
	ids, unresolved := s.ResolveAreaList([]string{"0135", "999999"})
	if !slices.Equal(ids, []string{"135"}) || !slices.Equal(unresolved, []string{"999999 (no such area id)"}) {
		t.Errorf("ids = %v unresolved = %v", ids, unresolved)
	}
	bare := &Service{}
	if ids, unresolved := bare.ResolveAreaList([]string{"999999"}); !slices.Equal(ids, []string{"999999"}) || len(unresolved) != 0 {
		t.Errorf("no catalog: ids = %v unresolved = %v, want pass-through", ids, unresolved)
	}
	if got, err := ParseAreaIDs([]string{"0305", "305"}); err != nil || !slices.Equal(got, []string{"305", "305"}) {
		t.Errorf("ParseAreaIDs = %v, %v", got, err)
	}
}

func TestResolveAreasEmptyCatalog(t *testing.T) {
	got := resolveAreas(nil, "Manhattan")
	if len(got.AreaIDs) != 0 {
		t.Errorf("empty catalog should resolve nothing, got %v", got.AreaIDs)
	}
}

// A bare fragment that only substring-matches several names ("East") is a
// preview for resolve_areas, not a scope: request_crawl must not fan out on it.
func TestResolveAreaListRefusesAmbiguousFragments(t *testing.T) {
	s := &Service{areas: testAreaCatalog()}
	ids, unresolved := s.ResolveAreaList([]string{"East"})
	if len(ids) != 0 || len(unresolved) != 1 || !strings.Contains(unresolved[0], "ambiguous") {
		t.Fatalf("ids = %v unresolved = %v, want the fragment reported as ambiguous", ids, unresolved)
	}
	// A fragment naming exactly one area still resolves (typo tolerance).
	if ids, unresolved := s.ResolveAreaList([]string{"Park Slop"}); !slices.Equal(ids, []string{"305"}) || len(unresolved) != 0 {
		t.Fatalf("ids = %v unresolved = %v, want [305]", ids, unresolved)
	}
	if r := resolveAreas(testAreaCatalog(), "East"); !r.Fuzzy || len(r.AreaIDs) < 2 {
		t.Fatalf("preview should still list the fuzzy matches: %+v", r)
	}
}
