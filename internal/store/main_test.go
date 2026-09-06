package store_test

import (
	"os"
	"testing"

	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/store"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.StartMain(m))
}

// newStore returns a Store over a freshly truncated database, skipping when
// Postgres is unavailable so the suite stays runnable without Docker.
func newStore(t *testing.T) *store.Store {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.TruncateAll(t, pool)
	return store.New(pool)
}
