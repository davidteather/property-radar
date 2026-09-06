package main

import (
	"bytes"
	"context"
	"database/sql"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/migrations"
)

// migrateUp is what mcpd runs before serving; a fresh database must end up with
// the schema present and a second run must be a no-op (goose owns the locking).
func TestMigrateUpBringsSchemaCurrent(t *testing.T) {
	if testing.Short() {
		t.Skip("migrate-on-startup test needs Postgres; skipped with -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("property_radar"),
		tcpostgres.WithUsername("property_radar"),
		tcpostgres.WithPassword("property_radar"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		if pgtest.IsDockerUnavailable(err) {
			t.Skipf("docker unavailable: %v", err)
		}
		t.Fatalf("start postgres container: %v", err)
	}
	t.Cleanup(func() { _ = testcontainers.TerminateContainer(container) })

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("connection string: %v", err)
	}

	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	if err := migrateUp(ctx, logger, dsn); err != nil {
		t.Fatalf("first migrateUp on a fresh database: %v", err)
	}
	// Idempotent: a server restart re-runs this and must not fail.
	if err := migrateUp(ctx, logger, dsn); err != nil {
		t.Fatalf("second migrateUp should be a no-op: %v", err)
	}

	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	var count int
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM crawl_targets").Scan(&count); err != nil {
		t.Fatalf("crawl_targets should exist after migrateUp: %v", err)
	}

	// An upgrade with several replicas starting at once: roll the last
	// migration back, then race migrateUp; without the session lock the losers
	// fail on "relation already exists" and the deploy crashes.
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.Down(ctx); err != nil {
		t.Fatalf("roll back last migration: %v", err)
	}
	// With migrations off, a stale schema is refused rather than served.
	if err := requireSchema(ctx, logger, dsn); err == nil || !strings.Contains(err.Error(), "schema version") {
		t.Fatalf("requireSchema on a stale schema err = %v, want a version mismatch", err)
	}
	const replicas = 6
	start := make(chan struct{})
	errs := make(chan error, replicas)
	for range replicas {
		go func() {
			<-start
			errs <- migrateUp(ctx, logger, dsn)
		}()
	}
	close(start)
	for range replicas {
		if err := <-errs; err != nil {
			t.Errorf("concurrent migrateUp: %v", err)
		}
	}
	if err := requireSchema(ctx, logger, dsn); err != nil {
		t.Fatalf("requireSchema on a current schema: %v", err)
	}

	// A database ahead of the binary (a rollback) is served, but not silently.
	if _, err := db.ExecContext(ctx, "INSERT INTO goose_db_version (version_id, is_applied) VALUES (9999, true)"); err != nil {
		t.Fatalf("fake a newer migration: %v", err)
	}
	var warned bytes.Buffer
	if err := requireSchema(ctx, slog.New(slog.NewTextHandler(&warned, nil)), dsn); err != nil {
		t.Fatalf("requireSchema on a newer schema: %v", err)
	}
	if !strings.Contains(warned.String(), "newer than this binary") {
		t.Fatalf("no warning for a newer schema; log = %q", warned.String())
	}
}
