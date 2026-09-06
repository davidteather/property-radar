// Package pgtest is the shared Postgres testcontainer harness for the
// integration suites: one migrated postgres:17-alpine container per test binary,
// tables truncated between tests, skipped when Docker is unavailable or -short.
package pgtest

import (
	"context"
	"database/sql"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib" // database/sql driver for goose
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/davidteather/property-radar/migrations"
)

// shared holds the one migrated container serving the whole test binary,
// populated by StartMain and read through Pool/DSN after Require's readiness gate.
var (
	shared    *pgxpool.Pool
	sharedDSN string
	setupErr  error
)

// StartMain launches one migrated container for the binary, runs the suite,
// tears it down, and returns the exit code; skips container startup under -short.
// Each package's TestMain is: func TestMain(m *testing.M) { os.Exit(pgtest.StartMain(m)) }
func StartMain(m *testing.M) int {
	flag.Parse()
	var terminate func()
	if !testing.Short() {
		terminate, setupErr = startShared(context.Background())
	}
	code := m.Run()
	if terminate != nil {
		terminate()
	}
	return code
}

// Require gates on the shared container's readiness: skip under -short, skip when
// Docker is unavailable, fail on any other setup error.
func Require(t *testing.T) {
	t.Helper()
	if testing.Short() {
		t.Skip("integration test needs Postgres; skipped with -short")
	}
	if setupErr != nil {
		skipOrFail(t, setupErr, "postgres test container")
	}
}

// Docker being down is a skip on a laptop but a failure in CI: with
// PGTEST_REQUIRED set, a green run means the Postgres suites actually ran.
func skipOrFail(t *testing.T, err error, what string) {
	t.Helper()
	if IsDockerUnavailable(err) && os.Getenv("PGTEST_REQUIRED") == "" {
		t.Skipf("docker unavailable (set PGTEST_REQUIRED=1 to fail instead): %v", err)
	}
	t.Fatalf("%s: %v", what, err)
}

// Pool returns the shared migrated pool after the Require readiness gate.
func Pool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	Require(t)
	return shared
}

// DSN returns the shared container's connection string after the readiness gate.
func DSN(t *testing.T) string {
	t.Helper()
	Require(t)
	return sharedDSN
}

// TruncateAll empties every application table so each test starts clean. The
// table list is discovered from information_schema (minus goose's bookkeeping
// table), so new migrations are covered automatically.
func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	rows, err := pool.Query(ctx, `
		SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_type = 'BASE TABLE'
		  AND table_name <> 'goose_db_version'`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			t.Fatalf("scan table name: %v", err)
		}
		tables = append(tables, `"`+name+`"`)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate tables: %v", err)
	}
	if len(tables) == 0 {
		return
	}
	stmt := "TRUNCATE " + strings.Join(tables, ", ") + " RESTART IDENTITY CASCADE"
	if _, err := pool.Exec(ctx, stmt); err != nil {
		t.Fatalf("truncate tables: %v", err)
	}
}

// RunContainer starts a fresh, unmigrated postgres:17-alpine container for tests
// needing their own isolated container, registers teardown, and returns its DSN.
// Skips when Docker is unavailable; apply Migrate afterwards if the schema is needed.
func RunContainer(t *testing.T, ctx context.Context) string {
	t.Helper()
	container, dsn, err := runContainer(ctx)
	if err != nil {
		skipOrFail(t, err, "start postgres container")
	}
	t.Cleanup(func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			t.Logf("terminate postgres container: %v", err)
		}
	})
	return dsn
}

// Migrate applies the embedded goose migrations to dsn, failing the test on error.
func Migrate(t *testing.T, dsn string) {
	t.Helper()
	if err := applyMigrations(dsn); err != nil {
		t.Fatalf("apply migrations: %v", err)
	}
}

// IsDockerUnavailable reports whether err signals Docker is not reachable, so
// suites can skip rather than fail.
func IsDockerUnavailable(err error) bool {
	if err == nil {
		return false
	}
	msg := err.Error()
	for _, marker := range []string{
		"Cannot connect to the Docker daemon",
		"docker daemon",
		"rootless Docker not found",
		"failed to find any Docker host",
		"could not connect to Docker",
		"docker: command not found",
		"dial unix",
	} {
		if strings.Contains(msg, marker) {
			return true
		}
	}
	return false
}

// startShared brings up the binary-wide container, migrates it, opens the shared
// pool, and returns a teardown closure.
func startShared(ctx context.Context) (func(), error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()

	container, dsn, err := runContainer(ctx)
	if err != nil {
		return nil, err
	}
	terminate := func() {
		if err := testcontainers.TerminateContainer(container); err != nil {
			log.Printf("terminate postgres container: %v", err)
		}
	}
	if err := applyMigrations(dsn); err != nil {
		terminate()
		return nil, err
	}
	pool, err := pgxpool.New(ctx, dsn)
	if err != nil {
		terminate()
		return nil, fmt.Errorf("open pool: %w", err)
	}
	shared, sharedDSN = pool, dsn
	return func() {
		pool.Close()
		terminate()
	}, nil
}

// runContainer starts an unmigrated postgres:17-alpine container and returns it
// with its sslmode=disable connection string.
func runContainer(ctx context.Context) (*tcpostgres.PostgresContainer, string, error) {
	container, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("property_radar"),
		tcpostgres.WithUsername("property_radar"),
		tcpostgres.WithPassword("property_radar"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return nil, "", fmt.Errorf("start postgres container: %w", err)
	}
	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		if termErr := testcontainers.TerminateContainer(container); termErr != nil {
			log.Printf("terminate postgres container: %v", termErr)
		}
		return nil, "", fmt.Errorf("connection string: %w", err)
	}
	return container, dsn, nil
}

// applyMigrations runs the embedded goose migrations against dsn.
func applyMigrations(dsn string) error {
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		return fmt.Errorf("open sql db: %w", err)
	}
	defer func() { _ = db.Close() }()

	goose.SetBaseFS(migrations.FS)
	goose.SetLogger(goose.NopLogger())
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "."); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}
