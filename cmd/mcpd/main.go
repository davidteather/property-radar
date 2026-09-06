package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"html"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/pressly/goose/v3/lock"

	"github.com/davidteather/property-radar/internal/bearerauth"
	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/mcp"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/publicurl"
	"github.com/davidteather/property-radar/internal/restapi"
	"github.com/davidteather/property-radar/internal/shared/logging"
	"github.com/davidteather/property-radar/internal/shared/retry"
	"github.com/davidteather/property-radar/internal/store"
	"github.com/davidteather/property-radar/internal/streeteasy"
	"github.com/davidteather/property-radar/migrations"
)

const (
	pingTimeout       = 10 * time.Second
	startupBudget     = 2 * time.Minute
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 15 * time.Second
	transportStdio    = "stdio"
	transportHTTP     = "http"
	defaultAddr       = ":8787"
)

type options struct {
	transport string
	addr      string
	migrate   bool
	rest      bool
	urlToken  bool
}

func main() {
	if err := run(); err != nil {
		slog.New(slog.NewTextHandler(os.Stderr, nil)).Error("mcpd exited", "error", err)
		os.Exit(1)
	}
}

func run() error {
	var opt options
	flag.StringVar(&opt.transport, "transport", transportStdio, "stdio or http")
	flag.StringVar(&opt.addr, "addr", defaultAddr, "listen address for -transport http; $PORT applies only when -addr is not passed, an explicit -addr always wins over $PORT")
	flag.BoolVar(&opt.migrate, "migrate", false, "apply embedded goose migrations before serving; defaults to MIGRATE_ON_START, disable only when migrating out of band")
	flag.BoolVar(&opt.rest, "rest", true, "also serve the REST API and /docs on the same http port; -transport http only")
	flag.BoolVar(&opt.urlToken, "url-token", false, "also accept the bearer token in a ?token= query param so URL-only clients (claude.ai web connectors) can connect; token then appears in logs/URLs")
	flag.Parse()

	if opt.transport != transportStdio && opt.transport != transportHTTP {
		return fmt.Errorf("-transport must be %s or %s, got %q", transportStdio, transportHTTP, opt.transport)
	}

	// Only stdio mode reserves stdout for the MCP protocol; http mode logs with
	// the normal stdout/stderr level split.
	logger := logging.New(opt.transport == transportStdio)
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal during shutdown kills, not waits

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	if opt.transport == transportHTTP && cfg.MCPBearerToken == "" {
		return errors.New("MCP_BEARER_TOKEN is required for -transport http")
	}

	// -migrate defaults to cfg.MigrateOnStart unless the operator set it.
	migrate := opt.migrate
	if !flagSet("migrate") {
		migrate = cfg.MigrateOnStart
	}

	// mcpd owns migrations: a server on a partial schema is worse than a failed
	// deploy. The crawler must not migrate (it starts concurrently).
	if migrate {
		if err := migrateUp(ctx, logger, cfg.DatabaseURL); err != nil {
			return fmt.Errorf("apply migrations: %w", err)
		}
	} else if err := requireSchema(ctx, logger, cfg.DatabaseURL); err != nil {
		return err
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	// Postgres and the S3 gateway may still be rolling on a co-deploy; retry
	// for a couple of minutes before giving the platform a crash to restart.
	err = retry.Do(ctx, logger, "ping database", startupBudget, func(ctx context.Context) error {
		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		defer cancel()
		return pool.Ping(pingCtx)
	})
	if err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	var photos photostore.Store
	err = retry.Do(ctx, logger, "init photo store", startupBudget, func(ctx context.Context) (err error) {
		photos, err = photostore.New(ctx, cfg.PhotoStore())
		return err
	})
	if err != nil {
		return fmt.Errorf("init photo store: %w", err)
	}

	st := store.New(pool)
	// A fresh install starts with no scopes (the LLM onboards via request_crawl);
	// SEED_CRAWL_AREAS optionally pre-seeds one for a headless operator.
	if len(cfg.SeedCrawlAreas) > 0 {
		seedAreas, err := listings.ParseAreaIDs(cfg.SeedCrawlAreas)
		if err != nil {
			return fmt.Errorf("SEED_CRAWL_AREAS: %w", err)
		}
		seeded, err := st.SeedCrawlTargets(ctx, []domain.CrawlTarget{{
			Kind: domain.TargetStanding, Areas: seedAreas,
			ListingType: domain.ListingSale, Enabled: true, Note: "seeded from SEED_CRAWL_AREAS",
		}})
		if err != nil {
			return fmt.Errorf("seed crawl targets: %w", err)
		}
		if seeded > 0 {
			logger.Info("seeded crawl targets from SEED_CRAWL_AREAS", "count", seeded, "areas", seedAreas)
		}
	}
	if err := st.EnsureDefaultList(ctx); err != nil {
		return fmt.Errorf("ensure default favorites list: %w", err)
	}

	svc := listings.NewService(st, photos)
	if areas, err := streeteasy.Areas(); err != nil {
		logger.Warn("area catalog unavailable; resolve_areas will return no matches", "err", err)
	} else {
		svc.LoadAreas(areas)
	}
	server := mcp.NewServerFromService(svc,
		mcp.WithPublicBaseURL(cfg.PublicBaseURL),
		mcp.WithPublicImageToken(cfg.PublicImgToken),
		mcp.WithConsoleURL(cfg.ConsoleURL))
	if opt.transport == transportHTTP {
		for _, w := range cfg.Warnings(true) {
			logger.Warn(w)
		}
		addr := resolveAddr(flagSet("addr"), opt.addr, cfg.Port)
		urlToken := opt.urlToken
		if !flagSet("url-token") {
			urlToken = cfg.URLTokenAuth
		}
		logger.Info("mcpd serving http", "addr", addr, "storage_backend", cfg.StorageBackend, "rest", opt.rest, "url_token", urlToken)
		return serve(ctx, addr, combinedHandler(server, svc, cfg.MCPBearerToken, cfg.PublicImgToken, cfg.PublicBaseURL, opt.rest, urlToken, logger))
	}
	// No listener here: tools mint /img links against PUBLIC_BASE_URL instead
	// (Warnings flags it when unset).
	for _, w := range cfg.Warnings(false) {
		logger.Warn(w)
	}
	if cfg.StorageBackend == photostore.BackendLocal {
		if _, err := os.Stat(cfg.ThumbsDir); err != nil {
			logger.Warn("local thumbs dir does not exist; photos will read as uncached until a crawler writes to it — a compose stack keeps them in S3, so set STORAGE_BACKEND=s3 here too", "dir", cfg.ThumbsDir)
		}
	}
	logger.Info("mcpd serving on stdio", "storage_backend", cfg.StorageBackend)
	if err := server.RunStdio(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// combinedHandler serves MCP (at the root) plus either the full REST surface or
// just /healthz and the photo proxy; ServeMux yields "/" to the more specific paths.
func combinedHandler(server *mcp.Server, svc *listings.Service, token, imgToken, publicBase string, rest, urlToken bool, logger *slog.Logger) http.Handler {
	gate := bearerauth.Middleware
	if urlToken {
		gate = bearerauth.MiddlewareAllowQuery
	}
	mux := http.NewServeMux()
	if rest {
		restapi.Register(mux, svc, token, imgToken)
		mux.HandleFunc("GET /connect", connectPage)
	} else {
		mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "text/plain; charset=utf-8")
			_, _ = w.Write([]byte("ok\n"))
		})
		restapi.RegisterImages(mux, svc, token, imgToken)
	}
	mux.Handle("/", gate(token)(server.StreamHandler()))
	// publicurl stamps each request with its origin so tools can mint absolute /img photo links.
	return bearerauth.LogRequests(logger)(publicurl.Middleware(publicBase)(mux))
}

// connectPage is an open onboarding page (connect command + docs link); never the token.
func connectPage(w http.ResponseWriter, r *http.Request) {
	base := html.EscapeString(publicurl.FromContext(r.Context()))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'")
	_, _ = fmt.Fprintf(w, `<!doctype html><meta charset=utf-8><title>Connect Property Radar</title>
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
<style>body{font:16px system-ui;max-width:44rem;margin:3rem auto;padding:0 1rem;line-height:1.5}code,pre{background:#f4f4f5;border-radius:6px}pre{padding:1rem;overflow:auto}</style>
<h1>Connect Property Radar</h1>
<h2>Claude Code (MCP)</h2>
<p>Run this, replacing <code>YOUR_TOKEN</code> with your bearer token:</p>
<pre>claude mcp add property-radar --transport http %s \
  --header "Authorization: Bearer YOUR_TOKEN" --scope user</pre>
<h2>REST API</h2>
<p>Explore and try every endpoint in your browser at <a href="/docs">%s/docs</a> — click <b>Authorize</b>, paste the same token.</p>
<p>Health check (no token): <a href="/healthz">/healthz</a>.</p>
`, base, base)
}

func serve(ctx context.Context, addr string, handler http.Handler) error {
	srv := &http.Server{
		Addr:              addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", addr, err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve mcpd http: %w", err)
	case <-ctx.Done():
	}

	// The signal context is already done, so shutdown gets its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down mcpd http server: %w", err)
	}
	<-serveErr
	return nil
}

// migrateUp applies the embedded goose migrations under goose's advisory lock;
// a no-op when already current.
// requireSchema is the MIGRATE_ON_START=false path: serving on a schema older
// than the binary fails every query later, so refuse up front instead.
func requireSchema(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	current, target, err := migrations.Versions(ctx, databaseURL)
	if err != nil {
		return fmt.Errorf("check schema version: %w", err)
	}
	if current < target {
		return fmt.Errorf("database at schema version %d, this binary needs %d: apply migrations (MIGRATE_ON_START=true or goose up) before serving with migrations off", current, target)
	}
	if current > target {
		logger.Warn("database schema is newer than this binary (rolled back?)", "database", current, "binary", target)
	}
	return nil
}

func migrateUp(ctx context.Context, logger *slog.Logger, databaseURL string) error {
	db, err := sql.Open("pgx", databaseURL)
	if err != nil {
		return fmt.Errorf("open database: %w", err)
	}
	defer func() { _ = db.Close() }()

	locker, err := lock.NewPostgresSessionLocker()
	if err != nil {
		return fmt.Errorf("migration lock: %w", err)
	}
	provider, err := goose.NewProvider(goose.DialectPostgres, db, migrations.FS, goose.WithSessionLocker(locker))
	if err != nil {
		return fmt.Errorf("load migrations: %w", err)
	}
	results, err := provider.Up(ctx)
	if err != nil {
		return err
	}
	if len(results) == 0 {
		logger.Info("schema already current")
		return nil
	}
	applied := make([]int64, 0, len(results))
	for _, r := range results {
		applied = append(applied, r.Source.Version)
	}
	logger.Info("migrations applied", "versions", applied)
	return nil
}

func flagSet(name string) bool {
	set := false
	flag.Visit(func(f *flag.Flag) {
		if f.Name == name {
			set = true
		}
	})
	return set
}

// PaaS platforms inject $PORT (cfg.Port); it applies only when -addr was not passed.
func resolveAddr(addrExplicit bool, addr, port string) string {
	port = strings.TrimSpace(port)
	if addrExplicit || port == "" {
		return addr
	}
	return ":" + port
}
