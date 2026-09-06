package streeteasy

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

func TestDecodeRentalSearchFixtures(t *testing.T) {
	p1 := decodeSearchFixture(t, "search_rentals_brownstone_brooklyn_page1.json")
	p2 := decodeSearchFixture(t, "search_rentals_brownstone_brooklyn_page2.json")

	if p1.TotalCount != 826 || p1.TotalCount != p2.TotalCount {
		t.Errorf("totalCount = %d / %d, want 826 on both pages", p1.TotalCount, p2.TotalCount)
	}
	if !p1.PageInfo.HasNextPage || p2.PageInfo.HasNextPage {
		t.Errorf("pagination flags = %+v / %+v", p1.PageInfo, p2.PageInfo)
	}
	if p1.Search.Criteria == "" || !strings.Contains(p1.Search.Criteria, "status:open") {
		t.Errorf("criteria = %q", p1.Search.Criteria)
	}

	buildingTypes := map[string]int{}
	for _, e := range p1.Edges {
		var n searchListing
		if err := json.Unmarshal(e.Node, &n); err != nil {
			t.Fatalf("decode node: %v", err)
		}
		if n.ID == "" || n.URLPath == "" || n.Price.v == nil {
			t.Errorf("node missing identity/price: %+v", n)
		}
		if e.Typename != "OrganicRentalEdge" {
			t.Errorf("unexpected edge type %q", e.Typename)
		}
		if n.MonthlyMaintenance.v != nil || n.MonthlyCommonCharges.v != nil || n.MonthlyTaxes.v != nil {
			t.Errorf("rental node %s reported sale carrying costs", n.ID)
		}
		buildingTypes[n.BuildingType]++
	}
	// RENTAL is the dominant rental-side building type and has no sale analogue.
	if buildingTypes["RENTAL"] == 0 || len(buildingTypes) < 5 {
		t.Errorf("fixture building-type coverage = %v", buildingTypes)
	}
}

func TestConvertRental(t *testing.T) {
	out := decodeSearchFixture(t, "search_rentals_brownstone_brooklyn_page1.json")
	node := nodeByID(t, out, "4864627")
	detail := decodeDetailFixture(t, "detail_rental_4864627.json")

	sp := toSourceProperty(node, detail, json.RawMessage(`{}`), defaultSiteURL, domain.ListingRent)

	if sp.ProviderID != "4864627" {
		t.Errorf("providerID = %q", sp.ProviderID)
	}
	if want := "https://streeteasy.com/building/canal-house/9as"; sp.URL != want {
		t.Errorf("url = %q, want %q", sp.URL, want)
	}
	if sp.SourceStatus != "ACTIVE" {
		t.Errorf("sourceStatus = %q", sp.SourceStatus)
	}

	p := sp.Property
	if p.ListingType != domain.ListingRent {
		t.Errorf("listingType = %q", p.ListingType)
	}
	if p.Status != domain.StatusActive {
		t.Errorf("status = %q", p.Status)
	}
	// buildingType RENTAL has no canonical peer; "other" is the honest mapping.
	if p.PropertyType != domain.PropertyOther {
		t.Errorf("propertyType = %q", p.PropertyType)
	}
	if p.Address.Street != "118 Canalside Avenue" || p.Address.Unit != "9AS" || p.Address.Zip != "11217" {
		t.Errorf("address = %+v", p.Address)
	}
	if p.Address.Neighborhood != "Gowanus" {
		t.Errorf("neighborhood = %q", p.Address.Neighborhood)
	}

	assertMoney(t, "price", p.Price, 7995)
	if p.Maintenance != nil || p.CommonCharges != nil || p.TaxesMonthly != nil {
		t.Errorf("rentals must not report carrying costs: %v %v %v", p.Maintenance, p.CommonCharges, p.TaxesMonthly)
	}
	if p.MonthlyCarrying() != nil {
		t.Error("monthlyCarrying should be unknown for a rental")
	}
	assertInt(t, "bedrooms", p.Bedrooms, 2)
	assertInt(t, "daysOnMarket", p.DaysOnMarket, 338)
	// livingAreaSize is 0 on both payloads, which means unknown, not zero.
	if p.Sqft != nil {
		t.Errorf("sqft = %v, want nil", *p.Sqft)
	}
	if p.Bathrooms == nil || *p.Bathrooms != 2 {
		t.Errorf("bathrooms = %v, want 2", p.Bathrooms)
	}
	if !strings.HasPrefix(p.Description, "Canal House") || len(p.Description) < 500 {
		t.Errorf("description = %.60q (%d chars)", p.Description, len(p.Description))
	}
	if len(sp.PhotoURLs) != 27 {
		t.Errorf("photoURLs = %d, want 27", len(sp.PhotoURLs))
	}
	want := photoBaseURL + "27892abed0a7c4e9993f41ca850b643f" + photoVariant
	if sp.PhotoURLs[0] != want {
		t.Errorf("photoURLs[0] = %q", sp.PhotoURLs[0])
	}
}

// A co-op unit offered for rent keeps the co-op property type but must not pick
// up co-op carrying costs: rentals never report them.
func TestConvertCoopRental(t *testing.T) {
	out := decodeSearchFixture(t, "search_rentals_brownstone_brooklyn_page1.json")
	sp := toSourceProperty(nodeByID(t, out, "5056345"), decodeDetailFixture(t, "detail_rental_coop_5056345.json"),
		nil, defaultSiteURL, domain.ListingRent)

	p := sp.Property
	if p.PropertyType != domain.PropertyCoop || p.ListingType != domain.ListingRent {
		t.Errorf("propertyType/listingType = %q/%q", p.PropertyType, p.ListingType)
	}
	assertMoney(t, "price", p.Price, 3000)
	if p.MonthlyCarrying() != nil {
		t.Errorf("monthlyCarrying = %v, want nil", p.MonthlyCarrying())
	}
	assertInt(t, "daysOnMarket", p.DaysOnMarket, 86)
}

func TestConvertRentalWithoutDetail(t *testing.T) {
	out := decodeSearchFixture(t, "search_rentals_brownstone_brooklyn_page1.json")
	sp := toSourceProperty(nodeByID(t, out, "4864627"), nil, nil, defaultSiteURL, domain.ListingRent)

	if sp.Property.Description != "" || sp.Property.DaysOnMarket != nil {
		t.Error("description/daysOnMarket need the detail page")
	}
	assertMoney(t, "price", sp.Property.Price, 7995)
	if len(sp.PhotoURLs) != 27 {
		t.Errorf("photoURLs = %d, want 27 from the search node alone", len(sp.PhotoURLs))
	}
}

func TestConvertRentedListing(t *testing.T) {
	out := decodeSearchFixture(t, "search_rentals_rented.json")
	sp := toSourceProperty(nodeByID(t, out, "9140"), decodeDetailFixture(t, "detail_rental_rented_9140.json"),
		nil, defaultSiteURL, domain.ListingRent)

	if sp.SourceStatus != "RENTED" {
		t.Errorf("sourceStatus = %q", sp.SourceStatus)
	}
	if sp.Property.Status != domain.StatusRented {
		t.Errorf("status = %q, want %q", sp.Property.Status, domain.StatusRented)
	}
	if sp.Property.ListingType != domain.ListingRent {
		t.Errorf("listingType = %q", sp.Property.ListingType)
	}
}

func TestParseRentalDetailPage(t *testing.T) {
	fromHTML, raw, err := parseDetailPage(readFixture(t, "detail_page_rental_4864627.html"))
	if err != nil {
		t.Fatalf("parseDetailPage: %v", err)
	}
	if fromHTML.ID != "4864627" || fromHTML.Status != "ACTIVE" {
		t.Errorf("id/status = %q/%q", fromHTML.ID, fromHTML.Status)
	}
	assertInt(t, "daysOnMarket", positive(fromHTML.DaysOnMarket), 338)
	if strings.HasPrefix(fromHTML.Description, "$") {
		t.Fatalf("description reference not resolved: %q", fromHTML.Description)
	}
	if got := money(fromHTML.Pricing.Price); got == nil || *got != 7995 {
		t.Errorf("pricing.price = %v", fromHTML.Pricing.Price)
	}
	if len(fromHTML.PropertyDetails.Amenities.List) == 0 {
		t.Error("amenities not parsed")
	}

	var round map[string]any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("raw provenance is not valid json: %v", err)
	}
	// noFee/availableAt/lease terms are deliberately not promoted to typed fields.
	if _, ok := round["availableAt"]; !ok {
		t.Error("raw provenance should retain availableAt")
	}

	fromJSON := decodeDetailFixture(t, "detail_rental_4864627.json")
	if fromHTML.Description != fromJSON.Description || len(fromHTML.Media.Photos) != len(fromJSON.Media.Photos) {
		t.Error("html and extracted-json rental fixtures disagree")
	}
}

func TestSearchRentalsSendsExpectedRequest(t *testing.T) {
	stub := &stubServer{pages: []string{rentalPageJSON(false, rentalNode("1", "/a", "ACTIVE"))}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	q := rentQuery()
	q.MaxPrice = domain.Money(6000)
	q.MinBeds = 1
	props, errs := collect(t, c.Search(t.Context(), q))
	if len(props) != 1 || errs[0] != nil {
		t.Fatalf("props=%d err=%v", len(props), errs[0])
	}
	if props[0].Property.ListingType != domain.ListingRent {
		t.Errorf("listingType = %q", props[0].Property.ListingType)
	}
	assertMoney(t, "price", props[0].Property.Price, 4200)

	if gql, _ := stub.lastBody["query"].(string); !strings.Contains(gql, "searchRentals(input: $input)") {
		t.Errorf("query root = %.60q", gql)
	}
	input := stub.lastBody["variables"].(map[string]any)["input"].(map[string]any)
	filters := input["filters"].(map[string]any)
	if got := filters["rentalStatus"]; got != "ACTIVE" {
		t.Errorf("rentalStatus = %v", got)
	}
	if _, ok := filters["saleStatus"]; ok {
		t.Error("rental search must not send saleStatus")
	}
	if got := filters["price"].(map[string]any)["upperBound"].(float64); got != 6000 {
		t.Errorf("price upperBound = %v", got)
	}
	if got := filters["bedrooms"].(map[string]any)["lowerBound"].(float64); got != 1 {
		t.Errorf("bedrooms lowerBound = %v", got)
	}
	if input["adStrategy"] != "NONE" {
		t.Errorf("adStrategy = %v", input["adStrategy"])
	}
}

func TestSearchRentalsPaginatesAndMapsStatus(t *testing.T) {
	stub := &stubServer{pages: []string{
		rentalPageJSON(true, rentalNode("1", "/a", "ACTIVE"), rentalNode("2", "/b", "IN_CONTRACT")),
		rentalPageJSON(false, rentalNode("3", "/c", "RENTED"), rentalNode("4", "/d", "NO_LONGER_AVAILABLE")),
	}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), rentQuery()))
	if len(props) != 4 {
		t.Fatalf("got %d properties, want 4", len(props))
	}
	for i, err := range errs {
		if err != nil {
			t.Errorf("item %d error: %v", i, err)
		}
	}
	if stub.apiCalls != 2 {
		t.Errorf("api calls = %d, want 2", stub.apiCalls)
	}
	want := []domain.PropertyStatus{
		domain.StatusActive, domain.StatusInContract, domain.StatusRented, domain.StatusDelisted,
	}
	for i, w := range want {
		if props[i].Property.Status != w {
			t.Errorf("props[%d].Status = %q, want %q", i, props[i].Property.Status, w)
		}
	}
}

func TestSearchRentalsMissingRoot(t *testing.T) {
	stub := &stubServer{pages: []string{searchPageJSON(false, node("1", "/a", "CONDO"))}}
	srv := httptest.NewServer(stub.handler())
	defer srv.Close()

	cfg := testConfig(srv)
	cfg.DisableDetail = true
	c := NewClient(srv.Client(), cfg)

	props, errs := collect(t, c.Search(t.Context(), rentQuery()))
	if len(errs) != 1 || errs[0] == nil || props[0].ProviderID != "" {
		t.Fatalf("a sales-shaped response must terminate a rental search: %v", errs)
	}
	if !strings.Contains(errs[0].Error(), "searchRentals") {
		t.Errorf("err = %v", errs[0])
	}
}
