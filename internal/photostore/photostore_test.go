package photostore_test

import (
	"bytes"
	"context"
	"image"
	"image/jpeg"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/photostore"
)

func jpegBytes(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, nil); err != nil {
		t.Fatalf("encode jpeg: %v", err)
	}
	return buf.Bytes()
}

// storeContract is the behaviour every backend shares: absent, write-once round
// trip, and dimension read-back.
func storeContract(t *testing.T, s photostore.Store) {
	t.Helper()
	ctx := context.Background()
	const key = "streeteasy/ab/abcdef.jpg"

	info, err := s.Stat(ctx, key)
	if err != nil {
		t.Fatalf("stat absent: %v", err)
	}
	if info.Exists {
		t.Fatal("absent key reported as existing")
	}

	data := jpegBytes(t, 800, 600)
	if err := s.Put(ctx, key, data, 800, 600); err != nil {
		t.Fatalf("put: %v", err)
	}

	info, err = s.Stat(ctx, key)
	if err != nil {
		t.Fatalf("stat present: %v", err)
	}
	if !info.Exists || info.Width != 800 || info.Height != 600 {
		t.Fatalf("stat = %+v, want exists 800x600", info)
	}

	got, err := s.Get(ctx, key)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("get returned %d bytes, want the %d stored", len(got), len(data))
	}

	// Delete removes the bytes and is idempotent (a second delete is a no-op).
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if info, err := s.Stat(ctx, key); err != nil || info.Exists {
		t.Fatalf("stat after delete = %+v err %v, want absent", info, err)
	}
	if err := s.Delete(ctx, key); err != nil {
		t.Fatalf("second delete should be a no-op, got %v", err)
	}
}

func TestLocalContract(t *testing.T) {
	s, err := photostore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	storeContract(t, s)
}

func TestLocalWritesWorldReadableFiles(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("no POSIX permission bits")
	}
	dir := t.TempDir()
	s, err := photostore.NewLocal(dir)
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	const key = "streeteasy/ab/public.jpg"
	if err := s.Put(context.Background(), key, []byte("jpeg bytes"), 1, 1); err != nil {
		t.Fatalf("put: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, filepath.FromSlash(key)))
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Mode().Perm()&0o044 != 0o044 {
		t.Fatalf("thumbnail mode = %v, want group/other readable (a temp file's 0600 would hide it from a static server)", info.Mode().Perm())
	}
}

func TestMemoryContract(t *testing.T) {
	storeContract(t, photostore.NewMemory())
}

func TestLocalRejectsRelativeDir(t *testing.T) {
	if _, err := photostore.NewLocal("relative/thumbs"); err == nil {
		t.Fatal("relative directory should be rejected")
	}
}

func TestLocalTreatsCorruptFileAsAbsent(t *testing.T) {
	dir := t.TempDir()
	s, err := photostore.NewLocal(dir)
	if err != nil {
		t.Fatalf("new local: %v", err)
	}
	const key = "streeteasy/cd/corrupt.jpg"
	full := filepath.Join(dir, filepath.FromSlash(key))
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(full, []byte("not a jpeg"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	info, err := s.Stat(context.Background(), key)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	if info.Exists {
		t.Fatal("corrupt file should be reported absent so the next crawl rewrites it")
	}
}

func TestUnsafeKeysRejected(t *testing.T) {
	s := photostore.NewMemory()
	ctx := context.Background()
	for _, key := range []string{"", "/etc/passwd", "streeteasy/../../etc/passwd"} {
		if _, err := s.Stat(ctx, key); err == nil {
			t.Errorf("Stat(%q) should reject unsafe key", key)
		}
		if err := s.Put(ctx, key, []byte("x"), 1, 1); err == nil {
			t.Errorf("Put(%q) should reject unsafe key", key)
		}
		if err := s.Delete(ctx, key); err == nil {
			t.Errorf("Delete(%q) should reject unsafe key", key)
		}
	}
}

// A crash between CreateTemp and Rename leaves a .thumb-* file; startup removes
// stale ones and leaves a fresh one (another process may be mid-Put).
func TestLocalSweepsStaleTempFiles(t *testing.T) {
	dir := t.TempDir()
	shard := filepath.Join(dir, "streeteasy", "ab")
	if err := os.MkdirAll(shard, 0o755); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(shard, ".thumb-stale")
	fresh := filepath.Join(shard, ".thumb-fresh")
	for _, p := range []string{stale, fresh} {
		if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	old := time.Now().Add(-2 * time.Hour)
	if err := os.Chtimes(stale, old, old); err != nil {
		t.Fatal(err)
	}
	if _, err := photostore.NewLocal(dir); err != nil {
		t.Fatalf("new local: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("stale temp file survived startup: %v", err)
	}
	if _, err := os.Stat(fresh); err != nil {
		t.Fatalf("fresh temp file was removed: %v", err)
	}
}
