package main

import (
	"bytes"
	"context"
	"net/url"
	"os"
	"testing"

	"github.com/davidteather/property-radar/internal/pgtest"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.StartMain(m))
}

// requirePostgres skips the test cleanly when the shared container is
// unavailable so command integration tests stay runnable without Docker.
func requirePostgres(t *testing.T) {
	t.Helper()
	pgtest.Require(t)
}

// freshDatabase creates an empty, unmigrated database in the shared container.
func freshDatabase(t *testing.T, name string) string {
	t.Helper()
	ctx := context.Background()
	pool := pgtest.Pool(t)
	if _, err := pool.Exec(ctx, "DROP DATABASE IF EXISTS "+name); err != nil {
		t.Fatalf("drop database %s: %v", name, err)
	}
	if _, err := pool.Exec(ctx, "CREATE DATABASE "+name); err != nil {
		t.Fatalf("create database %s: %v", name, err)
	}
	u, err := url.Parse(pgtest.DSN(t))
	if err != nil {
		t.Fatalf("parse dsn: %v", err)
	}
	u.Path = "/" + name
	return u.String()
}

// runCommand drives the real root command so flag parsing, config loading, and
// pool wiring are exercised together.
func runCommand(t *testing.T, args ...string) (string, error) {
	t.Helper()
	t.Setenv("DATABASE_URL", pgtest.DSN(t))
	t.Setenv("THUMBS_DIR", t.TempDir())

	a := &app{}
	t.Cleanup(a.close)

	var buf bytes.Buffer
	root := newRootCmd(a)
	root.SetOut(&buf)
	root.SetErr(&buf)
	root.SetArgs(args)
	err := root.ExecuteContext(context.Background())
	return buf.String(), err
}
