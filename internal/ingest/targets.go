package ingest

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

// DeepInterval is how stale a standing target's last deep (full re-enrich)
// crawl may get before the next drain runs it deep again: ~daily, with slack
// so a 6h cadence lands one deep run per day rather than racing the clock.
const DeepInterval = 20 * time.Hour

// DefaultStandingInterval spaces incremental runs of a standing scope: a
// watch worker drains every poll, and a scope should not be re-crawled every minute.
const DefaultStandingInterval = time.Hour

// ErrTargetClaimed reports a once target another drain already started; the
// caller skips it rather than crawling the scope twice.
var ErrTargetClaimed = errors.New("crawl target already claimed")

// ErrTargetGone reports a target deleted (reset_state, manage_crawl_target)
// while its crawl ran; the listings landed, only the bookkeeping has no row.
var ErrTargetGone = errors.New("crawl target no longer exists")

// TargetStore is the queue side of persistence, kept apart from Store because
// only the drain workflow consumes it.
type TargetStore interface {
	// DueCrawlTargets lists pending one-offs plus standing scopes not run
	// within standingEvery (<= 0: every drain).
	DueCrawlTargets(ctx context.Context, standingEvery time.Duration) ([]domain.CrawlTarget, error)
	// MarkTargetStarted atomically claims a target; ErrTargetClaimed when a
	// once target is no longer pending.
	MarkTargetStarted(ctx context.Context, id domain.CrawlTargetID) error
	MarkTargetRun(ctx context.Context, id domain.CrawlTargetID, runID domain.IngestRunID, deep bool, runErr error) error
}

type TargetOutcome struct {
	Target domain.CrawlTarget
	Result Result
	// Err is nil only for a complete, non-suspect crawl.
	Err error
}

type TargetsResult struct {
	Outcomes []TargetOutcome
}

func (r TargetsResult) Failed() int {
	failed := 0
	for _, o := range r.Outcomes {
		if o.Err != nil {
			failed++
		}
	}
	return failed
}

// RunTargets crawls every due target in queue order. One target's failure is
// recorded against that target and never stops the rest.
func (s *Service) RunTargets(ctx context.Context, targets TargetStore) (TargetsResult, error) {
	due, err := targets.DueCrawlTargets(ctx, s.standingEvery)
	if err != nil {
		return TargetsResult{}, fmt.Errorf("load due crawl targets: %w", err)
	}

	var out TargetsResult
	for _, t := range due {
		if ctx.Err() != nil {
			return out, fmt.Errorf("crawl targets interrupted: %w", ctx.Err())
		}
		deep := targetDeep(t, time.Now())
		s.log.Info("crawl target starting", "target_id", int64(t.ID), "kind", string(t.Kind),
			"listing_type", string(t.ListingType), "areas", t.Areas, "deep", deep)
		if err := targets.MarkTargetStarted(ctx, t.ID); err != nil {
			if errors.Is(err, ErrTargetClaimed) {
				s.log.Info("crawl target claimed by another drain; skipping", "target_id", int64(t.ID))
				continue
			}
			// Unclaimed means unowned: leave it pending for the next drain.
			s.log.Warn("mark crawl target started; leaving it for the next drain", "target_id", int64(t.ID), "err", err)
			continue
		}

		q := targetQuery(t)
		q.Deep = deep
		q.OneOff = t.Kind == domain.TargetOnce
		res, runErr := s.Run(ctx, q)
		outcome := TargetOutcome{Target: t, Result: res, Err: RunOutcome(res, runErr)}
		// Anything that failed while the drain itself was dying is a retry, not a verdict on the target.
		if outcome.Err != nil && ctx.Err() != nil && !Retryable(outcome.Err) {
			outcome.Err = fmt.Errorf("%w: %w", ErrInterrupted, outcome.Err)
		}
		// Bookkeeping must land even when ctx died mid-run, or a once target
		// stays "running" forever and a standing one never records its deep pass.
		fctx, cancel := finishContext(ctx)
		err := targets.MarkTargetRun(fctx, t.ID, res.Run.ID, deep, outcome.Err)
		cancel()
		if errors.Is(err, ErrTargetGone) {
			s.log.Warn("crawl target deleted mid-run; outcome not recorded", "target", t.ID, "err", err)
			err = nil
		}
		if err != nil {
			outcome.Err = errors.Join(outcome.Err, err)
		}
		out.Outcomes = append(out.Outcomes, outcome)
	}
	return out, nil
}

// minEnrichForOutage is how many detail fetches must all fail before a run is
// called blocked rather than unlucky.
const minEnrichForOutage = 5

// RunOutcome classifies one crawl: incomplete, suspect, and detail-blocked runs
// are failures even though the provider returned search data.
func RunOutcome(res Result, runErr error) error {
	switch {
	case runErr != nil:
		return runErr
	case !res.Run.Complete:
		return errors.New("ingest run incomplete")
	case res.Stats.EnrichAttempts >= minEnrichForOutage && res.Stats.EnrichFailures == res.Stats.EnrichAttempts:
		return fmt.Errorf("ingest run blocked: all %d detail-page fetches failed", res.Stats.EnrichAttempts)
	case res.Run.Suspect:
		return fmt.Errorf("ingest run suspect: %d listings against a baseline of %.0f",
			res.Run.ListingsSeen, res.Baseline.Volume)
	default:
		return nil
	}
}

// targetDeep decides whether this drain runs the target deep (full re-enrich) or
// incremental. A once target is always deep; a standing target goes deep when it
// has never run deep or the last deep run is at least DeepInterval old.
func targetDeep(t domain.CrawlTarget, now time.Time) bool {
	if t.Kind == domain.TargetOnce {
		return true
	}
	return t.LastDeepAt == nil || now.Sub(*t.LastDeepAt) >= DeepInterval
}

func targetQuery(t domain.CrawlTarget) SearchQuery {
	q := SearchQuery{ListingType: t.ListingType, Areas: t.Areas}
	if q.ListingType == "" {
		q.ListingType = domain.ListingSale
	}
	if t.MaxPrice != nil {
		q.MaxPrice = *t.MaxPrice
	}
	if t.MinBeds != nil {
		q.MinBeds = *t.MinBeds
	}
	return q
}
