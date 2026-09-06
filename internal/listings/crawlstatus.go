package listings

import (
	"context"
	"errors"
	"fmt"

	"github.com/davidteather/property-radar/internal/domain"
)

// CrawlStatus is the async view of one crawl request: the target row is the
// job, and Run carries the outcome metrics once a crawl has actually run.
type CrawlStatus struct {
	Target domain.CrawlTarget
	Run    *domain.IngestRun
}

// CrawlStatus loads one crawl request and, when it has run, its run metrics.
// A missing run row (pruned) degrades to target-only; an outage still fails.
func (s *Service) CrawlStatus(ctx context.Context, id domain.CrawlTargetID) (CrawlStatus, error) {
	target, err := s.store.GetCrawlTarget(ctx, id)
	if err != nil {
		return CrawlStatus{}, err
	}
	out := CrawlStatus{Target: target}
	if target.LastRunID != nil {
		run, err := s.store.RunByID(ctx, *target.LastRunID)
		switch {
		case err == nil:
			out.Run = &run
		case !errors.Is(err, ErrCrawlTargetNotFound):
			return CrawlStatus{}, err
		}
	}
	return out, nil
}

// SetCrawlTargetEnabled pauses or resumes a standing crawl scope. One-off
// requests ignore the flag (they run once while pending), so pausing one is
// rejected rather than reported as a success that changes nothing.
func (s *Service) SetCrawlTargetEnabled(ctx context.Context, id domain.CrawlTargetID, enabled bool) error {
	t, err := s.store.GetCrawlTarget(ctx, id)
	if err != nil {
		return err
	}
	if t.Kind == domain.TargetOnce {
		return Invalidf("crawl request %d is a one-off; delete it to cancel it, or make_recurring to turn it into a scope you can pause and resume", id)
	}
	return s.store.SetCrawlTargetEnabled(ctx, id, enabled)
}

// DeleteCrawlTarget removes a crawl scope.
func (s *Service) DeleteCrawlTarget(ctx context.Context, id domain.CrawlTargetID) error {
	return s.store.DeleteCrawlTarget(ctx, id)
}

// MakeCrawlTargetRecurring promotes a once target to a standing (recurring)
// scope so the worker keeps crawling it, instead of it running exactly once.
func (s *Service) MakeCrawlTargetRecurring(ctx context.Context, id domain.CrawlTargetID) error {
	t, err := s.store.GetCrawlTarget(ctx, id)
	if err != nil {
		return err
	}
	if t.Kind == domain.TargetStanding {
		return Invalidf("crawl scope %d is already standing; use resume if it is paused", id)
	}
	return s.store.PromoteCrawlTargetToStanding(ctx, id)
}

func (s *Service) CorpusStats(ctx context.Context) (domain.CorpusStats, error) {
	stats, err := s.store.CorpusStats(ctx)
	if err != nil {
		return domain.CorpusStats{}, fmt.Errorf("corpus stats: %w", err)
	}
	return stats, nil
}
