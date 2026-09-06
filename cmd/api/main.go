// Command apid serves the Property Radar REST API over internal/listings.Service.
// It does not migrate — mcpd owns migrations; apid assumes the schema is current.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/davidteather/property-radar/internal/config"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/restapi"
	"github.com/davidteather/property-radar/internal/shared/logging"
	"github.com/davidteather/property-radar/internal/store"
	"github.com/davidteather/property-radar/internal/streeteasy"
)

const (
	pingTimeout       = 10 * time.Second
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 15 * time.Second
	defaultAddr       = ":8080"
)

func main() {
	logger := logging.New(false)
	slog.SetDefault(logger)

	if err := run(logger); err != nil {
		logger.Error("apid exited", "error", err)
		os.Exit(1)
	}
}

func run(logger *slog.Logger) error {
	var addr string
	flag.StringVar(&addr, "addr", defaultAddr, "listen address; $PORT applies only when -addr is not passed, an explicit -addr always wins over $PORT")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	context.AfterFunc(ctx, stop) // a second signal during shutdown kills, not waits

	cfg, err := config.Load()
	if err != nil {
		return err
	}
	// Refuse to start without the shared bearer token: an empty token would authorize an empty header.
	if cfg.MCPBearerToken == "" {
		return errors.New("MCP_BEARER_TOKEN is required")
	}

	pool, err := pgxpool.New(ctx, cfg.DatabaseURL)
	if err != nil {
		return fmt.Errorf("open database pool: %w", err)
	}
	defer pool.Close()

	pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
	defer cancel()
	if err := pool.Ping(pingCtx); err != nil {
		return fmt.Errorf("ping database: %w", err)
	}

	photos, err := photostore.New(ctx, cfg.PhotoStore())
	if err != nil {
		return fmt.Errorf("init photo store: %w", err)
	}

	svc := listings.NewService(store.New(pool), photos)
	if areas, err := streeteasy.Areas(); err != nil {
		logger.Warn("area catalog unavailable; area resolution will return no matches", "err", err)
	} else {
		svc.LoadAreas(areas)
	}
	handler := restapi.NewHandler(svc, cfg.MCPBearerToken, cfg.PublicImgToken, cfg.PublicBaseURL)
	for _, w := range cfg.Warnings(true) {
		logger.Warn(w)
	}

	listenAddr := resolveAddr(flagSet("addr"), addr, cfg.Port)
	// The token is deliberately never logged.
	logger.Info("apid serving", "addr", listenAddr, "storage_backend", cfg.StorageBackend)
	return serve(ctx, listenAddr, handler)
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
		return fmt.Errorf("serve api http: %w", err)
	case <-ctx.Done():
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down api http server: %w", err)
	}
	<-serveErr
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
