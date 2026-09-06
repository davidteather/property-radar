// Package photostore keeps capped thumbnail bytes behind a content-addressed
// key ("{provider}/{kk}/{key}.jpg"), on disk or an S3 bucket.
package photostore

import (
	"context"
	"fmt"
	"strings"
)

// Info is the write-once gate for one key; Width/Height are provenance only and
// may be zero for objects not written through Put.
type Info struct {
	Exists bool
	Width  int
	Height int
}

// Store is content-addressed thumbnail byte storage. Keys are always
// forward-slash relative paths; each implementation maps them onto its medium.
type Store interface {
	Stat(ctx context.Context, key string) (Info, error)
	Get(ctx context.Context, key string) ([]byte, error)
	Put(ctx context.Context, key string, data []byte, width, height int) error
	// Delete removes one object's bytes. Idempotent (deleting a missing key is not
	// an error), so the TTL/delist sweeps never race the write-once cache — this is
	// what makes the store a rolling cache rather than an archive.
	Delete(ctx context.Context, key string) error
}

const (
	BackendLocal = "local"
	BackendS3    = "s3"

	jpegMIME = "image/jpeg"
)

// Config is the composition-root view of storage; cmd/ maps validated config
// onto it so this package never reads the environment.
type Config struct {
	Backend   string
	ThumbsDir string // BackendLocal
	Endpoint  string // BackendS3, host:port with no scheme
	Bucket    string
	AccessKey string
	SecretKey string
	Region    string
	UseSSL    bool
}

// New builds the store the backend names; the s3 client is created and its
// bucket ensured eagerly so a misconfiguration fails at startup, not mid-crawl.
func New(ctx context.Context, cfg Config) (Store, error) {
	switch cfg.Backend {
	case "", BackendLocal:
		return NewLocal(cfg.ThumbsDir)
	case BackendS3:
		return NewS3(ctx, cfg)
	default:
		return nil, fmt.Errorf("photostore: unknown backend %q", cfg.Backend)
	}
}

// A key never carries an absolute or parent segment; callers build it from a
// content hash, but the guard keeps a poisoned cached_path off the filesystem.
func cleanKey(key string) (string, error) {
	if key == "" || strings.HasPrefix(key, "/") || strings.Contains(key, "..") {
		return "", fmt.Errorf("photostore: unsafe key %q", key)
	}
	return key, nil
}
