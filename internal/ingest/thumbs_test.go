package ingest_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/ingest/mocks"
	"github.com/davidteather/property-radar/internal/photostore"
)

func gradient(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
		}
	}
	return img
}

func pngBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, gradient(w, h)))
	return buf.Bytes()
}

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, gradient(w, h), nil))
	return buf.Bytes()
}

type photoServer struct {
	*httptest.Server
	requests atomic.Int64
}

// Serves /{name} from bodies; an unknown name is a 404.
func newPhotoServer(t *testing.T, bodies map[string][]byte) *photoServer {
	t.Helper()
	return newGatedPhotoServer(t, bodies, nil, nil)
}

// A non-nil arrived/release pair holds every request until release closes, so a
// test can prove fetches overlap.
func newGatedPhotoServer(t *testing.T, bodies map[string][]byte, arrived chan<- string, release <-chan struct{}) *photoServer {
	t.Helper()
	ps := &photoServer{}
	ps.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ps.requests.Add(1)
		if arrived != nil {
			arrived <- r.URL.Path
			<-release
		}
		body, ok := bodies[filepath.Base(r.URL.Path)]
		if !ok {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("content-type", "image/webp")
		_, _ = w.Write(body)
	}))
	t.Cleanup(ps.Close)
	return ps
}

func newTestThumbnailer(t *testing.T, dir string) *ingest.Thumbnailer {
	t.Helper()
	store, err := photostore.NewLocal(dir)
	if err != nil {
		t.Fatalf("local photo store: %v", err)
	}
	return ingest.NewThumbnailer(&http.Client{Timeout: 5 * time.Second}, store, ingest.ThumbConfig{
		// Non-zero: withDefaults would otherwise apply the 100ms production delay.
		Delay: time.Microsecond,
		// Pinned so the expected dimensions below do not track the production default.
		MaxEdge: 800,
	})
}

func expectedRelPath(sourceURL string) string {
	sum := sha256.Sum256([]byte(sourceURL))
	key := hex.EncodeToString(sum[:])
	return filepath.Join("streeteasy", key[:2], key+".jpg")
}

func TestThumbnailerResizesAndStoresRelativePath(t *testing.T) {
	webp, err := os.ReadFile(filepath.Join("testdata", "tiny.webp"))
	require.NoError(t, err)

	srv := newPhotoServer(t, map[string][]byte{
		"large.png": pngBytes(t, 1200, 900),
		"tall.jpg":  jpegBytes(t, 400, 1600),
		"small.png": pngBytes(t, 300, 200),
		"tiny.webp": webp,
	})
	dir := t.TempDir()
	thumbs := newTestThumbnailer(t, dir)

	tests := []struct {
		name          string
		width, height int
	}{
		{name: "large.png", width: 800, height: 600},
		{name: "tall.jpg", width: 200, height: 800},
		{name: "small.png", width: 300, height: 200},
		{name: "tiny.webp", width: 1, height: 1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sourceURL := srv.URL + "/" + tt.name
			photo, err := thumbs.Cache(context.Background(), "streeteasy", sourceURL)
			require.NoError(t, err)

			assert.Equal(t, expectedRelPath(sourceURL), photo.RelPath)
			assert.Equal(t, "image/jpeg", photo.MIMEType)
			assert.Equal(t, tt.width, photo.Width)
			assert.Equal(t, tt.height, photo.Height)

			written, err := os.Open(filepath.Join(dir, photo.RelPath))
			require.NoError(t, err)
			defer func() { _ = written.Close() }()
			cfg, format, err := image.DecodeConfig(written)
			require.NoError(t, err)
			assert.Equal(t, "jpeg", format)
			assert.Equal(t, tt.width, cfg.Width)
			assert.Equal(t, tt.height, cfg.Height)
		})
	}
}

func TestThumbnailerIsWriteOnce(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"photo.png": pngBytes(t, 1000, 500)})
	dir := t.TempDir()
	thumbs := newTestThumbnailer(t, dir)
	sourceURL := srv.URL + "/photo.png"

	first, err := thumbs.Cache(context.Background(), "streeteasy", sourceURL)
	require.NoError(t, err)
	require.Equal(t, int64(1), srv.requests.Load())

	// A fresh thumbnailer proves the skip comes from the file, not memory.
	second, err := newTestThumbnailer(t, dir).Cache(context.Background(), "streeteasy", sourceURL)
	require.NoError(t, err)

	assert.False(t, first.Reused)
	assert.True(t, second.Reused)
	second.Reused = false
	assert.Equal(t, first, second)
	assert.Equal(t, int64(1), srv.requests.Load(), "cached photo is not refetched")
}

func TestThumbnailerFetchesConcurrently(t *testing.T) {
	const photos = 8
	names := make([]string, photos)
	bodies := make(map[string][]byte, photos)
	for i := range names {
		names[i] = fmt.Sprintf("photo-%d.png", i)
		bodies[names[i]] = pngBytes(t, 1000, 750)
	}
	arrived := make(chan string, photos)
	release := make(chan struct{})
	srv := newGatedPhotoServer(t, bodies, arrived, release)
	thumbs := newTestThumbnailer(t, t.TempDir())

	results := make([]ingest.CachedPhoto, photos)
	errs := make([]error, photos)
	var wg sync.WaitGroup
	cacheAll := func() {
		for i, name := range names {
			wg.Go(func() {
				results[i], errs[i] = thumbs.Cache(context.Background(), "streeteasy", srv.URL+"/"+name)
			})
		}
	}

	cacheAll()
	for range 4 {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("fetches did not overlap, want at least 4 in flight")
		}
	}
	close(release)
	wg.Wait()

	for i, name := range names {
		require.NoError(t, errs[i])
		assert.Equal(t, expectedRelPath(srv.URL+"/"+name), results[i].RelPath)
		assert.Equal(t, 800, results[i].Width)
		assert.Equal(t, 600, results[i].Height)
	}
	require.Equal(t, int64(photos), srv.requests.Load())

	cacheAll()
	wg.Wait()
	for i := range names {
		require.NoError(t, errs[i])
	}
	assert.Equal(t, int64(photos), srv.requests.Load(), "cached photos are not refetched concurrently either")
}

func TestThumbnailerRewritesUnreadableCacheFile(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"photo.png": pngBytes(t, 900, 900)})
	dir := t.TempDir()
	thumbs := newTestThumbnailer(t, dir)
	sourceURL := srv.URL + "/photo.png"

	rel := expectedRelPath(sourceURL)
	require.NoError(t, os.MkdirAll(filepath.Dir(filepath.Join(dir, rel)), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, rel), []byte("truncated"), 0o644))

	photo, err := thumbs.Cache(context.Background(), "streeteasy", sourceURL)
	require.NoError(t, err)
	assert.Equal(t, 800, photo.Width)
	assert.Equal(t, int64(1), srv.requests.Load())
}

func TestThumbnailerErrors(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"broken.png": []byte("not an image at all")})
	dir := t.TempDir()
	thumbs := newTestThumbnailer(t, dir)

	tests := []struct {
		name      string
		provider  string
		sourceURL string
		contains  string
	}{
		{name: "missing photo", provider: "streeteasy", sourceURL: srv.URL + "/absent.png", contains: "unexpected status 404"},
		{name: "undecodable body", provider: "streeteasy", sourceURL: srv.URL + "/broken.png", contains: "decode photo"},
		{name: "empty url", provider: "streeteasy", sourceURL: "  ", contains: "empty source url"},
		{name: "path traversal provider", provider: "../etc", sourceURL: srv.URL + "/broken.png", contains: "unsafe provider name"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := thumbs.Cache(context.Background(), tt.provider, tt.sourceURL)
			require.ErrorContains(t, err, tt.contains)
		})
	}

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.Empty(t, entries, "failed fetches leave nothing behind")
}

func TestThumbnailerWritesThroughAnyStore(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"photo.png": pngBytes(t, 1000, 500)})
	mem := photostore.NewMemory()
	thumbs := ingest.NewThumbnailer(&http.Client{Timeout: 5 * time.Second}, mem, ingest.ThumbConfig{Delay: time.Microsecond, MaxEdge: 800})
	sourceURL := srv.URL + "/photo.png"

	first, err := thumbs.Cache(context.Background(), "streeteasy", sourceURL)
	require.NoError(t, err)
	assert.Equal(t, expectedRelPath(sourceURL), first.RelPath)
	assert.Equal(t, 800, first.Width)
	assert.Equal(t, 400, first.Height)

	// The bytes landed in the store under the content-addressed key.
	stored, err := mem.Get(context.Background(), first.RelPath)
	require.NoError(t, err)
	assert.NotEmpty(t, stored)

	// Write-once: a second Cache serves from the store without refetching.
	second, err := thumbs.Cache(context.Background(), "streeteasy", sourceURL)
	require.NoError(t, err)
	assert.True(t, second.Reused)
	second.Reused = false
	assert.Equal(t, first, second)
	assert.Equal(t, int64(1), srv.requests.Load())
}

func TestThumbnailerReportsStoreErrors(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"photo.png": pngBytes(t, 100, 100)})
	store := mocks.NewMockPhotoStore(t)
	store.EXPECT().Stat(mock.Anything, mock.Anything).Return(photostore.Info{}, nil).Once()
	store.EXPECT().Put(mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("boom")).Once()
	thumbs := ingest.NewThumbnailer(&http.Client{Timeout: 5 * time.Second}, store, ingest.ThumbConfig{Delay: time.Microsecond})
	_, err := thumbs.Cache(context.Background(), "streeteasy", srv.URL+"/photo.png")
	require.ErrorContains(t, err, "boom")
}

func TestThumbnailerHonoursContextCancellation(t *testing.T) {
	srv := newPhotoServer(t, map[string][]byte{"photo.png": pngBytes(t, 100, 100)})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	_, err := newTestThumbnailer(t, t.TempDir()).Cache(ctx, "streeteasy", srv.URL+"/photo.png")
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, int64(0), srv.requests.Load())
}
