package store_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/ingest"
)

// A delisted listing's cached thumbnails are freed in the same transaction and
// their store keys are returned so the crawler can delete the bytes.
func TestMarkMissingSourcesEvictsDelistedPhotos(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	seed := startRun(t, s)
	gone := apply(t, s, seed, newSource("3001"), baseTime)
	if err := s.SetPhotoCache(ctx, gone.PropertyID, 0, "https://photos.example/3001/1.jpg", "streeteasy/ab/gone.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache: %v", err)
	}
	// Position 1 shares its key with a listing that stays active.
	if err := s.SetPhotoCache(ctx, gone.PropertyID, 1, "https://photos.example/3001/2.jpg", "streeteasy/cd/shared.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache 1: %v", err)
	}
	stays := apply(t, s, seed, newSource("3002"), baseTime)
	if err := s.SetPhotoCache(ctx, stays.PropertyID, 0, "https://photos.example/3002/1.jpg", "streeteasy/cd/shared.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("set photo cache stays: %v", err)
	}
	finishRun(t, s, seed, ingest.RunStats{Complete: true, ListingsSeen: 2})

	// Two runs that see only 3002 cross the delist threshold for 3001.
	run2 := completeRun(t, s, 1)
	if _, err := s.MarkMissingSources(ctx, testProvider, []string{"3002"}, run2); err != nil {
		t.Fatalf("mark missing 1: %v", err)
	}
	run3 := completeRun(t, s, 1)
	res, err := s.MarkMissingSources(ctx, testProvider, []string{"3002"}, run3)
	if err != nil {
		t.Fatalf("mark missing 2: %v", err)
	}
	if res.Delisted != 1 {
		t.Fatalf("delisted = %d, want 1", res.Delisted)
	}
	if want := []string{"streeteasy/ab/gone.jpg"}; !slices.Equal(res.EvictedKeys, want) {
		t.Fatalf("evicted keys = %v, want %v", res.EvictedKeys, want)
	}

	// The cache is forgotten in the DB so /img can no longer resolve it.
	_, _, photos, _ := mustGet(t, s, gone.PropertyID)
	if len(photos) == 0 || photos[0].CachedPath != "" {
		t.Fatalf("delisted listing kept its cached photo: %+v", photos)
	}
}

// FreeExpiredCachedPhotos is the TTL/LRU sweep: it frees cached photos older
// than the cutoff (oldest first, capped by limit) and returns their store keys.
// After the bytes are gone, a row a concurrent drain re-pointed at the key is
// forgotten too, so /img never resolves to missing bytes.
func TestForgetPhotoKeysClearsRepointedRows(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	run := startRun(t, s)
	a := apply(t, s, run, newSource("4101"), baseTime)
	if err := s.SetPhotoCache(ctx, a.PropertyID, 0, "https://photos.example/4101/1.jpg", "streeteasy/aa/a.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("cache a: %v", err)
	}
	if err := s.SetPhotoCache(ctx, a.PropertyID, 1, "https://photos.example/4101/2.jpg", "streeteasy/bb/keep.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("cache keep: %v", err)
	}
	if n, err := s.ForgetPhotoKeys(ctx, nil); err != nil || n != 0 {
		t.Fatalf("forget nothing = %d, %v", n, err)
	}
	n, err := s.ForgetPhotoKeys(ctx, []string{"streeteasy/aa/a.jpg", "streeteasy/zz/unknown.jpg"})
	if err != nil {
		t.Fatalf("forget: %v", err)
	}
	if n != 1 {
		t.Fatalf("forgot %d rows, want 1", n)
	}
	_, _, photos, _ := mustGet(t, s, a.PropertyID)
	if len(photos) != 2 || photos[0].CachedPath != "" || photos[1].CachedPath != "streeteasy/bb/keep.jpg" {
		t.Fatalf("photos after forget = %+v", photos)
	}
}

func TestFreeExpiredCachedPhotos(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	run := startRun(t, s)
	a := apply(t, s, run, newSource("4001"), baseTime)
	b := apply(t, s, run, newSource("4002"), baseTime)
	if err := s.SetPhotoCache(ctx, a.PropertyID, 0, "https://photos.example/4001/1.jpg", "streeteasy/aa/a.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("cache a: %v", err)
	}
	if err := s.SetPhotoCache(ctx, b.PropertyID, 0, "https://photos.example/4002/1.jpg", "streeteasy/bb/b.jpg", "image/jpeg", 800, 600); err != nil {
		t.Fatalf("cache b: %v", err)
	}

	// A cutoff before the just-now refresh frees nothing (the touch keeps them fresh).
	if keys, freed, err := s.FreeExpiredCachedPhotos(ctx, time.Now().Add(-time.Hour), 100); err != nil {
		t.Fatalf("sweep fresh: %v", err)
	} else if freed != 0 || len(keys) != 0 {
		t.Fatalf("fresh photos freed = %d keys %v, want none", freed, keys)
	}

	// A future cutoff makes both stale; limit caps one page.
	keys1, freed1, err := s.FreeExpiredCachedPhotos(ctx, time.Now().Add(time.Hour), 1)
	if err != nil {
		t.Fatalf("sweep page 1: %v", err)
	}
	if freed1 != 1 || len(keys1) != 1 {
		t.Fatalf("limited sweep freed = %d keys %v, want 1", freed1, keys1)
	}

	// The rest drain on the next page.
	keys2, freed2, err := s.FreeExpiredCachedPhotos(ctx, time.Now().Add(time.Hour), 100)
	if err != nil {
		t.Fatalf("sweep page 2: %v", err)
	}
	if freed2 != 1 || len(keys2) != 1 {
		t.Fatalf("second sweep freed = %d keys %v, want 1", freed2, keys2)
	}

	got := slices.Concat(keys1, keys2)
	slices.Sort(got)
	if want := []string{"streeteasy/aa/a.jpg", "streeteasy/bb/b.jpg"}; !slices.Equal(got, want) {
		t.Fatalf("freed keys = %v, want %v", got, want)
	}

	// Both are now uncached, and a further sweep finds nothing.
	if _, _, photos, _ := mustGet(t, s, a.PropertyID); photos[0].CachedPath != "" {
		t.Fatalf("photo a still cached: %+v", photos[0])
	}
	if _, _, photos, _ := mustGet(t, s, b.PropertyID); photos[0].CachedPath != "" {
		t.Fatalf("photo b still cached: %+v", photos[0])
	}
	if _, freed3, err := s.FreeExpiredCachedPhotos(ctx, time.Now().Add(time.Hour), 100); err != nil {
		t.Fatalf("sweep drained: %v", err)
	} else if freed3 != 0 {
		t.Fatalf("third sweep freed = %d, want 0", freed3)
	}
}

// A zero or negative limit is a no-op.
func TestFreeExpiredCachedPhotosZeroLimit(t *testing.T) {
	s := newStore(t)
	keys, freed, err := s.FreeExpiredCachedPhotos(context.Background(), time.Now(), 0)
	if err != nil {
		t.Fatalf("zero-limit sweep: %v", err)
	}
	if freed != 0 || len(keys) != 0 {
		t.Fatalf("zero-limit sweep = %d keys %v, want none", freed, keys)
	}
}
