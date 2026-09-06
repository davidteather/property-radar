package domain

import "time"

type CrawlTargetID int64

// TargetKind distinguishes a standing target (re-crawled on a cadence while Enabled)
// from a once target (crawled exactly once, outcome then carried in Status).
type TargetKind string

const (
	TargetStanding TargetKind = "standing"
	TargetOnce     TargetKind = "once"
)

type TargetStatus string

const (
	TargetPending TargetStatus = "pending"
	TargetRunning TargetStatus = "running"
	TargetDone    TargetStatus = "done"
	TargetFailed  TargetStatus = "failed"
)

// A once target that was interrupted (shutdown, drain timeout) returns to
// TargetPending with LastError set, so the next drain retries it.

// CrawlTarget is one stored crawl scope. Nil numerics mean "unconstrained".
// Status is a once target's outcome; for a standing target it reports the
// latest pass (TargetPending until the first one).
type CrawlTarget struct {
	ID          CrawlTargetID
	Kind        TargetKind
	Areas       []string
	ListingType ListingType
	MaxPrice    *Money
	MinBeds     *int
	Enabled     bool
	Status      TargetStatus
	Note        string
	CreatedAt   time.Time
	LastRunID   *IngestRunID
	LastError   string
	LastRunAt   *time.Time
	LastDeepAt  *time.Time
}
