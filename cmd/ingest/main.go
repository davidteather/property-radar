package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"os"
	"os/signal"
	"slices"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/shared/logging"
	"github.com/davidteather/property-radar/internal/shared/retry"
	"github.com/davidteather/property-radar/internal/shared/xslices"
	"github.com/davidteather/property-radar/internal/store"
	"github.com/davidteather/property-radar/internal/streeteasy"
	"github.com/davidteather/property-radar/migrations"
)

type options struct {
	areas         string
	fromTargets   bool
	standing      bool
	listingType   string
	maxPrice      int
	minBeds       int
	photoCap      int
	photoQuality  int
	timeout       time.Duration
	delay         time.Duration
	proxy         bool
	proxyMode     string
	watch         bool
	poll          time.Duration
	standingEvery time.Duration
}

// Flags that describe one ad-hoc scope; stored targets carry their own.
var scopeFlags = []string{"areas", "listing-type", "max-price", "min-beds", "standing"}

func main() {
	logger := logging.New(false)
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("ingest failed", "err", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	var opt options
	flag.StringVar(&opt.areas, "areas", "", "comma-separated provider area ids (one of -areas or -from-targets is required)")
	flag.BoolVar(&opt.fromTargets, "from-targets", false, "crawl every due crawl_targets row instead of one -areas scope")
	flag.BoolVar(&opt.standing, "standing", false, "treat the -areas run as a standing scope that owns absence (may delist listings it no longer sees); default is a one-off that only adds (-areas only)")
	flag.StringVar(&opt.listingType, "listing-type", "sale", "sale or rent (-areas only)")
	flag.IntVar(&opt.maxPrice, "max-price", 0, "maximum sale price in dollars; 0 means no cap (-areas only)")
	flag.IntVar(&opt.minBeds, "min-beds", 0, "minimum bedrooms; 0 means no minimum (-areas only)")
	flag.IntVar(&opt.photoCap, "photo-cap", ingest.DefaultPhotoCap, "thumbnails cached per listing")
	flag.IntVar(&opt.photoQuality, "photo-quality", 0, "thumbnail JPEG quality 1-100; 0 uses the default (70)")
	flag.DurationVar(&opt.timeout, "timeout", defaultTimeout, "whole-run deadline")
	flag.DurationVar(&opt.delay, "delay", 1500*time.Millisecond, "minimum spacing between provider requests")
	flag.BoolVar(&opt.watch, "watch", false, "run as a long-lived worker: drain due targets, then wait for a crawl_due notification or -poll; -from-targets only")
	flag.DurationVar(&opt.poll, "poll", time.Minute, "poll fallback interval between drains in -watch mode")
	flag.DurationVar(&opt.standingEvery, "standing-every", ingest.DefaultStandingInterval, "minimum spacing between runs of one standing scope; 0 re-crawls every drain")
	flag.BoolVar(&opt.proxy, "proxy", false, "deprecated alias for -proxy-mode split")
	flag.StringVar(&opt.proxyMode, "proxy-mode", streeteasy.ProxyOff, "off (direct), split (site pages via proxy, API direct), or all (API + site pages via proxy; required for residential cloud crawling; see docs/decisions.md)")
	flag.Parse()

	proxyMode, err := resolveProxyMode(opt)
	if err != nil {
		return err
	}

	areas := xslices.SplitCSV(opt.areas)
	if opt.fromTargets == (len(areas) > 0) {
		flag.Usage()
		return errors.New("exactly one of -areas or -from-targets is required")
	}
	if len(areas) > 0 {
		if areas, err = listings.ParseAreaIDs(areas); err != nil {
			return fmt.Errorf("-areas: %w", err)
		}
	}
	if opt.fromTargets {
		if given := givenScopeFlags(); len(given) > 0 {
			return fmt.Errorf("-from-targets takes its scope from the database; remove -%s", strings.Join(given, ", -"))
		}
	}
	if opt.watch && !opt.fromTargets {
		return errors.New("-watch requires -from-targets")
	}
	if opt.timeout <= 0 || opt.poll <= 0 {
		return errors.New("-timeout and -poll must be positive durations")
	}
	listingType, err := domain.ParseListingType(opt.listingType)
	if err != nil {
		return err
	}
	if opt.maxPrice < 0 || opt.maxPrice > listings.MaxMoney || opt.minBeds < 0 || opt.minBeds > listings.MaxBeds {
		return fmt.Errorf("-max-price must be 0..%d and -min-beds 0..%d", listings.MaxMoney, listings.MaxBeds)
	}

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal during shutdown kills, not waits
	// A watch worker lives until signalled; -timeout then bounds each drain, not
	// the process.
	if !opt.watch {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, opt.timeout)
		defer cancel()
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open postgres pool: %w", err)
	}
	defer pool.Close()
	// A watch worker waits for its dependencies (they roll together on a
	// co-deploy); a one-shot fails fast so an operator sees the cause.
	if err := startupStep(ctx, logger, "ping database", opt.watch, pool.Ping); err != nil {
		return fmt.Errorf("connect to postgres (is `make db-up` up and are migrations applied?): %w", err)
	}
	if err := waitForSchema(ctx, logger, cfg.DatabaseURL, opt.watch); err != nil {
		return err
	}
	st := store.New(pool)

	// Cloud crawling needs residential proxies; with none configured a proxied run
	// only PX-blocks, so skip it — recording an incomplete run (never a baseline or
	// delisting input) so `lst status` and the API show why nothing crawled.
	if proxyMode != streeteasy.ProxyOff && cfg.WebshareAPIKey == "" {
		reason := fmt.Sprintf("WEBSHARE_API_KEY not set: -proxy-mode %s needs a residential Webshare plan (docs/decisions.md); crawl from your own machine instead", proxyMode)
		logger.Error("crawl skipped", "reason", reason, "proxy_mode", proxyMode)
		if err := recordSkippedRun(ctx, st, reason); err != nil {
			logger.Error("record skipped run", "err", err)
		}
		if opt.watch {
			// Idle instead of exiting so an always-on worker deploy does not
			// crash-loop; the key only appears with a restart anyway.
			<-ctx.Done()
		}
		return nil
	}

	var transport http.RoundTripper = http.DefaultTransport.(*http.Transport).Clone()
	if proxyMode != streeteasy.ProxyOff {
		proxies, err := fetchProxies(ctx, logger, cfg.WebshareAPIKey, opt.watch)
		if err != nil {
			return fmt.Errorf("fetch webshare proxies: %w", err)
		}
		// Proxy URLs embed credentials: log the count and mode, never the URLs.
		pool := streeteasy.NewProxyPool(proxies)
		transport, err = streeteasy.NewProxyTransport(transport.(*http.Transport), proxyMode, pool)
		if err != nil {
			return err
		}
		logger.Info("proxy rotation enabled", "mode", proxyMode, "proxies", len(proxies))
		if opt.watch {
			go refreshProxies(ctx, logger, cfg.WebshareAPIKey, pool)
		}
	}

	provider := streeteasy.NewClient(
		&http.Client{Timeout: 60 * time.Second, Transport: transport},
		streeteasy.Config{Delay: opt.delay},
	)
	var photos photostore.Store
	err = startupStep(ctx, logger, "init photo store", opt.watch, func(ctx context.Context) (err error) {
		photos, err = photostore.New(ctx, cfg.PhotoStore())
		return err
	})
	if err != nil {
		return fmt.Errorf("init photo store: %w", err)
	}
	thumbs := ingest.NewThumbnailer(&http.Client{Timeout: 30 * time.Second}, photos,
		ingest.ThumbConfig{Quality: opt.photoQuality})
	standingEvery := opt.standingEvery
	if standingEvery <= 0 {
		standingEvery = -1
	}
	// Half the deadline goes to detail pages so a scope of any size finishes
	// as a complete run instead of being cut off mid-enrich.
	svc := ingest.NewService(provider, st, thumbs, ingest.Options{
		PhotoCap: opt.photoCap, Logger: logger, StandingEvery: standingEvery, EnrichFor: opt.timeout / 2,
	})

	if opt.fromTargets {
		if opt.watch {
			return watchTargets(ctx, logger, svc, st, opt.poll, opt.timeout, cfg.PhotoTTL)
		}
		recoverStaleTargets(ctx, logger, st, staleAfter(opt.timeout))
		err := runTargets(ctx, logger, svc, st)
		// The photo TTL sweep is maintenance, independent of crawl success.
		if _, sweepErr := svc.SweepExpiredPhotos(ctx, cfg.PhotoTTL); sweepErr != nil {
			logger.Warn("photo ttl sweep", "err", sweepErr)
		}
		return err
	}

	q := ingest.SearchQuery{
		ListingType: listingType,
		MaxPrice:    domain.Money(opt.maxPrice),
		MinBeds:     opt.minBeds,
		Areas:       areas,
		OneOff:      !opt.standing,
	}
	logger.Info("ingest starting", "provider", provider.Name(), "scope_hash", ingest.ScopeHash(provider.Name(), q),
		"listing_type", listingType, "areas", areas, "max_price", opt.maxPrice, "min_beds", opt.minBeds, "one_off", q.OneOff,
		"photo_cap", opt.photoCap, "storage_backend", cfg.StorageBackend)

	res, runErr := svc.Run(ctx, q)
	logSummary(logger, res)
	return ingest.RunOutcome(res, runErr)
}

// recordSkippedRun persists a config-skip as an incomplete run so the operator sees
// why a crawl did nothing. Incomplete runs never feed the baseline or advance delisting.
func recordSkippedRun(ctx context.Context, st *store.Store, reason string) error {
	run, err := st.CreateRun(ctx, streeteasy.ProviderName, "preflight", false)
	if err != nil {
		return err
	}
	return st.FinishRun(ctx, run.ID, ingest.RunStats{
		ItemErrors: []domain.RunError{{Message: reason}},
	})
}

// pruneAfter is the TTL for settled once targets (the async job rows).
const pruneAfter = 7 * 24 * time.Hour

// photoSweepEvery bounds how often the always-on worker runs the photo TTL
// sweep; per-drain would be wasteful on a notification-driven worker.
const photoSweepEvery = time.Hour

const firstSweepDelay = 5 * time.Minute

// watchTargets is the always-on worker loop: drain due targets, prune settled
// jobs, sweep expired photo cache, then sleep on the crawl_due LISTEN (poll
// fallback). Drain failures are logged, never fatal.
func watchTargets(ctx context.Context, logger *slog.Logger, svc *ingest.Service, st *store.Store, poll, drainTimeout, photoTTL time.Duration) error {
	listener, err := st.ListenCrawlDue(ctx)
	if err != nil {
		logger.Warn("crawl_due listen unavailable; polling until it returns", "err", err)
	}
	defer func() {
		if listener != nil {
			listener.Close()
		}
	}()
	logger.Info("crawl worker watching", "poll", poll, "drain_timeout", drainTimeout, "photo_ttl", photoTTL.String())

	// The first sweep waits a few minutes (skipping the busy first drain) rather
	// than a full interval, or a worker redeployed hourly would never sweep.
	lastSweep := time.Now().Add(firstSweepDelay - photoSweepEvery)
	for {
		recoverStaleTargets(ctx, logger, st, staleAfter(drainTimeout))
		drainCtx, cancel := context.WithTimeout(ctx, drainTimeout)
		if err := runTargets(drainCtx, logger, svc, st); err != nil {
			logger.Error("drain failed", "err", err)
		}
		cancel()

		if ctx.Err() != nil {
			return nil
		}
		if pruned, err := st.PruneCrawlTargets(ctx, pruneAfter); err != nil {
			logger.Warn("prune crawl targets", "err", err)
		} else if pruned > 0 {
			logger.Info("pruned settled crawl targets", "count", pruned)
		}

		if photoTTL > 0 && time.Since(lastSweep) >= photoSweepEvery {
			if _, err := svc.SweepExpiredPhotos(ctx, photoTTL); err != nil {
				logger.Warn("photo ttl sweep", "err", err)
			}
			lastSweep = time.Now()
		}
		if ctx.Err() != nil {
			return nil
		}
		// Without a listener each poll retries LISTEN, so a Postgres blip does
		// not leave the worker on slow polling for the rest of its life.
		if listener == nil {
			if listener, err = st.ListenCrawlDue(ctx); err != nil {
				listener = nil
			} else {
				logger.Info("crawl_due listen restored")
			}
		}
		if listener == nil {
			select {
			case <-time.After(poll):
			case <-ctx.Done():
				return nil
			}
			continue
		}
		if err := listener.Wait(ctx, poll); err != nil {
			if ctx.Err() != nil {
				return nil
			}
			logger.Warn("crawl_due wait failed; will re-listen", "err", err)
			listener.Close()
			listener = nil
			select {
			case <-time.After(poll):
			case <-ctx.Done():
				return nil
			}
		}
	}
}

// fetchProxies loads the Webshare proxy list; a watch worker retries with
// backoff so a Webshare blip at boot does not crash-loop the deployment.
func fetchProxies(ctx context.Context, logger *slog.Logger, apiKey string, watch bool) ([]*url.URL, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	var proxies []*url.URL
	fetch := func(ctx context.Context) (err error) {
		proxies, err = streeteasy.FetchWebshareProxies(ctx, client, streeteasy.WebshareConfig{APIKey: apiKey})
		return err
	}
	// Assign before returning: Go does not fix the order of a variable read
	// against a call in the same return statement.
	var err error
	if !watch {
		err = fetch(ctx)
	} else {
		err = retry.Do(ctx, logger, "fetch webshare proxies", 0, fetch)
	}
	return proxies, err
}

// proxyRefreshEvery bounds how stale an always-on worker's proxy list gets;
// Webshare replaces list proxies over time and a dead pool would only PX-block.
const proxyRefreshEvery = 6 * time.Hour

// refreshProxies periodically swaps in a fresh proxy list; a failed refresh keeps the current one.
func refreshProxies(ctx context.Context, logger *slog.Logger, apiKey string, pool *streeteasy.ProxyPool) {
	client := &http.Client{Timeout: 30 * time.Second}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(proxyRefreshEvery):
		}
		proxies, err := streeteasy.FetchWebshareProxies(ctx, client, streeteasy.WebshareConfig{APIKey: apiKey})
		if err != nil {
			logger.Warn("refresh webshare proxies; keeping the current list", "err", err)
			continue
		}
		pool.Replace(proxies)
		healthy, total := pool.Healthy()
		logger.Info("proxy list refreshed", "proxies", total, "healthy", healthy)
	}
}

// startupStep runs one dependency check: a watch worker retries until
// signalled, a one-shot gets a single attempt.
func startupStep(ctx context.Context, logger *slog.Logger, what string, watch bool, fn func(context.Context) error) error {
	if !watch {
		return fn(ctx)
	}
	return retry.Do(ctx, logger, what, 0, fn)
}

// waitForSchema keeps the crawler off a schema older than its binary: on a
// co-deploy mcpd migrates while the crawler boots, and a drain on the old
// columns would misrecord targets. A watch worker waits; a one-shot fails.
func waitForSchema(ctx context.Context, logger *slog.Logger, databaseURL string, watch bool) error {
	check := func(ctx context.Context) error {
		current, target, err := migrations.Versions(ctx, databaseURL)
		if err != nil {
			return err
		}
		if current < target {
			return fmt.Errorf("database at schema version %d, this crawler needs %d (mcpd applies migrations on start)", current, target)
		}
		if current > target {
			logger.Warn("database schema is newer than this crawler", "database", current, "binary", target)
		}
		return nil
	}
	if !watch {
		return check(ctx)
	}
	return retry.Do(ctx, logger, "schema check", 0, check)
}

// A target still running past one whole-run deadline belongs to a crawler that
// died; settle it so the queue and the agent's status view move on.
// defaultTimeout bounds one drain; it is also the floor of the stale window, so
// a short -timeout one-shot never "recovers" a target another crawler is
// legitimately still running under the default.
const defaultTimeout = 2 * time.Hour

func staleAfter(timeout time.Duration) time.Duration { return max(timeout, defaultTimeout) }

func recoverStaleTargets(ctx context.Context, logger *slog.Logger, st *store.Store, olderThan time.Duration) {
	n, err := st.RecoverStaleTargets(ctx, olderThan)
	if err != nil {
		logger.Warn("recover stale crawl targets", "err", err)
	} else if n > 0 {
		logger.Warn("recovered stale running crawl targets", "count", n)
	}
}

// One bad target does not stop the drain, but any failed/incomplete/suspect target
// fails the whole invocation so a one-shot caller notices.
func runTargets(ctx context.Context, logger *slog.Logger, svc *ingest.Service, targets ingest.TargetStore) error {
	res, err := svc.RunTargets(ctx, targets)
	for _, outcome := range res.Outcomes {
		logSummary(logger, outcome.Result)
		logger.Info("crawl target finished",
			"target_id", int64(outcome.Target.ID),
			"kind", string(outcome.Target.Kind),
			"areas", outcome.Target.Areas,
			"listing_type", string(outcome.Target.ListingType),
			"run_id", int64(outcome.Result.Run.ID),
			"ok", outcome.Err == nil,
			"err", outcome.Err,
		)
	}
	level := slog.LevelInfo
	if len(res.Outcomes) == 0 {
		level = slog.LevelDebug
	}
	logger.Log(ctx, level, "crawl targets drained", "targets", len(res.Outcomes), "failed", res.Failed())

	if err != nil {
		return err
	}
	if failed := res.Failed(); failed > 0 {
		return fmt.Errorf("%d of %d crawl targets failed", failed, len(res.Outcomes))
	}
	return nil
}

// resolveProxyMode folds the deprecated -proxy bool into -proxy-mode: -proxy
// means split, and it may not contradict an explicit -proxy-mode.
func resolveProxyMode(opt options) (string, error) {
	mode := opt.proxyMode
	switch mode {
	case streeteasy.ProxyOff, streeteasy.ProxySplit, streeteasy.ProxyAll:
	default:
		return "", fmt.Errorf("-proxy-mode must be %s, %s, or %s, got %q",
			streeteasy.ProxyOff, streeteasy.ProxySplit, streeteasy.ProxyAll, mode)
	}
	if opt.proxy {
		if modeSet() && mode != streeteasy.ProxySplit {
			return "", fmt.Errorf("-proxy is a deprecated alias for -proxy-mode split; do not combine it with -proxy-mode %s", mode)
		}
		mode = streeteasy.ProxySplit
	}
	return mode, nil
}

func modeSet() bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == "proxy-mode" {
			set = true
		}
	})
	return set
}

func givenScopeFlags() []string {
	var given []string
	flag.Visit(func(f *flag.Flag) {
		if slices.Contains(scopeFlags, f.Name) {
			given = append(given, f.Name)
		}
	})
	return given
}

func logSummary(logger *slog.Logger, res ingest.Result) {
	logger.Info("ingest finished",
		"run_id", int64(res.Run.ID),
		"complete", res.Run.Complete,
		"suspect", res.Run.Suspect,
		"listings_seen", res.Run.ListingsSeen,
		"created", res.Run.Created,
		"updated", res.Run.Updated,
		"photo_failures", res.Run.PhotoFailures,
		"item_errors", len(res.Run.ItemErrors),
		"baseline_volume", res.Baseline.Volume,
		"baseline_runs", res.Baseline.Runs,
		"missing_advanced", res.Missing.Advanced,
		"delisted", res.Missing.Delisted,
	)
}
