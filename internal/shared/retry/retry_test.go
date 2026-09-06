package retry

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"
	"time"
)

var quiet = slog.New(slog.NewTextHandler(io.Discard, nil))

func TestDoRetriesUntilSuccess(t *testing.T) {
	calls := 0
	err := do(context.Background(), quiet, "probe", 0, time.Millisecond, 4*time.Millisecond, func(context.Context) error {
		calls++
		if calls < 3 {
			return errors.New("not yet")
		}
		return nil
	})
	if err != nil || calls != 3 {
		t.Fatalf("err = %v calls = %d, want nil after 3", err, calls)
	}
}

func TestDoGivesUpAtBudgetWithTheLastError(t *testing.T) {
	calls := 0
	err := do(context.Background(), quiet, "probe", 10*time.Millisecond, 4*time.Millisecond, 4*time.Millisecond, func(context.Context) error {
		calls++
		return errors.New("down")
	})
	if err == nil || err.Error() != "down" || calls < 2 || calls > 4 {
		t.Fatalf("err = %v calls = %d, want the last error after a few tries", err, calls)
	}
}

func TestDoStopsWhenContextEnds(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	err := do(ctx, quiet, "probe", 0, time.Hour, time.Hour, func(context.Context) error {
		calls++
		cancel()
		return errors.New("down")
	})
	if err == nil || calls != 1 {
		t.Fatalf("err = %v calls = %d, want one try then stop", err, calls)
	}
}
