package domain

import "strings"

// Address holds normalized parts. Normalization lives here, not in adapters:
// the form is display output now and matcher input later.
type Address struct {
	Street       string
	Unit         string
	Neighborhood string
	Zip          string
}

func (a Address) String() string {
	var b strings.Builder
	b.WriteString(a.Street)
	if a.Unit != "" {
		b.WriteString(" #")
		b.WriteString(a.Unit)
	}
	if a.Neighborhood != "" {
		b.WriteString(", ")
		b.WriteString(a.Neighborhood)
	}
	return b.String()
}

type GeoPoint struct {
	Latitude  float64
	Longitude float64
}
