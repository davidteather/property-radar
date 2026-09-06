// Package ingest is the crawl use-case: orchestration, lifecycle, monitoring, thumbnails.
package ingest

import (
	"context"
	"encoding/json"
	"errors"
	"iter"

	"github.com/davidteather/property-radar/internal/domain"
)

// SearchQuery is one crawl scope; adapters translate it into provider-native parameters.
type SearchQuery struct {
	ListingType domain.ListingType
	MaxPrice    domain.Money // 0 = no cap
	MinBeds     int          // 0 = no minimum
	Areas       []string     // provider-native area identifiers
	// Deep re-enriches every listing (detail page + full field refresh) instead
	// of only new/changed/unenriched ones. Never affects the search sweep, which
	// is always full so change and delist detection keep working.
	Deep bool
	// OneOff marks a one-time crawl: it observes listings but never takes
	// absence ownership away from the standing scope that tracks them.
	OneOff bool
}

// SourceProperty is one listing from a provider. Raw is opaque provenance:
// never parsed by application logic and never carrying auth material.
type SourceProperty struct {
	Provider     string
	ProviderID   string
	URL          string
	Raw          json.RawMessage
	SourceStatus string // provider-native status string, kept as provenance
	Property     domain.Property
	PhotoURLs    []string // full-size photo URLs in display order
	// Enriched reports that the detail page was merged in, so the store can
	// stamp when the listing was last fully fetched.
	Enriched bool
}

// ErrInterrupted wraps a crawl cut short by its own context (shutdown, drain
// timeout): not an outcome, so the target is retried rather than failed.
var ErrInterrupted = errors.New("crawl interrupted")

// ErrNotStarted wraps a crawl that never opened its run row (the store refused
// StartRun): nothing was observed, so the target is retried, not failed.
var ErrNotStarted = errors.New("crawl not started")

// Retryable reports whether a crawl error is a non-outcome the queue should retry.
func Retryable(err error) bool {
	return errors.Is(err, ErrInterrupted) || errors.Is(err, ErrNotStarted)
}

// ErrUnusableListing marks an item-scoped error for a listing the provider
// showed but the adapter could not decode: it counts as seen (never aged
// toward delisting) and is not applied.
var ErrUnusableListing = errors.New("listing present but undecodable")

// ErrSearchAborted marks a provider-level failure (page fetch, page cap): the
// sweep is incomplete and the crawl must not age anything toward delisting.
var ErrSearchAborted = errors.New("search aborted")

// Source yields listings for a scope. An error wrapping ErrSearchAborted ends
// the stream; any other error is item-scoped, with a zero SourceProperty when
// the listing could not even be identified. EnrichDetail adds one listing's detail.
type Source interface {
	Name() string
	Search(ctx context.Context, q SearchQuery) iter.Seq2[SourceProperty, error]
	EnrichDetail(ctx context.Context, sp SourceProperty) (SourceProperty, error)
}
