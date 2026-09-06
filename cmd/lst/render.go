package main

import (
	"fmt"
	"io"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

// unknown marks a nil/absent value (distinct from zero).
const unknown = "-"

const timeLayout = "2006-01-02 15:04Z"

func renderRows(w io.Writer, rows [][]string) error {
	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, row := range rows {
		if _, err := fmt.Fprintln(tw, strings.Join(row, "\t")); err != nil {
			return err
		}
	}
	return tw.Flush()
}

func renderTable(w io.Writer, headers []string, rows [][]string) error {
	return renderRows(w, append([][]string{headers}, rows...))
}

func renderFields(w io.Writer, fields [][2]string) error {
	rows := make([][]string, 0, len(fields))
	for _, f := range fields {
		rows = append(rows, []string{"  " + f[0], f[1]})
	}
	return renderRows(w, rows)
}

func formatDollars(v int64) string {
	sign := ""
	if v < 0 {
		sign, v = "-", -v
	}
	digits := strconv.FormatInt(v, 10)
	var b strings.Builder
	b.WriteString(sign)
	b.WriteByte('$')
	for i := range len(digits) {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteByte(digits[i])
	}
	return b.String()
}

func formatMoney(m *domain.Money) string {
	if m == nil {
		return unknown
	}
	return formatDollars(m.Dollars())
}

func formatInt(v *int) string {
	if v == nil {
		return unknown
	}
	return strconv.Itoa(*v)
}

func formatBaths(v *float64) string {
	if v == nil {
		return unknown
	}
	return strconv.FormatFloat(*v, 'f', -1, 64)
}

func formatBedsBaths(beds *int, baths *float64) string {
	return formatInt(beds) + "/" + formatBaths(baths)
}

func formatText(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return unknown
	}
	return s
}

func formatTime(t time.Time) string {
	if t.IsZero() {
		return unknown
	}
	return t.UTC().Format(timeLayout)
}

func formatDuration(started time.Time, finished *time.Time) string {
	if finished == nil {
		return "running"
	}
	d := finished.Sub(started)
	if d < 0 {
		return unknown
	}
	return d.Round(time.Second).String()
}

func formatBool(b bool) string {
	if b {
		return "yes"
	}
	return "no"
}

func formatGeo(g *domain.GeoPoint) string {
	if g == nil {
		return unknown
	}
	return fmt.Sprintf("%.5f, %.5f", g.Latitude, g.Longitude)
}

// streetLine is the address without the neighborhood (rendered as its own column).
func streetLine(a domain.Address) string {
	street := formatText(a.Street)
	if a.Unit != "" {
		street += " #" + a.Unit
	}
	return street
}

func truncate(s string, max int) string {
	if len([]rune(s)) <= max {
		return s
	}
	return string([]rune(s)[:max-1]) + "…"
}

// oneLine keeps multi-line driver errors from breaking column alignment.
func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}

func shortHash(h string) string {
	if h == "" {
		return unknown
	}
	return truncate(h, 10)
}

var listHeaders = []string{
	"ID", "ADDRESS", "PRICE", "BD/BA", "SQFT", "TYPE", "CARRY", "DOM", "NEIGHBORHOOD", "STATUS",
}

func listRow(p domain.Property) []string {
	return []string{
		strconv.FormatInt(int64(p.ID), 10),
		truncate(streetLine(p.Address), 40),
		formatMoney(p.Price),
		formatBedsBaths(p.Bedrooms, p.Bathrooms),
		formatInt(p.Sqft),
		formatText(string(p.PropertyType)),
		formatMoney(p.MonthlyCarrying()),
		formatInt(p.DaysOnMarket),
		formatText(p.Address.Neighborhood),
		formatText(string(p.Status)),
	}
}
