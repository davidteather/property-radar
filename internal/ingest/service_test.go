package ingest_test

import (
	"context"
	"errors"
	"fmt"
	"io"
	"iter"
	"log/slog"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/ingest/mocks"
)

const testRunID = domain.IngestRunID(7)

func testQuery() ingest.SearchQuery {
	return ingest.SearchQuery{ListingType: domain.ListingSale, Areas: []string{"319", "305"}, MinBeds: 2}
}

type yielded struct {
	sp  ingest.SourceProperty
	err error
}

func listing(providerID string, photos ...string) ingest.SourceProperty {
	return ingest.SourceProperty{
		Provider:   "streeteasy",
		ProviderID: providerID,
		URL:        "https://streeteasy.com/sale/" + providerID,
		Property:   domain.Property{ListingType: domain.ListingSale, Status: domain.StatusActive},
		PhotoURLs:  photos,
	}
}

func sourceSeq(items ...yielded) iter.Seq2[ingest.SourceProperty, error] {
	return func(yield func(ingest.SourceProperty, error) bool) {
		for _, item := range items {
			if !yield(item.sp, item.err) {
				return
			}
		}
	}
}

// cacheURLs is the sourceURL argument of every Cache call the mock recorded.
func cacheURLs(m *mocks.MockPhotoCacher) []string {
	urls := []string{}
	for _, c := range m.Calls {
		if c.Method == "Cache" {
			urls = append(urls, c.Arguments.String(2))
		}
	}
	return urls
}

type harness struct {
	source *mocks.MockSource
	store  *mocks.MockStore
	photos *mocks.MockPhotoCacher
	svc    *ingest.Service
	stats  ingest.RunStats
	// Error state of the context FinishRun was called with.
	finishCtxErr error
}

func newHarness(t *testing.T, opts ingest.Options, items ...yielded) *harness {
	t.Helper()

	h := &harness{
		source: mocks.NewMockSource(t),
		store:  mocks.NewMockStore(t),
		photos: mocks.NewMockPhotoCacher(t),
	}
	if opts.Logger == nil {
		opts.Logger = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(items...)).Once()
	// The incremental default: no stored state, so every listing enriches;
	// enrichment is the identity here so listings pass through unchanged.
	h.store.EXPECT().EnrichmentState(mock.Anything, "streeteasy", mock.Anything).
		Return(ingest.EnrichState{}, nil).Maybe()
	h.source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Maybe()
	h.store.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).
		Return(domain.IngestRun{ID: testRunID, Provider: "streeteasy"}, nil).Once()
	h.store.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).
		RunAndReturn(func(ctx context.Context, _ domain.IngestRunID, stats ingest.RunStats) error {
			h.stats, h.finishCtxErr = stats, ctx.Err()
			return nil
		}).Once()

	h.svc = ingest.NewService(h.source, h.store, h.photos, opts)
	return h
}

func (h *harness) expectBaseline(b ingest.Baseline) {
	h.store.EXPECT().BaselineVolume(mock.Anything, "streeteasy", mock.Anything).Return(b, nil).Once()
}

// expectPhotoCache installs the default success behaviour: a deterministic
// cached photo for any URL. Register specific per-URL expectations first, as
// testify matches expectations in registration order.
func (h *harness) expectPhotoCache() {
	h.photos.EXPECT().Cache(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, provider, sourceURL string) (ingest.CachedPhoto, error) {
			return ingest.CachedPhoto{
				RelPath:  provider + "/ab/" + sourceURL + ".jpg",
				MIMEType: "image/jpeg",
				Width:    800,
				Height:   600,
			}, nil
		})
}

func TestRunAppliesEveryListingAndAdvancesMissing(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1", "photo-a", "photo-b")}, yielded{sp: listing("2")})
	h.expectBaseline(ingest.Baseline{Volume: 4, Runs: 5})
	h.expectPhotoCache()

	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1", "photo-a", "photo-b"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("2"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 12}, nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 0, mock.Anything, "streeteasy/ab/photo-a.jpg", "image/jpeg", 800, 600).Return(nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 1, mock.Anything, "streeteasy/ab/photo-b.jpg", "image/jpeg", 800, 600).Return(nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).
		Return(ingest.MissingResult{Advanced: 3, Delisted: 1}, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.Equal(t, ingest.RunStats{Complete: true, ListingsSeen: 2, Created: 1, Updated: 1, EnrichAttempts: 2}, h.stats)
	assert.Equal(t, ingest.MissingResult{Advanced: 3, Delisted: 1}, res.Missing)
	assert.True(t, res.Run.Complete)
	assert.False(t, res.Run.Suspect)
	assert.Equal(t, 2, res.Run.ListingsSeen)
	assert.NotNil(t, res.Run.FinishedAt)
}

func TestRunProviderErrorMarksIncompleteAndSkipsMissing(t *testing.T) {
	providerErr := fmt.Errorf("streeteasy search page 2: %w: boom", ingest.ErrSearchAborted)
	h := newHarness(t, ingest.Options{},
		yielded{sp: listing("1")},
		yielded{err: providerErr},
	)
	h.expectBaseline(ingest.Baseline{Volume: 4, Runs: 5})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.ErrorIs(t, err, providerErr)

	assert.False(t, h.stats.Complete)
	assert.Equal(t, 1, h.stats.ListingsSeen)
	require.Len(t, h.stats.ItemErrors, 1)
	assert.Empty(t, h.stats.ItemErrors[0].ProviderID)
	assert.Equal(t, ingest.MissingResult{}, res.Missing)
	h.store.AssertNotCalled(t, "MarkMissingSources", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// The adapter also reports an unusable listing as a zero SourceProperty; only
// ErrSearchAborted is provider-level, even when the bad edge sorts last.
func TestRunUnusableListingDoesNotAbortTheCrawl(t *testing.T) {
	h := newHarness(t, ingest.Options{},
		yielded{sp: listing("1")},
		yielded{sp: listing("2")},
		yielded{err: errors.New("streeteasy decode listing: missing id")},
	)
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Twice()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.True(t, h.stats.Complete)
	assert.Equal(t, 2, h.stats.ListingsSeen)
	require.Len(t, h.stats.ItemErrors, 1)
	assert.Empty(t, h.stats.ItemErrors[0].ProviderID)
	assert.Contains(t, h.stats.ItemErrors[0].Message, "missing id")
}

func TestRunUndecodableListingIsSeenButNotApplied(t *testing.T) {
	h := newHarness(t, ingest.Options{},
		yielded{sp: listing("1")},
		yielded{sp: ingest.SourceProperty{ProviderID: "2"}, err: fmt.Errorf("streeteasy decode listing 2: %w: price", ingest.ErrUnusableListing)},
		yielded{sp: listing("3")},
	)
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Twice()
	// Listing 2 is still on the market, so it is protected from delisting.
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2", "3"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.True(t, h.stats.Complete)
	assert.Equal(t, 3, h.stats.ListingsSeen)
	assert.Equal(t, 2, h.stats.Updated)
	require.Len(t, h.stats.ItemErrors, 1)
	assert.Equal(t, "2", h.stats.ItemErrors[0].ProviderID)
	h.source.AssertNotCalled(t, "EnrichDetail", mock.Anything, ingest.SourceProperty{ProviderID: "2"})
}

func TestRunItemErrorStillAppliesTheListing(t *testing.T) {
	itemErr := errors.New("streeteasy detail 2: 403")
	h := newHarness(t, ingest.Options{},
		yielded{sp: listing("1")},
		yielded{sp: listing("2"), err: itemErr},
	)
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Twice()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.True(t, h.stats.Complete)
	assert.Equal(t, 2, h.stats.ListingsSeen)
	assert.Equal(t, 2, h.stats.Updated)
	require.Len(t, h.stats.ItemErrors, 1)
	assert.Equal(t, "2", h.stats.ItemErrors[0].ProviderID)
	assert.Contains(t, h.stats.ItemErrors[0].Message, "403")
}

// A failed detail fetch applies the search row, whose status is always "active":
// it must not overturn the in_contract/sold the detail page last reported.
func TestRunEnrichFailureDoesNotRevertStatus(t *testing.T) {
	rented := listing("2")
	rented.Property.Status = domain.StatusRented
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")}, yielded{sp: rented})
	h.expectBaseline(ingest.Baseline{})
	h.source.ExpectedCalls = nil
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(yielded{sp: listing("1")}, yielded{sp: rented})).Once()
	h.source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		Return(ingest.SourceProperty{}, errors.New("streeteasy detail: 403")).Twice()

	blank := listing("1")
	blank.Property.Status = ""
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, blank, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Once()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, rented, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 12}, nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Len(t, h.stats.ItemErrors, 2)
	assert.Equal(t, 2, res.Stats.EnrichAttempts)
	assert.Equal(t, 2, res.Stats.EnrichFailures)
}

// Once detail fetches fail 20 times in a row the run stops asking: a blocked
// provider only gets more blocked. The rest store search-only and retry later.
func TestRunStopsEnrichingAfterFailureStreak(t *testing.T) {
	const n = 30
	items := make([]yielded, 0, n)
	ids := make([]string, 0, n)
	for i := range n {
		id := strconv.Itoa(i + 1)
		items = append(items, yielded{sp: listing(id)})
		ids = append(ids, id)
	}
	h := newHarness(t, ingest.Options{}, items...)
	h.expectBaseline(ingest.Baseline{})
	h.source.ExpectedCalls = nil
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(items...)).Once()
	h.source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		Return(ingest.SourceProperty{}, errors.New("streeteasy detail: 403")).Times(20)

	// Every row lands, the skipped ones with status cleared like a failed one.
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.MatchedBy(func(sp ingest.SourceProperty) bool {
		return sp.Property.Status == ""
	}), mock.Anything).Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(n)
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", ids, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Equal(t, 20, res.Stats.EnrichAttempts)
	assert.Equal(t, 20, res.Stats.EnrichFailures)
	assert.Len(t, h.stats.ItemErrors, 20, "skipped listings are not errors")
	assert.ErrorContains(t, ingest.RunOutcome(res, nil), "all 20 detail-page fetches failed")
}

// One success resets the streak: intermittent failures never trip the breaker.
func TestRunFailureStreakResetsOnSuccess(t *testing.T) {
	const n = 40
	items := make([]yielded, 0, n)
	for i := range n {
		items = append(items, yielded{sp: listing(strconv.Itoa(i + 1))})
	}
	h := newHarness(t, ingest.Options{}, items...)
	h.expectBaseline(ingest.Baseline{})
	h.source.ExpectedCalls = nil
	h.source.EXPECT().Name().Return("streeteasy").Maybe()
	h.source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(items...)).Once()
	calls := 0
	h.source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) {
			calls++
			if calls%20 == 0 {
				return sp, nil
			}
			return ingest.SourceProperty{}, errors.New("streeteasy detail: 403")
		}).Times(n)
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(n)
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", mock.Anything, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	res, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Equal(t, n, res.Stats.EnrichAttempts)
	assert.Equal(t, n-2, res.Stats.EnrichFailures)
}

// An empty page is never evidence of absence: with no baseline to flag it
// suspect, advancing would delist everything the scope had seen.
func TestRunEmptyPageNeverAdvancesMissing(t *testing.T) {
	h := newHarness(t, ingest.Options{})
	h.expectBaseline(ingest.Baseline{})

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.True(t, h.stats.Complete)
	assert.False(t, h.stats.Suspect)
	h.store.AssertNotCalled(t, "MarkMissingSources", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRunApplyErrorIsAnItemErrorAndDoesNotAbort(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")}, yielded{sp: listing("2")})
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1"), mock.Anything).
		Return(ingest.ApplyResult{}, errors.New("insert listing: constraint violation")).Once()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("2"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 12, Created: true}, nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.True(t, h.stats.Complete)
	assert.Equal(t, 2, h.stats.ListingsSeen)
	assert.Equal(t, 1, h.stats.Created)
	assert.Zero(t, h.stats.Updated)
	require.Len(t, h.stats.ItemErrors, 1)
	assert.Equal(t, "1", h.stats.ItemErrors[0].ProviderID)
}

func TestRunSuspectVolume(t *testing.T) {
	tests := []struct {
		name     string
		baseline ingest.Baseline
		listings int
		suspect  bool
	}{
		{name: "no baseline runs", baseline: ingest.Baseline{}, listings: 1},
		{name: "two baseline runs never suspect", baseline: ingest.Baseline{Volume: 400, Runs: 2}, listings: 1},
		{name: "three runs and under 30 percent", baseline: ingest.Baseline{Volume: 400, Runs: 3}, listings: 1, suspect: true},
		{name: "three runs and at 30 percent", baseline: ingest.Baseline{Volume: 4, Runs: 3}, listings: 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			items := make([]yielded, 0, tt.listings)
			for i := range tt.listings {
				items = append(items, yielded{sp: listing(fmt.Sprint(i))})
			}
			h := newHarness(t, ingest.Options{}, items...)
			h.expectBaseline(tt.baseline)
			h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
				Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(tt.listings)
			if !tt.suspect {
				h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", mock.Anything, testRunID).
					Return(ingest.MissingResult{}, nil).Once()
			}

			res, err := h.svc.Run(context.Background(), testQuery())
			require.NoError(t, err)

			assert.Equal(t, tt.suspect, h.stats.Suspect)
			assert.Equal(t, tt.suspect, res.Run.Suspect)
			if tt.suspect {
				h.store.AssertNotCalled(t, "MarkMissingSources", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
			}
		})
	}
}

func TestRunPhotoFailuresAreSoftAndCapped(t *testing.T) {
	h := newHarness(t, ingest.Options{PhotoCap: 3},
		yielded{sp: listing("1", "good", "bad-fetch", "bad-store", "over-cap")})
	h.photos.EXPECT().Cache(mock.Anything, mock.Anything, "bad-fetch").
		Return(ingest.CachedPhoto{}, errors.New("decode photo: unsupported format")).Once()
	h.expectPhotoCache()
	h.expectBaseline(ingest.Baseline{})

	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 0, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 2, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(errors.New("no photo at position 2")).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)

	assert.ElementsMatch(t, []string{"good", "bad-fetch", "bad-store"}, cacheURLs(h.photos))
	assert.Equal(t, 2, h.stats.PhotoFailures)
	assert.True(t, h.stats.Complete)
	assert.Equal(t, 1, h.stats.Created)
	assert.Empty(t, h.stats.ItemErrors)
}

// A decoder panic on one photo is that photo's failure, not the crawler's.
func TestRunSurvivesAPhotoDecoderPanic(t *testing.T) {
	h := newHarness(t, ingest.Options{PhotoCap: 3}, yielded{sp: listing("1", "good", "bomb")})
	h.photos.EXPECT().Cache(mock.Anything, mock.Anything, "bomb").
		Run(func(context.Context, string, string) { panic("index out of range in decoder") }).
		Return(ingest.CachedPhoto{}, nil).Once()
	h.expectPhotoCache()
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 0, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(nil).Once()
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.Equal(t, 1, h.stats.PhotoFailures)
	assert.True(t, h.stats.Complete)
}

func TestRunFetchesPhotosConcurrentlyAndAppliesInOrder(t *testing.T) {
	urls := make([]string, 8)
	for i := range urls {
		urls[i] = fmt.Sprintf("photo-%d", i)
	}

	h := newHarness(t, ingest.Options{PhotoCap: len(urls)}, yielded{sp: listing("1", urls...)})
	// The two channels are closed over by a single RunAndReturn: every fetch
	// announces itself on arrived and blocks until release closes, proving the
	// fetches overlap without a hand-written stub type.
	arrived := make(chan string, len(urls))
	release := make(chan struct{})
	h.photos.EXPECT().Cache(mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, provider, sourceURL string) (ingest.CachedPhoto, error) {
			arrived <- sourceURL
			<-release
			return ingest.CachedPhoto{
				RelPath:  provider + "/ab/" + sourceURL + ".jpg",
				MIMEType: "image/jpeg",
				Width:    800,
				Height:   600,
			}, nil
		}).Times(len(urls))
	h.expectBaseline(ingest.Baseline{})

	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	var applied []int
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.PropertyID, position int, _, _, _ string, _, _ int) error {
			applied = append(applied, position)
			return nil
		}).Times(len(urls))
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	done := make(chan error, 1)
	go func() {
		_, err := h.svc.Run(context.Background(), testQuery())
		done <- err
	}()

	// Every worker must be in flight before any of them is released.
	for range 4 {
		select {
		case <-arrived:
		case <-time.After(10 * time.Second):
			t.Fatal("photo fetches did not overlap, want 4 concurrent")
		}
	}
	close(release)
	require.NoError(t, <-done)

	assert.ElementsMatch(t, urls, cacheURLs(h.photos))
	assert.Equal(t, []int{0, 1, 2, 3, 4, 5, 6, 7}, applied,
		"positions are applied in order however the fetches interleaved")
	assert.Equal(t, 0, h.stats.PhotoFailures)
}

func TestRunCancelledContextMarksIncomplete(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")}, yielded{sp: listing("2")})
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Maybe()

	cancel()
	_, err := h.svc.Run(ctx, testQuery())
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, h.stats.Complete)
	assert.NoError(t, h.finishCtxErr, "the run record is still written after cancellation")
	h.store.AssertNotCalled(t, "MarkMissingSources", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

// A mid-page cancellation stops at the next listing instead of churning the
// rest of the decoded page through failing enrich/apply calls, and thumbnails
// a worker already wrote are still recorded so the store holds no orphans.
func TestRunCancelledMidPageStopsAndRecordsFetchedPhotos(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarness(t, ingest.Options{},
		yielded{sp: listing("1", "photo-a")}, yielded{sp: listing("2")}, yielded{sp: listing("3")})
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, listing("1", "photo-a"), mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Once()
	// Photos drain in the background, so the loop may apply 2 and 3 before it sees the cancel.
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 12}, nil).Maybe()
	h.photos.EXPECT().Cache(mock.Anything, "streeteasy", "photo-a").
		RunAndReturn(func(context.Context, string, string) (ingest.CachedPhoto, error) {
			cancel()
			return ingest.CachedPhoto{RelPath: "streeteasy/ab/photo-a.jpg", MIMEType: "image/jpeg", Width: 8, Height: 6}, nil
		}).Once()
	var recordCtxErr error
	h.store.EXPECT().SetPhotoCache(mock.Anything, domain.PropertyID(11), 0, "photo-a", "streeteasy/ab/photo-a.jpg", "image/jpeg", 8, 6).
		RunAndReturn(func(ctx context.Context, _ domain.PropertyID, _ int, _, _, _ string, _, _ int) error {
			recordCtxErr = ctx.Err()
			return nil
		}).Once()

	_, err := h.svc.Run(ctx, testQuery())
	require.ErrorIs(t, err, context.Canceled)
	assert.NoError(t, recordCtxErr, "the fetched thumbnail is recorded on a live context")
	assert.Equal(t, 3, h.stats.ListingsSeen, "the scope is enumerated before any fetch")
	assert.Empty(t, h.stats.ItemErrors)
}

// Shutdown is classified from the context, and that classification survives a
// bookkeeping failure: the target drain retries the scope instead of failing it.
func TestRunInterruptedStaysClassifiedWhenFinishRunFails(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(yielded{sp: listing("1")})).Once()
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).
		Return(domain.IngestRun{ID: testRunID, Provider: "streeteasy"}, nil).Once()
	st.EXPECT().BaselineVolume(mock.Anything, "streeteasy", mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(errors.New("db gone")).Times(ingest.FinishAttempts)
	svc := ingest.NewService(source, st, mocks.NewMockPhotoCacher(t), ingest.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})

	noRetryWait(t)
	cancel()
	_, err := svc.Run(ctx, testQuery())
	require.ErrorIs(t, err, ingest.ErrInterrupted)
	require.ErrorContains(t, err, "db gone")
	assert.ErrorIs(t, ingest.RunOutcome(ingest.Result{}, err), ingest.ErrInterrupted)
}

func TestRunCancelledDuringApplyDoesNotChurnThePage(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")}, yielded{sp: listing("2")}, yielded{sp: listing("3")})
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		RunAndReturn(func(context.Context, domain.IngestRunID, ingest.SourceProperty, time.Time) (ingest.ApplyResult, error) {
			cancel()
			return ingest.ApplyResult{}, context.Canceled
		}).Once()

	_, err := h.svc.Run(ctx, testQuery())
	require.ErrorIs(t, err, context.Canceled)
	require.Len(t, h.stats.ItemErrors, 1, "only the interrupted listing is recorded as an error")
	assert.Equal(t, 3, h.stats.ListingsSeen, "the scope is enumerated before any fetch")
	h.store.AssertNumberOfCalls(t, "ApplySourceProperty", 1)
}

// The heart of incremental crawling: an unchanged, already-enriched listing
// costs no detail request, while a price change (or missing description, or a
// new listing) triggers one. Deep runs enrich everything regardless.
func TestRunIncrementalEnrichesOnlyChangedListings(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	price := func(v int64) *domain.Money { p := domain.Money(v); return &p }
	withPrice := func(sp ingest.SourceProperty, v int64) ingest.SourceProperty {
		sp.Property.Price = price(v)
		return sp
	}

	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(
		yielded{sp: withPrice(listing("1"), 500000)}, // unchanged: skip detail
		yielded{sp: withPrice(listing("2"), 450000)}, // price drop: enrich
		yielded{sp: listing("3")},                    // brand new: enrich
	)).Once()

	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "1").
		Return(ingest.EnrichState{Exists: true, HasDescription: true, Price: price(500000), Status: domain.StatusActive}, nil).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "2").
		Return(ingest.EnrichState{Exists: true, HasDescription: true, Price: price(475000), Status: domain.StatusActive}, nil).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "3").
		Return(ingest.EnrichState{}, nil).Once()
	// Strict mocks make the skip assertion for "1" implicit: an EnrichDetail
	// call for it would have no matching expectation and fail the test.
	for _, id := range []string{"2", "3"} {
		source.EXPECT().EnrichDetail(mock.Anything, mock.MatchedBy(func(sp ingest.SourceProperty) bool { return sp.ProviderID == id })).
			RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Once()
	}

	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: testRunID}, nil).Once()
	st.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(3)
	st.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(nil).Once()
	st.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2", "3"}, testRunID).Return(ingest.MissingResult{}, nil).Once()

	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: logger})
	_, err := svc.Run(context.Background(), testQuery()) // Deep=false
	require.NoError(t, err)
}

// The whole scope is enumerated before the first detail fetch, and fetches go
// fresh listings first, then refreshes, then rows that need none; a blocked run
// therefore always spends its budget on what the corpus lacks most.
func TestRunEnumeratesThenEnrichesFreshFirst(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	price := func(v int64) *domain.Money { p := domain.Money(v); return &p }
	withPrice := func(sp ingest.SourceProperty, v int64) ingest.SourceProperty {
		sp.Property.Price = price(v)
		return sp
	}
	current := ingest.EnrichState{Exists: true, HasDescription: true, Price: price(500000), Status: domain.StatusActive}
	changed := ingest.EnrichState{Exists: true, HasDescription: true, Price: price(475000), Status: domain.StatusActive}

	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	// Search order deliberately puts the current row first and the new one last.
	items := []yielded{
		{sp: withPrice(listing("c1"), 500000)}, {sp: withPrice(listing("r1"), 500000)}, {sp: listing("n1")},
		{sp: withPrice(listing("c2"), 500000)}, {sp: withPrice(listing("r2"), 500000)}, {sp: listing("n2")},
	}
	pagesDone := false
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(func(yield func(ingest.SourceProperty, error) bool) {
		for _, it := range items {
			if !yield(it.sp, it.err) {
				return
			}
		}
		pagesDone = true
	}).Once()
	for _, id := range []string{"c1", "c2"} {
		st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", id).Return(current, nil).Once()
	}
	for _, id := range []string{"r1", "r2"} {
		st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", id).Return(changed, nil).Once()
	}
	for _, id := range []string{"n1", "n2"} {
		st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", id).Return(ingest.EnrichState{}, nil).Once()
	}
	var fetched []string
	source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) {
			assert.True(t, pagesDone, "detail fetch before the search finished enumerating")
			fetched = append(fetched, sp.ProviderID)
			return sp, nil
		}).Times(4)
	var applied []string
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: testRunID}, nil).Once()
	st.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, _ domain.IngestRunID, sp ingest.SourceProperty, _ time.Time) (ingest.ApplyResult, error) {
			applied = append(applied, sp.ProviderID)
			return ingest.ApplyResult{PropertyID: 11}, nil
		}).Times(6)
	st.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(nil).Once()
	st.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"c1", "r1", "n1", "c2", "r2", "n2"}, testRunID).
		Return(ingest.MissingResult{}, nil).Once()

	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: logger})
	_, err := svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"n1", "n2"}, fetched[:2], "fresh listings fetched first")
	assert.ElementsMatch(t, []string{"r1", "r2"}, fetched[2:], "then refreshes")
	assert.ElementsMatch(t, []string{"c1", "c2"}, applied[4:], "rows needing no fetch land last")
}

// A deep run re-enriches every unchanged listing whose detail is older than
// DeepInterval (or never fetched), but resumes past ones enriched recently, so
// an interrupted deep pass picks up where it stopped instead of starting over.
func TestRunDeepResumesRecentlyEnriched(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	price := func(v int64) *domain.Money { p := domain.Money(v); return &p }
	withPrice := func(sp ingest.SourceProperty, v int64) ingest.SourceProperty {
		sp.Property.Price = price(v)
		return sp
	}
	unchanged := func(enrichedAt *time.Time) ingest.EnrichState {
		return ingest.EnrichState{Exists: true, HasDescription: true, Price: price(500000), Status: domain.StatusActive, EnrichedAt: enrichedAt}
	}
	fresh := time.Now().Add(-time.Hour)
	stale := time.Now().Add(-ingest.DeepInterval - time.Hour)

	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(
		yielded{sp: withPrice(listing("1"), 500000)}, // enriched an hour ago: resume past it
		yielded{sp: withPrice(listing("2"), 500000)}, // enriched before the interval: re-enrich
		yielded{sp: withPrice(listing("3"), 500000)}, // never enriched: re-enrich
	)).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "1").Return(unchanged(&fresh), nil).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "2").Return(unchanged(&stale), nil).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "3").Return(unchanged(nil), nil).Once()
	for _, id := range []string{"2", "3"} {
		source.EXPECT().EnrichDetail(mock.Anything, mock.MatchedBy(func(sp ingest.SourceProperty) bool { return sp.ProviderID == id })).
			RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Once()
	}
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: testRunID}, nil).Once()
	st.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(3)
	st.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(nil).Once()
	st.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2", "3"}, testRunID).Return(ingest.MissingResult{}, nil).Once()

	q := testQuery()
	q.Deep = true
	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: logger})
	_, err := svc.Run(context.Background(), q)
	require.NoError(t, err)
}

// A listing whose detail page was fetched and simply carries no description
// is not re-fetched on every incremental run; one never enriched still is.
func TestIncrementalRunDoesNotRefetchDescriptionlessListings(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	price := func(v int64) *domain.Money { p := domain.Money(v); return &p }
	withPrice := func(sp ingest.SourceProperty, v int64) ingest.SourceProperty {
		sp.Property.Price = price(v)
		return sp
	}
	enrichedAt := time.Now().Add(-time.Hour)
	noDescription := func(at *time.Time) ingest.EnrichState {
		return ingest.EnrichState{Exists: true, HasDescription: false, Price: price(500000), Status: domain.StatusActive, EnrichedAt: at}
	}

	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(
		yielded{sp: withPrice(listing("1"), 500000)}, // enriched, no description: leave it
		yielded{sp: withPrice(listing("2"), 500000)}, // never enriched: fetch
	)).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "1").Return(noDescription(&enrichedAt), nil).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", "2").Return(noDescription(nil), nil).Once()
	source.EXPECT().EnrichDetail(mock.Anything, mock.MatchedBy(func(sp ingest.SourceProperty) bool { return sp.ProviderID == "2" })).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Once()
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: testRunID}, nil).Once()
	st.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11}, nil).Times(2)
	st.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(nil).Once()
	st.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1", "2"}, testRunID).Return(ingest.MissingResult{}, nil).Once()

	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: logger})
	_, err := svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
}

// Item errors are capped per run so a provider-wide outage cannot stuff
// thousands of rows into ingest_runs.errors; the tail is summarised instead.
func TestRunCapsItemErrors(t *testing.T) {
	const n = 250
	items := make([]yielded, n)
	for i := range items {
		items[i] = yielded{sp: listing(strconv.Itoa(i))}
	}
	h := newHarness(t, ingest.Options{}, items...)
	h.expectBaseline(ingest.Baseline{})
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{}, errors.New("upsert failed")).Times(n)
	h.store.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", mock.Anything, testRunID).Return(ingest.MissingResult{}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	require.Len(t, h.stats.ItemErrors, 201)
	assert.NotEmpty(t, h.stats.ItemErrors[199].ProviderID)
	assert.Contains(t, h.stats.ItemErrors[0].Message, "upsert failed")
	assert.Contains(t, h.stats.ItemErrors[200].Message, "50 more errors not recorded")
}

func TestRunWithoutPhotoCacher(t *testing.T) {
	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Maybe()
	source.EXPECT().Search(mock.Anything, mock.Anything).Return(sourceSeq(yielded{sp: listing("1", "photo-a")})).Once()
	st.EXPECT().EnrichmentState(mock.Anything, "streeteasy", mock.Anything).Return(ingest.EnrichState{}, nil).Maybe()
	source.EXPECT().EnrichDetail(mock.Anything, mock.Anything).
		RunAndReturn(func(_ context.Context, sp ingest.SourceProperty) (ingest.SourceProperty, error) { return sp, nil }).Maybe()
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).Return(domain.IngestRun{ID: testRunID}, nil).Once()
	st.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()
	st.EXPECT().BaselineVolume(mock.Anything, mock.Anything, mock.Anything).Return(ingest.Baseline{}, nil).Once()
	st.EXPECT().FinishRun(mock.Anything, testRunID, mock.Anything).Return(nil).Once()
	st.EXPECT().MarkMissingSources(mock.Anything, "streeteasy", []string{"1"}, testRunID).Return(ingest.MissingResult{}, nil).Once()

	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	_, err := svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	st.AssertNotCalled(t, "SetPhotoCache", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRunBaselineErrorSkipsMissingAdvancement(t *testing.T) {
	h := newHarness(t, ingest.Options{}, yielded{sp: listing("1")})
	h.store.EXPECT().BaselineVolume(mock.Anything, "streeteasy", mock.Anything).
		Return(ingest.Baseline{}, errors.New("query baseline volume: connection reset")).Once()
	h.store.EXPECT().ApplySourceProperty(mock.Anything, testRunID, mock.Anything, mock.Anything).
		Return(ingest.ApplyResult{PropertyID: 11, Created: true}, nil).Once()

	_, err := h.svc.Run(context.Background(), testQuery())
	require.NoError(t, err)
	assert.True(t, h.stats.Complete)
	assert.False(t, h.stats.Suspect)
	h.store.AssertNotCalled(t, "MarkMissingSources", mock.Anything, mock.Anything, mock.Anything, mock.Anything)
}

func TestRunCreateRunFailure(t *testing.T) {
	source := mocks.NewMockSource(t)
	st := mocks.NewMockStore(t)
	source.EXPECT().Name().Return("streeteasy").Once()
	st.EXPECT().StartRun(mock.Anything, "streeteasy", mock.Anything).
		Return(domain.IngestRun{}, errors.New("insert ingest run: down")).Once()

	svc := ingest.NewService(source, st, nil, ingest.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))})
	_, err := svc.Run(context.Background(), testQuery())
	require.ErrorContains(t, err, "create ingest run")
	source.AssertNotCalled(t, "Search", mock.Anything, mock.Anything)
}

// noRetryWait makes FinishRun retries immediate for the test's duration.
func noRetryWait(t *testing.T) {
	t.Helper()
	prev := *ingest.FinishRetryWait
	*ingest.FinishRetryWait = 0
	t.Cleanup(func() { *ingest.FinishRetryWait = prev })
}
