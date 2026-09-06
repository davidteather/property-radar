package main

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/pgtest"
)

// A one-shot drain on an unmigrated database must refuse (mcpd owns the
// migration); once the schema is current the same check passes.
func TestWaitForSchemaRefusesAnOldSchema(t *testing.T) {
	if testing.Short() {
		t.Skip("needs Postgres; skipped with -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	dsn := pgtest.RunContainer(t, ctx)
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	err := waitForSchema(ctx, logger, dsn, false)
	if err == nil || !strings.Contains(err.Error(), "schema version 0") {
		t.Fatalf("unmigrated: err = %v, want a version-0 refusal", err)
	}
	pgtest.Migrate(t, dsn)
	if err := waitForSchema(ctx, logger, dsn, false); err != nil {
		t.Fatalf("migrated: %v", err)
	}
}
