package listings

import (
	"context"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/davidteather/property-radar/internal/domain"
)

// ResolvedArea is one geographic match for a human query, provider-agnostic.
type ResolvedArea struct {
	ID      string
	Name    string
	Borough string
	Level   int
}

// AreaResolution is the outcome of resolving a human place phrase to a minimal set of provider area ids ready to hand to request_crawl.
type AreaResolution struct {
	Query    string
	CityWide bool
	Areas    []ResolvedArea
	AreaIDs  []string
	// Fuzzy marks a substring-only match ("East" inside several names): a preview, not a name the caller gave.
	Fuzzy bool
}

// LoadAreas installs the provider area catalog the resolver matches against; until called, ResolveAreas returns no matches.
func (s *Service) LoadAreas(areas []domain.Area) { s.areas = areas }

// ResolveAreas turns a human place phrase ("Upper West Side", "all of Manhattan") into a minimal covering set of provider area ids. A deterministic lookup over the catalog — no ranking model.
func (s *Service) ResolveAreas(_ context.Context, query string) (AreaResolution, error) {
	return resolveAreas(s.areas, query), nil
}

var numericArea = regexp.MustCompile(`^[0-9]+$`)

// ResolveAreaList maps a mixed list of area ids and place names to numeric area ids (ids pass through; names are resolved and expanded), reporting any names that matched nothing.
func (s *Service) ResolveAreaList(entries []string) (ids []string, unresolved []string) {
	seen := map[string]bool{}
	add := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, e := range entries {
		e = strings.TrimSpace(e)
		if e == "" {
			continue
		}
		if numericArea.MatchString(e) {
			id := canonicalAreaID(e)
			// A hallucinated id would become a scope that crawls nothing.
			if len(s.areas) > 0 && !hasArea(s.areas, id) {
				unresolved = append(unresolved, e+" (no such area id)")
				continue
			}
			add(id)
			continue
		}
		r := resolveAreas(s.areas, e)
		if len(r.AreaIDs) == 0 {
			unresolved = append(unresolved, e)
			continue
		}
		if r.Fuzzy && len(r.AreaIDs) > 1 {
			unresolved = append(unresolved, fmt.Sprintf("%s (ambiguous: %d areas contain it, e.g. %s)", e, len(r.AreaIDs), r.Areas[0].Name))
			continue
		}
		for _, id := range r.AreaIDs {
			add(id)
		}
	}
	return ids, unresolved
}

// canonicalAreaID strips leading zeros so "0305" and "305" dedupe to one scope.
func canonicalAreaID(id string) string {
	if t := strings.TrimLeft(id, "0"); t != "" {
		return t
	}
	return "0"
}

func hasArea(catalog []domain.Area, id string) bool {
	for _, a := range catalog {
		if a.ID == id {
			return true
		}
	}
	return false
}

var (
	nonWord     = regexp.MustCompile(`[^a-z0-9 ]+`)
	multiSpace  = regexp.MustCompile(`\s+`)
	fillerWords = map[string]bool{
		"all": true, "of": true, "the": true, "entire": true, "whole": true,
		"greater": true, "area": true, "areas": true, "neighborhood": true,
		"neighborhoods": true, "nabe": true, "in": true, "and": true, "or": true,
		"plus": true, "with": true,
	}
	nycBoroughSet = map[string]bool{
		"manhattan": true, "brooklyn": true, "queens": true,
		"bronx": true, "the bronx": true, "staten island": true,
	}
)

func normalizeArea(s string) string {
	s = nonWord.ReplaceAllString(strings.ToLower(strings.TrimSpace(s)), " ")
	return strings.TrimSpace(multiSpace.ReplaceAllString(s, " "))
}

// areaKey is the catalog name with the same filler stripped from queries, so
// "All Upper West Side" and "Upper Manhattan" match the way people say them.
func areaKey(a domain.Area) string { return stripFiller(normalizeArea(a.Name)) }

func stripFiller(s string) string {
	out := make([]string, 0)
	for _, w := range strings.Fields(s) {
		if !fillerWords[w] {
			out = append(out, w)
		}
	}
	return strings.Join(out, " ")
}

func resolveAreas(catalog []domain.Area, query string) AreaResolution {
	res := AreaResolution{Query: query}
	if len(catalog) == 0 {
		return res
	}
	q := normalizeArea(query)
	clean := stripFiller(q)

	// City-wide: the whole of NYC -> the five boroughs (never NJ). Only an
	// explicit phrase qualifies; filler alone ("the", "all") matches nothing.
	if clean == "nyc" || clean == "new york" || clean == "new york city" ||
		clean == "city" || clean == "boroughs" || strings.Contains(q, "all of nyc") ||
		strings.Contains(q, "entire city") || strings.Contains(q, "whole city") {
		res.CityWide = true
		res.Areas = nycBoroughs(catalog)
		res.AreaIDs = areaIDs(res.Areas)
		return res
	}
	if clean == "" {
		return res
	}
	// A numeric id resolves to its own catalog row, so a model can check what
	// an id it saw in list_crawl_targets stands for.
	if numericArea.MatchString(clean) {
		for _, a := range catalog {
			if a.ID == clean {
				res.Areas = toResolved([]domain.Area{a})
				res.AreaIDs = areaIDs(res.Areas)
				break
			}
		}
		return res
	}

	// Exact name/short match, else two-way substring.
	var matched []domain.Area
	for _, a := range catalog {
		if areaKey(a) == clean || (a.Short != "" && normalizeArea(a.Short) == clean) {
			matched = append(matched, a)
		}
	}
	if len(matched) > 0 || len(clean) < 3 {
		res.Areas = toResolved(reduceToRoots(catalog, matched))
		res.AreaIDs = areaIDs(res.Areas)
		return res
	}
	// "park" names several areas: keep their roots. "Park Slope, Brooklyn" names a
	// chain of areas: keep the most specific name, or the phrase widens to a borough.
	phrased := map[string][]domain.Area{}
	for _, a := range catalog {
		if n := areaKey(a); containsPhrase(clean, n) {
			phrased[n] = append(phrased[n], a)
		}
	}
	if len(phrased) == 0 {
		var partial []domain.Area
		for _, a := range catalog {
			if strings.Contains(areaKey(a), clean) {
				partial = append(partial, a)
			}
		}
		res.Areas = toResolved(reduceToRoots(catalog, partial))
		res.AreaIDs = areaIDs(res.Areas)
		res.Fuzzy = true
		return res
	}
	// "West Side" inside "Upper West Side" is not its own match: keep the longest phrases.
	for n := range phrased {
		for m := range phrased {
			if m != n && containsPhrase(m, n) {
				delete(phrased, n)
				break
			}
		}
	}
	// Words no phrase covered ("Harlem" in "Harlem, Manhattan") match names
	// containing them, so a nickname does not silently widen to the borough.
	remainder := clean
	for n := range phrased {
		remainder = removePhrase(remainder, n)
	}
	var named, partial []domain.Area
	for _, group := range phrased {
		named = append(named, reduceToRoots(catalog, group)...)
	}
	if len(remainder) >= 3 {
		for _, a := range catalog {
			if containsPhrase(areaKey(a), remainder) {
				partial = append(partial, a)
			}
		}
	}
	matched = append(reduceToRoots(catalog, partial), reduceToLeaves(catalog, named)...)
	matched = applyBoroughQualifier(matched)
	res.Areas = toResolved(reduceToRoots(catalog, matched))
	res.AreaIDs = areaIDs(res.Areas)
	return res
}

// applyBoroughQualifier treats a borough named alongside specific areas as a
// qualifier: keep only areas in that borough, and drop the borough itself when
// any survive ("Harlem, Manhattan" is Harlem; "Brooklyn and Queens" stays both).
func applyBoroughQualifier(matched []domain.Area) []domain.Area {
	boroughs := map[string]bool{}
	for _, a := range matched {
		if a.Level == 1 {
			boroughs[normalizeArea(a.Borough)] = true
		}
	}
	if len(boroughs) == 0 {
		return matched
	}
	var specific, wide []domain.Area
	for _, a := range matched {
		switch {
		case a.Level == 1:
			wide = append(wide, a)
		case boroughs[normalizeArea(a.Borough)]:
			specific = append(specific, a)
		}
	}
	if len(specific) == 0 {
		return wide
	}
	return specific
}

// containsPhrase reports whether needle appears in hay as whole words.
func containsPhrase(hay, needle string) bool {
	return needle != "" && strings.Contains(" "+hay+" ", " "+needle+" ")
}

func removePhrase(hay, needle string) string {
	out := strings.ReplaceAll(" "+hay+" ", " "+needle+" ", " ")
	return strings.TrimSpace(multiSpace.ReplaceAllString(out, " "))
}

// reduceToRoots drops any match that descends from another match, so "Upper West Side" (a region) is returned once rather than alongside its sub-areas.
func reduceToRoots(catalog, matched []domain.Area) []domain.Area {
	return reduceMatches(catalog, matched, false)
}

// reduceToLeaves drops any match that is an ancestor of another match.
func reduceToLeaves(catalog, matched []domain.Area) []domain.Area {
	return reduceMatches(catalog, matched, true)
}

func reduceMatches(catalog, matched []domain.Area, keepLeaves bool) []domain.Area {
	parent := make(map[string]string, len(catalog))
	for _, a := range catalog {
		parent[a.ID] = a.ParentID
	}
	inMatch := make(map[string]bool, len(matched))
	for _, a := range matched {
		inMatch[a.ID] = true
	}
	hasMatchedAncestor := func(id string) bool {
		for p := parent[id]; p != ""; p = parent[p] {
			if inMatch[p] {
				return true
			}
		}
		return false
	}
	isAncestorOfMatch := func(id string) bool {
		for _, a := range matched {
			if a.ID != id {
				for p := parent[a.ID]; p != ""; p = parent[p] {
					if p == id {
						return true
					}
				}
			}
		}
		return false
	}
	seen := map[string]bool{}
	var out []domain.Area
	for _, a := range matched {
		if seen[a.ID] {
			continue
		}
		seen[a.ID] = true
		drop := hasMatchedAncestor(a.ID)
		if keepLeaves {
			drop = isAncestorOfMatch(a.ID)
		}
		if !drop {
			out = append(out, a)
		}
	}
	return out
}

func nycBoroughs(catalog []domain.Area) []ResolvedArea {
	var out []ResolvedArea
	for _, a := range catalog {
		if a.Level == 1 && nycBoroughSet[normalizeArea(a.Borough)] {
			out = append(out, ResolvedArea{ID: a.ID, Name: a.Name, Borough: a.Borough, Level: a.Level})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func toResolved(areas []domain.Area) []ResolvedArea {
	out := make([]ResolvedArea, 0, len(areas))
	for _, a := range areas {
		out = append(out, ResolvedArea{ID: a.ID, Name: a.Name, Borough: a.Borough, Level: a.Level})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Level != out[j].Level {
			return out[i].Level < out[j].Level
		}
		return out[i].Name < out[j].Name
	})
	return out
}

func areaIDs(areas []ResolvedArea) []string {
	out := make([]string, len(areas))
	for i, a := range areas {
		out[i] = a.ID
	}
	return out
}
