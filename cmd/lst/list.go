package main

import (
	"fmt"
	"io"
	"strings"

	"github.com/spf13/cobra"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

type listOptions struct {
	maxPrice      int
	minBeds       int
	neighborhoods []string
	propertyTypes []string
	listingType   string
	limit         int
}

var (
	listingTypes  = []domain.ListingType{domain.ListingSale, domain.ListingRent}
	propertyTypes = []domain.PropertyType{
		domain.PropertyCoop, domain.PropertyCondo,
		domain.PropertyTownhouse, domain.PropertyHouse, domain.PropertyOther,
	}
)

// Zero max-price/min-beds mean "unconstrained": neither is a useful filter.
// This calls the store directly, so it enforces the service's column limits itself.
func (o listOptions) filter() (listings.SearchFilter, error) {
	f := listings.SearchFilter{
		Neighborhoods: o.neighborhoods,
		Limit:         o.limit,
	}
	switch {
	case o.maxPrice < 0:
		return listings.SearchFilter{}, fmt.Errorf("--max-price %d is negative", o.maxPrice)
	case o.maxPrice > listings.MaxMoney:
		return listings.SearchFilter{}, fmt.Errorf("--max-price %d exceeds the maximum %d", o.maxPrice, listings.MaxMoney)
	case o.maxPrice > 0:
		f.MaxPrice = ptr.To(domain.Money(o.maxPrice))
	}
	switch {
	case o.minBeds < 0:
		return listings.SearchFilter{}, fmt.Errorf("--min-beds %d is negative", o.minBeds)
	case o.minBeds > listings.MaxBeds:
		return listings.SearchFilter{}, fmt.Errorf("--min-beds %d exceeds the maximum %d", o.minBeds, listings.MaxBeds)
	case o.minBeds > 0:
		f.MinBeds = ptr.To(o.minBeds)
	}
	if o.listingType != "" {
		lt, err := domain.ParseListingType(o.listingType)
		if err != nil {
			return listings.SearchFilter{}, err
		}
		f.ListingType = lt
	}
	for _, t := range o.propertyTypes {
		pt, err := domain.ParsePropertyType(t)
		if err != nil {
			return listings.SearchFilter{}, err
		}
		f.PropertyTypes = append(f.PropertyTypes, pt)
	}
	return f, nil
}

func newListCmd(a *app) *cobra.Command {
	var opts listOptions
	cmd := &cobra.Command{
		Use:   "list",
		Short: "List active listings as a compact table",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			filter, err := opts.filter()
			if err != nil {
				return err
			}
			ctx, cancel := commandContext(cmd)
			defer cancel()

			s, err := a.store(ctx)
			if err != nil {
				return err
			}
			props, err := s.SearchProperties(ctx, filter)
			if err != nil {
				return err
			}
			return renderList(cmd.OutOrStdout(), props)
		},
	}
	f := cmd.Flags()
	f.IntVar(&opts.maxPrice, "max-price", 0, "maximum price in dollars (0 = no cap)")
	f.IntVar(&opts.minBeds, "min-beds", 0, "minimum bedrooms (0 = no minimum)")
	f.StringArrayVar(&opts.neighborhoods, "neighborhood", nil, "neighborhood filter (repeatable)")
	f.StringArrayVar(&opts.propertyTypes, "type", nil, "property type filter, one of "+joinPropertyTypes()+" (repeatable)")
	f.StringVar(&opts.listingType, "listing-type", string(domain.ListingSale), "listing type: "+joinListingTypes())
	f.IntVar(&opts.limit, "limit", listings.DefaultReadLimit, fmt.Sprintf("maximum rows (capped at %d)", listings.MaxBulkLimit))
	return cmd
}

func renderList(w io.Writer, props []domain.Property) error {
	if len(props) == 0 {
		_, err := fmt.Fprintln(w, "no active listings matched")
		return err
	}
	return renderTable(w, listHeaders, xslices.Map(props, listRow))
}

func joinListingTypes() string {
	return strings.Join(xslices.Map(listingTypes, func(t domain.ListingType) string { return string(t) }), "|")
}

func joinPropertyTypes() string {
	return strings.Join(xslices.Map(propertyTypes, func(t domain.PropertyType) string { return string(t) }), "|")
}
