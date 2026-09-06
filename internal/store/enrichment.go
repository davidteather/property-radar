package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
)

// EnrichmentState backs the incremental crawl's enrich-or-skip decision with
// the stored snapshot for one provider listing.
func (s *Store) EnrichmentState(ctx context.Context, provider, providerID string) (ingest.EnrichState, error) {
	const query = `
SELECT c.price, l.status, COALESCE(c.description, '') <> '' AS has_description, src.enriched_at
FROM listing_sources src
JOIN listings l ON l.id = src.listing_id
LEFT JOIN listing_current c ON c.listing_id = src.listing_id
WHERE src.provider = $1 AND src.provider_id = $2`

	var (
		price      *int32
		status     string
		hasDesc    bool
		enrichedAt *time.Time
	)
	err := s.q.QueryRow(ctx, query, provider, providerID).Scan(&price, &status, &hasDesc, &enrichedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return ingest.EnrichState{}, nil
	}
	if err != nil {
		return ingest.EnrichState{}, fmt.Errorf("query enrichment state %s/%s: %w", provider, providerID, err)
	}
	return ingest.EnrichState{
		Exists:         true,
		Price:          numPtr[domain.Money](price),
		Status:         domain.PropertyStatus(status),
		HasDescription: hasDesc,
		EnrichedAt:     enrichedAt,
	}, nil
}
