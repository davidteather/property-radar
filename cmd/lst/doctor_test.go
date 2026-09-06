package main

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/config"
)

func TestRunChecksRendersOutcomes(t *testing.T) {
	checks := []check{
		{"config", func(context.Context) (string, error) { return "loaded", nil }},
		{"database", func(context.Context) (string, error) { return "", errors.New("refused:\n  bad host") }},
	}
	var buf bytes.Buffer
	ok, err := runChecks(context.Background(), &buf, checks)
	if err != nil {
		t.Fatalf("runChecks: %v", err)
	}
	if ok {
		t.Error("runChecks reported success despite a failing check")
	}
	want := `PASS  config    loaded
FAIL  database  refused: bad host
`
	if got := buf.String(); got != want {
		t.Errorf("runChecks mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRunChecksAllPass(t *testing.T) {
	var buf bytes.Buffer
	ok, err := runChecks(context.Background(), &buf, []check{
		{"thumbs dir", func(context.Context) (string, error) { return "writable", nil }},
	})
	if err != nil {
		t.Fatalf("runChecks: %v", err)
	}
	if !ok {
		t.Error("runChecks should report success")
	}
	if got, want := buf.String(), "PASS  thumbs dir  writable\n"; got != want {
		t.Errorf("runChecks = %q, want %q", got, want)
	}
}

func TestDoctorChecksStopsAtConfigFailure(t *testing.T) {
	checks := doctorChecks(config.Config{}, errors.New("bad DATABASE_URL"))
	if len(checks) != 1 || checks[0].name != "config" {
		t.Fatalf("expected a single config check, got %d", len(checks))
	}
	if _, err := checks[0].run(context.Background()); err == nil {
		t.Error("config check should fail")
	}
}

func TestDoctorChecksNeverPrintCredentials(t *testing.T) {
	cfg := config.Config{
		DatabaseURL: "postgres://someone:hunter2@db.internal:5432/property_radar",
		ThumbsDir:   t.TempDir(),
	}
	checks := doctorChecks(cfg, nil)
	detail, err := checks[0].run(context.Background())
	if err != nil {
		t.Fatalf("config check: %v", err)
	}
	if strings.Contains(detail, "hunter2") || strings.Contains(detail, "someone") {
		t.Errorf("config check leaked credentials: %q", detail)
	}
	if !strings.Contains(detail, "db.internal:5432") {
		t.Errorf("config check should name the host, got %q", detail)
	}
}

func TestDoctorChecksSelectStorageCheckByBackend(t *testing.T) {
	local := doctorChecks(config.Config{
		DatabaseURL:    "postgres://localhost/db",
		StorageBackend: config.StorageLocal,
		ThumbsDir:      t.TempDir(),
	}, nil)
	if last := local[len(local)-1].name; last != "thumbs dir" {
		t.Errorf("local backend storage check = %q, want %q", last, "thumbs dir")
	}

	s3 := doctorChecks(config.Config{
		DatabaseURL:    "postgres://localhost/db",
		StorageBackend: config.StorageS3,
		S3:             config.S3Config{Endpoint: "s3.internal:9000", Bucket: "thumbs"},
	}, nil)
	if last := s3[len(s3)-1].name; last != "photo store" {
		t.Errorf("s3 backend storage check = %q, want %q", last, "photo store")
	}
}

func TestDoctorConfigSummaryS3NeverLeaksKeys(t *testing.T) {
	cfg := config.Config{
		DatabaseURL:    "postgres://localhost:5432/db",
		StorageBackend: config.StorageS3,
		S3: config.S3Config{
			Endpoint:  "s3.internal:9000",
			Bucket:    "thumbs",
			AccessKey: "AKIASECRET",
			SecretKey: "hunter2",
		},
	}
	detail, err := doctorChecks(cfg, nil)[0].run(context.Background())
	if err != nil {
		t.Fatalf("config check: %v", err)
	}
	if strings.Contains(detail, "AKIASECRET") || strings.Contains(detail, "hunter2") {
		t.Errorf("config check leaked s3 keys: %q", detail)
	}
	if !strings.Contains(detail, "s3.internal:9000") || !strings.Contains(detail, "thumbs") {
		t.Errorf("config check should name the endpoint and bucket, got %q", detail)
	}
}

func TestDatabaseHost(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"postgres://u:p@localhost:5432/db", "localhost:5432"},
		{"postgres://db.internal/property_radar", "db.internal"},
		{"::not a url::", unknown},
	}
	for _, tc := range tests {
		if got := databaseHost(tc.in); got != tc.want {
			t.Errorf("databaseHost(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestCheckThumbsDir(t *testing.T) {
	dir := t.TempDir()
	detail, err := checkThumbsDir(dir)
	if err != nil {
		t.Fatalf("checkThumbsDir(%s): %v", dir, err)
	}
	if !strings.Contains(detail, dir) {
		t.Errorf("detail %q should name the directory", detail)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("read dir: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("probe file was not cleaned up: %v", entries)
	}
}

func TestCheckThumbsDirMissingIsCreated(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "absent", "thumbs")
	if _, err := checkThumbsDir(dir); err != nil {
		t.Fatalf("missing thumbs dir should be created like the photo store does: %v", err)
	}
	if info, err := os.Stat(dir); err != nil || !info.IsDir() {
		t.Errorf("thumbs dir after check: %v, %v", info, err)
	}
}

func TestCheckThumbsDirRejectsAFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "thumbs")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := checkThumbsDir(path); err == nil {
		t.Error("a regular file where the thumbs dir should be must fail")
	}
}

func TestCheckThumbsDirNotWritable(t *testing.T) {
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits are not enforced for this user")
	}
	dir := filepath.Join(t.TempDir(), "readonly")
	if err := os.Mkdir(dir, 0o555); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if _, err := checkThumbsDir(dir); err == nil {
		t.Error("read-only thumbs dir should fail")
	}
}

func TestCheckThumbsDirNotADirectory(t *testing.T) {
	file := filepath.Join(t.TempDir(), "thumbs")
	if err := os.WriteFile(file, nil, 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}
	if _, err := checkThumbsDir(file); err == nil {
		t.Error("a plain file should fail the thumbs dir check")
	}
}
