package domain

import "time"

// CorpusStats is a whole-corpus snapshot for callers deciding what to do next:
// an empty corpus means "queue a crawl and tell the user to wait", a stale
// LastCrawlAt explains missing fresh listings.
type CorpusStats struct {
	Listings       int
	ActiveListings int
	ActiveSale     int
	ActiveRent     int
	PhotosCached   int
	Verdicts       int
	Lists          int
	PendingCrawls  int // once targets still pending or running
	StandingScopes int // enabled standing targets
	LastCrawlAt    *time.Time
	// Neighborhoods lists the canonical names active listings carry, busiest
	// first, so a caller can spell a neighborhood filter exactly.
	Neighborhoods []NeighborhoodCount
}

type NeighborhoodCount struct {
	Name   string
	Active int
}
