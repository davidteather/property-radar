// Package domain holds the canonical application-owned types; no external deps.
package domain

import "time"

// PropertyID is zero until persisted.
type PropertyID int64

type ListingType string

const (
	ListingSale ListingType = "sale"
	ListingRent ListingType = "rent"
)

type PropertyType string

const (
	PropertyCoop      PropertyType = "coop"
	PropertyCondo     PropertyType = "condo"
	PropertyTownhouse PropertyType = "townhouse"
	PropertyHouse     PropertyType = "house"
	PropertyOther     PropertyType = "other"
)

// PropertyStatus advances toward sold/delisted only via explicit provider status
// or repeated absence from complete, non-suspect runs.
type PropertyStatus string

const (
	StatusActive     PropertyStatus = "active"
	StatusInContract PropertyStatus = "in_contract"
	StatusSold       PropertyStatus = "sold"
	StatusRented     PropertyStatus = "rented"
	StatusDelisted   PropertyStatus = "delisted"
)

// Property is the canonical current snapshot of one listing. Nil numerics mean
// "unknown", distinct from zero; monthly costs are dollars per month.
type Property struct {
	ID      PropertyID
	Address Address
	Geo     *GeoPoint

	ListingType  ListingType
	PropertyType PropertyType
	Status       PropertyStatus

	Price         *Money
	Currency      Currency // empty means CurrencyUSD
	Bedrooms      *int
	Bathrooms     *float64 // half-baths as .5
	Sqft          *int
	Maintenance   *Money
	CommonCharges *Money
	TaxesMonthly  *Money
	DaysOnMarket  *int

	Description string

	// URL is the shareable provider listing link; populated on compact search and
	// candidate rows, empty where provenance is carried separately.
	URL string

	FirstSeen         time.Time
	LastSeen          time.Time
	MaterialChangedAt time.Time
}

// MonthlyCarrying sums the known monthly costs; a partial sum is a lower bound.
func (p Property) MonthlyCarrying() *Money {
	var sum Money
	known := false
	for _, m := range []*Money{p.Maintenance, p.CommonCharges, p.TaxesMonthly} {
		if m != nil {
			sum += *m
			known = true
		}
	}
	if !known {
		return nil
	}
	return &sum
}

type Photo struct {
	Position   int // 0-based display order
	SourceURL  string
	CachedPath string
	MIMEType   string
	Width      int
	Height     int
}

// PriceEvent records an actual price change, never a re-observation.
type PriceEvent struct {
	PropertyID PropertyID
	Price      Money
	ObservedAt time.Time
}
