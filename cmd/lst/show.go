package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/store"
)

type detail struct {
	property   domain.Property
	priceEvent []domain.PriceEvent
	photos     []domain.Photo
	provenance store.ProvenanceSummary
	verdicts   []domain.Verdict
}

func newShowCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "show <id>",
		Short: "Show one listing in full detail",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := strconv.ParseInt(args[0], 10, 64)
			if err != nil {
				return fmt.Errorf("listing id %q is not a number", args[0])
			}
			ctx, cancel := commandContext(cmd)
			defer cancel()

			s, err := a.store(ctx)
			if err != nil {
				return err
			}
			// No photo bytes wanted here, so a nil photo store is fine.
			svc := listings.NewService(s, nil)
			det, err := svc.Listing(ctx, domain.PropertyID(id), listings.PhotoRequest{})
			if err != nil {
				return err
			}
			verdicts, err := svc.VerdictsFor(ctx, domain.PropertyID(id))
			if err != nil {
				return err
			}
			return renderDetail(cmd.OutOrStdout(), detail{
				property:   det.Property,
				priceEvent: det.PriceEvents,
				photos:     det.Photos.All,
				provenance: det.Provenance,
				verdicts:   verdicts,
			})
		},
	}
}

func renderDetail(w io.Writer, d detail) error {
	p := d.property
	if _, err := fmt.Fprintf(w, "Listing %d\n", p.ID); err != nil {
		return err
	}
	fields := [][2]string{
		{"address", streetLine(p.Address)},
		{"neighborhood", formatText(p.Address.Neighborhood)},
		{"zip", formatText(p.Address.Zip)},
		{"geo", formatGeo(p.Geo)},
		{"listing type", formatText(string(p.ListingType))},
		{"property type", formatText(string(p.PropertyType))},
		{"status", formatText(string(p.Status))},
		{"price", formatMoney(p.Price)},
		{"currency", formatText(currencyOf(p))},
		{"beds", formatInt(p.Bedrooms)},
		{"baths", formatBaths(p.Bathrooms)},
		{"sqft", formatInt(p.Sqft)},
		{"maintenance", formatMoney(p.Maintenance)},
		{"common charges", formatMoney(p.CommonCharges)},
		{"taxes monthly", formatMoney(p.TaxesMonthly)},
		{"monthly carrying", formatMoney(p.MonthlyCarrying())},
		{"days on market", formatInt(p.DaysOnMarket)},
		{"first seen", formatTime(p.FirstSeen)},
		{"last seen", formatTime(p.LastSeen)},
		{"material changed", formatTime(p.MaterialChangedAt)},
	}
	if err := renderFields(w, fields); err != nil {
		return err
	}

	if err := section(w, "Description"); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(w, "  %s\n", formatText(p.Description)); err != nil {
		return err
	}

	if err := section(w, "Price history"); err != nil {
		return err
	}
	if err := renderPriceHistory(w, d.priceEvent); err != nil {
		return err
	}

	if err := section(w, "Provenance"); err != nil {
		return err
	}
	if err := renderProvenance(w, d.provenance); err != nil {
		return err
	}

	if err := section(w, "Photos"); err != nil {
		return err
	}
	if err := renderPhotos(w, d.photos); err != nil {
		return err
	}

	if err := section(w, "Verdicts"); err != nil {
		return err
	}
	return renderVerdicts(w, d.verdicts)
}

func section(w io.Writer, title string) error {
	_, err := fmt.Fprintf(w, "\n%s\n", title)
	return err
}

func currencyOf(p domain.Property) string {
	if p.Currency == "" {
		return string(domain.CurrencyUSD)
	}
	return string(p.Currency)
}

func renderPriceHistory(w io.Writer, events []domain.PriceEvent) error {
	if len(events) == 0 {
		_, err := fmt.Fprintln(w, "  no recorded price changes")
		return err
	}
	rows := make([][]string, 0, len(events))
	for i, e := range events {
		change := unknown
		if i > 0 {
			change = formatDelta(int64(e.Price - events[i-1].Price))
		}
		rows = append(rows, []string{
			"  " + formatTime(e.ObservedAt),
			formatDollars(e.Price.Dollars()),
			change,
		})
	}
	return renderTable(w, []string{"  OBSERVED", "PRICE", "CHANGE"}, rows)
}

func formatDelta(v int64) string {
	if v > 0 {
		return "+" + formatDollars(v)
	}
	return formatDollars(v)
}

func renderProvenance(w io.Writer, p store.ProvenanceSummary) error {
	return renderFields(w, [][2]string{
		{"provider", formatText(p.Provider)},
		{"provider id", formatText(p.ProviderID)},
		{"url", formatText(p.URL)},
		{"source status", formatText(p.SourceStatus)},
		{"first seen", formatTime(p.FirstSeenAt)},
		{"last seen", formatTime(p.LastSeenAt)},
		{"fetched", formatTime(p.FetchedAt)},
		{"missing runs", strconv.Itoa(p.MissingRuns)},
	})
}

func renderPhotos(w io.Writer, photos []domain.Photo) error {
	if len(photos) == 0 {
		_, err := fmt.Fprintln(w, "  no photos")
		return err
	}
	rows := make([][]string, 0, len(photos))
	for _, p := range photos {
		rows = append(rows, []string{
			"  " + strconv.Itoa(p.Position),
			formatBool(p.CachedPath != ""),
			formatDimensions(p.Width, p.Height),
			formatText(p.MIMEType),
			formatText(p.SourceURL),
		})
	}
	return renderTable(w, []string{"  POS", "CACHED", "DIMENSIONS", "MIME", "SOURCE"}, rows)
}

func formatDimensions(width, height int) string {
	if width <= 0 || height <= 0 {
		return unknown
	}
	return fmt.Sprintf("%dx%d", width, height)
}

func renderVerdicts(w io.Writer, verdicts []domain.Verdict) error {
	if len(verdicts) == 0 {
		_, err := fmt.Fprintln(w, "  no verdicts recorded")
		return err
	}
	rows := make([][]string, 0, len(verdicts))
	for _, v := range verdicts {
		rows = append(rows, []string{
			"  " + strconv.FormatInt(int64(v.ID), 10),
			formatTime(v.CreatedAt),
			formatText(string(v.Kind)),
			formatText(v.Note),
		})
	}
	return renderTable(w, []string{"  ID", "RECORDED", "VERDICT", "NOTE"}, rows)
}
