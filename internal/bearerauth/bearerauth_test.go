package bearerauth_test

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/davidteather/property-radar/internal/bearerauth"
)

const token = "test-token-6f1c9d"

func okHandler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
}

func TestMiddlewareRejectsBadTokens(t *testing.T) {
	handler := bearerauth.Middleware(token)(okHandler())

	cases := map[string]string{
		"no authorization header":  "",
		"wrong token":              "Bearer not-the-token",
		"right token wrong scheme": "Basic " + token,
		"bare token":               token,
		"empty bearer":             "Bearer ",
		"prefix only":              "Bearer",
	}
	for name, header := range cases {
		t.Run(name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", rec.Code)
			}
			if got := rec.Header().Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge", got)
			}
		})
	}
}

// A misconfigured empty token must not turn into "any request with an empty
// bearer is in": the gate fails closed.
func TestMiddlewareWithEmptyTokenAdmitsNobody(t *testing.T) {
	handler := bearerauth.Middleware("")(okHandler())
	for _, header := range []string{"", "Bearer ", "Bearer x"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
		if header != "" {
			req.Header.Set("Authorization", header)
		}
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("Authorization=%q with an empty configured token = %d, want 401", header, rec.Code)
		}
	}
}

func TestMiddlewareAllowsCorrectToken(t *testing.T) {
	handler := bearerauth.Middleware(token)(okHandler())

	for _, header := range []string{"Bearer " + token, "bearer " + token, "BEARER " + token} {
		req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
		req.Header.Set("Authorization", header)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)

		if rec.Code != http.StatusOK {
			t.Fatalf("header %q: status = %d, want 200", header, rec.Code)
		}
		if rec.Body.String() != "ok" {
			t.Fatalf("header %q: body = %q, want ok", header, rec.Body.String())
		}
	}
}

// A token of a different length must still be rejected: SHA-256 hashing keeps the comparison constant-time regardless of length.
func TestMiddlewareConstantTimeAcrossLengths(t *testing.T) {
	handler := bearerauth.Middleware(token)(okHandler())

	for _, presented := range []string{"", "x", strings.Repeat("y", 4096), token[:len(token)-1], token + "z"} {
		req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
		req.Header.Set("Authorization", "Bearer "+presented)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusUnauthorized {
			t.Fatalf("presented %q: status = %d, want 401", presented, rec.Code)
		}
	}
}

// The Authorization header (token) must never appear in a logged line.
func TestLogRequestsNeverLogsToken(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))

	// Compose the same way the servers do: log wraps auth wraps the handler.
	handler := bearerauth.LogRequests(logger)(bearerauth.Middleware(token)(okHandler()))

	req := httptest.NewRequest(http.MethodGet, "/v1/state", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	logged := buf.String()
	if logged == "" {
		t.Fatal("expected a request log line")
	}
	if strings.Contains(logged, token) {
		t.Fatalf("log line leaked the bearer token: %q", logged)
	}
	if !strings.Contains(logged, "status=200") || !strings.Contains(logged, "method=GET") {
		t.Fatalf("log line missing method/status: %q", logged)
	}
}

func TestLogRequestsRecordsStatus(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&buf, nil))
	handler := bearerauth.LogRequests(logger)(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusTeapot)
	}))

	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	handler.ServeHTTP(httptest.NewRecorder(), req)

	if !strings.Contains(buf.String(), "status=418") {
		t.Fatalf("expected status=418 in log, got %q", buf.String())
	}
	_ = io.Discard
}

func TestMiddlewareAllowQuery(t *testing.T) {
	handler := bearerauth.MiddlewareAllowQuery(token)(okHandler())

	// Header still works.
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("header token: status = %d, want 200", rec.Code)
	}

	// ?token= works with no header.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token="+token, nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("query token: status = %d, want 200", rec.Code)
	}

	// Wrong ?token= is rejected.
	rec = httptest.NewRecorder()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token=nope", nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("bad query token: status = %d, want 401", rec.Code)
	}

	// The default Middleware must NOT honor ?token=.
	headerOnly := bearerauth.Middleware(token)(okHandler())
	rec = httptest.NewRecorder()
	headerOnly.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/?token="+token, nil))
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("header-only with query token: status = %d, want 401", rec.Code)
	}
}
