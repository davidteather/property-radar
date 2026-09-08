package ingest

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/jpeg"
	_ "image/png"
	"io"
	"math"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	xdraw "golang.org/x/image/draw"
	_ "golang.org/x/image/webp"

	"github.com/davidteather/property-radar/internal/photostore"
)

const (
	jpegMIME = "image/jpeg"

	// 640px at q65 is ~50 KB, plenty for contact sheets and vision models; the
	// cache is write-once and a 5 GB volume has to hold a whole city's listings.
	defaultMaxEdge     = 640
	defaultJPEGQuality = 65
	defaultPhotoDelay  = 50 * time.Millisecond
	defaultPhotoAgent  = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/141.0.0.0 Safari/537.36"

	maxPhotoBytes = 24 << 20
	// A decode bomb declares huge dimensions in a tiny file; 25 MP is above any
	// listing photo and bounds a progressive-JPEG decode near 400 MB per worker.
	maxPhotoPixels = 25_000_000
)

// PhotoStore is the byte tier the Thumbnailer writes thumbnails through (disk or
// S3-compatible bucket). Stat is the write-once gate; Delete backs eviction.
type PhotoStore interface {
	Stat(ctx context.Context, key string) (photostore.Info, error)
	Put(ctx context.Context, key string, data []byte, width, height int) error
	Delete(ctx context.Context, key string) error
}

type CachedPhoto struct {
	// RelPath is the store key (also listing_photos.cached_path), relative and
	// forward-slashed for portability across disk and object storage.
	RelPath  string
	MIMEType string
	Width    int
	Height   int
	// Reused is a write-once hit: the bytes were there before this crawl.
	Reused bool
}

type ThumbConfig struct {
	// Delay is per photo worker, not per fetch: the gate spaces fetch starts by
	// Delay/photoConcurrency across the pool.
	Delay     time.Duration
	MaxEdge   int
	Quality   int
	UserAgent string
}

func (c ThumbConfig) withDefaults() ThumbConfig {
	if c.Delay <= 0 {
		c.Delay = defaultPhotoDelay
	}
	if c.MaxEdge <= 0 {
		c.MaxEdge = defaultMaxEdge
	}
	if c.Quality <= 0 {
		c.Quality = defaultJPEGQuality
	}
	if c.UserAgent == "" {
		c.UserAgent = defaultPhotoAgent
	}
	return c
}

// Thumbnailer is the write-once photo cache, safe for concurrent use. Its HTTP
// client is separate from the provider client: the CDN isn't rate limited the
// same way.
type Thumbnailer struct {
	http  *http.Client
	store PhotoStore
	cfg   ThumbConfig

	mu          sync.Mutex
	nextAllowed time.Time
}

var _ PhotoCacher = (*Thumbnailer)(nil)

func NewThumbnailer(httpClient *http.Client, store PhotoStore, cfg ThumbConfig) *Thumbnailer {
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	return &Thumbnailer{http: httpClient, store: store, cfg: cfg.withDefaults()}
}

// Delete removes one cached thumbnail by store key; idempotent, so eviction
// sweeps never fail on an object the write-once cache already dropped.
func (t *Thumbnailer) Delete(ctx context.Context, key string) error {
	return t.store.Delete(ctx, key)
}

// Exists reports whether the bytes behind a key are still in the store.
func (t *Thumbnailer) Exists(ctx context.Context, key string) (bool, error) {
	info, err := t.store.Stat(ctx, key)
	return err == nil && info.Exists, err
}

func (t *Thumbnailer) Cache(ctx context.Context, provider, sourceURL string) (CachedPhoto, error) {
	if strings.TrimSpace(sourceURL) == "" {
		return CachedPhoto{}, errors.New("thumbnail cache: empty source url")
	}
	key, err := thumbKey(provider, sourceURL)
	if err != nil {
		return CachedPhoto{}, err
	}

	if info, err := t.store.Stat(ctx, key); err != nil {
		return CachedPhoto{}, fmt.Errorf("thumbnail cache: stat %s: %w", key, err)
	} else if info.Exists {
		return CachedPhoto{RelPath: key, MIMEType: jpegMIME, Width: info.Width, Height: info.Height, Reused: true}, nil
	}

	raw, err := t.fetch(ctx, sourceURL)
	if err != nil {
		return CachedPhoto{}, err
	}
	if cfg, _, err := image.DecodeConfig(bytes.NewReader(raw)); err != nil {
		return CachedPhoto{}, fmt.Errorf("decode photo %s: %w", sourceURL, err)
	} else if cfg.Width*cfg.Height > maxPhotoPixels {
		return CachedPhoto{}, fmt.Errorf("decode photo %s: %dx%d exceeds the pixel cap", sourceURL, cfg.Width, cfg.Height)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return CachedPhoto{}, fmt.Errorf("decode photo %s: %w", sourceURL, err)
	}
	thumb := shrink(img, t.cfg.MaxEdge)
	b := thumb.Bounds()

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, thumb, &jpeg.Options{Quality: t.cfg.Quality}); err != nil {
		return CachedPhoto{}, fmt.Errorf("encode thumbnail %s: %w", key, err)
	}
	if err := t.store.Put(ctx, key, buf.Bytes(), b.Dx(), b.Dy()); err != nil {
		return CachedPhoto{}, fmt.Errorf("thumbnail cache: put %s: %w", key, err)
	}

	return CachedPhoto{RelPath: key, MIMEType: jpegMIME, Width: b.Dx(), Height: b.Dy()}, nil
}

func (t *Thumbnailer) fetch(ctx context.Context, sourceURL string) ([]byte, error) {
	if err := t.wait(ctx); err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, sourceURL, nil)
	if err != nil {
		return nil, fmt.Errorf("build photo request: %w", err)
	}
	req.Header.Set("accept", "image/webp,image/jpeg,image/png,*/*")
	req.Header.Set("user-agent", t.cfg.UserAgent)

	resp, err := t.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request photo %s: %w", sourceURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request photo %s: unexpected status %d", sourceURL, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxPhotoBytes))
	if err != nil {
		return nil, fmt.Errorf("read photo %s: %w", sourceURL, err)
	}
	return body, nil
}

// wait reserves the next CDN slot rather than blocking others: fetch starts are
// spaced Delay/photoConcurrency apart, so each worker waits ~Delay.
func (t *Thumbnailer) wait(ctx context.Context) error {
	t.mu.Lock()
	start := time.Now()
	if t.nextAllowed.After(start) {
		start = t.nextAllowed
	}
	t.nextAllowed = start.Add(t.cfg.Delay / photoConcurrency)
	t.mu.Unlock()

	d := time.Until(start)
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

// No caller-controlled path segment ever reaches the store key.
func thumbKey(provider, sourceURL string) (string, error) {
	name := strings.ToLower(strings.TrimSpace(provider))
	if name == "" || strings.ContainsFunc(name, unsafeProviderRune) {
		return "", fmt.Errorf("thumbnail cache: unsafe provider name %q", provider)
	}
	sum := sha256.Sum256([]byte(sourceURL))
	key := hex.EncodeToString(sum[:])
	return path.Join(name, key[:2], key+".jpg"), nil
}

func unsafeProviderRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_':
		return false
	default:
		return true
	}
}

func shrink(img image.Image, maxEdge int) image.Image {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || (w <= maxEdge && h <= maxEdge) {
		return img
	}
	scale := float64(maxEdge) / float64(max(w, h))
	dst := image.NewRGBA(image.Rect(0, 0,
		max(1, int(math.Round(float64(w)*scale))),
		max(1, int(math.Round(float64(h)*scale)))))
	xdraw.CatmullRom.Scale(dst, dst.Bounds(), img, b, xdraw.Src, nil)
	return dst
}
