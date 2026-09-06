package config_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/config"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("THUMBS_DIR", "")
	t.Setenv("WEBSHARE_API_KEY", "")
	t.Setenv("MCP_BEARER_TOKEN", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MCPBearerToken != "" {
		t.Fatalf("bearer token = %q, want empty", cfg.MCPBearerToken)
	}
	if cfg.DatabaseURL != config.DefaultDatabaseURL {
		t.Fatalf("database url = %q", cfg.DatabaseURL)
	}
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	want := filepath.Join(home, ".local", "share", "property-radar", "thumbs")
	if cfg.ThumbsDir != want {
		t.Fatalf("thumbs dir = %q, want %q", cfg.ThumbsDir, want)
	}
	if cfg.WebshareAPIKey != "" {
		t.Fatalf("webshare key = %q, want empty", cfg.WebshareAPIKey)
	}
}

func TestLoadFromEnvironment(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgresql://user:pw@db.example:6543/radar")
	t.Setenv("THUMBS_DIR", "/var/tmp/radar-thumbs/")
	t.Setenv("WEBSHARE_API_KEY", "  secret  ")
	t.Setenv("MCP_BEARER_TOKEN", "  hunter2  ")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.DatabaseURL != "postgresql://user:pw@db.example:6543/radar" {
		t.Fatalf("database url = %q", cfg.DatabaseURL)
	}
	if cfg.ThumbsDir != "/var/tmp/radar-thumbs" {
		t.Fatalf("thumbs dir = %q", cfg.ThumbsDir)
	}
	if cfg.WebshareAPIKey != "secret" {
		t.Fatalf("webshare key = %q", cfg.WebshareAPIKey)
	}
	if cfg.MCPBearerToken != "hunter2" {
		t.Fatalf("bearer token = %q", cfg.MCPBearerToken)
	}
}

func TestLoadExpandsTildeThumbsDir(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Skipf("no home directory: %v", err)
	}
	t.Setenv("DATABASE_URL", "")
	t.Setenv("THUMBS_DIR", "~/pictures/radar")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ThumbsDir != filepath.Join(home, "pictures", "radar") {
		t.Fatalf("thumbs dir = %q", cfg.ThumbsDir)
	}

	t.Setenv("THUMBS_DIR", "~")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ThumbsDir != filepath.Clean(home) {
		t.Fatalf("bare tilde = %q, want %q", cfg.ThumbsDir, home)
	}
}

func TestLoadStorageBackendDefaultsLocal(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != config.StorageLocal {
		t.Fatalf("backend = %q, want local", cfg.StorageBackend)
	}
}

func TestLoadS3Backend(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("S3_ENDPOINT", "minio.railway.internal:9000")
	t.Setenv("S3_BUCKET", "thumbs")
	t.Setenv("S3_ACCESS_KEY", "  ak  ")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("S3_REGION", "us-east-1")
	t.Setenv("S3_USE_SSL", "false")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != config.StorageS3 {
		t.Fatalf("backend = %q", cfg.StorageBackend)
	}
	if cfg.S3.Endpoint != "minio.railway.internal:9000" || cfg.S3.Bucket != "thumbs" {
		t.Fatalf("s3 endpoint/bucket = %+v", cfg.S3)
	}
	if cfg.S3.AccessKey != "ak" || cfg.S3.SecretKey != "sk" || cfg.S3.Region != "us-east-1" {
		t.Fatalf("s3 creds = %+v", cfg.S3)
	}
	if cfg.S3.UseSSL {
		t.Fatal("S3_USE_SSL=false should disable TLS")
	}
}

func TestLoadS3BackendSkipsThumbsDirWithoutHome(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("S3_ENDPOINT", "minio.railway.internal:9000")
	t.Setenv("S3_BUCKET", "thumbs")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")
	t.Setenv("HOME", "")
	if err := os.Unsetenv("HOME"); err != nil {
		t.Fatalf("unset HOME: %v", err)
	}

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != config.StorageS3 {
		t.Fatalf("backend = %q, want s3", cfg.StorageBackend)
	}
	if cfg.ThumbsDir != "" {
		t.Fatalf("thumbs dir = %q, want empty under s3", cfg.ThumbsDir)
	}
	if cfg.S3.Region != config.DefaultS3Region {
		t.Fatalf("s3 region = %q, want default %q", cfg.S3.Region, config.DefaultS3Region)
	}
}

func TestLoadInfersS3FromDeployedShape(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "")
	t.Setenv("S3_ENDPOINT", "minio.railway.internal:9000")
	t.Setenv("S3_BUCKET", "thumbs")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != config.StorageS3 {
		t.Fatalf("backend = %q, want inferred s3", cfg.StorageBackend)
	}

	t.Setenv("S3_ENDPOINT", "")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.StorageBackend != config.StorageLocal {
		t.Fatalf("backend = %q, want local when only S3_BUCKET is set", cfg.StorageBackend)
	}
}

func TestLoadDefaultsMigrateAndPort(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("MIGRATE_ON_START", "")
	t.Setenv("PORT", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.MigrateOnStart {
		t.Fatal("MigrateOnStart = false, want true by default")
	}
	if cfg.Port != "" {
		t.Fatalf("port = %q, want empty", cfg.Port)
	}

	t.Setenv("MIGRATE_ON_START", "false")
	t.Setenv("PORT", " 8080 ")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.MigrateOnStart {
		t.Fatal("MigrateOnStart = true, want false")
	}
	if cfg.Port != "8080" {
		t.Fatalf("port = %q, want 8080", cfg.Port)
	}
}

func TestLoadPublicBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")

	// Unset: empty, meaning "derive from each request".
	t.Setenv("PUBLIC_BASE_URL", "")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PublicBaseURL != "" {
		t.Fatalf("public base url = %q, want empty", cfg.PublicBaseURL)
	}

	// Set: trimmed, with any trailing slash removed.
	t.Setenv("PUBLIC_BASE_URL", "  https://radar.example.com/  ")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.PublicBaseURL != "https://radar.example.com" {
		t.Fatalf("public base url = %q, want the trimmed origin", cfg.PublicBaseURL)
	}

	// Invalid scheme is rejected.
	t.Setenv("PUBLIC_BASE_URL", "ftp://radar.example.com")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Fatalf("err = %v, want PUBLIC_BASE_URL scheme validation", err)
	}

	// Missing host is rejected.
	t.Setenv("PUBLIC_BASE_URL", "https://")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "PUBLIC_BASE_URL") {
		t.Fatalf("err = %v, want PUBLIC_BASE_URL host validation", err)
	}
}

func TestPhotoStoreMapping(t *testing.T) {
	cfg := config.Config{
		StorageBackend: config.StorageS3,
		ThumbsDir:      "/thumbs",
		S3: config.S3Config{
			Endpoint:  "e",
			Bucket:    "b",
			AccessKey: "ak",
			SecretKey: "sk",
			Region:    "r",
			UseSSL:    true,
		},
	}
	ps := cfg.PhotoStore()
	if ps.Backend != config.StorageS3 || ps.ThumbsDir != "/thumbs" || ps.Endpoint != "e" ||
		ps.Bucket != "b" || ps.AccessKey != "ak" || ps.SecretKey != "sk" || ps.Region != "r" || !ps.UseSSL {
		t.Fatalf("photostore config = %+v", ps)
	}
}

func TestLoadS3BackendRequiresVars(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("S3_ENDPOINT", "")
	t.Setenv("S3_BUCKET", "")
	t.Setenv("S3_ACCESS_KEY", "")
	t.Setenv("S3_SECRET_KEY", "")

	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error for s3 backend with no credentials")
	}
	for _, want := range []string{"S3_ENDPOINT", "S3_BUCKET", "S3_ACCESS_KEY", "S3_SECRET_KEY"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %v, want it to mention %q", err, want)
		}
	}
}

func TestLoadRejectsUnknownStorageBackend(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "gcs")
	if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "STORAGE_BACKEND") {
		t.Fatalf("err = %v, want STORAGE_BACKEND validation", err)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	tests := map[string]struct {
		databaseURL string
		thumbsDir   string
		env         map[string]string
		wantErr     string
	}{
		"non-postgres scheme": {databaseURL: "mysql://localhost/radar", thumbsDir: "/tmp/t", wantErr: "postgres scheme"},
		"missing host":        {databaseURL: "postgres:///radar", thumbsDir: "/tmp/t", wantErr: "host"},
		"relative thumbs dir": {databaseURL: config.DefaultDatabaseURL, thumbsDir: "relative/thumbs", wantErr: "absolute"},
		"bad bool":            {databaseURL: config.DefaultDatabaseURL, thumbsDir: "/tmp/t", env: map[string]string{"URL_TOKEN_AUTH": "yess"}, wantErr: `URL_TOKEN_AUTH="yess" is not a boolean`},
		"bad int":             {databaseURL: config.DefaultDatabaseURL, thumbsDir: "/tmp/t", env: map[string]string{"PHOTO_TTL_DAYS": "30d"}, wantErr: `PHOTO_TTL_DAYS="30d" is not an integer`},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			t.Setenv("DATABASE_URL", tc.databaseURL)
			t.Setenv("THUMBS_DIR", tc.thumbsDir)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}

			_, err := config.Load()
			if err == nil {
				t.Fatal("expected an error")
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

// A malformed DATABASE_URL is rejected without echoing it: the value carries the password.
func TestLoadNeverEchoesDatabaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "postgres://radar:s3cretpw@db:5432/ra%zzdar")
	t.Setenv("THUMBS_DIR", "/tmp/t")
	_, err := config.Load()
	if err == nil {
		t.Fatal("expected an error")
	}
	if strings.Contains(err.Error(), "s3cretpw") {
		t.Fatalf("error echoes the password: %v", err)
	}
	if !strings.Contains(err.Error(), "DATABASE_URL") {
		t.Fatalf("err = %v, want it to name DATABASE_URL", err)
	}
}
