// Package bearerauth holds HTTP transport middleware shared by the MCP and REST servers: a constant-time bearer-token gate and a header-free request logger. It lives outside internal/shared, which must not depend on transport concerns.
package bearerauth

import (
	"crypto/sha256"
	"crypto/subtle"
	"log/slog"
	"net/http"
	"strings"
	"time"
)

// realm labels the WWW-Authenticate challenge; it is not a secret.
const realm = `Bearer realm="property-radar"`

// Middleware returns a bearer-token gate: constant-time compare (SHA-256 of both sides, so cost never leaks token length), answering a missing or wrong token with 401 plus a WWW-Authenticate challenge. token must be non-empty (an empty token would authorize an empty header; callers validate it first).
func Middleware(token string) func(http.Handler) http.Handler {
	return middleware(token, false)
}

// MiddlewareAllowQuery is Middleware that also accepts the token in a ?token= query parameter, for URL-only MCP clients (the claude.ai custom-connector dialog) that cannot send a header. The token then appears in URLs and logs, so it is opt-in.
func MiddlewareAllowQuery(token string) func(http.Handler) http.Handler {
	return middleware(token, true)
}

func middleware(token string, allowQuery bool) func(http.Handler) http.Handler {
	// Hashing both sides keeps the compare constant-time regardless of presented length.
	want := sha256.Sum256([]byte(token))
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			presented := parseBearer(r.Header.Get("Authorization"))
			if presented == "" && allowQuery {
				presented = r.URL.Query().Get("token")
			}
			got := sha256.Sum256([]byte(presented))
			// An empty configured token fails closed rather than admitting a bare header.
			if token == "" || subtle.ConstantTimeCompare(got[:], want[:]) != 1 {
				w.Header().Set("WWW-Authenticate", realm)
				http.Error(w, "unauthorized", http.StatusUnauthorized)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// TokenMatches reports whether presented equals want in constant time (SHA-256 of both sides). An empty want never matches, so it cannot authorize an empty credential. Lets the gated /img proxy reuse this comparison.
func TokenMatches(presented, want string) bool {
	if want == "" {
		return false
	}
	p := sha256.Sum256([]byte(presented))
	w := sha256.Sum256([]byte(want))
	return subtle.ConstantTimeCompare(p[:], w[:]) == 1
}

// ParseBearer extracts the credential from an "Authorization: Bearer <token>" header, returning "" for any other scheme or a malformed value.
func ParseBearer(header string) string { return parseBearer(header) }

func parseBearer(header string) string {
	const prefix = "bearer "
	if len(header) <= len(prefix) || !strings.EqualFold(header[:len(prefix)], prefix) {
		return ""
	}
	return strings.TrimSpace(header[len(prefix):])
}

// LogRequests logs one info line per request (method, path, status, duration, remote address) and never any headers, so the Authorization token cannot leak.
func LogRequests(logger *slog.Logger) func(http.Handler) http.Handler {
	if logger == nil {
		logger = slog.Default()
	}
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rec := &statusRecorder{ResponseWriter: w, status: http.StatusOK}
			next.ServeHTTP(rec, r)
			logger.Info("http request",
				"method", r.Method,
				"path", r.URL.Path,
				"status", rec.status,
				"duration_ms", time.Since(start).Milliseconds(),
				"remote", r.RemoteAddr,
			)
		})
	}
}

// statusRecorder captures the response status for logging while staying transparent to handlers that need Flush (streaming) or Unwrap.
type statusRecorder struct {
	http.ResponseWriter
	status int
}

func (r *statusRecorder) WriteHeader(status int) {
	r.status = status
	r.ResponseWriter.WriteHeader(status)
}

func (r *statusRecorder) Unwrap() http.ResponseWriter { return r.ResponseWriter }

func (r *statusRecorder) Flush() {
	if f, ok := r.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}
