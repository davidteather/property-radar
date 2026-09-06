package domain_test

import (
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

func money(v int64) *domain.Money {
	m := domain.Money(v)
	return &m
}

func TestMonthlyCarrying(t *testing.T) {
	tests := []struct {
		name string
		p    domain.Property
		want *domain.Money
	}{
		{"all unknown", domain.Property{}, nil},
		{"maintenance only", domain.Property{Maintenance: money(1200)}, money(1200)},
		{"partial sum is a lower bound", domain.Property{CommonCharges: money(800), TaxesMonthly: money(300)}, money(1100)},
		{"all known", domain.Property{Maintenance: money(1200), CommonCharges: money(800), TaxesMonthly: money(300)}, money(2300)},
		{"known zero is known", domain.Property{TaxesMonthly: money(0)}, money(0)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.p.MonthlyCarrying()
			switch {
			case (got == nil) != (tt.want == nil):
				t.Fatalf("MonthlyCarrying() = %v, want %v", got, tt.want)
			case got != nil && *got != *tt.want:
				t.Fatalf("MonthlyCarrying() = %d, want %d", *got, *tt.want)
			}
		})
	}
}

func TestAddressString(t *testing.T) {
	tests := []struct {
		name string
		a    domain.Address
		want string
	}{
		{"street only", domain.Address{Street: "123 Prospect Park West"}, "123 Prospect Park West"},
		{"with unit", domain.Address{Street: "123 Prospect Park West", Unit: "4B"}, "123 Prospect Park West #4B"},
		{
			"with unit and neighborhood",
			domain.Address{Street: "123 Prospect Park West", Unit: "4B", Neighborhood: "Park Slope"},
			"123 Prospect Park West #4B, Park Slope",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.a.String(); got != tt.want {
				t.Fatalf("String() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMoneyString(t *testing.T) {
	if got := domain.Money(925_000).String(); got != "$925000" {
		t.Fatalf("String() = %q, want %q", got, "$925000")
	}
}
