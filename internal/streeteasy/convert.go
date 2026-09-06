package streeteasy

import (
	"encoding/json"
	"math"
	"strings"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

const photoVariant = "-uncropped_scaled_within_1536_1152.webp"

const photoBaseURL = "https://photos.zillowstatic.com/fp/"

// maxPhotoURLs bounds listing_photos rows per listing; a provider page with hundreds of photos is a data problem, not a gallery.
const maxPhotoURLs = 100

func toSourceProperty(node searchListing, detail *detailListing, raw json.RawMessage, siteURL string, listingType domain.ListingType) ingest.SourceProperty {
	status := node.Status
	if detail != nil && detail.Status != "" {
		status = detail.Status
	}
	return ingest.SourceProperty{
		Provider:     ProviderName,
		ProviderID:   node.ID,
		URL:          listingURL(siteURL, node.URLPath),
		Raw:          raw,
		SourceStatus: status,
		Property:     toProperty(node, detail, status, listingType),
		PhotoURLs:    photoURLs(node, detail),
		Enriched:     detail != nil,
	}
}

// Price is the sale price or monthly rent; carrying-cost fields are never reported on rentals.
func toProperty(node searchListing, detail *detailListing, status string, listingType domain.ListingType) domain.Property {
	p := domain.Property{
		Address:      toAddress(node, detail),
		Geo:          toGeo(node.GeoPoint),
		ListingType:  listingType,
		PropertyType: toPropertyType(node.BuildingType),
		Status:       toStatus(status, listingType),
		Currency:     domain.CurrencyUSD,

		Price:         money(node.Price),
		Maintenance:   money(node.MonthlyMaintenance),
		CommonCharges: money(node.MonthlyCommonCharges),
		TaxesMonthly:  money(node.MonthlyTaxes),
		Bedrooms:      count(node.BedroomCount),
		Sqft:          positive(node.LivingAreaSize),
		Bathrooms:     bathrooms(node.FullBathroomCount, node.HalfBathroomCount)}

	if detail != nil {
		p.Description = strings.TrimSpace(detail.Description)
		p.DaysOnMarket = positive(detail.DaysOnMarket)
		p.Price = ptr.First(money(detail.Pricing.Price), p.Price)
		p.Maintenance = ptr.First(money(detail.Pricing.MonthlyMaintenance), p.Maintenance)
		p.CommonCharges = ptr.First(money(detail.Pricing.MonthlyCommonCharges), p.CommonCharges)
		p.TaxesMonthly = ptr.First(money(detail.Pricing.MonthlyTaxes), p.TaxesMonthly)
		p.Bedrooms = ptr.First(count(detail.PropertyDetails.BedroomCount), p.Bedrooms)
		p.Sqft = ptr.First(positive(detail.PropertyDetails.LivingAreaSize), p.Sqft)
		if b := bathrooms(detail.PropertyDetails.FullBathroomCount, detail.PropertyDetails.HalfBathroomCount); b != nil {
			p.Bathrooms = b
		}
	}
	return p
}

func toAddress(node searchListing, detail *detailListing) domain.Address {
	a := domain.Address{
		Street:       strings.TrimSpace(node.Street),
		Unit:         normalizeUnit(node.DisplayUnit, node.Unit),
		Neighborhood: strings.TrimSpace(node.AreaName),
		Zip:          strings.TrimSpace(node.ZipCode),
	}
	if detail == nil {
		return a
	}
	d := detail.PropertyDetails.Address
	if a.Street == "" {
		a.Street = strings.TrimSpace(d.Street)
	}
	if a.Unit == "" {
		a.Unit = normalizeUnit(d.DisplayUnit, "")
	}
	if a.Zip == "" {
		a.Zip = strings.TrimSpace(d.ZipCode)
	}
	return a
}

// Whole-building listings carry a building-kind token where the unit goes.
func normalizeUnit(preferred, fallback string) string {
	u := strings.TrimSpace(preferred)
	if u == "" {
		u = strings.TrimSpace(fallback)
	}
	u = strings.TrimSpace(strings.TrimPrefix(u, "#"))
	switch strings.ToUpper(u) {
	case "HOUSE", "TWNHSE", "TOWNHOUSE", "RESIDENTIAL", "BUILDING", "N/A", "NA":
		return ""
	}
	return u
}

func toGeo(g *geoPoint) *domain.GeoPoint {
	if g == nil || (g.Latitude == 0 && g.Longitude == 0) {
		return nil
	}
	return &domain.GeoPoint{Latitude: g.Latitude, Longitude: g.Longitude}
}

func toPropertyType(buildingType string) domain.PropertyType {
	switch strings.ToUpper(strings.TrimSpace(buildingType)) {
	case "CO_OP", "COOP", "CONDOP":
		return domain.PropertyCoop
	case "CONDO":
		return domain.PropertyCondo
	case "TOWNHOUSE":
		return domain.PropertyTownhouse
	case "HOUSE", "MULTIFAMILY", "TWOFAMILY", "THREEFAMILY", "FOURFAMILY":
		return domain.PropertyHouse
	default:
		return domain.PropertyOther
	}
}

// An unrecognized provider status must never advance lifecycle: only explicit
// provider signals may retire a listing.
func toStatus(status string, listingType domain.ListingType) domain.PropertyStatus {
	s := strings.ToUpper(strings.TrimSpace(status))
	// RENTED/COMPLETED are rental-only terminal states; on a sale they stay active.
	if listingType == domain.ListingRent && (s == "RENTED" || s == "COMPLETED") {
		return domain.StatusRented
	}
	switch s {
	case "IN_CONTRACT", "PENDING":
		return domain.StatusInContract
	case "SOLD", "CLOSED":
		return domain.StatusSold
	case "NO_LONGER_AVAILABLE", "DELISTED", "OFF_MARKET", "TEMPORARILY_OFF_MARKET":
		return domain.StatusDelisted
	default:
		return domain.StatusActive
	}
}

func photoURLs(node searchListing, detail *detailListing) []string {
	keys := node.Photos
	if detail != nil && len(detail.Media.Photos) > len(keys) {
		keys = detail.Media.Photos
	}
	if len(keys) == 0 && node.LeadMedia != nil {
		keys = []photoRef{node.LeadMedia.Photo}
	}
	if len(keys) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(keys))
	urls := make([]string, 0, len(keys))
	for _, p := range keys {
		k := strings.TrimSpace(p.Key)
		if k == "" {
			continue
		}
		if _, dup := seen[k]; dup {
			continue
		}
		seen[k] = struct{}{}
		urls = append(urls, photoBaseURL+k+photoVariant)
		if len(urls) == maxPhotoURLs {
			break
		}
	}
	if len(urls) == 0 {
		return nil
	}
	return urls
}

func listingURL(siteURL, urlPath string) string {
	if urlPath == "" {
		return ""
	}
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	return siteURL + urlPath
}

// StreetEasy uses 0 for "not applicable" in price/cost fields; treat it as unknown, not a value.
func money(v num) *domain.Money {
	if v.v == nil || *v.v <= 0 {
		return nil
	}
	m := domain.Money(*v.v)
	return &m
}

func positive(v num) *int {
	if v.v == nil || *v.v <= 0 || *v.v > math.MaxInt32 {
		return nil
	}
	n := int(*v.v)
	return &n
}

// count keeps 0 as a value: bedroomCount 0 is a studio, not "unknown".
func count(v num) *int {
	if v.v == nil || *v.v < 0 || *v.v > math.MaxInt32 {
		return nil
	}
	n := int(*v.v)
	return &n
}

func bathrooms(full, half num) *float64 {
	if full.v == nil && half.v == nil {
		return nil
	}
	var total float64
	if full.v != nil {
		total += float64(*full.v)
	}
	if half.v != nil {
		total += float64(*half.v) * 0.5
	}
	// The column is numeric(3,1); a provider glitch past it must not fail the row.
	if total <= 0 || total >= 100 {
		return nil
	}
	return &total
}
