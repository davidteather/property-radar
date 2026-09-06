package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/bearerauth"
	"github.com/davidteather/property-radar/internal/publicurl"
)

const (
	readHeaderTimeout = 10 * time.Second
	readTimeout       = 30 * time.Second
	writeTimeout      = 2 * time.Minute
	idleTimeout       = 2 * time.Minute
	shutdownTimeout   = 15 * time.Second
)

type HTTPConfig struct {
	Addr string
	// BearerToken is required; a secret, never logged.
	BearerToken string
	// PublicBaseURL optionally overrides the origin for public /img links; empty derives it from each request.
	PublicBaseURL string
	Logger        *slog.Logger
}

// StreamHandler returns the raw stateless streamable MCP handler with no auth and no mux, so a combined server can bearer-wrap and mount it at the root.
func (s *Server) StreamHandler() http.Handler {
	return mcpsdk.NewStreamableHTTPHandler(
		func(*http.Request) *mcpsdk.Server { return s.mcp },
		&mcpsdk.StreamableHTTPOptions{
			Stateless: true, JSONResponse: true,
			// The bearer gate already defeats DNS rebinding; the SDK's Host check
			// would 403 a host binary fronted by a same-host reverse proxy.
			DisableLocalhostProtection: true,
		},
	)
}

// HTTPHandler serves stateless streamable MCP behind bearer auth, plus an unauthenticated GET /healthz so uptime probes need no secret.
func (s *Server) HTTPHandler(cfg HTTPConfig) (http.Handler, error) {
	if strings.TrimSpace(cfg.BearerToken) == "" {
		return nil, errors.New("mcp http transport requires a bearer token")
	}
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		_, _ = w.Write([]byte("ok\n"))
	})
	mux.Handle("/", bearerauth.Middleware(cfg.BearerToken)(s.StreamHandler()))
	return bearerauth.LogRequests(logger)(publicurl.Middleware(cfg.PublicBaseURL)(mux)), nil
}

func (s *Server) RunHTTP(ctx context.Context, cfg HTTPConfig) error {
	handler, err := s.HTTPHandler(cfg)
	if err != nil {
		return err
	}
	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadHeaderTimeout: readHeaderTimeout,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}

	listener, err := net.Listen("tcp", cfg.Addr)
	if err != nil {
		return fmt.Errorf("listen on %s: %w", cfg.Addr, err)
	}

	serveErr := make(chan error, 1)
	go func() { serveErr <- srv.Serve(listener) }()

	select {
	case err := <-serveErr:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve mcp http: %w", err)
	case <-ctx.Done():
	}

	// The signal context is already done, so shutdown gets its own deadline.
	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("shut down mcp http server: %w", err)
	}
	<-serveErr
	return nil
}
