// Package retry runs a startup dependency check with exponential backoff so a
// co-deployed service that is still coming up does not crash-loop its peers.
package retry

import (
	"context"
	"log/slog"
	"time"
)

const (
	firstWait = 5 * time.Second
	maxWait   = time.Minute
)

// Do runs fn until it succeeds, ctx ends, or budget elapses (budget <= 0
// retries until ctx ends). Each failure is logged with the wait before the next try.
func Do(ctx context.Context, logger *slog.Logger, what string, budget time.Duration, fn func(context.Context) error) error {
	return do(ctx, logger, what, budget, firstWait, maxWait, fn)
}

func do(ctx context.Context, logger *slog.Logger, what string, budget, wait, capWait time.Duration, fn func(context.Context) error) error {
	deadline := time.Time{}
	if budget > 0 {
		deadline = time.Now().Add(budget)
	}
	for {
		err := fn(ctx)
		if err == nil || ctx.Err() != nil {
			return err
		}
		if !deadline.IsZero() && time.Now().Add(wait).After(deadline) {
			return err
		}
		logger.Warn(what+" failed; retrying", "err", err, "in", wait)
		select {
		case <-ctx.Done():
			return err
		case <-time.After(wait):
		}
		wait = min(wait*2, capWait)
	}
}
