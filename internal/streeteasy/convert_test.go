package streeteasy

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

func fixturePath(t *testing.T, name string) string {
	t.Helper()
	return filepath.Join("..", "..", "testdata", "streeteasy", name)
}

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(fixturePath(t, name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	return b
}

func decodeSearchFixture(t *testing.T, name string) *searchOutput {
	t.Helper()
	var resp graphQLResponse
	if err := json.Unmarshal(readFixture(t, name), &resp); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	out := ptr.First(resp.Data.SearchSales, resp.Data.SearchRentals)
	if out == nil {
		t.Fatalf("%s: missing searchSales/searchRentals", name)
	}
	return out
}

func decodeDetailFixture(t *testing.T, name string) *detailListing {
	t.Helper()
	var d detailListing
	if err := json.Unmarshal(readFixture(t, name), &d); err != nil {
		t.Fatalf("decode %s: %v", name, err)
	}
	return &d
}

func nodeByID(t *testing.T, out *searchOutput, id string) searchListing {
	t.Helper()
	for _, e := range out.Edges {
		var n searchListing
		if err := json.Unmarshal(e.Node, &n); err != nil {
			t.Fatalf("decode node: %v", err)
		}
		if n.ID == id {
			return n
		}
	}
	t.Fatalf("listing %s not in fixture", id)
	return searchListing{}
}

func TestDecodeSearchFixtures(t *testing.T) {
	bk := decodeSearchFixture(t, "search_brownstone_brooklyn.json")
	if bk.TotalCount != 320 {
		t.Errorf("totalCount = %d, want 320", bk.TotalCount)
	}
	if bk.PageInfo.HasNextPage {
		t.Error("brooklyn fixture should be a single page")
	}
	if len(bk.Edges) == 0 {
		t.Fatal("no edges")
	}

	types := map[domain.PropertyType]int{}
	for _, e := range bk.Edges {
		var n searchListing
		if err := json.Unmarshal(e.Node, &n); err != nil {
			t.Fatalf("decode node: %v", err)
		}
		if n.ID == "" || n.URLPath == "" {
			t.Errorf("node missing identity: %+v", n)
		}
		if e.Typename != "OrganicSaleEdge" {
			t.Errorf("unexpected edge type %q", e.Typename)
		}
		types[toPropertyType(n.BuildingType)]++
	}
	for _, want := range []domain.PropertyType{domain.PropertyCoop, domain.PropertyCondo, domain.PropertyTownhouse, domain.PropertyHouse, domain.PropertyOther} {
		if types[want] == 0 {
			t.Errorf("fixture covers no %s listings", want)
		}
	}

	p1 := decodeSearchFixture(t, "search_upper_west_side_page1.json")
	p2 := decodeSearchFixture(t, "search_upper_west_side_page2.json")
	if !p1.PageInfo.HasNextPage {
		t.Error("page1 should report hasNextPage")
	}
	if p2.PageInfo.HasNextPage {
		t.Error("page2 should be terminal")
	}
	if p1.TotalCount != p2.TotalCount {
		t.Errorf("totalCount differs across pages: %d vs %d", p1.TotalCount, p2.TotalCount)
	}
}

func TestConvertCoop(t *testing.T) {
	out := decodeSearchFixture(t, "search_brownstone_brooklyn.json")
	node := nodeByID(t, out, "1777453")
	detail := decodeDetailFixture(t, "detail_coop_1777453.json")

	sp := toSourceProperty(node, detail, json.RawMessage(`{}`), defaultSiteURL, domain.ListingSale)

	if sp.Provider != "streeteasy" {
		t.Errorf("provider = %q", sp.Provider)
	}
	if sp.ProviderID != "1777453" {
		t.Errorf("providerID = %q", sp.ProviderID)
	}
	if want := "https://streeteasy.com/building/288-larkspur-street-brooklyn/614"; sp.URL != want {
		t.Errorf("url = %q, want %q", sp.URL, want)
	}
	if sp.SourceStatus != "ACTIVE" {
		t.Errorf("sourceStatus = %q", sp.SourceStatus)
	}

	p := sp.Property
	if p.PropertyType != domain.PropertyCoop {
		t.Errorf("propertyType = %q", p.PropertyType)
	}
	if p.Status != domain.StatusActive {
		t.Errorf("status = %q", p.Status)
	}
	if p.ListingType != domain.ListingSale {
		t.Errorf("listingType = %q", p.ListingType)
	}
	if p.Currency != domain.CurrencyUSD {
		t.Errorf("currency = %q", p.Currency)
	}
	if p.Address.Street != "288 Larkspur Street" || p.Address.Unit != "614" || p.Address.Zip != "11205" {
		t.Errorf("address = %+v", p.Address)
	}
	if p.Address.Neighborhood != "Clinton Hill" {
		t.Errorf("neighborhood = %q", p.Address.Neighborhood)
	}
	if p.Geo == nil || p.Geo.Latitude == 0 {
		t.Errorf("geo = %+v", p.Geo)
	}
	assertMoney(t, "price", p.Price, 499000)
	assertMoney(t, "maintenance", p.Maintenance, 912)
	if p.CommonCharges != nil {
		t.Errorf("commonCharges = %v, want nil for a co-op", *p.CommonCharges)
	}
	// monthlyTaxes is 0 on this listing, which means "not applicable", not zero.
	if p.TaxesMonthly != nil {
		t.Errorf("taxesMonthly = %v, want nil", *p.TaxesMonthly)
	}
	if got := p.MonthlyCarrying(); got == nil || *got != 912 {
		t.Errorf("monthlyCarrying = %v, want 912", got)
	}
	assertInt(t, "bedrooms", p.Bedrooms, 1)
	assertInt(t, "sqft", p.Sqft, 711)
	assertInt(t, "daysOnMarket", p.DaysOnMarket, 426)
	if p.Bathrooms == nil || *p.Bathrooms != 1 {
		t.Errorf("bathrooms = %v, want 1", p.Bathrooms)
	}
	if len(p.Description) < 100 {
		t.Errorf("description too short (%d chars): %q", len(p.Description), p.Description)
	}

	if len(sp.PhotoURLs) != 8 {
		t.Errorf("photoURLs = %d, want 8", len(sp.PhotoURLs))
	}
	want := "https://photos.zillowstatic.com/fp/e47137fc31e9b66b594612a8f34510d9-uncropped_scaled_within_1536_1152.webp"
	if sp.PhotoURLs[0] != want {
		t.Errorf("photoURLs[0] = %q, want %q", sp.PhotoURLs[0], want)
	}
}

func TestConvertCondoAndTownhouse(t *testing.T) {
	out := decodeSearchFixture(t, "search_brownstone_brooklyn.json")

	condo := toSourceProperty(nodeByID(t, out, "1644015"), decodeDetailFixture(t, "detail_condo_1644015.json"), nil, defaultSiteURL, domain.ListingSale)
	if condo.Property.PropertyType != domain.PropertyCondo {
		t.Errorf("condo propertyType = %q", condo.Property.PropertyType)
	}
	assertMoney(t, "commonCharges", condo.Property.CommonCharges, 12484)
	assertMoney(t, "taxesMonthly", condo.Property.TaxesMonthly, 7568)
	if condo.Property.Maintenance != nil {
		t.Errorf("condo maintenance = %v, want nil", *condo.Property.Maintenance)
	}
	if got := condo.Property.MonthlyCarrying(); got == nil || *got != 12484+7568 {
		t.Errorf("monthlyCarrying = %v", got)
	}
	if condo.Property.Bathrooms == nil || *condo.Property.Bathrooms != 5 {
		t.Errorf("bathrooms = %v, want 5 (4 full + 2 half)", condo.Property.Bathrooms)
	}
	assertInt(t, "daysOnMarket", condo.Property.DaysOnMarket, 1305)
	if len(condo.PhotoURLs) != 63 {
		t.Errorf("photoURLs = %d, want 63", len(condo.PhotoURLs))
	}

	th := toSourceProperty(nodeByID(t, out, "1832614"), decodeDetailFixture(t, "detail_townhouse_1832614.json"), nil, defaultSiteURL, domain.ListingSale)
	if th.Property.PropertyType != domain.PropertyTownhouse {
		t.Errorf("townhouse propertyType = %q", th.Property.PropertyType)
	}
	assertMoney(t, "taxesMonthly", th.Property.TaxesMonthly, 211)
	// monthlyCommonCharges is 0 for a townhouse: not applicable.
	if th.Property.CommonCharges != nil {
		t.Errorf("townhouse commonCharges = %v, want nil", *th.Property.CommonCharges)
	}
	if th.Property.Address.Unit != "" {
		t.Errorf("townhouse unit = %q, want empty", th.Property.Address.Unit)
	}
}

func TestConvertWithoutDetail(t *testing.T) {
	out := decodeSearchFixture(t, "search_brownstone_brooklyn.json")
	sp := toSourceProperty(nodeByID(t, out, "1777453"), nil, nil, defaultSiteURL, domain.ListingSale)

	if sp.Property.Description != "" {
		t.Error("description should be empty without detail enrichment")
	}
	if sp.Property.DaysOnMarket != nil {
		t.Error("daysOnMarket should be nil without detail enrichment")
	}
	assertMoney(t, "price", sp.Property.Price, 499000)
	assertMoney(t, "maintenance", sp.Property.Maintenance, 912)
	if len(sp.PhotoURLs) != 8 {
		t.Errorf("photoURLs = %d, want 8 from the search node alone", len(sp.PhotoURLs))
	}
}

func TestToPropertyType(t *testing.T) {
	cases := map[string]domain.PropertyType{
		"CO_OP":       domain.PropertyCoop,
		"CONDOP":      domain.PropertyCoop,
		"CONDO":       domain.PropertyCondo,
		"TOWNHOUSE":   domain.PropertyTownhouse,
		"HOUSE":       domain.PropertyHouse,
		"MULTIFAMILY": domain.PropertyHouse,
		"TWOFAMILY":   domain.PropertyHouse,
		"THREEFAMILY": domain.PropertyHouse,
		"FOURFAMILY":  domain.PropertyHouse,
		"HYBRID":      domain.PropertyOther,
		"MIXED_USE":   domain.PropertyOther,
		"COMMERCIAL":  domain.PropertyOther,
		"RENTAL":      domain.PropertyOther,
		"":            domain.PropertyOther,
		"WHAT_IS_NEW": domain.PropertyOther,
	}
	for in, want := range cases {
		if got := toPropertyType(in); got != want {
			t.Errorf("toPropertyType(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestToStatus(t *testing.T) {
	cases := map[string]struct{ sale, rent domain.PropertyStatus }{
		"ACTIVE":                 {domain.StatusActive, domain.StatusActive},
		"LISTED":                 {domain.StatusActive, domain.StatusActive},
		"IN_CONTRACT":            {domain.StatusInContract, domain.StatusInContract},
		"PENDING":                {domain.StatusInContract, domain.StatusInContract},
		"SOLD":                   {domain.StatusSold, domain.StatusSold},
		"CLOSED":                 {domain.StatusSold, domain.StatusSold},
		"NO_LONGER_AVAILABLE":    {domain.StatusDelisted, domain.StatusDelisted},
		"DELISTED":               {domain.StatusDelisted, domain.StatusDelisted},
		"OFF_MARKET":             {domain.StatusDelisted, domain.StatusDelisted},
		"TEMPORARILY_OFF_MARKET": {domain.StatusDelisted, domain.StatusDelisted},
		// RENTED/COMPLETED are rental-only; on a sale they are unrecognized.
		"RENTED":    {domain.StatusActive, domain.StatusRented},
		"COMPLETED": {domain.StatusActive, domain.StatusRented},
		"rented":    {domain.StatusActive, domain.StatusRented},
		// An unknown status must never advance lifecycle toward delisted.
		"SOMETHING_NEW": {domain.StatusActive, domain.StatusActive},
		"":              {domain.StatusActive, domain.StatusActive},
	}
	for in, want := range cases {
		if got := toStatus(in, domain.ListingSale); got != want.sale {
			t.Errorf("toStatus(%q, sale) = %q, want %q", in, got, want.sale)
		}
		if got := toStatus(in, domain.ListingRent); got != want.rent {
			t.Errorf("toStatus(%q, rent) = %q, want %q", in, got, want.rent)
		}
	}
}

func TestMoneyTreatsZeroAsUnknown(t *testing.T) {
	zero := int64(0)
	neg := int64(-5)
	val := int64(1200)
	if money(num{}) != nil || money(num{&zero}) != nil || money(num{&neg}) != nil {
		t.Error("nil/zero/negative should convert to nil Money")
	}
	if got := money(num{&val}); got == nil || *got != domain.Money(1200) {
		t.Errorf("money(1200) = %v", got)
	}
}

func TestBathroomsDropImplausibleCounts(t *testing.T) {
	two, one, huge := int64(2), int64(1), int64(250)
	if got := bathrooms(num{&two}, num{&one}); got == nil || *got != 2.5 {
		t.Errorf("bathrooms(2 full, 1 half) = %v, want 2.5", got)
	}
	if got := bathrooms(num{&huge}, num{}); got != nil {
		t.Errorf("bathrooms(250) = %v, want nil (numeric(3,1) cannot hold it)", *got)
	}
	if bathrooms(num{}, num{}) != nil {
		t.Error("bathrooms(nil, nil) should be nil")
	}
}

func TestPhotoURLsDedupeAndSkipEmpty(t *testing.T) {
	node := searchListing{Photos: []photoRef{{Key: "aaa"}, {Key: ""}, {Key: "aaa"}, {Key: "bbb"}}}
	got := photoURLs(node, nil)
	if len(got) != 2 {
		t.Fatalf("got %d urls, want 2: %v", len(got), got)
	}
	if got[0] != photoBaseURL+"aaa"+photoVariant || got[1] != photoBaseURL+"bbb"+photoVariant {
		t.Errorf("urls = %v", got)
	}
	if photoURLs(searchListing{}, nil) != nil {
		t.Error("no keys should yield nil")
	}

	// A handful of rental nodes carry only the card thumbnail.
	lead := photoURLs(searchListing{LeadMedia: &leadMediaRef{Photo: photoRef{Key: "ccc"}}}, nil)
	if len(lead) != 1 || lead[0] != photoBaseURL+"ccc"+photoVariant {
		t.Errorf("leadMedia fallback = %v", lead)
	}
	if got := photoURLs(node, &detailListing{}); len(got) != 2 {
		t.Errorf("leadMedia must not override real photos: %v", got)
	}
	// A runaway gallery is capped rather than turned into hundreds of photo rows.
	many := make([]photoRef, maxPhotoURLs+40)
	for i := range many {
		many[i] = photoRef{Key: fmt.Sprintf("k%d", i)}
	}
	if got := photoURLs(searchListing{Photos: many}, nil); len(got) != maxPhotoURLs {
		t.Errorf("got %d urls from %d keys, want the cap %d", len(got), len(many), maxPhotoURLs)
	}
}

func TestNormalizeUnit(t *testing.T) {
	cases := []struct{ display, unit, want string }{
		{"#614", "614", "614"},
		{"", "4B", "4B"},
		{"#PHD", "#PHD", "PHD"},
		{"", "", ""},
		{"#TWNHSE", "TWNHSE", ""},
		{"RESIDENTIAL", "RESIDENTIAL", ""},
		{"", "HOUSE", ""},
		{"", "house", ""},
	}
	for _, c := range cases {
		if got := normalizeUnit(c.display, c.unit); got != c.want {
			t.Errorf("normalizeUnit(%q,%q) = %q, want %q", c.display, c.unit, got, c.want)
		}
	}
}

func TestParseAreas(t *testing.T) {
	got, err := parseAreas([]string{"319", " 305 "})
	if err != nil {
		t.Fatalf("parseAreas: %v", err)
	}
	if len(got) != 2 || got[0] != 319 || got[1] != 305 {
		t.Errorf("parseAreas = %v", got)
	}
	if _, err := parseAreas(nil); err == nil {
		t.Error("empty areas should error")
	}
	if _, err := parseAreas([]string{"park-slope"}); err == nil {
		t.Error("non-numeric area should error")
	}
}

func assertMoney(t *testing.T, name string, got *domain.Money, want int64) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %d", name, want)
		return
	}
	if int64(*got) != want {
		t.Errorf("%s = %d, want %d", name, int64(*got), want)
	}
}

func assertInt(t *testing.T, name string, got *int, want int) {
	t.Helper()
	if got == nil {
		t.Errorf("%s = nil, want %d", name, want)
		return
	}
	if *got != want {
		t.Errorf("%s = %d, want %d", name, *got, want)
	}
}

// bedroomCount 0 is a studio, a value the search must keep (a nil count means
// unknown); sqft and days-on-market still treat 0 as unknown.
func TestConvertKeepsStudioBedrooms(t *testing.T) {
	out := decodeSearchFixture(t, "search_brownstone_brooklyn.json")
	node := nodeByID(t, out, "1777453")
	node.BedroomCount = num{ptr.To(int64(0))}
	node.LivingAreaSize = num{ptr.To(int64(0))}
	sp := toSourceProperty(node, nil, nil, defaultSiteURL, domain.ListingSale)
	if sp.Property.Bedrooms == nil || *sp.Property.Bedrooms != 0 {
		t.Fatalf("bedrooms = %v, want 0 (studio)", sp.Property.Bedrooms)
	}
	if sp.Property.Sqft != nil {
		t.Fatalf("sqft = %v, want nil for 0", sp.Property.Sqft)
	}
	node.BedroomCount = num{ptr.To(int64(-1))}
	if sp := toSourceProperty(node, nil, nil, defaultSiteURL, domain.ListingSale); sp.Property.Bedrooms != nil {
		t.Fatalf("bedrooms = %v, want nil for a negative count", sp.Property.Bedrooms)
	}
}

// Numeric fields tolerate the representations a provider drifts between; a
// value that is not a number at all reads as absent, never fails the listing.
func TestNumAcceptsDriftedRepresentations(t *testing.T) {
	var node searchListing
	raw := `{"id":"1","price":"1500000","monthlyTaxes":1200.0,"bedroomCount":"2","livingAreaSize":"nine","fullBathroomCount":null}`
	if err := json.Unmarshal([]byte(raw), &node); err != nil {
		t.Fatalf("drifted numerics must not fail the listing: %v", err)
	}
	if got := money(node.Price); got == nil || *got != 1500000 {
		t.Errorf("string price = %v, want 1500000", got)
	}
	if got := money(node.MonthlyTaxes); got == nil || *got != 1200 {
		t.Errorf("float taxes = %v, want 1200", got)
	}
	assertInt(t, "string bedrooms", count(node.BedroomCount), 2)
	if positive(node.LivingAreaSize) != nil || node.FullBathroomCount.v != nil {
		t.Error("non-numeric and null must read as absent")
	}
	var frac num
	if err := frac.UnmarshalJSON([]byte(`1.5`)); err != nil || frac.v != nil {
		t.Error("a fractional value is not an integer field; must read as absent")
	}
}
