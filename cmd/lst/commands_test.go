package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/store"
)

func TestListCommandAgainstEmptyStore(t *testing.T) {
	requirePostgres(t)
	truncateTables(t, "ingest_runs")

	out, err := runCommand(t, "list", "--max-price", "1200000", "--min-beds", "2", "--neighborhood", "Park Slope")
	if err != nil {
		t.Fatalf("lst list: %v", err)
	}
	if !strings.Contains(out, "no active listings matched") {
		t.Errorf("unexpected output:\n%s", out)
	}
}

func TestListCommandRejectsBadFlagBeforeQuerying(t *testing.T) {
	requirePostgres(t)

	if _, err := runCommand(t, "list", "--listing-type", "lease"); err == nil {
		t.Error("expected an error for an unknown listing type")
	}
}

func TestShowCommandMissingListing(t *testing.T) {
	requirePostgres(t)

	_, err := runCommand(t, "show", "424242")
	if !errors.Is(err, store.ErrPropertyNotFound) {
		t.Errorf("show unknown listing error = %v, want ErrPropertyNotFound", err)
	}
}

func TestStatusCommandReportsRuns(t *testing.T) {
	requirePostgres(t)
	truncateTables(t, "ingest_runs")

	ctx := context.Background()
	s := store.New(pgtest.Pool(t))
	run, err := s.CreateRun(ctx, "streeteasy", "scope-a1b2c3d4e5f6", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.FinishRun(ctx, run.ID, ingest.RunStats{
		Complete: true, ListingsSeen: 412, Created: 12, Updated: 30, PhotoFailures: 2, Suspect: true,
	}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	out, err := runCommand(t, "status")
	if err != nil {
		t.Fatalf("lst status: %v", err)
	}
	for _, want := range []string{
		"Last 10 ingest runs", "streeteasy", "scope-a1b…", "412",
		"active listings", "sources with missing_runs > 0", "runs are suspect",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("status output missing %q:\n%s", want, out)
		}
	}
}

func TestDoctorCommandAgainstMigratedDatabase(t *testing.T) {
	requirePostgres(t)

	out, err := runCommand(t, "doctor")
	if err != nil {
		t.Fatalf("lst doctor: %v\n%s", err, out)
	}
	if strings.Contains(out, "FAIL") {
		t.Errorf("doctor should pass against a migrated database:\n%s", out)
	}
	for _, want := range []string{"PASS  config", "PASS  database", "PASS  migrations", "PASS  thumbs dir"} {
		if !strings.Contains(out, want) {
			t.Errorf("doctor output missing %q:\n%s", want, out)
		}
	}
}

func TestCheckMigrationsDetectsPendingSchema(t *testing.T) {
	requirePostgres(t)

	dsn := freshDatabase(t, "lst_doctor_pending")
	_, err := checkMigrations(context.Background(), dsn)
	if err == nil {
		t.Fatal("expected pending migrations on a fresh database")
	}
	if !strings.Contains(err.Error(), "run migrations") {
		t.Errorf("error should tell the operator what to do, got %v", err)
	}
}

func TestCheckDatabaseUnreachable(t *testing.T) {
	// Port 1 is reserved and never listening; no container needed.
	_, err := checkDatabase(context.Background(), "postgres://property_radar:pw@127.0.0.1:1/property_radar")
	if err == nil {
		t.Fatal("expected a connection failure")
	}
	if strings.Contains(err.Error(), "pw") {
		t.Errorf("connection error leaked the password: %v", err)
	}
}

func TestDoctorChecksAgainstContainer(t *testing.T) {
	requirePostgres(t)

	cfg := config.Config{DatabaseURL: pgtest.DSN(t), ThumbsDir: t.TempDir()}
	for _, c := range doctorChecks(cfg, nil) {
		if _, err := c.run(context.Background()); err != nil {
			t.Errorf("check %q failed: %v", c.name, err)
		}
	}
}

func truncateTables(t *testing.T, tables ...string) {
	t.Helper()
	stmt := "TRUNCATE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := pgtest.Pool(t).Exec(context.Background(), stmt); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}
