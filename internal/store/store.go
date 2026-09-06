// Package store is the Postgres persistence layer; rows and SQL stay private,
// public methods speak domain vocabulary.
package store

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidteather/property-radar/internal/listings"
)

var (
	// Re-exported from the consumer package so transports map them without
	// importing store (avoids an import cycle).
	ErrPropertyNotFound     = listings.ErrNotFound
	ErrFutureThroughVerdict = listings.ErrFutureThroughVerdict

	// Lifecycle may only advance on complete, non-suspect runs.
	ErrRunNotEligible = errors.New("ingest run is not complete and non-suspect")
)

// querier is the subset of pgx shared by *pgxpool.Pool and pgx.Tx, so the same
// statements run inside or outside a transaction.
type querier interface {
	Exec(ctx context.Context, sql string, args ...any) (pgconn.CommandTag, error)
	Query(ctx context.Context, sql string, args ...any) (pgx.Rows, error)
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

type Store struct {
	// pool is nil for the transaction-bound Store handed to InTx.
	pool *pgxpool.Pool
	q    querier
}

func New(db *pgxpool.Pool) *Store {
	return &Store{pool: db, q: db}
}

// InTx runs fn against a transaction-bound Store, committing when fn returns
// nil. On an already transaction-bound Store it just runs fn.
func (s *Store) InTx(ctx context.Context, fn func(*Store) error) error {
	return s.InTxOptions(ctx, pgx.TxOptions{}, fn)
}

// InTxOptions is InTx with explicit transaction options (isolation level).
func (s *Store) InTxOptions(ctx context.Context, opts pgx.TxOptions, fn func(*Store) error) error {
	if s.pool == nil {
		return fn(s)
	}
	tx, err := s.pool.BeginTx(ctx, opts)
	if err != nil {
		return fmt.Errorf("begin transaction: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if err := fn(&Store{q: tx}); err != nil {
		return err
	}
	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("commit transaction: %w", err)
	}
	return nil
}

func isForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23503"
}
