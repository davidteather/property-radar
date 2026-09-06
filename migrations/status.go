package migrations

import (
	"context"
	"database/sql"
	"fmt"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"
)

// Versions reports the schema version the database is at and the newest one
// embedded here. goose creates its (empty) version table on a never-migrated database.
func Versions(ctx context.Context, databaseURL string) (current, target int64, err error) {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return 0, 0, fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return 0, 0, fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, FS, goose.WithSessionLocker(locker))
	if err != nil {
		return 0, 0, fmt.Errorf("load migrations: %w", err)
	}
	current, target, err = provider.GetVersions(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("read migration version: %w", err)
	}
	return current, target, nil
}
