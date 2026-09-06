package store_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

func TestVerdictsAreAppendOnly(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	first, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictDislike, "dark")
	if err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	second, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictLove, "saw it in person")
	if err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if second.ID <= first.ID {
		t.Fatalf("verdict ids not increasing: %d then %d", first.ID, second.ID)
	}

	verdicts, err := s.Verdicts(ctx)
	if err != nil {
		t.Fatalf("verdicts: %v", err)
	}
	if len(verdicts) != 2 {
		t.Fatalf("history len = %d, want both verdicts retained", len(verdicts))
	}
	if verdicts[0].Kind != domain.VerdictDislike || verdicts[1].Kind != domain.VerdictLove {
		t.Fatalf("history order/content = %+v", verdicts)
	}
	if verdicts[1].Note != "saw it in person" || verdicts[1].PropertyID != created.PropertyID {
		t.Fatalf("latest verdict = %+v", verdicts[1])
	}
}

func TestRecordVerdictUnknownProperty(t *testing.T) {
	s := newStore(t)
	_, err := s.RecordVerdict(context.Background(), domain.PropertyID(777), domain.VerdictMaybe, "")
	if !errors.Is(err, store.ErrPropertyNotFound) {
		t.Fatalf("err = %v, want ErrPropertyNotFound", err)
	}
}

func TestStateRubricStaleness(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	runID := startRun(t, s)
	created := apply(t, s, runID, newSource("1001"), baseTime)

	profile, rubric, stale, verdicts, err := s.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if rubric != nil || stale || len(verdicts) != 0 {
		t.Fatalf("empty state = rubric %v stale %v verdicts %d", rubric, stale, len(verdicts))
	}
	if profile.ListingType != domain.ListingSale {
		t.Fatalf("default profile listing type = %q", profile.ListingType)
	}

	v1, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictLove, "")
	if err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if _, _, stale, _, err = s.State(ctx); err != nil || !stale {
		t.Fatalf("verdicts without a rubric should be stale (stale=%v err=%v)", stale, err)
	}

	if _, err := s.AppendRubric(ctx, "likes light", v1.ID+1); !errors.Is(err, store.ErrFutureThroughVerdict) {
		t.Fatalf("err = %v, want ErrFutureThroughVerdict", err)
	}

	appended, err := s.AppendRubric(ctx, "likes light", v1.ID)
	if err != nil {
		t.Fatalf("append rubric: %v", err)
	}
	if appended.ThroughVerdictID != v1.ID || appended.Content != "likes light" {
		t.Fatalf("appended rubric = %+v", appended)
	}

	_, rubric, stale, verdicts, err = s.State(ctx)
	if err != nil {
		t.Fatalf("state: %v", err)
	}
	if rubric == nil || rubric.ID != appended.ID || stale {
		t.Fatalf("fresh rubric state = %+v stale %v", rubric, stale)
	}
	if len(verdicts) != 1 {
		t.Fatalf("verdict history len = %d", len(verdicts))
	}

	if _, err := s.RecordVerdict(ctx, created.PropertyID, domain.VerdictMaybe, "changed my mind"); err != nil {
		t.Fatalf("record verdict: %v", err)
	}
	if _, _, stale, _, err = s.State(ctx); err != nil || !stale {
		t.Fatalf("new verdict should make the rubric stale (stale=%v err=%v)", stale, err)
	}

	_, err = s.AppendRubric(ctx, "revised", 0)
	if !errors.Is(err, listings.ErrInvalidInput) || !strings.Contains(err.Error(), fmt.Sprintf("(%d)", v1.ID+1)) {
		t.Fatalf("through 0 with verdicts present: err = %v, want invalid input naming the newest verdict", err)
	}
}

func TestAppendRubricAcceptsZeroBeforeAnyVerdict(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	r, err := s.AppendRubric(ctx, "stated preferences", 0)
	if err != nil || r.ThroughVerdictID != 0 {
		t.Fatalf("rubric = %+v err = %v", r, err)
	}
	if _, _, stale, _, err := s.State(ctx); err != nil || stale {
		t.Fatalf("no verdicts: stale = %v err = %v", stale, err)
	}
}

func TestProfileUpsert(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	empty, err := s.GetProfile(ctx)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if empty.MaxPrice != nil || empty.ListingType != domain.ListingSale {
		t.Fatalf("default profile = %+v", empty)
	}

	want := domain.Profile{
		MaxPrice:           money(1_250_000),
		MinBeds:            ptr.To(2),
		MinBaths:           ptr.To(1.5),
		MaxMonthlyCarrying: money(2000),
		ListingType:        domain.ListingSale,
		Neighborhoods:      []string{"Park Slope", "Upper West Side"},
		PropertyTypes:      []domain.PropertyType{domain.PropertyCoop, domain.PropertyCondo},
	}
	got, err := s.SetProfile(ctx, want)
	if err != nil {
		t.Fatalf("set profile: %v", err)
	}
	if got.MaxPrice == nil || *got.MaxPrice != 1_250_000 || got.MinBaths == nil || *got.MinBaths != 1.5 {
		t.Fatalf("returned profile = %+v", got)
	}
	if len(got.Neighborhoods) != 2 || got.Neighborhoods[1] != "Upper West Side" {
		t.Fatalf("neighborhoods = %v", got.Neighborhoods)
	}
	if len(got.PropertyTypes) != 2 || got.PropertyTypes[0] != domain.PropertyCoop {
		t.Fatalf("property types = %v", got.PropertyTypes)
	}
	if got.UpdatedAt.IsZero() {
		t.Fatal("updated_at not set")
	}

	updated, err := s.SetProfile(ctx, domain.Profile{MinBeds: ptr.To(3)})
	if err != nil {
		t.Fatalf("set profile: %v", err)
	}
	if updated.MaxPrice != nil || updated.MinBeds == nil || *updated.MinBeds != 3 {
		t.Fatalf("upsert did not replace previous values: %+v", updated)
	}
	if updated.ListingType != domain.ListingSale {
		t.Fatalf("listing type = %q, want the sale default", updated.ListingType)
	}

	reloaded, err := s.GetProfile(ctx)
	if err != nil {
		t.Fatalf("get profile: %v", err)
	}
	if reloaded.MinBeds == nil || *reloaded.MinBeds != 3 || reloaded.MaxPrice != nil {
		t.Fatalf("reloaded profile = %+v", reloaded)
	}
}

// Two overlapping field updates both land: the second waits for the first
// instead of merging over a stale read.
func TestUpdateProfileSerializesOverlappingMerges(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()
	entered, gate := make(chan struct{}), make(chan struct{})
	first, second := make(chan error, 1), make(chan error, 1)
	go func() {
		_, err := s.UpdateProfile(ctx, func(p *domain.Profile) error {
			close(entered)
			<-gate
			p.MaxPrice = money(900_000)
			return nil
		})
		first <- err
	}()
	<-entered
	go func() {
		_, err := s.UpdateProfile(ctx, func(p *domain.Profile) error {
			p.MinBeds = ptr.To(2)
			return nil
		})
		second <- err
	}()
	// The second merge must wait on the first's row lock, not read a stale profile.
	select {
	case err := <-second:
		t.Fatalf("second update finished while the first merge was still open (err=%v)", err)
	case <-time.After(200 * time.Millisecond):
	}
	close(gate)
	if err := <-first; err != nil {
		t.Fatalf("first update: %v", err)
	}
	if err := <-second; err != nil {
		t.Fatalf("second update: %v", err)
	}
	got, err := s.GetProfile(ctx)
	if err != nil || got.MaxPrice == nil || got.MinBeds == nil {
		t.Fatalf("profile after overlapping updates = %+v, %v; want both fields", got, err)
	}
	// A merge that mutates and then fails leaves nothing behind.
	_, err = s.UpdateProfile(ctx, func(p *domain.Profile) error {
		p.MaxPrice = money(1)
		p.MinBeds = ptr.To(9)
		return errors.New("no")
	})
	if err == nil {
		t.Fatal("a merge error must abort the update")
	}
	after, err := s.GetProfile(ctx)
	if err != nil || !reflect.DeepEqual(after, got) {
		t.Fatalf("profile after failed merge = %+v, %v; want unchanged %+v", after, err, got)
	}
}

func TestRunsAndBaselineVolume(t *testing.T) {
	s := newStore(t)
	ctx := context.Background()

	run, err := s.CreateRun(ctx, testProvider, "scope-a", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if run.ID == 0 || run.StartedAt.IsZero() || run.Complete || run.FinishedAt != nil {
		t.Fatalf("new run = %+v", run)
	}

	stats := ingest.RunStats{
		Complete:      true,
		ListingsSeen:  100,
		Created:       12,
		Updated:       88,
		PhotoFailures: 3,
		ItemErrors:    []domain.RunError{{ProviderID: "1001", Message: "missing price"}},
	}
	if err := s.FinishRun(ctx, run.ID, stats); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	runs, err := s.RecentRuns(ctx, 10)
	if err != nil {
		t.Fatalf("recent runs: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("recent runs len = %d", len(runs))
	}
	got := runs[0]
	if got.FinishedAt == nil || !got.Complete || got.ListingsSeen != 100 || got.PhotoFailures != 3 {
		t.Fatalf("finished run = %+v", got)
	}
	if len(got.ItemErrors) != 1 || got.ItemErrors[0].ProviderID != "1001" || got.ItemErrors[0].Message != "missing price" {
		t.Fatalf("item errors = %+v", got.ItemErrors)
	}

	base, err := s.BaselineVolume(ctx, testProvider, "scope-a")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if base.Runs != 1 || base.Volume != 100 {
		t.Fatalf("baseline after one run = %+v", base)
	}

	for _, s2 := range []ingest.RunStats{
		{Complete: true, ListingsSeen: 110},
		{Complete: true, ListingsSeen: 90},
		{Complete: true, ListingsSeen: 5, Suspect: true},
		{Complete: false, ListingsSeen: 1},
	} {
		r, err := s.CreateRun(ctx, testProvider, "scope-a", false)
		if err != nil {
			t.Fatalf("create run: %v", err)
		}
		if err := s.FinishRun(ctx, r.ID, s2); err != nil {
			t.Fatalf("finish run: %v", err)
		}
	}
	other, err := s.CreateRun(ctx, testProvider, "scope-b", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	if err := s.FinishRun(ctx, other.ID, ingest.RunStats{Complete: true, ListingsSeen: 9000}); err != nil {
		t.Fatalf("finish run: %v", err)
	}

	base, err = s.BaselineVolume(ctx, testProvider, "scope-a")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if base.Runs != 3 || base.Volume != 100 {
		t.Fatalf("baseline = %+v, want median 100 over 3 clean same-scope runs", base)
	}

	empty, err := s.BaselineVolume(ctx, testProvider, "scope-unknown")
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	if empty.Runs != 0 || empty.Volume != 0 {
		t.Fatalf("unknown scope baseline = %+v", empty)
	}
}
