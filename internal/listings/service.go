package listings

import (
	"context"
	"errors"
	"fmt"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/shared/xslices"
)

// Service is the transport-independent application logic every transport drives. It maps store sentinels to its own at the boundary and wraps errors with operation context.
type Service struct {
	store  Store
	photos PhotoStore
	areas  []domain.Area // provider area catalog for ResolveAreas; see LoadAreas
}

// NewService returns a concrete *Service. photos may be nil for callers that never request photo bytes or counts (e.g. the CLI detail view).
func NewService(store Store, photos PhotoStore) *Service {
	return &Service{store: store, photos: photos}
}

func (s *Service) State(ctx context.Context) (State, error) {
	profile, rubric, stale, verdicts, err := s.store.State(ctx)
	if err != nil {
		return State{}, fmt.Errorf("load state: %w", err)
	}
	return State{Profile: profile, Rubric: rubric, Stale: stale, Verdicts: verdicts}, nil
}

// ResetState hard-clears the saved taste memory (verdicts, rubric, shown history, profile) and reports the per-table counts removed. The listings corpus and operational state are kept.
func (s *Service) ResetState(ctx context.Context) (ResetResult, error) {
	counts, err := s.store.ResetTasteState(ctx)
	if err != nil {
		return ResetResult{}, fmt.Errorf("reset taste state: %w", err)
	}
	return ResetResult{Cleared: counts}, nil
}

// VerdictsFor returns just one listing's verdicts, oldest first.
func (s *Service) VerdictsFor(ctx context.Context, id domain.PropertyID) ([]domain.Verdict, error) {
	_, _, _, verdicts, err := s.store.State(ctx)
	if err != nil {
		return nil, fmt.Errorf("load verdicts for listing %d: %w", id, err)
	}
	var out []domain.Verdict
	for _, v := range verdicts {
		if v.PropertyID == id {
			out = append(out, v)
		}
	}
	return out, nil
}

func (s *Service) Search(ctx context.Context, q SearchQuery) (SearchResult, error) {
	if err := checkMoney("max_price", q.MaxPrice); err != nil {
		return SearchResult{}, err
	}
	if err := checkBeds("min_beds", q.MinBeds); err != nil {
		return SearchResult{}, err
	}
	if err := checkNeighborhoods(q.Neighborhoods); err != nil {
		return SearchResult{}, err
	}
	if err := checkBaths("min_baths", q.MinBaths); err != nil {
		return SearchResult{}, err
	}
	listingType := q.ListingType
	if listingType == "" {
		listingType = domain.ListingSale
	}
	offset := q.Offset
	if offset < 0 {
		offset = 0
	}
	filter := SearchFilter{
		ListingType:     listingType,
		MaxPrice:        q.MaxPrice,
		MinBeds:         q.MinBeds,
		MinBaths:        q.MinBaths,
		Neighborhoods:   cleanNames(q.Neighborhoods),
		PropertyTypes:   nilIfEmpty(q.PropertyTypes),
		Limit:           q.Limit,
		Offset:          offset,
		IncludeInactive: q.IncludeInactive,
	}
	total, err := s.store.CountProperties(ctx, filter)
	if err != nil {
		return SearchResult{}, fmt.Errorf("count listings: %w", err)
	}
	props, err := s.store.SearchProperties(ctx, filter)
	if err != nil {
		return SearchResult{}, fmt.Errorf("search listings: %w", err)
	}
	rows, err := s.rows(ctx, props)
	if err != nil {
		return SearchResult{}, err
	}
	unmatched, err := s.UnmatchedNeighborhoods(ctx, filter.Neighborhoods)
	if err != nil {
		return SearchResult{}, err
	}
	return SearchResult{Rows: rows, Filters: filter, Total: total, Offset: offset, Limit: effectiveBulkLimit(q.Limit), UnmatchedNeighborhoods: unmatched}, nil
}

// UnmatchedNeighborhoods returns the given names (original spelling) that no listing carries, so a filter that silently matches nothing can be flagged.
func (s *Service) UnmatchedNeighborhoods(ctx context.Context, names []string) ([]string, error) {
	names = cleanNames(names)
	if len(names) == 0 {
		return nil, nil
	}
	known, err := s.store.MatchNeighborhoods(ctx, names)
	if err != nil {
		return nil, fmt.Errorf("match neighborhoods: %w", err)
	}
	have := make(map[string]bool, len(known))
	for _, k := range known {
		have[strings.ToLower(k)] = true
	}
	var out []string
	for _, n := range names {
		if !have[strings.ToLower(n)] {
			out = append(out, n)
		}
	}
	return out, nil
}

func (s *Service) Candidates(ctx context.Context, limit int) ([]Row, error) {
	profile, err := s.store.GetProfile(ctx)
	if err != nil {
		return nil, fmt.Errorf("load profile: %w", err)
	}
	props, err := s.store.Candidates(ctx, profile, limit)
	if err != nil {
		return nil, fmt.Errorf("query candidates: %w", err)
	}
	return s.rows(ctx, props)
}

func (s *Service) Listing(ctx context.Context, id domain.PropertyID, req PhotoRequest) (Detail, error) {
	prop, events, photos, prov, err := s.store.GetProperty(ctx, id)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Detail{}, ErrNotFound
		}
		return Detail{}, fmt.Errorf("get listing %d: %w", id, err)
	}

	layout, err := photoLayout(req.Layout)
	if err != nil {
		return Detail{}, err
	}

	result := PhotoResult{All: photos}
	result.Counts.Total = len(photos)
	if req.Include {
		result.Layout = layout
	}

	switch {
	case req.Include && layout == LayoutContactSheet:
		tiles, tally := s.collectTiles(ctx, photos, photoBudget(req.MaxPhotos, layout))
		sheets, err := BuildSheets(tiles)
		if err != nil {
			return Detail{}, fmt.Errorf("build contact sheets for listing %d: %w", id, err)
		}
		result.Sheets = sheets
		result.Counts.Returned = len(tiles)
		result.Counts.Cached, result.Counts.Missing = tally.cached, tally.missing
	case req.Include:
		images, tally := s.collectPhotos(ctx, photos, photoBudget(req.MaxPhotos, layout))
		result.Images = images
		result.Counts.Returned = len(images)
		result.Counts.Cached, result.Counts.Missing = tally.cached, tally.missing
	case req.Links:
		// Links are per-photo and cost no bytes, so they get the contact-sheet cap (REST plans sheets from them).
		links, tally := s.collectLinks(ctx, photos, photoBudget(req.MaxPhotos, LayoutContactSheet))
		result.Links = links
		result.Counts.Returned = len(links)
		result.Counts.Cached, result.Counts.Missing = tally.cached, tally.missing
	case req.Count:
		_, tally := s.collectLinks(ctx, photos, 0)
		result.Counts.Cached, result.Counts.Missing = tally.cached, tally.missing
	}
	return Detail{Property: prop, PriceEvents: events, Provenance: prov, Photos: result}, nil
}

// PhotoObject returns the raw bytes of one cached thumbnail by store key, for
// the public /img proxy: a missing object is ErrNotFound, a store outage is not.
func (s *Service) PhotoObject(ctx context.Context, key string) ([]byte, error) {
	if s.photos == nil {
		return nil, ErrNotFound
	}
	data, err := s.photos.Get(ctx, key)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("photo %s: %w", key, err)
	}
	return data, nil
}

// MarkShown records ids as presented. asOf is when the caller read them (zero
// means now): a material change committed after it still resurfaces them.
func (s *Service) MarkShown(ctx context.Context, ids []domain.PropertyID, asOf time.Time) (int, error) {
	var cap *time.Time
	if !asOf.IsZero() {
		if asOf.After(time.Now().Add(time.Minute)) {
			return 0, Invalidf("as_of %s is in the future", asOf.UTC().Format(time.RFC3339))
		}
		cap = &asOf
	}
	marked, err := s.store.MarkShown(ctx, distinct(ids), cap)
	if err != nil {
		return 0, fmt.Errorf("mark listings shown: %w", err)
	}
	return marked, nil
}

func (s *Service) RecordVerdict(ctx context.Context, id domain.PropertyID, kind domain.VerdictKind, note string) (domain.Verdict, error) {
	if err := checkNote(note); err != nil {
		return domain.Verdict{}, err
	}
	v, err := s.store.RecordVerdict(ctx, id, kind, note)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return domain.Verdict{}, ErrNotFound
		}
		return domain.Verdict{}, fmt.Errorf("record verdict for listing %d: %w", id, err)
	}
	return v, nil
}

// RecordVerdicts is best-effort per item: an unknown id is reported in
// Failed while the rest are saved. Only invalid input (nothing recorded) or a
// store outage (Recorded holds what landed before it) returns an error.
func (s *Service) RecordVerdicts(ctx context.Context, in []VerdictInput) (VerdictBatch, error) {
	if len(in) > MaxVerdictBatch {
		return VerdictBatch{}, Invalidf("%d verdicts in one call; at most %d fit, split the batch", len(in), MaxVerdictBatch)
	}
	// Validate every note before writing any, so a bad batch records nothing.
	for i, item := range in {
		if err := checkNote(item.Note); err != nil {
			return VerdictBatch{}, fmt.Errorf("verdict %d for listing %d: %w", i, item.PropertyID, err)
		}
	}
	out := VerdictBatch{Recorded: make([]domain.Verdict, 0, len(in))}
	for i, item := range in {
		v, err := s.store.RecordVerdict(ctx, item.PropertyID, item.Kind, item.Note)
		if err != nil {
			if errors.Is(err, ErrNotFound) {
				out.Failed = append(out.Failed, VerdictFailure{Index: i, PropertyID: item.PropertyID, Message: "listing does not exist"})
				continue
			}
			return out, fmt.Errorf("record verdict %d for listing %d (%d recorded before it): %w", i, item.PropertyID, len(out.Recorded), err)
		}
		out.Recorded = append(out.Recorded, v)
	}
	return out, nil
}

func (s *Service) SetProfile(ctx context.Context, up ProfileUpdate) (domain.Profile, error) {
	if err := up.validate(); err != nil {
		return domain.Profile{}, err
	}
	saved, err := s.store.UpdateProfile(ctx, func(next *domain.Profile) error {
		up.applyTo(next)
		return nil
	})
	if err != nil {
		return domain.Profile{}, fmt.Errorf("save profile: %w", err)
	}
	return saved, nil
}

func (up ProfileUpdate) validate() error {
	if up.MaxPrice.Set {
		if err := checkMoneyHint("max_price", up.MaxPrice.Val, "send null to clear the cap"); err != nil {
			return err
		}
	}
	if up.MinBeds.Set {
		if err := checkBeds("min_beds", up.MinBeds.Val); err != nil {
			return err
		}
	}
	if up.MinBaths.Set {
		if err := checkBaths("min_baths", up.MinBaths.Val); err != nil {
			return err
		}
	}
	if up.MaxMonthlyCarrying.Set {
		if err := checkMoneyHint("max_monthly_carrying", up.MaxMonthlyCarrying.Val, "send null to clear the cap"); err != nil {
			return err
		}
	}
	if up.Neighborhoods.Set {
		return checkNeighborhoods(up.Neighborhoods.Val)
	}
	return nil
}

// applyTo overwrites only the fields that were sent: an absent field is left
// alone; an explicit value (a nil pointer or empty slice included) clears it.
func (up ProfileUpdate) applyTo(next *domain.Profile) {
	if up.MaxPrice.Set {
		next.MaxPrice = up.MaxPrice.Val
	}
	if up.MinBeds.Set {
		next.MinBeds = up.MinBeds.Val
	}
	if up.MinBaths.Set {
		next.MinBaths = up.MinBaths.Val
	}
	if up.MaxMonthlyCarrying.Set {
		next.MaxMonthlyCarrying = up.MaxMonthlyCarrying.Val
	}
	if up.ListingType.Set {
		next.ListingType = up.ListingType.Val
	}
	if up.Neighborhoods.Set {
		next.Neighborhoods = cleanNames(up.Neighborhoods.Val)
	}
	if up.PropertyTypes.Set {
		next.PropertyTypes = nilIfEmpty(up.PropertyTypes.Val)
	}
}

func (s *Service) UpdateRubric(ctx context.Context, content string, through domain.VerdictID) (domain.Rubric, error) {
	if strings.TrimSpace(content) == "" {
		return domain.Rubric{}, Invalidf("rubric content is empty")
	}
	if len(content) > MaxRubricBytes {
		return domain.Rubric{}, Invalidf("rubric content is %d bytes; at most %d fit, a rubric is a summary not a transcript", len(content), MaxRubricBytes)
	}
	if through < 0 {
		return domain.Rubric{}, Invalidf("through_verdict_id %d is negative", through)
	}
	rubric, err := s.store.AppendRubric(ctx, content, through)
	if err != nil {
		if errors.Is(err, ErrFutureThroughVerdict) {
			return domain.Rubric{}, ErrFutureThroughVerdict
		}
		return domain.Rubric{}, fmt.Errorf("append rubric: %w", err)
	}
	return rubric, nil
}

func (s *Service) RequestCrawl(ctx context.Context, req CrawlRequest) (CrawlRequestResult, error) {
	areas, err := ParseAreaIDs(req.Areas)
	if err != nil {
		return CrawlRequestResult{}, err
	}
	if err := checkMoney("max_price", req.MaxPrice); err != nil {
		return CrawlRequestResult{}, err
	}
	if err := checkBeds("min_beds", req.MinBeds); err != nil {
		return CrawlRequestResult{}, err
	}
	if err := checkNote(req.Note); err != nil {
		return CrawlRequestResult{}, err
	}

	existing, err := s.store.ListCrawlTargets(ctx)
	if err != nil {
		return CrawlRequestResult{}, fmt.Errorf("list crawl targets: %w", err)
	}
	pending := pendingRequests(existing)

	// A recurring request that matches an existing standing scope is idempotent:
	// re-enable it if paused and return it rather than duplicating the scope.
	if req.Recurring {
		for _, t := range existing {
			if t.Kind != domain.TargetStanding || !sameCrawlScope(t, areas, req.ListingType, req.MaxPrice, req.MinBeds) {
				continue
			}
			if !t.Enabled {
				if err := s.store.SetCrawlTargetEnabled(ctx, t.ID, true); err != nil {
					return CrawlRequestResult{}, fmt.Errorf("re-enable standing scope %d: %w", t.ID, err)
				}
				t.Enabled = true
			}
			return CrawlRequestResult{Target: t, AlreadyQueued: true, PendingRequests: pending}, nil
		}
	}

	// Only one-offs sit in the pending queue, so only they count against its cap.
	if !req.Recurring && pending >= MaxPendingRequests {
		return CrawlRequestResult{}, fmt.Errorf("%d crawl requests are already waiting to run (the cap is %d); call list_crawl_targets and wait for the crawler to drain them before requesting more: %w", pending, MaxPendingRequests, ErrQueueFull)
	}

	kind := domain.TargetOnce
	if req.Recurring {
		kind = domain.TargetStanding
	}
	target, err := s.store.CreateCrawlTarget(ctx, domain.CrawlTarget{
		Kind:        kind,
		Areas:       areas,
		ListingType: req.ListingType,
		MaxPrice:    req.MaxPrice,
		MinBeds:     ptr.Clone(req.MinBeds),
		Enabled:     req.Recurring, // a standing scope must be enabled to be due
		Status:      domain.TargetPending,
		Note:        strings.TrimSpace(req.Note),
	})
	duplicate := errors.Is(err, ErrDuplicateCrawlTarget)
	if err != nil && !duplicate {
		return CrawlRequestResult{}, fmt.Errorf("create crawl target: %w", err)
	}

	result := CrawlRequestResult{Target: target, AlreadyQueued: duplicate, PendingRequests: pending}
	// Only a new one-off adds to the pending-request queue; a standing scope is
	// not a queued request.
	if !duplicate && !req.Recurring {
		result.PendingRequests++
	}
	return result, nil
}

// sameCrawlScope reports whether an existing target covers exactly the scope a
// request describes: same listing type, price cap, bedroom floor, and area set
// (order/duplicates ignored).
func sameCrawlScope(t domain.CrawlTarget, areas []string, lt domain.ListingType, maxPrice *domain.Money, minBeds *int) bool {
	return t.ListingType == lt &&
		equalMoneyPtr(t.MaxPrice, maxPrice) &&
		equalIntPtr(t.MinBeds, minBeds) &&
		slices.Equal(canonAreas(t.Areas), canonAreas(areas))
}

func canonAreas(a []string) []string {
	out := slices.Clone(a)
	slices.Sort(out)
	return slices.Compact(out)
}

func equalMoneyPtr(a, b *domain.Money) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func equalIntPtr(a, b *int) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}

func (s *Service) CrawlTargets(ctx context.Context) ([]domain.CrawlTarget, error) {
	targets, err := s.store.ListCrawlTargets(ctx)
	if err != nil {
		return nil, fmt.Errorf("list crawl targets: %w", err)
	}
	return targets, nil
}

func (s *Service) rows(ctx context.Context, props []domain.Property) ([]Row, error) {
	ids := xslices.Map(props, func(p domain.Property) domain.PropertyID { return p.ID })
	drops, err := s.store.PriceDrops(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("query price drops: %w", err)
	}
	counts, err := s.store.PhotoCounts(ctx, ids)
	if err != nil {
		return nil, fmt.Errorf("query photo counts: %w", err)
	}
	return xslices.Map(props, func(p domain.Property) Row {
		c := counts[p.ID]
		return Row{
			ID:                 p.ID,
			Address:            p.Address.String(),
			Neighborhood:       p.Address.Neighborhood,
			Price:              ptr.Clone(p.Price),
			Beds:               ptr.Clone(p.Bedrooms),
			Baths:              ptr.Clone(p.Bathrooms),
			Sqft:               ptr.Clone(p.Sqft),
			PropertyType:       p.PropertyType,
			ListingType:        p.ListingType,
			MonthlyCarrying:    p.MonthlyCarrying(),
			DOM:                ptr.Clone(p.DaysOnMarket),
			DescriptionExcerpt: Excerpt(p.Description, ExcerptRunes),
			PriceDrop:          drops[p.ID],
			Status:             p.Status,
			URL:                p.URL,
			PhotoCount:         c.Total,
			PhotosCached:       c.Cached,
		}
	}), nil
}

// PhotoKeyAt resolves the cached_path of the listing's n-th cached photo (dense: 0..photos_cached-1) for the public resolver GET /img/l/{id}/{n}. Any miss (unknown listing, past the end) is ErrNotFound; a store outage is passed through so the transport can answer 500 rather than a misleading 404.
func (s *Service) PhotoKeyAt(ctx context.Context, id domain.PropertyID, position int) (string, error) {
	if id <= 0 || position < 0 {
		return "", ErrNotFound
	}
	key, err := s.store.PhotoKeyAt(ctx, id, position)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("photo key for listing %d position %d: %w", id, position, err)
	}
	return key, nil
}

// PhotosForListings returns the bulk photo summary for up to MaxBulkPhotoListings listings (total/cached counts), in input order with duplicates removed. A pure DB read (no photo bytes) so a caller mints every gallery URL for many listings in one call. Unknown ids come back with zero counts.
func (s *Service) PhotosForListings(ctx context.Context, ids []domain.PropertyID) ([]ListingPhotos, error) {
	unique := distinct(ids)
	if len(unique) > MaxBulkPhotoListings {
		unique = unique[:MaxBulkPhotoListings]
	}
	counts, err := s.store.PhotoCounts(ctx, unique)
	if err != nil {
		return nil, fmt.Errorf("query photo counts: %w", err)
	}
	out := make([]ListingPhotos, 0, len(unique))
	for _, id := range unique {
		c := counts[id]
		out = append(out, ListingPhotos{ID: id, Total: c.Total, Cached: c.Cached})
	}
	return out, nil
}
