package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/ingest/mocks"
	"github.com/davidteather/property-radar/internal/shared/ptr"
)

type targetHarness struct {
	source   *mocks.MockSource
	store    *mocks.MockStore
	targets  *mocks.MockTargetStore
	svc      *ingest.Service
	queries  []ingest.SearchQuery
	stats    map[domain.IngestRunID]ingest.RunStats
	baseline ingest.Baseline
}

// Each target crawl gets its own run id, and the source yields whatever the
// test registered for that target's first area.
func newTargetHarness(t *testing.T, byArea map[string][]yielded, opts ...ingest.Options) *targetHarness {
	t.Helper()
	h := &targetHarness{
		source:  mocks.NewMockSource(t),
		store:   mocks.NewMockStore(t),
		targets: mocks.NewMockTargetStore(t),
		stats:   map[domain.IngestRunID]ingest.RunStats{},
	}

	nextRun := domain.IngestRunID(0)
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.source.EXPECT().Search(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, q ingest.SearchQuery) iter.Seq2[ingest.SourceProperty, error] {
			h.queries = append(h.queries, q)
			return sourceSeq(byArea[q.Areas[0]]...)
		}).Maybe()
	h.store.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).
		RunAndReturn(func(context.Context, string, ingest.SearchQuery) (domain.IngestRun, error) {
			nextRun++
			return domain.IngestRun{ID: nextRun, Provider: "streeteasy"}, nil
		}).Maybe()
	h.store.EXPECT().EnrichmentState(mock.Anything, mock.Anything, mock.Anything).
		Return(ingest.EnrichState{}, nil).Maybe()
	h.source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Maybe()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Maybe()
	h.store.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, string, string) (ingest.Baseline, error) { return h.baseline, nil }).Maybe()
	h.store.EXPECT().FinishRun(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id domain.IngestRunID, stats ingest.RunStats) error {
			h.stats[id] = stats
			return nil
		}).Maybe()
	h.store.EXPECT().MarkMissingSources(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(ingest.MissingResult{}, nil).Maybe()

	opt := ingest.Options{}
	if len(opts) > 0 {
		opt = opts[0]
	}
	opt.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	h.svc = ingest.NewService(h.source, h.store, nil, opt)
	return h
}

func standingTarget(id int64, area string) domain.CrawlTarget {
	return domain.CrawlTarget{
		ID: domain.CrawlTargetID(id), Kind: domain.TargetStanding,
		Areas: []string{area}, ListingType: domain.ListingSale, Enabled: true,
	}
}

func TestRunTargetsCrawlsEveryDueTargetAndRecordsOutcomes(t *testing.T) {
	providerErr := fmt.Errorf("streeteasy search page 2: %w: 403", ingest.ErrSearchAborted)
	h := newTargetHarness(t, map[string][]yielded{
		"305": {{sp: listing("1")}},
		"319": {{err: providerErr}},
		"326": {{sp: listing("2")}},
	})

	once := domain.CrawlTarget{
		ID: 7, Kind: domain.TargetOnce, Areas: []string{"326"},
		ListingType: domain.ListingRent, MaxPrice: ptr.To(domain.Money(4500)), MinBeds: ptr.To(1),
		Status: domain.TargetPending,
	}
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305"), standingTarget(2, "319"), once}, nil).Once()

	h.targets.EXPECT().MarkTargetStarted(mock.Anything, mock.Anything).Return(nil).Times(3)
	marked := map[domain.CrawlTargetID]error{}
	runIDs := map[domain.CrawlTargetID]domain.IngestRunID{}
	deeps := map[domain.CrawlTargetID]bool{}
	h.targets.EXPECT().MarkTargetRun(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, id domain.CrawlTargetID, runID domain.IngestRunID, deep bool, runErr error) error {
			marked[id], runIDs[id], deeps[id] = runErr, runID, deep
			return nil
		}).Times(3)

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	require.Len(t, res.Outcomes, 3)

	assert.Equal(t, 1, res.Failed())
	assert.NoError(t, marked[1])
	assert.ErrorIs(t, marked[2], providerErr, "a provider failure is recorded against its own target")
	assert.NoError(t, marked[7], "the failure of an earlier target does not stop the queue")
	assert.NotZero(t, runIDs[7])

	// Never-deep-crawled targets (nil LastDeepAt) and once targets all run deep.
	assert.Equal(t, map[domain.CrawlTargetID]bool{1: true, 2: true, 7: true}, deeps)
	assert.Equal(t, []ingest.SearchQuery{
		{ListingType: domain.ListingSale, Areas: []string{"305"}, Deep: true},
		{ListingType: domain.ListingSale, Areas: []string{"319"}, Deep: true},
		{ListingType: domain.ListingRent, Areas: []string{"326"}, MaxPrice: 4500, MinBeds: 1, Deep: true, OneOff: true},
	}, h.queries, "only the once target crawls as a one-off (no absence ownership)")
	assert.ErrorIs(t, res.Outcomes[1].Err, providerErr)
	assert.False(t, res.Outcomes[1].Result.Run.Complete)
}

func TestRunTargetsTreatsSuspectRunsAsFailures(t *testing.T) {
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
	h.baseline = ingest.Baseline{Volume: 400, Runs: 5}
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305")}, nil).Once()

	h.targets.EXPECT().MarkTargetStarted(mock.Anything, mock.Anything).Return(nil).Once()
	var marked error
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.CrawlTargetID, _ domain.IngestRunID, _ bool, runErr error) error {
			marked = runErr
			return nil
		}).Once()

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	require.ErrorContains(t, marked, "suspect")
	assert.Equal(t, 1, res.Failed())
}

func TestRunTargetsDepthCadence(t *testing.T) {
	run := func(t *testing.T, lastDeep time.Time) (deep bool, q ingest.SearchQuery) {
		t.Helper()
		h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
		target := standingTarget(1, "305")
		target.LastDeepAt = &lastDeep
		h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).Return([]domain.CrawlTarget{target}, nil).Once()
		h.targets.EXPECT().MarkTargetStarted(mock.Anything, mock.Anything).Return(nil).Once()
		h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), mock.Anything, mock.Anything, mock.Anything).
			RunAndReturn(func(_ context.Context, _ domain.CrawlTargetID, _ domain.IngestRunID, d bool, _ error) error {
				deep = d
				return nil
			}).Once()
		_, err := h.svc.RunTargets(context.Background(), h.targets)
		require.NoError(t, err)
		require.Len(t, h.queries, 1)
		return deep, h.queries[0]
	}

	deep, q := run(t, time.Now().Add(-2*time.Hour))
	assert.False(t, deep, "a standing target crawled deep 2h ago runs incremental")
	assert.False(t, q.Deep)

	deep, q = run(t, time.Now().Add(-ingest.DeepInterval-time.Hour))
	assert.True(t, deep, "past DeepInterval the standing target goes deep again")
	assert.True(t, q.Deep)
}

func TestRunTargetsMarkFailureSurfacesInTheOutcome(t *testing.T) {
	markErr := errors.New("mark crawl target 1: connection reset")
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305")}, nil).Once()
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, mock.Anything).Return(nil).Once()
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), mock.Anything, mock.Anything, mock.Anything).
		Return(markErr).Once()

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	require.Len(t, res.Outcomes, 1)
	assert.ErrorIs(t, res.Outcomes[0].Err, markErr)
	assert.Equal(t, 1, res.Failed())
}

func TestRunTargetsRecordsTheRunAfterCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305")}, nil).Once()
	// Shutdown arrives while the target is mid-crawl.
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, domain.CrawlTargetID) error { cancel(); return nil }).Once()
	var markCtxErr error
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ domain.CrawlTargetID, _ domain.IngestRunID, _ bool, _ error) error {
			markCtxErr = ctx.Err()
			return nil
		}).Once()

	res, err := h.svc.RunTargets(ctx, h.targets)
	require.NoError(t, err)
	require.Len(t, res.Outcomes, 1)
	assert.ErrorIs(t, res.Outcomes[0].Err, context.Canceled)
	// Otherwise the target stays "running" forever after a shutdown mid-crawl.
	assert.NoError(t, markCtxErr, "bookkeeping runs on a live context after cancellation")
}

// A drain that cannot claim a target (transient DB error, schema still
// migrating) leaves it pending for the next drain instead of crawling unowned.
func TestRunTargetsSkipsATargetItCannotClaim(t *testing.T) {
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}, "319": {{sp: listing("2")}}})
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305"), standingTarget(2, "319")}, nil).Once()
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, domain.CrawlTargetID(1)).
		Return(errors.New(`column "started_at" does not exist`)).Once()
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, domain.CrawlTargetID(2)).Return(nil).Once()
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(2), mock.Anything, mock.Anything, nil).Return(nil).Once()

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	require.Len(t, res.Outcomes, 1)
	assert.Equal(t, domain.CrawlTargetID(2), res.Outcomes[0].Target.ID)
	require.Len(t, h.queries, 1, "the unclaimed target must not be crawled")
}

// A run row the store refused (e.g. the crawler drained before mcpd migrated
// ingest_runs) observed nothing: a retry, never a failed target.
func TestRunTargetsTreatsStartRunFailureAsRetry(t *testing.T) {
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
	h.store.ExpectedCalls = nil
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.store.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).
		Return(domain.IngestRun{}, errors.New(`column "areas" does not exist`)).Once()
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305")}, nil).Once()
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, domain.CrawlTargetID(1)).Return(nil).Once()
	var recorded error
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), domain.IngestRunID(0), mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.CrawlTargetID, _ domain.IngestRunID, _ bool, runErr error) error {
			recorded = runErr
			return nil
		}).Once()

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	require.Len(t, res.Outcomes, 1)
	assert.ErrorIs(t, recorded, ingest.ErrNotStarted)
	assert.True(t, ingest.Retryable(recorded))
	assert.False(t, ingest.Retryable(errors.New("provider blocked")))
}

// A failure that lands while the drain context is already dead is an
// interruption, whatever the underlying error says.
func TestRunTargetsWrapsFailuresDuringShutdownAsInterrupted(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newTargetHarness(t, map[string][]yielded{"305": {{sp: listing("1")}}})
	// The crawl itself completes; the DB drops while the run row is being closed, just as shutdown lands.
	h.store.ExpectedCalls = h.store.ExpectedCalls[:0]
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.store.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: 1}, nil).Once()
	h.store.EXPECT().EnrichmentState(mock.Anything, mock.Anything, mock.Anything).Return(ingest.EnrichState{}, nil).Maybe()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ingest.ApplyResult{PropertyID: 11}, nil).Maybe()
	h.store.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	h.store.EXPECT().FinishRun(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, domain.IngestRunID, ingest.RunStats) error {
			cancel()
			return errors.New("connection reset")
		}).Times(ingest.FinishAttempts)
	noRetryWait(t)
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).
		Return([]domain.CrawlTarget{standingTarget(1, "305")}, nil).Once()
	h.targets.EXPECT().MarkTargetStarted(mock.Anything, domain.CrawlTargetID(1)).Return(nil).Once()
	var recorded error
	h.targets.EXPECT().MarkTargetRun(mock.Anything, domain.CrawlTargetID(1), mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.CrawlTargetID, _ domain.IngestRunID, _ bool, runErr error) error {
			recorded = runErr
			return nil
		}).Once()

	_, err := h.svc.RunTargets(ctx, h.targets)
	require.NoError(t, err)
	assert.ErrorIs(t, recorded, ingest.ErrInterrupted)
	assert.ErrorContains(t, recorded, "connection reset")
}

// Pins the cadence plumbing: the configured spacing reaches the store
// unchanged, and the "every drain" sentinel is not swallowed by the default.
func TestRunTargetsPassesStandingInterval(t *testing.T) {
	for _, tc := range []struct {
		opt  time.Duration
		want time.Duration
	}{{0, ingest.DefaultStandingInterval}, {-1, -1}, {6 * time.Hour, 6 * time.Hour}} {
		h := newTargetHarness(t, nil, ingest.Options{StandingEvery: tc.opt})
		h.targets.EXPECT().DueCrawlTargets(mock.Anything, tc.want).Return(nil, nil).Once()
		_, err := h.svc.RunTargets(context.Background(), h.targets)
		require.NoError(t, err)
	}
}

func TestRunTargetsWithNothingDue(t *testing.T) {
	h := newTargetHarness(t, nil)
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).Return(nil, nil).Once()

	res, err := h.svc.RunTargets(context.Background(), h.targets)
	require.NoError(t, err)
	assert.Empty(t, res.Outcomes)
	assert.Zero(t, res.Failed())
	h.source.AssertNotCalled(t, "Search", mock.Anything, mock.Anything)
}

func TestRunTargetsLoadFailure(t *testing.T) {
	h := newTargetHarness(t, nil)
	h.targets.EXPECT().DueCrawlTargets(mock.Anything, mock.Anything).Return(nil, errors.New("query due crawl targets: down")).Once()

	_, err := h.svc.RunTargets(context.Background(), h.targets)
	require.ErrorContains(t, err, "load due crawl targets")
}

func TestRunOutcomeClassification(t *testing.T) {
	runErr := errors.New("provider search: boom")
	complete := ingest.Result{Run: domain.IngestRun{Complete: true}}

	assert.ErrorIs(t, ingest.RunOutcome(complete, runErr), runErr)
	assert.NoError(t, ingest.RunOutcome(complete, nil))
	assert.ErrorContains(t, ingest.RunOutcome(ingest.Result{}, nil), "incomplete")
	assert.ErrorContains(t, ingest.RunOutcome(ingest.Result{
		Run:      domain.IngestRun{Complete: true, Suspect: true, ListingsSeen: 2},
		Baseline: ingest.Baseline{Volume: 400, Runs: 5},
	}, nil), "suspect")
	// Every detail fetch failing is the provider blocking us, and the target must say so.
	blocked := ingest.Result{Run: domain.IngestRun{Complete: true}, Stats: ingest.RunStats{EnrichAttempts: 5, EnrichFailures: 5}}
	assert.ErrorContains(t, ingest.RunOutcome(blocked, nil), "all 5 detail-page fetches failed")
	few := ingest.Result{Run: domain.IngestRun{Complete: true}, Stats: ingest.RunStats{EnrichAttempts: 4, EnrichFailures: 4}}
	assert.NoError(t, ingest.RunOutcome(few, nil))
	mostly := ingest.Result{Run: domain.IngestRun{Complete: true}, Stats: ingest.RunStats{EnrichAttempts: 20, EnrichFailures: 19}}
	assert.NoError(t, ingest.RunOutcome(mostly, nil))
}
