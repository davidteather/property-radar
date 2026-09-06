package photostore

import (
	"context"
	"fmt"
	"image"
	_ "image/jpeg" // Stat decodes the header; the binary may register nothing else.
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Local struct {
	dir string
}

var _ Store = (*Local)(nil)

func NewLocal(dir string) (*Local, error) {
	if !filepath.IsAbs(dir) {
		return nil, fmt.Errorf("photostore: local directory %q is not absolute", dir)
	}
	l := &Local{dir: filepath.Clean(dir)}
	l.sweepTemp()
	return l, nil
}

// tempStale is how old a .thumb-* file must be before startup removes it: a
// crash between CreateTemp and Rename leaves one; a live Put holds one briefly.
const tempStale = time.Hour

func (l *Local) sweepTemp() {
	cutoff := time.Now().Add(-tempStale)
	_ = filepath.WalkDir(l.dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.HasPrefix(d.Name(), ".thumb-") {
			return nil
		}
		if info, err := d.Info(); err == nil && info.ModTime().Before(cutoff) {
			_ = os.Remove(path)
		}
		return nil
	})
}

func (l *Local) path(key string) (string, error) {
	if _, err := cleanKey(key); err != nil {
		return "", err
	}
	full := filepath.Join(l.dir, filepath.FromSlash(key))
	if !strings.HasPrefix(full, l.dir+string(os.PathSeparator)) {
		return "", fmt.Errorf("photostore: key %q escapes %s", key, l.dir)
	}
	return full, nil
}

// An unreadable or undecodable file is reported absent so the next crawl
// rewrites it (write-once treats a corrupt cache entry as missing).
func (l *Local) Stat(_ context.Context, key string) (Info, error) {
	full, err := l.path(key)
	if err != nil {
		return Info{}, err
	}
	f, err := os.Open(full)
	if err != nil {
		return Info{}, nil
	}
	defer func() { _ = f.Close() }()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return Info{}, nil
	}
	return Info{Exists: true, Width: cfg.Width, Height: cfg.Height}, nil
}

func (l *Local) Get(_ context.Context, key string) ([]byte, error) {
	full, err := l.path(key)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(full)
	if err != nil {
		return nil, fmt.Errorf("photostore: read %s: %w", key, err)
	}
	return data, nil
}

// Delete removes the file, treating an already-absent key as success.
func (l *Local) Delete(_ context.Context, key string) error {
	full, err := l.path(key)
	if err != nil {
		return err
	}
	if err := os.Remove(full); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("photostore: delete %s: %w", key, err)
	}
	return nil
}

func (l *Local) Put(_ context.Context, key string, data []byte, _, _ int) error {
	full, err := l.path(key)
	if err != nil {
		return err
	}
	dir := filepath.Dir(full)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("photostore: create %s: %w", dir, err)
	}
	tmp, err := os.CreateTemp(dir, ".thumb-*")
	if err != nil {
		return fmt.Errorf("photostore: temp file in %s: %w", dir, err)
	}
	defer func() { _ = os.Remove(tmp.Name()) }()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("photostore: write %s: %w", key, err)
	}
	// CreateTemp makes 0600 files; thumbnails are public assets another
	// process (a static file server) may serve, so open them up like a plain write.
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("photostore: chmod %s: %w", key, err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("photostore: close %s: %w", key, err)
	}
	if err := os.Rename(tmp.Name(), full); err != nil {
		return fmt.Errorf("photostore: rename %s: %w", key, err)
	}
	return nil
}
