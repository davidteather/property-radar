package store_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/store"
)

func TestMarkTargetRunStampsScheduling(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	target := createTarget(t, s, standingTarget(true, "305"))
	runID := completeRun(t, s, 10)

	if err := s.MarkTargetRun(ctx, target.ID, runID, false, nil); err != nil {
		t.Fatalf("mark incremental run: %v", err)
	}
	got, err := s.GetCrawlTarget(ctx, target.ID)
	if err != nil {
		t.Fatalf("get target: %v", err)
	}
	if got.LastRunAt == nil || got.LastDeepAt != nil {
		t.Fatalf("incremental run stamps = run %v deep %v, want run set and deep nil", got.LastRunAt, got.LastDeepAt)
	}
	if got.Status != domain.TargetDone {
		t.Fatalf("standing status after a pass = %s, want done", got.Status)
	}
	if err := s.MarkTargetRun(ctx, target.ID, runID, false, errors.New("provider search: boom")); err != nil {
		t.Fatalf("mark failed run: %v", err)
	}
	if got, _ = s.GetCrawlTarget(ctx, target.ID); got.Status != domain.TargetFailed || got.LastError == "" {
		t.Fatalf("standing status after a failed pass = %+v, want failed with the error", got)
	}

	if err := s.MarkTargetRun(ctx, target.ID, runID, true, nil); err != nil {
		t.Fatalf("mark deep run: %v", err)
	}
	got, err = s.GetCrawlTarget(ctx, target.ID)
	if err != nil {
		t.Fatalf("get target after deep: %v", err)
	}
	if got.LastDeepAt == nil {
		t.Fatal("deep run did not stamp last_deep_at")
	}
}

// An interrupted run (shutdown, drain timeout) is not an outcome: the once
// target returns to pending and a standing one keeps its stamps so it is due
// again next drain rather than after the full interval.
// The interrupted once-target's return to pending must not trip the pending
// unique index when an identical request was queued meanwhile (a stale-target
// sweep failed it, the agent re-asked): it is settled as superseded instead.
func TestMarkTargetRunInterruptedSupersededByNewerRequest(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	first := createTarget(t, s, onceTarget("305"))
	if err := s.MarkTargetStarted(ctx, first.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := pgtest.Pool(t).Exec(ctx, `UPDATE crawl_targets SET status = 'failed' WHERE id = $1`, int64(first.ID)); err != nil {
		t.Fatal(err)
	}
	second := createTarget(t, s, onceTarget("305"))
	runID := completeRun(t, s, 10)
	interrupted := fmt.Errorf("%w: %w", ingest.ErrInterrupted, context.Canceled)
	if err := s.MarkTargetRun(ctx, first.ID, runID, false, interrupted); err != nil {
		t.Fatalf("mark superseded run: %v", err)
	}
	got, _ := s.GetCrawlTarget(ctx, first.ID)
	if got.Status != domain.TargetFailed || !strings.Contains(got.LastError, "newer request") {
		t.Fatalf("superseded target = %+v, want failed as superseded", got)
	}
	if got, _ := s.GetCrawlTarget(ctx, second.ID); got.Status != domain.TargetPending {
		t.Fatalf("newer target = %q, want still pending", got.Status)
	}
	// A target deleted mid-run reports ErrTargetGone, which the drain tolerates.
	if _, err := pgtest.Pool(t).Exec(ctx, `DELETE FROM crawl_targets WHERE id = $1`, int64(second.ID)); err != nil {
		t.Fatal(err)
	}
	if err := s.MarkTargetRun(ctx, second.ID, runID, false, nil); !errors.Is(err, ingest.ErrTargetGone) {
		t.Fatalf("mark deleted target = %v, want ErrTargetGone", err)
	}
}

func TestMarkTargetRunInterruptedRetries(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	once := createTarget(t, s, onceTarget("305"))
	standing := createTarget(t, s, standingTarget(true, "306"))
	runID := completeRun(t, s, 10)
	interrupted := fmt.Errorf("%w: %w", ingest.ErrInterrupted, context.Canceled)

	if err := s.MarkTargetStarted(ctx, once.ID); err != nil {
		t.Fatalf("start once: %v", err)
	}
	if err := s.MarkTargetRun(ctx, once.ID, runID, true, interrupted); err != nil {
		t.Fatalf("mark once interrupted: %v", err)
	}
	if err := s.MarkTargetStarted(ctx, standing.ID); err != nil {
		t.Fatalf("start standing: %v", err)
	}
	// A run the store refused to open (no run id) is the same non-outcome.
	if err := s.MarkTargetRun(ctx, standing.ID, 0, true, fmt.Errorf("%w: create ingest run: schema", ingest.ErrNotStarted)); err != nil {
		t.Fatalf("mark standing not started: %v", err)
	}

	got, err := s.GetCrawlTarget(ctx, once.ID)
	if err != nil {
		t.Fatalf("get once: %v", err)
	}
	if got.Status != domain.TargetPending || got.LastError == "" || got.LastDeepAt != nil {
		t.Fatalf("interrupted once target = %+v, want pending with the error and no deep stamp", got)
	}
	got, err = s.GetCrawlTarget(ctx, standing.ID)
	if err != nil {
		t.Fatalf("get standing: %v", err)
	}
	if got.LastRunAt != nil || got.LastDeepAt != nil || got.Status != domain.TargetPending {
		t.Fatalf("interrupted standing target = %+v, want no stamps and pending", got)
	}
	due, err := s.DueCrawlTargets(ctx, time.Hour)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if len(due) != 2 {
		t.Fatalf("due after interruption = %v, want both targets", due)
	}
}

// Re-requesting a scope whose one-off is mid-run returns that row; a second
// pending row would block the running one from returning to pending when
// interrupted (the pending-once unique index).
func TestRunningOnceTargetDedupesAndCanReturnToPending(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	first := createTarget(t, s, onceTarget("305"))
	if err := s.MarkTargetStarted(ctx, first.ID); err != nil {
		t.Fatalf("start: %v", err)
	}
	again, err := s.CreateCrawlTarget(ctx, onceTarget("305"))
	if !errors.Is(err, listings.ErrDuplicateCrawlTarget) || again.ID != first.ID {
		t.Fatalf("re-request while running = %+v, %v; want the running row as a duplicate", again, err)
	}
	interrupted := fmt.Errorf("%w: %w", ingest.ErrInterrupted, context.Canceled)
	if err := s.MarkTargetRun(ctx, first.ID, 0, false, interrupted); err != nil {
		t.Fatalf("mark interrupted: %v", err)
	}
	got, err := s.GetCrawlTarget(ctx, first.ID)
	if err != nil || got.Status != domain.TargetPending {
		t.Fatalf("after interruption = %+v, %v; want pending", got, err)
	}
}

// Creating a standing scope that already exists returns the existing row, so
// two overlapping recurring requests cannot double-crawl the scope.
func TestCreateStandingTargetDedupesScope(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	first := createTarget(t, s, standingTarget(true, "306"))
	again, err := s.CreateCrawlTarget(ctx, standingTarget(false, "306"))
	if !errors.Is(err, listings.ErrDuplicateCrawlTarget) || again.ID != first.ID {
		t.Fatalf("second standing create = %+v, %v; want the existing row as a duplicate", again, err)
	}
	if other, err := s.CreateCrawlTarget(ctx, standingTarget(true, "306", "307")); err != nil || other.ID == first.ID {
		t.Fatalf("different scope = %+v, %v; want a new row", other, err)
	}
}

// A provider timeout also reads as DeadlineExceeded, but it is a failure the
// target records, not a pause: the deep stamp is withheld so the next drain
// retries the full pass instead of waiting out DeepInterval.
func TestMarkTargetRunFailedDeepDoesNotStampDeep(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	standing := createTarget(t, s, standingTarget(true, "305"))
	runID := completeRun(t, s, 10)

	failed := fmt.Errorf("streeteasy search page 1: %w", context.DeadlineExceeded)
	if err := s.MarkTargetRun(ctx, standing.ID, runID, true, failed); err != nil {
		t.Fatalf("mark failed deep run: %v", err)
	}
	got, err := s.GetCrawlTarget(ctx, standing.ID)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if got.Status != domain.TargetFailed || got.LastRunAt == nil || got.LastDeepAt != nil {
		t.Fatalf("failed deep run = %+v, want failed, last_run_at set, last_deep_at nil", got)
	}
}

// A watch worker drains every poll; a standing scope must not be re-crawled
// each minute, only once its last run is older than the spacing.
func TestDueCrawlTargetsSpacesStandingRuns(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	fresh := createTarget(t, s, standingTarget(true, "305"))
	ran := createTarget(t, s, standingTarget(true, "306"))
	once := createTarget(t, s, onceTarget("307"))
	runID := completeRun(t, s, 10)
	if err := s.MarkTargetRun(ctx, ran.ID, runID, false, nil); err != nil {
		t.Fatalf("mark run: %v", err)
	}

	due, err := s.DueCrawlTargets(ctx, time.Hour)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	if got, want := targetIDs(due), []int64{int64(once.ID), int64(fresh.ID)}; !slices.Equal(got, want) {
		t.Fatalf("due with 1h spacing = %v, want %v (never-run standing + once, not the one just run)", got, want)
	}
	for _, every := range []time.Duration{0, -1} {
		due, err = s.DueCrawlTargets(ctx, every)
		if err != nil {
			t.Fatalf("due(%v): %v", every, err)
		}
		if got, want := targetIDs(due), []int64{int64(once.ID), int64(fresh.ID), int64(ran.ID)}; !slices.Equal(got, want) {
			t.Fatalf("due with spacing %v = %v, want %v", every, got, want)
		}
	}
}

func TestMarkTargetStartedMovesBothKindsToRunning(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	once := createTarget(t, s, onceTarget("305"))
	standing := createTarget(t, s, standingTarget(true, "319"))

	if err := s.MarkTargetStarted(ctx, once.ID); err != nil {
		t.Fatalf("mark once started: %v", err)
	}
	if err := s.MarkTargetStarted(ctx, standing.ID); err != nil {
		t.Fatalf("mark standing started: %v", err)
	}
	gotOnce, _ := s.GetCrawlTarget(ctx, once.ID)
	gotStanding, _ := s.GetCrawlTarget(ctx, standing.ID)
	if gotOnce.Status != domain.TargetRunning {
		t.Fatalf("once status = %s, want running", gotOnce.Status)
	}
	if gotStanding.Status != domain.TargetRunning {
		t.Fatalf("standing status = %s, want running", gotStanding.Status)
	}
	if gotStanding.LastRunAt != nil {
		t.Fatal("starting a standing target must not stamp last_run_at (that would defer it a full interval)")
	}
}

func TestPruneCrawlTargetsRespectsTTLAndState(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	settled := createTarget(t, s, onceTarget("305"))
	pending := createTarget(t, s, onceTarget("319"))
	standing := createTarget(t, s, standingTarget(true, "326"))
	runID := completeRun(t, s, 10)
	if err := s.MarkTargetRun(ctx, settled.ID, runID, true, nil); err != nil {
		t.Fatalf("settle once target: %v", err)
	}

	// Fresh rows survive any positive TTL.
	if n, err := s.PruneCrawlTargets(ctx, time.Hour); err != nil || n != 0 {
		t.Fatalf("prune fresh = %d, %v; want 0, nil", n, err)
	}
	// Zero TTL prunes the settled once row, never pending or standing rows.
	n, err := s.PruneCrawlTargets(ctx, 0)
	if err != nil || n != 1 {
		t.Fatalf("prune settled = %d, %v; want 1, nil", n, err)
	}
	if _, err := s.GetCrawlTarget(ctx, settled.ID); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("settled target lookup = %v, want ErrCrawlTargetNotFound", err)
	}
	if _, err := s.GetCrawlTarget(ctx, pending.ID); err != nil {
		t.Fatalf("pending target pruned: %v", err)
	}
	if _, err := s.GetCrawlTarget(ctx, standing.ID); err != nil {
		t.Fatalf("standing target pruned: %v", err)
	}
}

func TestRunByIDAndCorpusStats(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := completeRun(t, s, 12)

	run, err := s.RunByID(ctx, runID)
	if err != nil {
		t.Fatalf("run by id: %v", err)
	}
	if run.ID != runID || run.ListingsSeen != 12 || !run.Complete {
		t.Fatalf("run = %+v", run)
	}
	if _, err := s.RunByID(ctx, 999999); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("missing run = %v, want not-found sentinel", err)
	}

	createTarget(t, s, onceTarget("305"))
	createTarget(t, s, standingTarget(true, "319"))
	stats, err := s.CorpusStats(ctx)
	if err != nil {
		t.Fatalf("corpus stats: %v", err)
	}
	if stats.PendingCrawls != 1 || stats.StandingScopes != 1 {
		t.Fatalf("stats queue counts = %+v", stats)
	}
	if stats.LastCrawlAt == nil {
		t.Fatal("stats.LastCrawlAt = nil, want the completed run's finish time")
	}
}

func TestSetEnabledAndDeleteCrawlTarget(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	target := createTarget(t, s, standingTarget(true, "305"))

	if err := s.SetCrawlTargetEnabled(ctx, target.ID, false); err != nil {
		t.Fatalf("pause: %v", err)
	}
	got, _ := s.GetCrawlTarget(ctx, target.ID)
	if got.Enabled {
		t.Fatal("target still enabled after pause")
	}
	// A paused standing scope drops out of the due set.
	due, err := s.DueCrawlTargets(ctx, 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	for _, d := range due {
		if d.ID == target.ID {
			t.Fatal("paused standing scope is still due")
		}
	}

	if err := s.SetCrawlTargetEnabled(ctx, 999999, true); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("enable unknown = %v, want not-found", err)
	}

	if err := s.DeleteCrawlTarget(ctx, target.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := s.GetCrawlTarget(ctx, target.ID); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("deleted target lookup = %v, want not-found", err)
	}
	if err := s.DeleteCrawlTarget(ctx, target.ID); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("re-delete = %v, want not-found", err)
	}
}

func TestPromoteCrawlTargetToStanding(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	// A settled once target: not due (it already ran).
	once := createTarget(t, s, domain.CrawlTarget{
		Kind: domain.TargetOnce, Areas: []string{"305"},
		ListingType: domain.ListingSale, Status: domain.TargetDone,
	})
	if isDue(t, s, once.ID) {
		t.Fatal("a done once target should not be due")
	}

	if err := s.PromoteCrawlTargetToStanding(ctx, once.ID); err != nil {
		t.Fatalf("promote: %v", err)
	}
	got, _ := s.GetCrawlTarget(ctx, once.ID)
	if got.Kind != domain.TargetStanding || !got.Enabled {
		t.Fatalf("after promote = kind %s enabled %v, want standing+enabled", got.Kind, got.Enabled)
	}
	// Now recurring, so the worker will keep crawling it.
	if !isDue(t, s, once.ID) {
		t.Fatal("promoted scope is not due")
	}

	if err := s.PromoteCrawlTargetToStanding(ctx, 999999); !errors.Is(err, listings.ErrCrawlTargetNotFound) {
		t.Fatalf("promote unknown = %v, want not-found", err)
	}
}

func isDue(t *testing.T, s *store.Store, id domain.CrawlTargetID) bool {
	t.Helper()
	due, err := s.DueCrawlTargets(context.Background(), 0)
	if err != nil {
		t.Fatalf("due: %v", err)
	}
	for _, d := range due {
		if d.ID == id {
			return true
		}
	}
	return false
}

func TestCrawlDueNotifyWakesListener(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	listener, err := s.ListenCrawlDue(ctx)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	done := make(chan error, 1)
	go func() { done <- listener.Wait(ctx, 10*time.Second) }()
	// Give the waiter a beat to be blocked on the notification.
	time.Sleep(100 * time.Millisecond)
	createTarget(t, s, onceTarget("305"))

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("wait: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("listener was not woken by CreateCrawlTarget's notify")
	}

	// With nothing arriving, Wait returns cleanly at the poll fallback.
	if err := listener.Wait(ctx, 150*time.Millisecond); err != nil {
		t.Fatalf("poll-fallback wait: %v", err)
	}
}
