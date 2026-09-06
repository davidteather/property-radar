package streeteasy

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
)

const salesQuery = `query GetPropertyRadarSales($input: SearchSalesInput!) {
  searchSales(input: $input) {
    search { criteria searchId }
    totalCount
    pageInfo { currentPage hasNextPage hasPreviousPage totalPages }
    edges {
      __typename
      ... on OrganicSaleEdge { node { ...SaleListingFields } }
      ... on FeaturedSaleEdge { node { ...SaleListingFields } }
      ... on SponsoredSaleEdge { node { ...SaleListingFields } }
    }
  }
}

fragment SaleListingFields on SearchSaleListing {
  id
  areaName
  street
  unit
  displayUnit
  zipCode
  state
  urlPath
  slug
  status
  buildingType
  saleType
  sourceType
  sourceGroupLabel
  price
  soldPrice
  priceDelta
  priceChangedAt
  interestingPriceDelta
  monthlyTaxes
  monthlyMaintenance
  monthlyCommonCharges
  bedroomCount
  fullBathroomCount
  halfBathroomCount
  livingAreaSize
  isNewDevelopment
  furnished
  hasTour3d
  hasVideos
  advertisedOnSe
  verifiedAt
  availableAt
  offMarketAt
  mediaAssetCount
  geoPoint { latitude longitude }
  photos { key }
}`

// Rentals use a different root field and filter input, same node shape modulo
// carrying costs (absent) and rental-only extras (raw-only).
const rentalsQuery = `query GetPropertyRadarRentals($input: SearchRentalsInput!) {
  searchRentals(input: $input) {
    search { criteria searchId }
    totalCount
    pageInfo { currentPage hasNextPage hasPreviousPage totalPages }
    edges {
      __typename
      ... on OrganicRentalEdge { node { ...RentalListingFields } }
      ... on FeaturedRentalEdge { node { ...RentalListingFields } }
      ... on SponsoredRentalEdge { node { ...RentalListingFields } }
    }
  }
}

fragment RentalListingFields on SearchRentalListing {
  id
  areaName
  street
  unit
  displayUnit
  zipCode
  state
  urlPath
  slug
  status
  buildingType
  sourceType
  sourceGroupLabel
  price
  totalMonthlyPrice
  priceDelta
  priceChangedAt
  interestingPriceDelta
  noFee
  leaseTermMonths
  bedroomCount
  fullBathroomCount
  halfBathroomCount
  livingAreaSize
  isNewDevelopment
  furnished
  hasTour3d
  hasVideos
  availableAt
  offMarketAt
  mediaAssetCount
  geoPoint { latitude longitude }
  photos { key }
  leadMedia { photo { key } }
}`

type graphQLRequest struct {
	Query     string        `json:"query"`
	Variables searchVarsDTO `json:"variables"`
}

type searchVarsDTO struct {
	Input searchInput `json:"input"`
}

// Filters is untyped because SaleFiltersInput and RentalFiltersInput share no Go type.
type searchInput struct {
	Filters         any          `json:"filters"`
	Page            int          `json:"page"`
	PerPage         int          `json:"perPage"`
	Sorting         sortingInput `json:"sorting"`
	UserSearchToken string       `json:"userSearchToken,omitempty"`
	AdStrategy      string       `json:"adStrategy"`
}

type saleFiltersInput struct {
	Areas      []int        `json:"areas"`
	SaleStatus string       `json:"saleStatus"`
	Price      *boundsInput `json:"price,omitempty"`
	Bedrooms   *boundsInput `json:"bedrooms,omitempty"`
}

type rentalFiltersInput struct {
	Areas        []int        `json:"areas"`
	RentalStatus string       `json:"rentalStatus"`
	Price        *boundsInput `json:"price,omitempty"`
	Bedrooms     *boundsInput `json:"bedrooms,omitempty"`
}

type boundsInput struct {
	LowerBound *int `json:"lowerBound,omitempty"`
	UpperBound *int `json:"upperBound,omitempty"`
}

type sortingInput struct {
	Attribute string `json:"attribute"`
	Direction string `json:"direction"`
}

type graphQLResponse struct {
	Data struct {
		SearchSales   *searchOutput `json:"searchSales"`
		SearchRentals *searchOutput `json:"searchRentals"`
	} `json:"data"`
	Errors []struct {
		Message string `json:"message"`
	} `json:"errors"`
}

type searchOutput struct {
	Search struct {
		Criteria string `json:"criteria"`
	} `json:"search"`
	TotalCount int          `json:"totalCount"`
	PageInfo   pageInfoWire `json:"pageInfo"`
	Edges      []searchEdge `json:"edges"`
}

type pageInfoWire struct {
	CurrentPage     int  `json:"currentPage"`
	HasNextPage     bool `json:"hasNextPage"`
	HasPreviousPage bool `json:"hasPreviousPage"`
	TotalPages      int  `json:"totalPages"`
}

// Node stays raw so the exact per-listing bytes are retained as provenance.
type searchEdge struct {
	Typename string          `json:"__typename"`
	Node     json.RawMessage `json:"node"`
}

// One struct for both roots: monthly-cost fields stay nil (unknown) on rentals,
// and rental-only fields live in raw provenance.
type searchListing struct {
	ID                   string        `json:"id"`
	AreaName             string        `json:"areaName"`
	Street               string        `json:"street"`
	Unit                 string        `json:"unit"`
	DisplayUnit          string        `json:"displayUnit"`
	ZipCode              string        `json:"zipCode"`
	State                string        `json:"state"`
	URLPath              string        `json:"urlPath"`
	Status               string        `json:"status"`
	BuildingType         string        `json:"buildingType"`
	SaleType             string        `json:"saleType"`
	SourceGroupLabel     string        `json:"sourceGroupLabel"`
	Price                num           `json:"price"`
	MonthlyTaxes         num           `json:"monthlyTaxes"`
	MonthlyMaintenance   num           `json:"monthlyMaintenance"`
	MonthlyCommonCharges num           `json:"monthlyCommonCharges"`
	BedroomCount         num           `json:"bedroomCount"`
	FullBathroomCount    num           `json:"fullBathroomCount"`
	HalfBathroomCount    num           `json:"halfBathroomCount"`
	LivingAreaSize       num           `json:"livingAreaSize"`
	MediaAssetCount      int           `json:"mediaAssetCount"`
	GeoPoint             *geoPoint     `json:"geoPoint"`
	Photos               []photoRef    `json:"photos"`
	LeadMedia            *leadMediaRef `json:"leadMedia"`
}

type geoPoint struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

type photoRef struct {
	Key string `json:"key"`
}

type leadMediaRef struct {
	Photo photoRef `json:"photo"`
}

// detailListing is the `listing` object from the detail page's RSC flight
// payload, after `$<hexid>` chunk references are resolved.
type detailListing struct {
	ID              string                `json:"id"`
	Status          string                `json:"status"`
	Description     string                `json:"description"`
	DaysOnMarket    num                   `json:"daysOnMarket"`
	OnMarketAt      string                `json:"onMarketAt"`
	SaleType        string                `json:"saleType"`
	URLPath         string                `json:"urlPath"`
	ListingAddress  string                `json:"listingAddress"`
	Pricing         detailPricing         `json:"pricing"`
	PropertyDetails detailPropertyDetails `json:"propertyDetails"`
	Media           detailMedia           `json:"media"`
}

type detailPricing struct {
	Price                num `json:"price"`
	MonthlyMaintenance   num `json:"monthlyMaintenance"`
	MonthlyCommonCharges num `json:"monthlyCommonCharges"`
	MonthlyTaxes         num `json:"monthlyTaxes"`
}

type detailPropertyDetails struct {
	Address struct {
		Street      string `json:"street"`
		City        string `json:"city"`
		State       string `json:"state"`
		ZipCode     string `json:"zipCode"`
		DisplayUnit string `json:"displayUnit"`
	} `json:"address"`
	BedroomCount      num `json:"bedroomCount"`
	FullBathroomCount num `json:"fullBathroomCount"`
	HalfBathroomCount num `json:"halfBathroomCount"`
	LivingAreaSize    num `json:"livingAreaSize"`
	Amenities         struct {
		List []string `json:"list"`
	} `json:"amenities"`
}

type detailMedia struct {
	Photos []photoRef `json:"photos"`
}

// rawProvenance lands in ingest.SourceProperty.Raw: byte-exact provider JSON;
// detail is absent when enrichment failed or is disabled.
type rawProvenance struct {
	SearchNode    json.RawMessage `json:"searchNode"`
	DetailListing json.RawMessage `json:"detailListing,omitempty"`
}

// num is a lenient JSON integer: a number, an integral float, or a numeric
// string decode; anything else (or null) reads as absent, so one drifted field
// costs a value, never the whole listing.
type num struct{ v *int64 }

func (n *num) UnmarshalJSON(b []byte) error {
	s := strings.Trim(strings.TrimSpace(string(b)), `"`)
	if s == "" || s == "null" {
		n.v = nil
		return nil
	}
	if i, err := strconv.ParseInt(s, 10, 64); err == nil {
		n.v = &i
		return nil
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil && f == math.Trunc(f) && math.Abs(f) < 1<<53 {
		i := int64(f)
		n.v = &i
	}
	return nil
}
