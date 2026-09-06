package listings

import (
	"math"
	"strings"

	"github.com/davidteather/property-radar/internal/domain"
)

const (
	// Queue-bomb guard: a caller must not be able to queue the whole city.
	MaxPendingRequests = 10
	MaxRequestAreas    = 20

	// Write-size guards so one call cannot pin the database or bloat a rubric row.
	MaxVerdictBatch = 200
	MaxRubricBytes  = 64 << 10
	MaxVerdictNote  = 4 << 10
	MaxListName     = 200
	MaxListEmoji    = 32
	// MaxNeighborhoods bounds a profile or search neighborhood filter; MaxNeighborhoodName bounds each entry.
	MaxNeighborhoods    = 50
	MaxNeighborhoodName = 100
)

// checkNeighborhoods bounds a filter list: it is echoed by every get_state and
// bound into every candidate query.
func checkNeighborhoods(names []string) error {
	if len(names) > MaxNeighborhoods {
		return Invalidf("neighborhoods lists %d names; at most %d fit, use area-level filters or a crawl scope instead", len(names), MaxNeighborhoods)
	}
	for _, n := range names {
		if err := checkText("neighborhood", n, MaxNeighborhoodName, "a neighborhood name, not a description"); err != nil {
			return err
		}
	}
	return nil
}

func checkNote(note string) error {
	return checkText("note", note, MaxVerdictNote, "keep the user's words not the whole chat")
}

func checkText(name, v string, limit int, hint string) error {
	if len(v) > limit {
		return Invalidf("%s is %d bytes; at most %d fit, %s", name, len(v), limit, hint)
	}
	return nil
}

// ParseAreaIDs validates and trims StreetEasy numeric area ids. A neighborhood name here would silently crawl the wrong scope, so non-numeric input fails.
func ParseAreaIDs(areas []string) ([]string, error) {
	out := make([]string, 0, len(areas))
	for _, a := range areas {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if strings.TrimLeft(a, "0123456789") != "" {
			return nil, Invalidf("area %q is not a StreetEasy numeric area id; ids look like \"305\" and appear in list_crawl_targets", a)
		}
		out = append(out, canonicalAreaID(a))
	}
	if len(out) == 0 {
		return nil, Invalidf("areas is empty; give at least one area name (e.g. \"Park Slope\") or StreetEasy numeric area id; call resolve_areas to check names")
	}
	if len(out) > MaxRequestAreas {
		return nil, Invalidf("%d areas requested; at most %d fit in one crawl request", len(out), MaxRequestAreas)
	}
	return out, nil
}

func pendingRequests(targets []domain.CrawlTarget) int {
	pending := 0
	for _, t := range targets {
		if t.Kind == domain.TargetOnce && t.Status == domain.TargetPending {
			pending++
		}
	}
	return pending
}

// Column widths: prices are integer, bed counts smallint. The store's numPtr
// narrowing would wrap silently, so the Service refuses out-of-range values.
const (
	MaxMoney = math.MaxInt32
	MaxBeds  = math.MaxInt16
	MaxBaths = 99.5
)

func checkMoney(name string, v *domain.Money) error {
	return checkMoneyHint(name, v, "omit it for no cap")
}

// checkMoneyHint takes the remedy wording: a query omits a cap, a partial
// profile update must send null to clear one.
func checkMoneyHint(name string, v *domain.Money, hint string) error {
	switch {
	case v == nil:
		return nil
	case *v <= 0:
		return Invalidf("%s %d is not positive; %s", name, int64(*v), hint)
	case *v > MaxMoney:
		return Invalidf("%s %d exceeds the maximum %d; %s", name, int64(*v), MaxMoney, hint)
	}
	return nil
}

func checkBaths(name string, v *float64) error {
	switch {
	case v == nil:
		return nil
	case math.IsNaN(*v) || math.IsInf(*v, 0):
		return Invalidf("%s is not a finite number", name)
	case *v < 0:
		return Invalidf("%s %g is negative", name, *v)
	case *v > MaxBaths:
		return Invalidf("%s %g exceeds the maximum %g", name, *v, MaxBaths)
	case *v*2 != math.Trunc(*v*2):
		return Invalidf("%s %g is not a half step (bathrooms come in halves: 1, 1.5, 2)", name, *v)
	}
	return nil
}

func checkBeds(name string, v *int) error {
	switch {
	case v == nil:
		return nil
	case *v < 0:
		return Invalidf("%s %d is negative", name, *v)
	case *v > MaxBeds:
		return Invalidf("%s %d exceeds the maximum %d", name, *v, MaxBeds)
	}
	return nil
}
