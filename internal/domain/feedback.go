package domain

import "time"

type VerdictID int64

type VerdictKind string

const (
	VerdictLove    VerdictKind = "love"
	VerdictMaybe   VerdictKind = "maybe"
	VerdictDislike VerdictKind = "dislike"
)

// Verdict rows are immutable; a changed opinion appends, and the latest row per
// property is current.
type Verdict struct {
	ID         VerdictID
	PropertyID PropertyID
	Kind       VerdictKind
	Note       string
	CreatedAt  time.Time
}

// Rubric is an append-only taste summary; raw verdicts stay authoritative.
// ThroughVerdictID makes staleness measurable.
type Rubric struct {
	ID               int64
	Content          string
	ThroughVerdictID VerdictID
	CreatedAt        time.Time
}

// Profile holds hard filters only; nil means unconstrained.
type Profile struct {
	MaxPrice           *Money
	MinBeds            *int
	MinBaths           *float64
	MaxMonthlyCarrying *Money
	ListingType        ListingType
	Neighborhoods      []string
	PropertyTypes      []PropertyType
	UpdatedAt          time.Time
}
