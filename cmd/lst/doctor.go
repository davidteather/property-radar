package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/spf13/cobra"

	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/migrations"
)

const checkTimeout = 5 * time.Second

// A key that never holds a real thumbnail; Stat just proves the store is reachable.
const doctorProbeKey = "doctor/probe"

type check struct {
	name string
	run  func(ctx context.Context) (string, error)
}

func newDoctorCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "doctor",
		Short: "Check configuration, database, migrations, and thumbnail storage",
		Args:  cobra.NoArgs,
		// Config loading is itself a check here, so shadow the root's load hook.
		PersistentPreRunE: func(*cobra.Command, []string) error { return nil },
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, cfgErr := config.Load()
			ok, err := runChecks(cmd.Context(), cmd.OutOrStdout(), doctorChecks(cfg, cfgErr))
			if err != nil {
				return err
			}
			if !ok {
				return errors.New("doctor reported failing checks")
			}
			return nil
		},
	}
}

func doctorChecks(cfg config.Config, cfgErr error) []check {
	if cfgErr != nil {
		return []check{{
			name: "config",
			run:  func(context.Context) (string, error) { return "", cfgErr },
		}}
	}
	checks := []check{
		{
			name: "config",
			run: func(context.Context) (string, error) {
				return fmt.Sprintf("database host %s, %s", databaseHost(cfg.DatabaseURL), storageSummary(cfg)), nil
			},
		},
		{
			name: "database",
			run:  func(ctx context.Context) (string, error) { return checkDatabase(ctx, cfg.DatabaseURL) },
		},
		{
			name: "migrations",
			run:  func(ctx context.Context) (string, error) { return checkMigrations(ctx, cfg.DatabaseURL) },
		},
	}
	if cfg.StorageBackend == config.StorageS3 {
		return append(checks, check{
			name: "photo store",
			run:  func(ctx context.Context) (string, error) { return checkPhotoStore(ctx, cfg) },
		})
	}
	return append(checks, check{
		name: "thumbs dir",
		run:  func(context.Context) (string, error) { return checkThumbsDir(cfg.ThumbsDir) },
	})
}

// storageSummary names the backend without ever printing S3 keys.
func storageSummary(cfg config.Config) string {
	if cfg.StorageBackend == config.StorageS3 {
		return fmt.Sprintf("photo store s3 endpoint %s bucket %s", cfg.S3.Endpoint, cfg.S3.Bucket)
	}
	return "thumbs dir " + cfg.ThumbsDir
}

// checkPhotoStore proves the s3 backend is reachable; endpoint/bucket are safe to print, keys are not.
func checkPhotoStore(ctx context.Context, cfg config.Config) (string, error) {
	ps, err := photostore.New(ctx, cfg.PhotoStore())
	if err != nil {
		return "", err
	}
	if _, err := ps.Stat(ctx, doctorProbeKey); err != nil {
		return "", fmt.Errorf("stat %s bucket %s: %w", cfg.S3.Endpoint, cfg.S3.Bucket, err)
	}
	return fmt.Sprintf("reachable at %s bucket %s", cfg.S3.Endpoint, cfg.S3.Bucket), nil
}

func runChecks(ctx context.Context, w io.Writer, checks []check) (bool, error) {
	ok := true
	rows := make([][]string, 0, len(checks))
	for _, c := range checks {
		checkCtx, cancel := context.WithTimeout(ctx, checkTimeout)
		detail, err := c.run(checkCtx)
		cancel()
		if err != nil {
			ok = false
			rows = append(rows, []string{"FAIL", c.name, oneLine(err.Error())})
			continue
		}
		rows = append(rows, []string{"PASS", c.name, detail})
	}
	return ok, renderRows(w, rows)
}

func checkDatabase(ctx context.Context, databaseURL string) (string, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return "", fmt.Errorf("open pool: %w", err)
	}
	defer pool.Close()

	if err := pool.Ping(ctx); err != nil {
		return "", fmt.Errorf("ping %s: %w", databaseHost(databaseURL), err)
	}
	return "reachable at " + databaseHost(databaseURL), nil
}

func checkMigrations(ctx context.Context, databaseURL string) (string, error) {
	current, target, err := migrations.Versions(ctx, databaseURL)
	if err != nil {
		return "", err
	}
	if current != target {
		return "", fmt.Errorf("database at version %d, migrations go to %d: run migrations", current, target)
	}
	return fmt.Sprintf("schema current at version %d", current), nil
}

// checkThumbsDir creates the directory when absent, as the photo store does
// on first write, so a fresh install passes.
func checkThumbsDir(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	f, err := os.CreateTemp(dir, ".lst-doctor-*")
	if err != nil {
		return "", fmt.Errorf("write to %s: %w", dir, err)
	}
	name := f.Name()
	defer func() { _ = os.Remove(name) }()
	if err := f.Close(); err != nil {
		return "", fmt.Errorf("close probe file in %s: %w", dir, err)
	}
	return "writable: " + filepath.Clean(dir), nil
}
