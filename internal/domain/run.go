package domain

import "time"

type IngestRunID int64

type RunError struct {
	ProviderID string
	Message    string
}

// IngestRun records one crawl. Lifecycle advancement may only consider runs that
// are Complete and not Suspect, for the same ScopeHash.
type IngestRun struct {
	ID            IngestRunID
	Provider      string
	ScopeHash     string
	StartedAt     time.Time
	FinishedAt    *time.Time
	Complete      bool
	ListingsSeen  int
	Created       int
	Updated       int
	PhotoFailures int
	ItemErrors    []RunError
	Suspect       bool
}
