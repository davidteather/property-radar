package store

import (
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

func TestPropertyRowToDomain(t *testing.T) {
	now := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	unit := "4B"
	price := int32(900_000)
	beds := int16(2)
	baths := 1.5
	lat, lng := 40.66, -73.98
	usd := "USD"

	row := propertyRow{
		ID:                7,
		CanonicalAddress:  "123 Prospect Park West",
		Unit:              &unit,
		Lat:               &lat,
		Lng:               &lng,
		ListingType:       "sale",
		Status:            "active",
		FirstSeen:         now,
		LastSeen:          now,
		MaterialChangedAt: now,
		Price:             &price,
		Currency:          &usd,
		Beds:              &beds,
		Baths:             &baths,
	}

	got := row.toDomain()
	if got.ID != 7 || got.Address.Unit != "4B" || got.Address.Neighborhood != "" {
		t.Fatalf("address/id conversion = %+v", got)
	}
	if got.Price == nil || *got.Price != domain.Money(900_000) {
		t.Fatalf("price = %v", got.Price)
	}
	if got.Bedrooms == nil || *got.Bedrooms != 2 || got.Bathrooms == nil || *got.Bathrooms != 1.5 {
		t.Fatalf("beds/baths = %v %v", got.Bedrooms, got.Bathrooms)
	}
	if got.Sqft != nil || got.Maintenance != nil || got.DaysOnMarket != nil {
		t.Fatalf("NULL columns did not stay nil: %+v", got)
	}
	if got.Geo == nil || got.Geo.Latitude != lat {
		t.Fatalf("geo = %+v", got.Geo)
	}
	if got.PropertyType != "" || got.Description != "" {
		t.Fatalf("NULL text columns = %q %q", got.PropertyType, got.Description)
	}
}

func TestPropertyRowGeoNeedsBothCoordinates(t *testing.T) {
	lat := 40.66
	row := propertyRow{ID: 1, ListingType: "sale", Status: "active", Lat: &lat}
	if got := row.toDomain(); got.Geo != nil {
		t.Fatalf("geo = %+v, want nil when longitude is missing", got.Geo)
	}
}

func TestCurrencyColumnRoundTrip(t *testing.T) {
	if got := currencyFromColumn(nil); got != domain.CurrencyUSD {
		t.Fatalf("nil currency = %q, want USD", got)
	}
	empty := ""
	if got := currencyFromColumn(&empty); got != domain.CurrencyUSD {
		t.Fatalf("empty currency = %q, want USD", got)
	}
	if got := currencyToColumn(""); got != "USD" {
		t.Fatalf("empty domain currency = %q, want USD", got)
	}
	if got := currencyToColumn(domain.CurrencyUSD); got != "USD" {
		t.Fatalf("USD = %q", got)
	}
}

func TestRunErrorsJSONRoundTrip(t *testing.T) {
	encoded, err := runErrorsToJSON(nil)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if encoded != nil {
		t.Fatalf("empty item errors encoded to %q, want SQL NULL", encoded)
	}

	in := []domain.RunError{{ProviderID: "1001", Message: "missing price"}}
	encoded, err = runErrorsToJSON(in)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	if string(encoded) != `[{"provider_id":"1001","message":"missing price"}]` {
		t.Fatalf("encoded = %s", encoded)
	}

	out, err := runErrorsFromJSON(encoded)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(out) != 1 || out[0] != in[0] {
		t.Fatalf("decoded = %+v", out)
	}

	if out, err := runErrorsFromJSON([]byte("null")); err != nil || out != nil {
		t.Fatalf("null decode = %+v (err %v)", out, err)
	}
}

func TestRunRowToDomain(t *testing.T) {
	finished := time.Date(2026, 8, 1, 13, 0, 0, 0, time.UTC)
	seen := int32(120)
	row := runRow{
		ID:           4,
		Provider:     "streeteasy",
		ScopeHash:    "scope-a",
		StartedAt:    finished.Add(-time.Hour),
		FinishedAt:   &finished,
		Complete:     true,
		ListingsSeen: &seen,
		Errors:       []byte(`[{"provider_id":"9","message":"bad"}]`),
	}
	got, err := row.toDomain()
	if err != nil {
		t.Fatalf("convert: %v", err)
	}
	if got.ID != domain.IngestRunID(4) || got.ListingsSeen != 120 || got.Created != 0 {
		t.Fatalf("run = %+v", got)
	}
	if len(got.ItemErrors) != 1 || got.ItemErrors[0].ProviderID != "9" {
		t.Fatalf("item errors = %+v", got.ItemErrors)
	}
}

func TestClampLimit(t *testing.T) {
	for _, tc := range []struct {
		in, want int
	}{
		{0, DefaultReadLimit},
		{-5, DefaultReadLimit},
		{10, 10},
		{MaxReadLimit, MaxReadLimit},
		{1000, MaxReadLimit},
	} {
		if got := clampLimit(tc.in); got != tc.want {
			t.Fatalf("clampLimit(%d) = %d, want %d", tc.in, got, tc.want)
		}
	}
}

func TestIsNilValue(t *testing.T) {
	var nilMoney *domain.Money
	var nilInt *int
	var nilFloat *float64
	if !isNilValue(nilMoney) || !isNilValue(nilInt) || !isNilValue(nilFloat) || !isNilValue("") {
		t.Fatal("unknown values must be skipped for field provenance")
	}
	m := domain.Money(1)
	i := 1
	f := 1.5
	if isNilValue(&m) || isNilValue(&i) || isNilValue(&f) || isNilValue("active") {
		t.Fatal("known values must be recorded")
	}
}

// fitPtr maps a value the column cannot hold to NULL rather than wrapping it.
func TestFitPtrRejectsOverflow(t *testing.T) {
	if fitPtr[int32]((*int)(nil)) != nil {
		t.Fatal("nil in, nil out")
	}
	if got := fitPtr[int32](ptr.To(900_000)); got == nil || *got != 900_000 {
		t.Fatalf("fitPtr(900000) = %v", got)
	}
	if got := fitPtr[int32](ptr.To(int64(1) << 40)); got != nil {
		t.Fatalf("fitPtr(2^40) = %v, want nil", *got)
	}
	if got := fitPtr[int16](ptr.To(40_000)); got != nil {
		t.Fatalf("fitPtr[int16](40000) = %v, want nil", *got)
	}
	if got := fitPtr[int16](ptr.To(-3)); got == nil || *got != -3 {
		t.Fatalf("fitPtr[int16](-3) = %v", got)
	}
}
