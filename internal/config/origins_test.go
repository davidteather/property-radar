package config_test

import (
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/config"
)

func hasWarning(ws []string, want string) bool {
	for _, w := range ws {
		if strings.Contains(w, want) {
			return true
		}
	}
	return false
}

// CONSOLE_URL is a convenience link: a bad value is dropped with a warning
// rather than refusing to boot (Railway renders an unset domain as "https://").
func TestLoadConsoleURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "")

	t.Setenv("CONSOLE_URL", "  https://console.example.com/  ")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.ConsoleURL != "https://console.example.com" {
		t.Fatalf("console url = %q, want the trimmed origin", cfg.ConsoleURL)
	}
	if hasWarning(cfg.Warnings(true), "CONSOLE_URL") {
		t.Fatalf("valid CONSOLE_URL should not warn: %v", cfg.Warnings(true))
	}

	for _, bad := range []string{"https://", "ftp://console.example.com", "console.example.com"} {
		t.Setenv("CONSOLE_URL", bad)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("CONSOLE_URL=%q must not fail startup: %v", bad, err)
		}
		if cfg.ConsoleURL != "" {
			t.Fatalf("CONSOLE_URL=%q should be dropped, got %q", bad, cfg.ConsoleURL)
		}
		if !hasWarning(cfg.Warnings(true), "CONSOLE_URL") {
			t.Fatalf("CONSOLE_URL=%q should warn, got %v", bad, cfg.Warnings(true))
		}
	}
}

// A stdio server has no listener to derive the photo origin from, so an unset
// PUBLIC_BASE_URL is flagged; the http server derives it per request and stays quiet.
func TestWarningsStdioWithoutPublicBaseURL(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "")
	t.Setenv("PUBLIC_BASE_URL", "")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !hasWarning(cfg.Warnings(false), "PUBLIC_BASE_URL is unset") {
		t.Fatalf("stdio without PUBLIC_BASE_URL should warn, got %v", cfg.Warnings(false))
	}
	if hasWarning(cfg.Warnings(true), "PUBLIC_BASE_URL") {
		t.Fatalf("http mode derives the origin per request; got %v", cfg.Warnings(true))
	}

	t.Setenv("PUBLIC_BASE_URL", "https://radar.example.com")
	cfg, err = config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if hasWarning(cfg.Warnings(false), "PUBLIC_BASE_URL") {
		t.Fatalf("set PUBLIC_BASE_URL should not warn, got %v", cfg.Warnings(false))
	}
}

func TestShortImageTokenWarns(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "")
	t.Setenv("PUBLIC_IMG_TOKEN", "short")
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !hasWarning(cfg.Warnings(true), "PUBLIC_IMG_TOKEN is shorter") {
		t.Fatalf("a 5-character image token should warn, got %v", cfg.Warnings(true))
	}
	t.Setenv("PUBLIC_IMG_TOKEN", "0123456789abcdef0123456789abcdef")
	if cfg, err = config.Load(); err != nil {
		t.Fatalf("load: %v", err)
	}
	if hasWarning(cfg.Warnings(true), "PUBLIC_IMG_TOKEN") {
		t.Fatalf("a long image token should not warn, got %v", cfg.Warnings(true))
	}
}

// minio rejects a scheme in the endpoint with an opaque message; config says so up front.
func TestLoadRejectsS3EndpointWithScheme(t *testing.T) {
	t.Setenv("DATABASE_URL", "")
	t.Setenv("STORAGE_BACKEND", "s3")
	t.Setenv("S3_ENDPOINT", "http://storage:9000")
	t.Setenv("S3_BUCKET", "thumbs")
	t.Setenv("S3_ACCESS_KEY", "ak")
	t.Setenv("S3_SECRET_KEY", "sk")

	_, err := config.Load()
	if err == nil || !strings.Contains(err.Error(), "S3_ENDPOINT must be host:port") {
		t.Fatalf("err = %v, want the no-scheme message", err)
	}
}
