package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/store"
)

// expectWake fails unless the listener is woken by fn; wantWake=false asserts
// the opposite (fn must NOT notify).
func expectWake(t *testing.T, s *store.Store, wantWake bool, fn func()) {
	t.Helper()
	ctx := context.Background()
	listener, err := s.ListenCrawlDue(ctx)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer listener.Close()

	// Wait returns nil on both a notification and the poll fallback, so a
	// wake is told apart from a timeout by how long it took.
	const maxWait = 1500 * time.Millisecond
	done := make(chan error, 1)
	start := time.Now()
	go func() { done <- listener.Wait(ctx, maxWait) }()
	time.Sleep(100 * time.Millisecond)
	fn()

	if err := <-done; err != nil {
		t.Fatalf("wait: %v", err)
	}
	woken := time.Since(start) < maxWait-200*time.Millisecond
	if woken != wantWake {
		t.Fatalf("woken = %v, want %v", woken, wantWake)
	}
}

func TestResumeAndPromoteWakeTheDrainWorker(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	standing := createTarget(t, s, standingTarget(false, "305"))
	once := createTarget(t, s, onceTarget("319"))

	expectWake(t, s, true, func() {
		if err := s.SetCrawlTargetEnabled(ctx, standing.ID, true); err != nil {
			t.Errorf("resume: %v", err)
		}
	})
	expectWake(t, s, false, func() {
		if err := s.SetCrawlTargetEnabled(ctx, standing.ID, false); err != nil {
			t.Errorf("pause: %v", err)
		}
	})
	expectWake(t, s, true, func() {
		if err := s.PromoteCrawlTargetToStanding(ctx, once.ID); err != nil {
			t.Errorf("promote: %v", err)
		}
	})
	got, err := s.GetCrawlTarget(ctx, once.ID)
	if err != nil || got.Kind != domain.TargetStanding || !got.Enabled {
		t.Fatalf("promoted target = %+v, %v", got, err)
	}
}
