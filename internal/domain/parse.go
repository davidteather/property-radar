package domain

import (
	"fmt"
	"strings"
)

// ParseListingType parses a listing type case-insensitively, ignoring surrounding whitespace.
func ParseListingType(s string) (ListingType, error) {
	switch v := ListingType(strings.ToLower(strings.TrimSpace(s))); v {
	case ListingSale, ListingRent:
		return v, nil
	default:
		return "", fmt.Errorf("listing type %q is not one of sale, rent", s)
	}
}

// ParsePropertyType parses a property type case-insensitively, ignoring surrounding whitespace.
func ParsePropertyType(s string) (PropertyType, error) {
	switch v := PropertyType(strings.ToLower(strings.TrimSpace(s))); v {
	case PropertyCoop, PropertyCondo, PropertyTownhouse, PropertyHouse, PropertyOther:
		return v, nil
	default:
		return "", fmt.Errorf("property type %q is not one of coop, condo, townhouse, house, other", s)
	}
}

// ParseVerdictKind parses a verdict kind case-insensitively, ignoring surrounding whitespace.
func ParseVerdictKind(s string) (VerdictKind, error) {
	switch v := VerdictKind(strings.ToLower(strings.TrimSpace(s))); v {
	case VerdictLove, VerdictMaybe, VerdictDislike:
		return v, nil
	default:
		return "", fmt.Errorf("verdict %q is not one of love, maybe, dislike", s)
	}
}
