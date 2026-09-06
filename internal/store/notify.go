package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// CrawlDueListener holds one dedicated connection subscribed to crawl_due
// (CreateCrawlTarget notifies on commit). Notifications arriving mid-drain queue
// on the connection and are delivered by the next Wait, so none are lost.
type CrawlDueListener struct {
	conn *pgxpool.Conn
}

// ListenCrawlDue subscribes a dedicated pooled connection to crawl_due. The
// caller owns the listener and must Close it.
func (s *Store) ListenCrawlDue(ctx context.Context) (*CrawlDueListener, error) {
	if s.pool == nil {
		return nil, errors.New("listen crawl_due: store is transaction-bound")
	}
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, fmt.Errorf("acquire listen connection: %w", err)
	}
	if _, err := conn.Exec(ctx, "LISTEN crawl_due"); err != nil {
		conn.Release()
		return nil, fmt.Errorf("listen crawl_due: %w", err)
	}
	return &CrawlDueListener{conn: conn}, nil
}

// Wait blocks until a crawl_due notification arrives or maxWait passes; the
// timeout doubles as the poll fallback, so a missed notification only delays a
// drain, never loses it. Returns ctx.Err only for the caller's own cancellation.
func (l *CrawlDueListener) Wait(ctx context.Context, maxWait time.Duration) error {
	waitCtx, cancel := context.WithTimeout(ctx, maxWait)
	defer cancel()
	_, err := l.conn.Conn().WaitForNotification(waitCtx)
	if err == nil || (errors.Is(err, context.DeadlineExceeded) && ctx.Err() == nil) {
		return nil
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return fmt.Errorf("wait for crawl_due: %w", err)
}

// Close unsubscribes before releasing, or the pooled connection would keep
// receiving (and discarding) notifications for the pool's lifetime.
func (l *CrawlDueListener) Close() {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := l.conn.Exec(ctx, "UNLISTEN crawl_due"); err != nil {
		_ = l.conn.Conn().Close(ctx) // a closed conn is discarded by the pool
	}
	l.conn.Release()
}
