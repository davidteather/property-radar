package store

import (
	"encoding/json"
	"fmt"

	"github.com/jackc/pgx/v5"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

func (r propertyRow) toDomain() domain.Property {
	p := domain.Property{
		ID: domain.PropertyID(r.ID),
		Address: domain.Address{
			Street:       r.CanonicalAddress,
			Unit:         ptr.Deref(r.Unit),
			Neighborhood: ptr.Deref(r.Neighborhood),
			Zip:          ptr.Deref(r.Zip),
		},
		ListingType:       domain.ListingType(r.ListingType),
		PropertyType:      domain.PropertyType(ptr.Deref(r.PropertyType)),
		Status:            domain.PropertyStatus(r.Status),
		Price:             numPtr[domain.Money](r.Price),
		Currency:          currencyFromColumn(r.Currency),
		Bedrooms:          numPtr[int](r.Beds),
		Bathrooms:         ptr.Clone(r.Baths),
		Sqft:              numPtr[int](r.Sqft),
		Maintenance:       numPtr[domain.Money](r.Maintenance),
		CommonCharges:     numPtr[domain.Money](r.CommonCharges),
		TaxesMonthly:      numPtr[domain.Money](r.TaxesMonthly),
		DaysOnMarket:      numPtr[int](r.DOM),
		Description:       ptr.Deref(r.Description),
		URL:               ptr.Deref(r.URL),
		FirstSeen:         r.FirstSeen,
		LastSeen:          r.LastSeen,
		MaterialChangedAt: r.MaterialChangedAt,
	}
	if r.Lat != nil && r.Lng != nil {
		p.Geo = &domain.GeoPoint{Latitude: *r.Lat, Longitude: *r.Lng}
	}
	return p
}

func (r photoRow) toDomain() domain.Photo {
	return domain.Photo{
		Position:   int(r.Position),
		SourceURL:  r.SourceURL,
		CachedPath: ptr.Deref(r.CachedPath),
		MIMEType:   ptr.Deref(r.MIMEType),
		Width:      int(ptr.Deref(r.Width)),
		Height:     int(ptr.Deref(r.Height)),
	}
}

func (r priceEventRow) toDomain() domain.PriceEvent {
	return domain.PriceEvent{
		PropertyID: domain.PropertyID(r.ListingID),
		Price:      domain.Money(r.Price),
		ObservedAt: r.ObservedAt,
	}
}

func (r verdictRow) toDomain() domain.Verdict {
	return domain.Verdict{
		ID:         domain.VerdictID(r.ID),
		PropertyID: domain.PropertyID(r.ListingID),
		Kind:       domain.VerdictKind(r.Verdict),
		Note:       ptr.Deref(r.Note),
		CreatedAt:  r.CreatedAt,
	}
}

func (r rubricRow) toDomain() domain.Rubric {
	rb := domain.Rubric{
		ID:        r.ID,
		Content:   r.Content,
		CreatedAt: r.CreatedAt,
	}
	if r.ThroughVerdictID != nil {
		rb.ThroughVerdictID = domain.VerdictID(*r.ThroughVerdictID)
	}
	return rb
}

func (r profileRow) toDomain() domain.Profile {
	return domain.Profile{
		MaxPrice:           numPtr[domain.Money](r.MaxPrice),
		MinBeds:            numPtr[int](r.MinBeds),
		MinBaths:           ptr.Clone(r.MinBaths),
		MaxMonthlyCarrying: numPtr[domain.Money](r.MaxMonthlyCarrying),
		ListingType:        domain.ListingType(r.ListingType),
		Neighborhoods:      r.Neighborhoods,
		PropertyTypes:      xslices.Map(r.PropertyTypes, func(t string) domain.PropertyType { return domain.PropertyType(t) }),
		UpdatedAt:          r.UpdatedAt,
	}
}

func (r runRow) toDomain() (domain.IngestRun, error) {
	itemErrors, err := runErrorsFromJSON(r.Errors)
	if err != nil {
		return domain.IngestRun{}, err
	}
	return domain.IngestRun{
		ID:            domain.IngestRunID(r.ID),
		Provider:      r.Provider,
		ScopeHash:     r.ScopeHash,
		StartedAt:     r.StartedAt,
		FinishedAt:    r.FinishedAt,
		Complete:      r.Complete,
		ListingsSeen:  int(ptr.Deref(r.ListingsSeen)),
		Created:       int(ptr.Deref(r.Created)),
		Updated:       int(ptr.Deref(r.Updated)),
		PhotoFailures: int(ptr.Deref(r.PhotoFailures)),
		ItemErrors:    itemErrors,
		Suspect:       r.Suspect,
	}, nil
}

func (r crawlTargetRow) toDomain() domain.CrawlTarget {
	t := domain.CrawlTarget{
		ID:          domain.CrawlTargetID(r.ID),
		Kind:        domain.TargetKind(r.Kind),
		Areas:       r.Areas,
		ListingType: domain.ListingType(r.ListingType),
		MaxPrice:    numPtr[domain.Money](r.MaxPrice),
		MinBeds:     numPtr[int](r.MinBeds),
		Enabled:     r.Enabled,
		Status:      domain.TargetStatus(r.Status),
		Note:        ptr.Deref(r.Note),
		CreatedAt:   r.CreatedAt,
		LastError:   ptr.Deref(r.LastError),
	}
	if r.LastRunID != nil {
		t.LastRunID = ptr.To(domain.IngestRunID(*r.LastRunID))
	}
	t.LastRunAt = r.LastRunAt
	t.LastDeepAt = r.LastDeepAt
	return t
}

func (r sourceRow) toProvenance() ProvenanceSummary {
	return ProvenanceSummary{
		Provider:     r.Provider,
		ProviderID:   r.ProviderID,
		URL:          r.URL,
		SourceStatus: ptr.Deref(r.SourceStatus),
		FirstSeenAt:  r.FirstSeenAt,
		LastSeenAt:   r.LastSeenAt,
		FetchedAt:    r.FetchedAt,
		MissingRuns:  int(r.MissingRuns),
	}
}

// An errorless run stores SQL NULL rather than an empty array.
func runErrorsToJSON(errs []domain.RunError) ([]byte, error) {
	if len(errs) == 0 {
		return nil, nil
	}
	out := xslices.Map(errs, func(e domain.RunError) runErrorJSON {
		return runErrorJSON{ProviderID: e.ProviderID, Message: e.Message}
	})
	b, err := json.Marshal(out)
	if err != nil {
		return nil, fmt.Errorf("encode run item errors: %w", err)
	}
	return b, nil
}

func runErrorsFromJSON(raw []byte) ([]domain.RunError, error) {
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var rows []runErrorJSON
	if err := json.Unmarshal(raw, &rows); err != nil {
		return nil, fmt.Errorf("decode run item errors: %w", err)
	}
	return xslices.Map(rows, func(r runErrorJSON) domain.RunError {
		return domain.RunError{ProviderID: r.ProviderID, Message: r.Message}
	}), nil
}

func currencyFromColumn(c *string) domain.Currency {
	if c == nil || *c == "" {
		return domain.CurrencyUSD
	}
	return domain.Currency(*c)
}

func currencyToColumn(c domain.Currency) string {
	if c == "" {
		return string(domain.CurrencyUSD)
	}
	return string(c)
}

type integer interface {
	~int | ~int16 | ~int32 | ~int64
}

// numPtr converts between column width and domain numeric type, preserving nil.
func numPtr[U, T integer](p *T) *U {
	if p == nil {
		return nil
	}
	v := U(*p)
	return &v
}

func collectMapped[R, D any](rows pgx.Rows, to func(R) D) ([]D, error) {
	structRows, err := pgx.CollectRows(rows, pgx.RowToStructByName[R])
	if err != nil {
		return nil, err
	}
	return xslices.Map(structRows, to), nil
}

// nullText maps "" to SQL NULL where empty and unknown should not differ.
func nullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func stringsFromPropertyTypes(types []domain.PropertyType) []string {
	return xslices.Map(types, func(t domain.PropertyType) string { return string(t) })
}

func int64IDs(ids []domain.PropertyID) []int64 {
	return xslices.Map(ids, func(id domain.PropertyID) int64 { return int64(id) })
}
