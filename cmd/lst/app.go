package main

import (
	"context"
	"fmt"
	"net/url"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/store"
)

const commandTimeout = 30 * time.Second

// Config loads once; the pool opens on first use so config-only commands stay offline.
type app struct {
	cfg  config.Config
	pool *pgxpool.Pool
}

func (a *app) loadConfig() error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	a.cfg = cfg
	return nil
}

func (a *app) store(ctx context.Context) (*store.Store, error) {
	if a.pool == nil {
		pool, err := pgxpool.New(ctx, a.cfg.DatabaseURL)
		if err != nil {
			return nil, fmt.Errorf("open database pool: %w", err)
		}
		a.pool = pool
	}
	return store.New(a.pool), nil
}

func (a *app) close() {
	if a.pool != nil {
		a.pool.Close()
		a.pool = nil
	}
}

func commandContext(cmd *cobra.Command) (context.Context, context.CancelFunc) {
	return context.WithTimeout(cmd.Context(), commandTimeout)
}

// databaseHost reports host:port only; credentials must never be printed.
func databaseHost(databaseURL string) string {
	u, err := url.Parse(databaseURL)
	if err != nil || u.Host == "" {
		return unknown
	}
	return u.Host
}
