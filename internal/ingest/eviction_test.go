package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/ingest/mocks"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// A run that delists listings deletes the freed thumbnail bytes from the store.
func TestRunEvictsDelistedPhotoBytes(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")})
	// Volume 2 keeps a single seen listing above the 30% suspect floor.
	h.expectBaseline(ingest.Baseline{Volume: 2, Runs: 5})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Once()

	evicted := []string{"streeteasy/aa/dead.jpg", "streeteasy/bb/dead2.jpg"}
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{Advanced: 2, Delisted: 1, EvictedKeys: evicted}, nil).Once()
	for _, key := range evicted {
		h.photos.EXPECT().Delete(mock.Anything, key).Return(nil).Once()
	}
	// Only the keys whose bytes were deleted are forgotten in the DB afterward.
	h.store.EXPECT().ForgetPhotoKeys(mock.Anything, evicted).Return(0, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Equal(t, evicted, res.Missing.EvictedKeys)
}

// A key whose bytes could not be deleted keeps its DB row: forgetting it would
// orphan bytes the next sweep can no longer find.
func TestRunForgetsOnlyDeletedKeys(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")})
	h.expectBaseline(ingest.Baseline{Volume: 2, Runs: 5})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Once()
	evicted := []string{"streeteasy/aa/dead.jpg", "streeteasy/bb/stuck.jpg"}
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{Advanced: 2, Delisted: 1, EvictedKeys: evicted}, nil).Once()
	h.photos.EXPECT().Delete(mock.Anything, evicted[0]).Return(nil).Once()
	h.photos.EXPECT().Delete(mock.Anything, evicted[1]).Return(errors.New("s3 down")).Once()
	h.store.EXPECT().ForgetPhotoKeys(mock.Anything, evicted[:1]).Return(0, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
}

// A re-observation that replaces a listing's photos hands back the displaced
// keys, and Run deletes those bytes too.
func TestRunEvictsReplacedPhotoBytes(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")})
	h.expectBaseline(ingest.Baseline{Volume: 2, Runs: 5})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, EvictedKeys: []string{"streeteasy/aa/old.jpg"}}, nil).Once()
	h.photos.EXPECT().Delete(mock.Anything, "streeteasy/aa/old.jpg").Return(nil).Once()
	h.store.EXPECT().ForgetPhotoKeys(mock.Anything, []string{"streeteasy/aa/old.jpg"}).Return(1, nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
}

// SweepExpiredPhotos pages through the store and deletes every freed key's bytes.
func TestSweepExpiredPhotos(t *testing.T) {
	st := mocks.NewMockStore(t)
	photos := mocks.NewMockPhotoCacher(t)
	src := mocks.NewMockSource(t)
	svc := ingest.NewService(src, st, photos, ingest.Options{Logger: discardLogger()})

	// A full page (freed == batch) forces a second call; a short page stops it.
	page1 := []string{"streeteasy/aa/1.jpg", "streeteasy/bb/2.jpg"}
	st.EXPECT().FreeExpiredCachedPhotos(mock.Anything, mock.Anything, ingest.PhotoSweepBatch).
		Return(page1, ingest.PhotoSweepBatch, nil).Once()
	page2 := []string{"streeteasy/cc/3.jpg"}
	st.EXPECT().FreeExpiredCachedPhotos(mock.Anything, mock.Anything, ingest.PhotoSweepBatch).
		Return(page2, 1, nil).Once()
	for _, key := range append(append([]string{}, page1...), page2...) {
		photos.EXPECT().Delete(mock.Anything, key).Return(nil).Once()
	}
	st.EXPECT().ForgetPhotoKeys(mock.Anything, page1).Return(0, nil).Once()
	st.EXPECT().ForgetPhotoKeys(mock.Anything, page2).Return(0, nil).Once()

	total, err := svc.SweepExpiredPhotos(context.Background(), 30*24*time.Hour)
	require.NoError(t, err)
	assert.Equal(t, ingest.PhotoSweepBatch+1, total)
}

// A non-positive TTL disables eviction entirely (opt-in archival): the store is
// never touched.
func TestSweepExpiredPhotosDisabled(t *testing.T) {
	st := mocks.NewMockStore(t)
	photos := mocks.NewMockPhotoCacher(t)
	src := mocks.NewMockSource(t)
	svc := ingest.NewService(src, st, photos, ingest.Options{Logger: discardLogger()})

	total, err := svc.SweepExpiredPhotos(context.Background(), 0)
	require.NoError(t, err)
	assert.Zero(t, total)
	// Strict mocks assert FreeExpiredCachedPhotos / Delete were never called.
}

// A large eviction batch gets a budget sized to it (not the 15s bookkeeping
// window) and deletes several keys at once, so an S3 tail is never orphaned.
func TestEvictionBudgetScalesWithTheBatch(t *testing.T) {
	st := mocks.NewMockStore(t)
	photos := mocks.NewMockPhotoCacher(t)
	src := mocks.NewMockSource(t)
	svc := ingest.NewService(src, st, photos, ingest.Options{Logger: discardLogger()})

	keys := make([]string, ingest.PhotoSweepBatch)
	for i := range keys {
		keys[i] = fmt.Sprintf("streeteasy/aa/%d.jpg", i)
	}
	st.EXPECT().FreeExpiredCachedPhotos(mock.Anything, mock.Anything, ingest.PhotoSweepBatch).
		Return(keys, ingest.PhotoSweepBatch, nil).Once()
	st.EXPECT().FreeExpiredCachedPhotos(mock.Anything, mock.Anything, ingest.PhotoSweepBatch).
		Return(nil, 0, nil).Once()
	st.EXPECT().ForgetPhotoKeys(mock.Anything, keys).Return(0, nil).Once()

	var mu sync.Mutex
	var inFlight, peak int
	var minBudget time.Duration
	start := time.Now()
	photos.EXPECT().Delete(mock.Anything, mock.Anything).RunAndReturn(func(ctx context.Context, _ string) error {
		dl, ok := ctx.Deadline()
		require.True(t, ok, "deletes must run under a deadline")
		mu.Lock()
		inFlight++
		peak = max(peak, inFlight)
		if left := time.Until(dl); minBudget == 0 || left < minBudget {
			minBudget = left
		}
		mu.Unlock()
		time.Sleep(2 * time.Millisecond)
		mu.Lock()
		inFlight--
		mu.Unlock()
		return nil
	}).Times(ingest.PhotoSweepBatch)

	_, err := svc.SweepExpiredPhotos(context.Background(), 30*24*time.Hour)
	require.NoError(t, err)
	assert.Greater(t, peak, 1, "deletes should overlap")
	// 500 keys × 250ms = 125s; the old fixed window was 15s.
	assert.Greater(t, minBudget+time.Since(start), 60*time.Second)
}

// A write-once hit followed by a sweep in another process deletes the bytes
// before the row is written: the row must be forgotten, not left dangling.
func TestReusedThumbnailEvictedMidCrawlIsForgotten(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1", "photo-a")})
	h.expectBaseline(ingest.Baseline{Volume: 1, Runs: 5})
	h.photos.EXPECT().Cache(mock.Anything, "streeteasy", "photo-a").
		Return(ingest.CachedPhoto{RelPath: "streeteasy/ab/photo-a.jpg", MIMEType: "image/jpeg", Width: 800, Height: 600, Reused: true}, nil).Once()
	h.photos.EXPECT().Exists(mock.Anything, "streeteasy/ab/photo-a.jpg").Return(false, nil).Once()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 0, "photo-a", "streeteasy/ab/photo-a.jpg", "image/jpeg", 800, 600).Return(nil).Once()
	h.store.EXPECT().ForgetPhotoKeys(mock.Anything, []string{"streeteasy/ab/photo-a.jpg"}).Return(1, nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(ingest.MissingResult{}, nil).Maybe()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Equal(t, 1, h.stats.PhotoFailures, "an evicted reuse counts as a photo failure")
}
