// Package config turns the process environment into one validated Config for the cmd/ composition roots; no other package reads the environment.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

const (
	DefaultDatabaseURL = "postgres://property_radar:property_radar@localhost:5432/property_radar"
	// Relative to home; expanded by Load.
	DefaultThumbsDir = "~/.local/share/property-radar/thumbs"
	DefaultS3Region  = "us-east-1"

	StorageLocal = "local"
	StorageS3    = "s3"

	// DefaultPhotoTTLDays is the PHOTO_TTL_DAYS fallback; see Config.PhotoTTL.
	DefaultPhotoTTLDays = 30
	// MaxPhotoTTLDays bounds PHOTO_TTL_DAYS; anything longer is a typo, not a cache policy.
	MaxPhotoTTLDays = 3650
)

type Config struct {
	DatabaseURL string
	// ThumbsDir is absolute after Load for the local backend, empty for s3.
	ThumbsDir string
	// WebshareAPIKey empty means "no proxies; fetch directly".
	WebshareAPIKey string
	// MCPBearerToken is optional for stdio, required by mcpd's http transport; a secret, never logged.
	MCPBearerToken string
	// StorageBackend: "local" is the dev/self-host default; "s3" is chosen automatically when S3_BUCKET and S3_ENDPOINT are set.
	StorageBackend string
	S3             S3Config
	MigrateOnStart bool
	// URLTokenAuth also accepts the bearer token in a ?token= query param so URL-only clients (claude.ai web connectors) can connect. Off by default.
	URLTokenAuth bool
	// Port is $PORT (PaaS-injected); empty when unset.
	Port string
	// PublicBaseURL optionally overrides the absolute origin the public /img proxy is reachable at. Empty derives it per request (correct for the combined same-origin server); set it only for stdio or when a proxy hides the real host. No trailing slash after Load.
	PublicBaseURL string
	// ConsoleURL (CONSOLE_URL) is the optional hosted web console get_console_url
	// points at. A malformed value (e.g. "https://" from an unset platform domain
	// reference) is dropped with a warning, not a failed boot. No trailing slash.
	ConsoleURL string
	// invalidConsoleURL carries a rejected CONSOLE_URL to Warnings.
	invalidConsoleURL string
	// PublicImgToken, when set, gates the public /img proxy: a request must carry ?k=<token> (embeddable in <img src>) or the operator bearer. A lesser, read-only capability than MCPBearerToken, appended to every minted image URL and exposed via get_state. Empty leaves the proxy fully public (local dev).
	PublicImgToken string
	// SeedCrawlAreas (SEED_CRAWL_AREAS), comma-separated provider area ids,
	// optionally pre-seeds one standing sale scope on a fresh install. Empty (the
	// default) starts with no scopes; the caller LLM onboards via request_crawl.
	SeedCrawlAreas []string
	// PhotoTTL is the rolling-cache window for cached thumbnails (PHOTO_TTL_DAYS,
	// default 30 days): the crawler evicts bytes not refreshed within it, so the
	// store expires stale photos instead of archiving them. Zero disables it.
	PhotoTTL time.Duration
}

// S3Config is only populated and validated when StorageBackend is "s3". Its keys are secrets, never logged.
type S3Config struct {
	Endpoint  string
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

func Load() (Config, error) {
	// Typed env values collect their errors so one message names every bad var.
	var envErrs []error
	s3 := S3Config{
		Endpoint:  strings.TrimSpace(os.Getenv("S3_ENDPOINT")),
		Bucket:    strings.TrimSpace(os.Getenv("S3_BUCKET")),
		AccessKey: strings.TrimSpace(os.Getenv("S3_ACCESS_KEY")),
		SecretKey: strings.TrimSpace(os.Getenv("S3_SECRET_KEY")),
		Region:    envOr("S3_REGION", DefaultS3Region),
		UseSSL:    envBool("S3_USE_SSL", true, &envErrs),
	}
	cfg := Config{
		DatabaseURL:    envOr("DATABASE_URL", DefaultDatabaseURL),
		WebshareAPIKey: strings.TrimSpace(os.Getenv("WEBSHARE_API_KEY")),
		MCPBearerToken: strings.TrimSpace(os.Getenv("MCP_BEARER_TOKEN")),
		StorageBackend: storageBackend(s3),
		S3:             s3,
		MigrateOnStart: envBool("MIGRATE_ON_START", true, &envErrs),
		URLTokenAuth:   envBool("URL_TOKEN_AUTH", false, &envErrs),
		Port:           strings.TrimSpace(os.Getenv("PORT")),
		PublicBaseURL:  strings.TrimRight(strings.TrimSpace(os.Getenv("PUBLIC_BASE_URL")), "/"),
		PublicImgToken: strings.TrimSpace(os.Getenv("PUBLIC_IMG_TOKEN")),
		ConsoleURL:     strings.TrimRight(strings.TrimSpace(os.Getenv("CONSOLE_URL")), "/"),
		SeedCrawlAreas: xslices.SplitCSV(os.Getenv("SEED_CRAWL_AREAS")),
	}
	ttlDays := envInt("PHOTO_TTL_DAYS", DefaultPhotoTTLDays, &envErrs)
	if err := errors.Join(envErrs...); err != nil {
		return Config{}, err
	}
	// Range-checked as days, before the multiplication that could overflow.
	if ttlDays < 0 || ttlDays > MaxPhotoTTLDays {
		return Config{}, fmt.Errorf("PHOTO_TTL_DAYS must be between 0 (disable eviction) and %d, got %d", MaxPhotoTTLDays, ttlDays)
	}
	cfg.PhotoTTL = time.Duration(ttlDays) * 24 * time.Hour
	if cfg.ConsoleURL != "" {
		if err := checkOrigin("CONSOLE_URL", cfg.ConsoleURL); err != nil {
			cfg.invalidConsoleURL = err.Error()
			cfg.ConsoleURL = ""
		}
	}

	// s3 never touches the local thumbs dir; skip expansion so an unset HOME (distroless) does not hard-fail the deployed backend.
	if cfg.StorageBackend != StorageS3 {
		dir, err := expandHome(envOr("THUMBS_DIR", DefaultThumbsDir))
		if err != nil {
			return Config{}, fmt.Errorf("resolve THUMBS_DIR: %w", err)
		}
		cfg.ThumbsDir = dir
	}

	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

// storageBackend honors an explicit STORAGE_BACKEND; otherwise both S3_BUCKET and S3_ENDPOINT set selects s3, else local.
func storageBackend(s3 S3Config) string {
	if v := strings.TrimSpace(os.Getenv("STORAGE_BACKEND")); v != "" {
		return strings.ToLower(v)
	}
	if s3.Bucket != "" && s3.Endpoint != "" {
		return StorageS3
	}
	return StorageLocal
}

func (c Config) PhotoStore() photostore.Config {
	return photostore.Config{
		Backend:   c.StorageBackend,
		ThumbsDir: c.ThumbsDir,
		Endpoint:  c.S3.Endpoint,
		Bucket:    c.S3.Bucket,
		AccessKey: c.S3.AccessKey,
		SecretKey: c.S3.SecretKey,
		Region:    c.S3.Region,
		UseSSL:    c.S3.UseSSL,
	}
}

// minTokenLen is the bearer length below which Warnings flags the token as guessable.
const minTokenLen = 16

// Warnings lists non-fatal misconfigurations worth logging: an open /img proxy
// (only when that route is mounted), a stdio server with no origin to mint photo
// links against, a short bearer token, and a dropped CONSOLE_URL.
func (c Config) Warnings(servesImages bool) []string {
	var out []string
	if servesImages && c.PublicImgToken == "" {
		out = append(out, "PUBLIC_IMG_TOKEN is unset: cached listing photos under /img are readable by anyone who can reach this server")
	}
	if !servesImages && c.PublicBaseURL == "" {
		out = append(out, "PUBLIC_BASE_URL is unset: with no http listener photo links come out as relative /img/... paths no client can load; set it to the origin of an http mcpd serving the same database")
	}
	if c.invalidConsoleURL != "" {
		out = append(out, c.invalidConsoleURL+"; ignoring it, so get_console_url reports no console")
	}
	if c.MCPBearerToken != "" && len(c.MCPBearerToken) < minTokenLen {
		out = append(out, fmt.Sprintf("MCP_BEARER_TOKEN is shorter than %d characters; use a long random value (e.g. openssl rand -hex 32)", minTokenLen))
	}
	if c.PublicImgToken != "" && len(c.PublicImgToken) < minTokenLen {
		out = append(out, fmt.Sprintf("PUBLIC_IMG_TOKEN is shorter than %d characters; it is the only thing between the internet and every cached photo (e.g. openssl rand -hex 16)", minTokenLen))
	}
	return out
}

func (c Config) validate() error {
	// *url.Error echoes the URL, and with it the password, so it is never wrapped.
	u, err := url.Parse(c.DatabaseURL)
	if err != nil {
		return errors.New("DATABASE_URL is not a valid URL (value withheld: it may contain the password)")
	}

	switch u.Scheme {
	case "postgres", "postgresql":
	default:
		return fmt.Errorf("DATABASE_URL must use the postgres scheme, got %q", u.Scheme)
	}
	if u.Host == "" {
		return errors.New("DATABASE_URL must include a host")
	}
	if c.PublicBaseURL != "" {
		if err := checkOrigin("PUBLIC_BASE_URL", c.PublicBaseURL); err != nil {
			return err
		}
	}
	switch c.StorageBackend {
	case StorageLocal:
		if !filepath.IsAbs(c.ThumbsDir) {
			return fmt.Errorf("THUMBS_DIR must resolve to an absolute path, got %q", c.ThumbsDir)
		}
	case StorageS3:
		var missing []string
		if c.S3.Endpoint == "" {
			missing = append(missing, "S3_ENDPOINT")
		} else if strings.Contains(c.S3.Endpoint, "://") {
			return fmt.Errorf("S3_ENDPOINT must be host:port with no scheme (S3_USE_SSL picks https), got %q", c.S3.Endpoint)
		}
		if c.S3.Bucket == "" {
			missing = append(missing, "S3_BUCKET")
		}
		if c.S3.AccessKey == "" {
			missing = append(missing, "S3_ACCESS_KEY")
		}
		if c.S3.SecretKey == "" {
			missing = append(missing, "S3_SECRET_KEY")
		}
		if len(missing) > 0 {
			return fmt.Errorf("STORAGE_BACKEND=s3 requires %s", strings.Join(missing, ", "))
		}
	default:
		return fmt.Errorf("STORAGE_BACKEND must be %q or %q, got %q", StorageLocal, StorageS3, c.StorageBackend)
	}
	return nil
}

// checkOrigin validates an http(s) origin such as PUBLIC_BASE_URL or CONSOLE_URL.
func checkOrigin(name, value string) error {
	u, err := url.Parse(value)
	if err != nil {
		return fmt.Errorf("%s %q is not a valid URL", name, value)
	}
	switch u.Scheme {
	case "http", "https":
	default:
		return fmt.Errorf("%s must use the http or https scheme, got %q", name, u.Scheme)
	}
	if u.Host == "" {
		return fmt.Errorf("%s must include a host", name)
	}
	return nil
}

// envInt parses an integer env value; empty falls back, a non-integer is an
// error (a typo must not silently become the default).
func envInt(key string, fallback int, errs *[]error) int {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		*errs = append(*errs, fmt.Errorf("%s=%q is not an integer", key, v))
		return fallback
	}
	return n
}

func envBool(key string, fallback bool, errs *[]error) bool {
	v := strings.TrimSpace(os.Getenv(key))
	if v == "" {
		return fallback
	}
	switch strings.ToLower(v) {
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	default:
		*errs = append(*errs, fmt.Errorf("%s=%q is not a boolean (use true/false)", key, v))
		return fallback
	}
}

func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

func expandHome(path string) (string, error) {
	if path != "~" && !strings.HasPrefix(path, "~/") {
		return filepath.Clean(path), nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("look up home directory: %w", err)
	}
	if path == "~" {
		return filepath.Clean(home), nil
	}
	return filepath.Join(home, strings.TrimPrefix(path, "~/")), nil
}
