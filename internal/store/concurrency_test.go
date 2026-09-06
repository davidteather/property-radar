package store_test

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

// holdOpen runs fn inside a transaction that stays open until release is
// closed, so a second caller can be observed racing it.
func holdOpen(t *testing.T, s *store.Store, fn func(tx *store.Store) error) (release func(), done <-chan error) {
	t.Helper()
	gate := make(chan struct{})
	errc := make(chan error, 1)
	entered := make(chan struct{})
	go func() {
		errc <- s.InTx(context.Background(), func(tx *store.Store) error {
			if err := fn(tx); err != nil {
				return err
			}
			close(entered)
			<-gate
			return nil
		})
	}()
	<-entered
	return func() { close(gate) }, errc
}

func TestConcurrentFirstApplyCreatesOneListing(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)

	release, done := holdOpen(t, s, func(tx *store.Store) error {
		_, err := tx.ApplySourceProperty(ctx, runID, newSource("1001"), baseTime)
		return err
	})
	second := make(chan ingest.ApplyResult, 1)
	go func() {
		res, err := s.ApplySourceProperty(ctx, runID, newSource("1001"), baseTime.Add(time.Minute))
		if err != nil {
			t.Errorf("second apply: %v", err)
		}
		second <- res
	}()
	// The second apply must wait for the first, not insert a twin listing.
	select {
	case <-second:
		t.Fatal("second apply did not block behind the first")
	case <-time.After(300 * time.Millisecond):
	}
	release()
	if err := <-done; err != nil {
		t.Fatalf("first apply: %v", err)
	}
	res := <-second
	if res.Created {
		t.Error("second apply created a listing instead of updating the first one")
	}
	n, err := s.CountProperties(ctx, store.SearchFilter{})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("listings = %d, want 1 (orphan created by the race)", n)
	}
}

func TestMarkTargetStartedClaimsOnce(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	target := createTarget(t, s, onceTarget("319"))

	if err := s.MarkTargetStarted(ctx, target.ID); err != nil {
		t.Fatalf("first claim: %v", err)
	}
	if err := s.MarkTargetStarted(ctx, target.ID); !errors.Is(err, ingest.ErrTargetClaimed) {
		t.Fatalf("second claim err = %v, want ErrTargetClaimed", err)
	}
	// A standing scope is claimed the same way: two drains (a watch worker and
	// a cron run) must not crawl it at once.
	standing := createTarget(t, s, standingTarget(true, "305"))
	if err := s.MarkTargetStarted(ctx, standing.ID); err != nil {
		t.Fatalf("standing claim: %v", err)
	}
	if err := s.MarkTargetStarted(ctx, standing.ID); !errors.Is(err, ingest.ErrTargetClaimed) {
		t.Fatalf("second standing claim err = %v, want ErrTargetClaimed", err)
	}
	if due, _ := s.DueCrawlTargets(ctx, 0); len(due) != 0 {
		t.Fatalf("running standing target listed as due: %v", targetIDs(due))
	}
	if err := s.MarkTargetRun(ctx, standing.ID, 0, false, nil); err != nil {
		t.Fatalf("finish standing: %v", err)
	}
	if err := s.MarkTargetStarted(ctx, standing.ID); err != nil {
		t.Fatalf("standing claim after a pass: %v", err)
	}
}

func TestRecoverStaleTargetsFailsAbandonedRuns(t *testing.T) {
	pool := pgtest.Pool(t)
	pgtest.TruncateAll(t, pool)
	s := store.New(pool)
	ctx := context.Background()
	stuck := createTarget(t, s, onceTarget("319"))
	fresh := createTarget(t, s, onceTarget("305"))
	standing := createTarget(t, s, standingTarget(true, "326"))
	for _, id := range []domain.CrawlTargetID{stuck.ID, fresh.ID, standing.ID} {
		if err := s.MarkTargetStarted(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	// Only the runs that have exceeded the deadline are treated as abandoned.
	if _, err := pool.Exec(ctx, `UPDATE crawl_targets SET started_at = now() - interval '3 hours' WHERE id = ANY($1)`, []int64{int64(stuck.ID), int64(standing.ID)}); err != nil {
		t.Fatal(err)
	}
	n, err := s.RecoverStaleTargets(ctx, 2*time.Hour)
	if err != nil || n != 2 {
		t.Fatalf("recovered = %d, %v; want 2", n, err)
	}
	// A standing scope is not a job: it goes back to pending and is due again.
	if got, _ := s.GetCrawlTarget(ctx, standing.ID); got.Status != domain.TargetPending || got.LastRunAt != nil {
		t.Errorf("stale standing target = %+v, want pending with no run stamp", got)
	}
	if due, _ := s.DueCrawlTargets(ctx, time.Hour); !slices.Contains(targetIDs(due), int64(standing.ID)) {
		t.Errorf("recovered standing target not due: %v", targetIDs(due))
	}
	got, err := s.GetCrawlTarget(ctx, stuck.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != domain.TargetFailed || got.LastError == "" {
		t.Errorf("stuck target = %+v, want failed with a reason", got)
	}
	if again, _ := s.RecoverStaleTargets(ctx, 2*time.Hour); again != 0 {
		t.Errorf("second sweep recovered %d", again)
	}
	// A standing row left running by a pre-started_at schema (never completed a
	// pass, so last_run_at is NULL too) ages from created_at rather than never.
	if _, err := pool.Exec(ctx, `UPDATE crawl_targets SET status = 'running', started_at = NULL, last_run_at = NULL, created_at = now() - interval '3 hours' WHERE id = $1`, int64(standing.ID)); err != nil {
		t.Fatal(err)
	}
	if n, _ := s.RecoverStaleTargets(ctx, 2*time.Hour); n != 1 {
		t.Errorf("legacy running row recovered = %d, want 1", n)
	}
	if got, _ := s.GetCrawlTarget(ctx, fresh.ID); got.Status != domain.TargetRunning {
		t.Errorf("fresh target = %q, want running", got.Status)
	}
}

// Two identical creates in flight at once: the loser must report the winner as
// a duplicate. A pending one-off has a unique index behind it; a standing scope
// has only the advisory lock, so both shapes are raced.
func TestCreateCrawlTargetRacedDuplicateIsDuplicate(t *testing.T) {
	for _, tc := range []struct {
		name   string
		target domain.CrawlTarget
	}{
		{"one_off", onceTarget("319")},
		{"standing", standingTarget(true, "319")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := newStore(t)
			ctx := context.Background()

			var first domain.CrawlTarget
			release, done := holdOpen(t, s, func(tx *store.Store) error {
				var err error
				first, err = tx.CreateCrawlTarget(ctx, tc.target)
				return err
			})
			type outcome struct {
				target domain.CrawlTarget
				err    error
			}
			second := make(chan outcome, 1)
			go func() {
				target, err := s.CreateCrawlTarget(ctx, tc.target)
				second <- outcome{target, err}
			}()
			select {
			case got := <-second:
				t.Fatalf("second create finished while the first was still open: %+v %v", got.target, got.err)
			case <-time.After(200 * time.Millisecond):
			}
			release()
			if err := <-done; err != nil {
				t.Fatal(err)
			}
			got := <-second
			if !errors.Is(got.err, store.ErrDuplicateCrawlTarget) || got.target.ID != first.ID {
				t.Fatalf("raced create = (%+v, %v), want first target + ErrDuplicateCrawlTarget", got.target, got.err)
			}
			all, _ := s.ListCrawlTargets(ctx)
			if len(all) != 1 {
				t.Fatalf("targets after the race = %d, want 1", len(all))
			}
		})
	}
}

func TestAppendRubricRejectsVanishedVerdict(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	v1, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictLove, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.ResetTasteState(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictDislike, ""); err != nil {
		t.Fatal(err)
	}
	// v1 is below the latest id but no longer exists: a client error, not an FK leak.
	_, err = s.AppendRubric(ctx, "old memory", v1.ID)
	if !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("append rubric err = %v, want ErrInvalidInput", err)
	}
}

func TestSetPhotoCacheIgnoresAMovedSlot(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	// A re-apply replaced position 0 while its old URL was still downloading.
	changed := newSource("1001")
	changed.PhotoURLs = []string{"https://photos.example/1001/new.jpg"}
	apply(t, s, runID, changed, baseTime.Add(time.Hour))

	err := s.SetPhotoCache(ctx, created.PropertyID, 0, "https://photos.example/1001/1.jpg", "thumbs/old.jpg", "image/jpeg", 800, 600)
	if err == nil {
		t.Fatal("stale thumbnail was attached to the replaced slot")
	}
	_, _, photos, _ := mustGet(t, s, created.PropertyID)
	if photos[0].CachedPath != "" {
		t.Errorf("slot 0 = %+v, want no cache", photos[0])
	}
}

// A search-only sighting that carries a subset of the kept gallery indexes it
// from zero; the cache row is matched by URL, so the thumb lands on its photo.
func TestSetPhotoCacheMatchesByURLAcrossAKeptGallery(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	full := newSource("1001")
	created := apply(t, s, runID, full, baseTime)
	last := full.PhotoURLs[len(full.PhotoURLs)-1]

	subset := newSource("1001")
	subset.Enriched = false
	subset.PhotoURLs = []string{last}
	apply(t, s, runID, subset, baseTime.Add(time.Hour))

	if err := s.SetPhotoCache(ctx, created.PropertyID, 0, last, "thumbs/last.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache by search position: %v", err)
	}
	_, _, photos, _ := mustGet(t, s, created.PropertyID)
	if len(photos) != len(full.PhotoURLs) || photos[len(photos)-1].CachedPath != "thumbs/last.jpg" {
		t.Errorf("photos = %+v, want the kept gallery with its last photo cached", photos)
	}
}

// numeric(3,1) rounds 99.96 up to 100.0, which the column cannot hold.
func TestApplyDropsBathsThatRoundPastTheColumn(t *testing.T) {
	s := newStore(t)
	runID := startRun(t, s)
	sp := newSource("1001")
	sp.Property.Bathrooms = ptr.To(99.96)
	created := apply(t, s, runID, sp, baseTime)
	p, _, _, _ := mustGet(t, s, created.PropertyID)
	if p.Bathrooms != nil {
		t.Errorf("baths = %v, want dropped", *p.Bathrooms)
	}
}
