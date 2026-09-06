package listings_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/listings/mocks"
)

// Oversized writes are rejected before the store sees them: the mock has no
// expectations, so any store call fails the test.
func TestWriteSizeCaps(t *testing.T) {
	ctx := context.Background()
	svc := listings.NewService(mocks.NewMockStore(t), nil)

	batch := make([]listings.VerdictInput, listings.MaxVerdictBatch+1)
	for i := range batch {
		batch[i] = listings.VerdictInput{PropertyID: 1, Kind: domain.VerdictLove}
	}
	if _, err := svc.RecordVerdicts(ctx, batch); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("oversized batch err = %v, want ErrInvalidInput", err)
	}

	long := strings.Repeat("x", listings.MaxVerdictNote+1)
	if _, err := svc.RecordVerdict(ctx, 1, domain.VerdictLove, long); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long note err = %v, want ErrInvalidInput", err)
	}
	mixed := []listings.VerdictInput{{PropertyID: 1, Kind: domain.VerdictLove}, {PropertyID: 2, Kind: domain.VerdictLove, Note: long}}
	if _, err := svc.RecordVerdicts(ctx, mixed); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("batch with a long note err = %v, want ErrInvalidInput and nothing recorded", err)
	}

	if _, err := svc.UpdateRubric(ctx, strings.Repeat("y", listings.MaxRubricBytes+1), 0); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("oversized rubric err = %v, want ErrInvalidInput", err)
	}

	if _, err := svc.CreateList(ctx, strings.Repeat("n", listings.MaxListName+1), ""); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long list name err = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.CreateList(ctx, "ok", strings.Repeat("e", listings.MaxListEmoji+1)); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long emoji err = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.AddToList(ctx, 1, 1, long); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long list note err = %v, want ErrInvalidInput", err)
	}
	if _, _, err := svc.AddToListBySlug(ctx, "favorites", 1, long); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long list note (slug) err = %v, want ErrInvalidInput", err)
	}
	crawl := listings.CrawlRequest{Areas: []string{"305"}, ListingType: domain.ListingSale, Note: long}
	if _, err := svc.RequestCrawl(ctx, crawl); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long crawl note err = %v, want ErrInvalidInput", err)
	}
}

// min_baths is stored with one decimal, so a value that would be rounded on
// the way in is refused rather than silently tightened.
func TestMinBathsMustBeHalfSteps(t *testing.T) {
	ctx := context.Background()
	svc := listings.NewService(mocks.NewMockStore(t), nil)
	for _, v := range []float64{1.75, 0.1, 2.25} {
		up := listings.ProfileUpdate{MinBaths: listings.Opt[*float64]{Set: true, Val: &v}}
		if _, err := svc.SetProfile(ctx, up); !errors.Is(err, listings.ErrInvalidInput) {
			t.Fatalf("min_baths %g err = %v, want ErrInvalidInput", v, err)
		}
	}
}

// A neighborhood filter is echoed by every get_state and bound into every
// candidate query, so its size is bounded like every other user text field.
func TestNeighborhoodFilterCaps(t *testing.T) {
	ctx := context.Background()
	svc := listings.NewService(mocks.NewMockStore(t), nil)
	many := make([]string, listings.MaxNeighborhoods+1)
	for i := range many {
		many[i] = strings.Repeat("n", i+1)
	}
	if _, err := svc.SetProfile(ctx, listings.ProfileUpdate{Neighborhoods: listings.Opt[[]string]{Set: true, Val: many}}); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("%d neighborhoods err = %v, want ErrInvalidInput", len(many), err)
	}
	long := []string{strings.Repeat("x", listings.MaxNeighborhoodName+1)}
	if _, err := svc.SetProfile(ctx, listings.ProfileUpdate{Neighborhoods: listings.Opt[[]string]{Set: true, Val: long}}); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("long neighborhood err = %v, want ErrInvalidInput", err)
	}
	if _, err := svc.Search(ctx, listings.SearchQuery{Neighborhoods: long}); !errors.Is(err, listings.ErrInvalidInput) {
		t.Fatalf("search with a long neighborhood err = %v, want ErrInvalidInput", err)
	}
}

// set_profile is a partial update, so "omit it" would keep the cap; the
// remedy it names must be the one that clears it.
func TestProfileCapErrorsSayHowToClear(t *testing.T) {
	ctx := context.Background()
	svc := listings.NewService(mocks.NewMockStore(t), nil)
	zero := domain.Money(0)
	for _, up := range []listings.ProfileUpdate{
		{MaxPrice: listings.Opt[*domain.Money]{Set: true, Val: &zero}},
		{MaxMonthlyCarrying: listings.Opt[*domain.Money]{Set: true, Val: &zero}},
	} {
		_, err := svc.SetProfile(ctx, up)
		if !errors.Is(err, listings.ErrInvalidInput) || !strings.Contains(err.Error(), "null") {
			t.Fatalf("zero cap err = %v, want ErrInvalidInput naming null", err)
		}
	}
	if _, err := svc.Search(ctx, listings.SearchQuery{MaxPrice: &zero}); err == nil || !strings.Contains(err.Error(), "omit") {
		t.Fatalf("search zero cap err = %v, want the omit hint", err)
	}
}
