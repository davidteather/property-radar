package mcp

import (
	"strconv"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/publicurl"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

func toProfile(p domain.Profile) profileDTO {
	dto := profileDTO{
		MaxPrice:           moneyValue(p.MaxPrice),
		MinBeds:            ptr.Clone(p.MinBeds),
		MinBaths:           ptr.Clone(p.MinBaths),
		MaxMonthlyCarrying: moneyValue(p.MaxMonthlyCarrying),
		ListingType:        string(p.ListingType),
		Neighborhoods:      nonNil(p.Neighborhoods),
		PropertyTypes:      nonNil(xslices.Map(p.PropertyTypes, func(t domain.PropertyType) string { return string(t) })),
	}
	if !p.UpdatedAt.IsZero() {
		dto.UpdatedAt = ptr.To(timestamp(p.UpdatedAt))
	}
	return dto
}

func toRubric(r *domain.Rubric) *rubricDTO {
	if r == nil {
		return nil
	}
	return &rubricDTO{
		Content:          r.Content,
		ThroughVerdictID: int64(r.ThroughVerdictID),
		CreatedAt:        timestamp(r.CreatedAt),
	}
}

func toVerdict(v domain.Verdict) verdictDTO {
	return verdictDTO{
		ID:        int64(v.ID),
		ListingID: int64(v.PropertyID),
		Verdict:   string(v.Kind),
		Note:      v.Note,
		CreatedAt: timestamp(v.CreatedAt),
	}
}

func toListingRows(rows []listings.Row) []listingRowDTO {
	return nonNil(xslices.Map(rows, toListingRow))
}

func toListingRow(r listings.Row) listingRowDTO {
	return listingRowDTO{
		ID:                 int64(r.ID),
		Address:            r.Address,
		Neighborhood:       r.Neighborhood,
		Price:              moneyValue(r.Price),
		Beds:               ptr.Clone(r.Beds),
		Baths:              ptr.Clone(r.Baths),
		Sqft:               ptr.Clone(r.Sqft),
		PropertyType:       string(r.PropertyType),
		ListingType:        string(r.ListingType),
		MonthlyCarrying:    moneyValue(r.MonthlyCarrying),
		DOM:                ptr.Clone(r.DOM),
		DescriptionExcerpt: r.DescriptionExcerpt,
		PriceDrop:          r.PriceDrop,
		Status:             string(r.Status),
		URL:                r.URL,
		PhotoCount:         r.PhotoCount,
		PhotosCached:       r.PhotosCached,
	}
}

// toListingPhotos builds one listing's bulk gallery entry, minting a public /img/l/{id}/{n} URL per cached photo (dense: n in 0..cached-1). base is the per-request origin (or fallback); empty yields relative paths. token, when set, is appended as ?k= so the URLs pass the gated proxy.
func toListingPhotos(base, token string, s listings.ListingPhotos) listingPhotosDTO {
	uris := make([]string, 0, s.Cached)
	for n := range s.Cached {
		path := "/img/l/" + strconv.FormatInt(int64(s.ID), 10) + "/" + strconv.Itoa(n)
		uris = append(uris, publicurl.ImageURL(base, path, token))
	}
	return listingPhotosDTO{
		ID:           int64(s.ID),
		PhotoCount:   s.Total,
		PhotosCached: s.Cached,
		ImageURIs:    uris,
	}
}

func toListingDetail(d listings.Detail) listingDetailDTO {
	p := d.Property
	r := d.Photos
	dto := listingDetailDTO{
		ID:                int64(p.ID),
		Address:           p.Address.String(),
		Street:            p.Address.Street,
		Unit:              p.Address.Unit,
		Neighborhood:      p.Address.Neighborhood,
		Zip:               p.Address.Zip,
		ListingType:       string(p.ListingType),
		PropertyType:      string(p.PropertyType),
		Status:            string(p.Status),
		Price:             moneyValue(p.Price),
		Currency:          string(p.Currency),
		Beds:              ptr.Clone(p.Bedrooms),
		Baths:             ptr.Clone(p.Bathrooms),
		Sqft:              ptr.Clone(p.Sqft),
		Maintenance:       moneyValue(p.Maintenance),
		CommonCharges:     moneyValue(p.CommonCharges),
		TaxesMonthly:      moneyValue(p.TaxesMonthly),
		MonthlyCarrying:   moneyValue(p.MonthlyCarrying()),
		DOM:               ptr.Clone(p.DaysOnMarket),
		Description:       p.Description,
		URL:               d.Provenance.URL,
		FirstSeen:         timestamp(p.FirstSeen),
		LastSeen:          timestamp(p.LastSeen),
		MaterialChangedAt: timestamp(p.MaterialChangedAt),
		PriceHistory: xslices.Map(d.PriceEvents, func(e domain.PriceEvent) priceEventDTO {
			return priceEventDTO{Price: int64(e.Price), ObservedAt: timestamp(e.ObservedAt)}
		}),
		Provenance: provenanceDTO{
			Provider:     d.Provenance.Provider,
			ProviderID:   d.Provenance.ProviderID,
			URL:          d.Provenance.URL,
			SourceStatus: d.Provenance.SourceStatus,
			FirstSeenAt:  timestamp(d.Provenance.FirstSeenAt),
			LastSeenAt:   timestamp(d.Provenance.LastSeenAt),
			FetchedAt:    timestamp(d.Provenance.FetchedAt),
			MissingRuns:  d.Provenance.MissingRuns,
		},
		PhotoCount:     r.Counts.Total,
		PhotosCached:   r.Counts.Cached,
		PhotosReturned: r.Counts.Returned,
		PhotoMissing:   r.Counts.Missing,
		PhotoLayout:    r.Layout,
		Sheets:         len(r.Sheets),
		PhotoSheets:    photoSheets(r.Sheets),
	}
	if p.Geo != nil {
		dto.Latitude = ptr.To(p.Geo.Latitude)
		dto.Longitude = ptr.To(p.Geo.Longitude)
	}
	if dto.PriceHistory == nil {
		dto.PriceHistory = []priceEventDTO{}
	}
	return dto
}

func photoSheets(sheets []listings.Sheet) []photoSheetDTO {
	out := make([]photoSheetDTO, 0, len(sheets))
	for i, sheet := range sheets {
		out = append(out, photoSheetDTO{
			Sheet:     i + 1,
			Positions: []int{sheet.First, sheet.Last},
			Photos:    sheet.Tiles,
		})
	}
	return out
}

func toCrawlTarget(t domain.CrawlTarget) crawlTargetDTO {
	dto := crawlTargetDTO{
		ID:          int64(t.ID),
		Kind:        string(t.Kind),
		Status:      string(t.Status),
		Enabled:     t.Enabled,
		Areas:       nonNil(t.Areas),
		ListingType: string(t.ListingType),
		MaxPrice:    moneyValue(t.MaxPrice),
		MinBeds:     ptr.Clone(t.MinBeds),
		Note:        t.Note,
		CreatedAt:   timestamp(t.CreatedAt),
		LastError:   t.LastError,
	}
	if t.LastRunID != nil {
		dto.LastRunID = ptr.To(int64(*t.LastRunID))
	}
	if t.LastRunAt != nil {
		dto.LastRunAt = timestamp(*t.LastRunAt)
	}
	return dto
}

func toEffectiveFilters(f listings.SearchFilter, limit int) effectiveFiltersDTO {
	return effectiveFiltersDTO{
		ListingType:     string(f.ListingType),
		MaxPrice:        moneyValue(f.MaxPrice),
		MinBeds:         ptr.Clone(f.MinBeds),
		MinBaths:        ptr.Clone(f.MinBaths),
		Neighborhoods:   nonNil(f.Neighborhoods),
		PropertyTypes:   nonNil(xslices.Map(f.PropertyTypes, func(t domain.PropertyType) string { return string(t) })),
		IncludeInactive: f.IncludeInactive,
		Limit:           limit,
	}
}

func moneyValue(m *domain.Money) *int64 {
	if m == nil {
		return nil
	}
	return ptr.To(m.Dollars())
}

func moneyPtr(v *int64) *domain.Money {
	if v == nil {
		return nil
	}
	return ptr.To(domain.Money(*v))
}

func nonNil[T any](s []T) []T {
	if s == nil {
		return []T{}
	}
	return s
}

func propertyIDs(ids []int64) []domain.PropertyID {
	return xslices.Map(ids, func(id int64) domain.PropertyID { return domain.PropertyID(id) })
}

func toListDTO(l domain.List) listDTO {
	return listDTO{
		ID: int64(l.ID), Slug: l.Slug, Name: l.Name, Emoji: l.Emoji,
		IsDefault: l.IsDefault, Count: l.Count,
	}
}

func toListDTOs(ls []domain.List) []listDTO {
	out := make([]listDTO, len(ls))
	for i, l := range ls {
		out[i] = toListDTO(l)
	}
	return out
}
