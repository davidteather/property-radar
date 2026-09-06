package ingest

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"strings"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

// ScopeHash identifies a crawl scope across runs: same provider and same
// query means the same hash regardless of area order or duplicates.
func ScopeHash(provider string, q SearchQuery) string {
	areas := xslices.Map(q.Areas, strings.TrimSpace)
	areas = slices.DeleteFunc(areas, func(a string) bool { return a == "" })
	slices.Sort(areas)
	areas = slices.Compact(areas)

	listingType := q.ListingType
	if listingType == "" {
		listingType = domain.ListingSale
	}

	canonical := fmt.Sprintf("provider=%s\nlisting_type=%s\nmax_price=%d\nmin_beds=%d\nareas=%s\n",
		strings.TrimSpace(provider), listingType, int64(q.MaxPrice), q.MinBeds, strings.Join(areas, ","))

	sum := sha256.Sum256([]byte(canonical))
	return hex.EncodeToString(sum[:])
}
