package main

import (
	"slices"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/store"
)

func TestListOptionsFilter(t *testing.T) {
	opts := listOptions{
		maxPrice:      1_250_000,
		minBeds:       2,
		neighborhoods: []string{"Park Slope", "Fort Greene"},
		propertyTypes: []string{"coop", "condo"},
		listingType:   "sale",
		limit:         25,
	}
	f, err := opts.filter()
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if f.MaxPrice == nil || *f.MaxPrice != domain.Money(1_250_000) {
		t.Errorf("MaxPrice = %v", f.MaxPrice)
	}
	if f.MinBeds == nil || *f.MinBeds != 2 {
		t.Errorf("MinBeds = %v", f.MinBeds)
	}
	if f.ListingType != domain.ListingSale {
		t.Errorf("ListingType = %q", f.ListingType)
	}
	want := []domain.PropertyType{domain.PropertyCoop, domain.PropertyCondo}
	if !slices.Equal(f.PropertyTypes, want) {
		t.Errorf("PropertyTypes = %v, want %v", f.PropertyTypes, want)
	}
	if !slices.Equal(f.Neighborhoods, opts.neighborhoods) {
		t.Errorf("Neighborhoods = %v", f.Neighborhoods)
	}
	if f.Limit != 25 {
		t.Errorf("Limit = %d", f.Limit)
	}
}

func TestListOptionsFilterTreatsZeroAsUnconstrained(t *testing.T) {
	f, err := listOptions{listingType: "sale"}.filter()
	if err != nil {
		t.Fatalf("filter: %v", err)
	}
	if f.MaxPrice != nil || f.MinBeds != nil {
		t.Errorf("zero flags should leave price/beds unconstrained: %+v", f)
	}
	if f.PropertyTypes != nil || f.Neighborhoods != nil {
		t.Errorf("unset repeatables should stay nil: %+v", f)
	}
}

func TestListOptionsFilterRejectsUnknownValues(t *testing.T) {
	if _, err := (listOptions{listingType: "lease"}).filter(); err == nil {
		t.Error("unknown listing type should fail")
	}
	if _, err := (listOptions{listingType: "sale", propertyTypes: []string{"yurt"}}).filter(); err == nil {
		t.Error("unknown property type should fail")
	}
	if _, err := (listOptions{listingType: "sale", maxPrice: -1}).filter(); err == nil {
		t.Error("negative max price should fail")
	}
	if _, err := (listOptions{listingType: "sale", maxPrice: listings.MaxMoney + 1}).filter(); err == nil {
		t.Error("max price beyond the column width should fail instead of wrapping")
	}
	if _, err := (listOptions{listingType: "sale", minBeds: listings.MaxBeds + 1}).filter(); err == nil {
		t.Error("min beds beyond the column width should fail instead of wrapping")
	}
}

func TestListDefaultLimitMatchesStore(t *testing.T) {
	cmd := newListCmd(&app{})
	limit, err := cmd.Flags().GetInt("limit")
	if err != nil {
		t.Fatalf("limit flag: %v", err)
	}
	if limit != store.DefaultReadLimit {
		t.Errorf("default limit = %d, want %d", limit, store.DefaultReadLimit)
	}
	listingType, err := cmd.Flags().GetString("listing-type")
	if err != nil {
		t.Fatalf("listing-type flag: %v", err)
	}
	if listingType != string(domain.ListingSale) {
		t.Errorf("default listing type = %q, want sale", listingType)
	}
}
