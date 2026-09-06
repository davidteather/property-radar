package ingest

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
)

func TestFinishContextSurvivesACancelMidBookkeeping(t *testing.T) {
	parent, cancel := context.WithCancel(context.Background())
	fin, done := finishContext(parent)
	defer done()
	cancel()
	if fin.Err() != nil {
		t.Fatal("bookkeeping ctx died with the run ctx; a signal during FinishRun would leave the run unrecorded")
	}
	if _, ok := fin.Deadline(); !ok {
		t.Fatal("detached bookkeeping ctx must still be bounded")
	}
}

// flakyFinish fails FinishRun the first `failures` times, then succeeds.
type flakyFinish struct {
	Store
	failures, calls int
}

func (f *flakyFinish) FinishRun(context.Context, domain.IngestRunID, RunStats) error {
	f.calls++
	if f.calls <= f.failures {
		return errors.New("connection reset")
	}
	return nil
}

// A DB blip while recording a finished run is retried, not reported as a
// failed crawl; a persistent outage still surfaces.
func TestFinishRunRetriesATransientFailure(t *testing.T) {
	finishRetryWait = 0
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	blip := &flakyFinish{failures: 1}
	svc := NewService(nil, blip, nil, Options{Logger: logger})
	if err := svc.finishRun(context.Background(), 7, RunStats{}); err != nil || blip.calls != 2 {
		t.Fatalf("one blip should be absorbed: err=%v calls=%d", err, blip.calls)
	}

	down := &flakyFinish{failures: finishAttempts + 1}
	svc = NewService(nil, down, nil, Options{Logger: logger})
	if err := svc.finishRun(context.Background(), 8, RunStats{}); err == nil || down.calls != finishAttempts {
		t.Fatalf("a persistent outage must fail after %d tries: err=%v calls=%d", finishAttempts, err, down.calls)
	}
}
