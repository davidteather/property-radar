package publicurl_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/davidteather/property-radar/internal/publicurl"
)

// capture runs one request through the Middleware and returns the base URL it stamped, read back from both the request header (MCP path) and context (REST path), asserting they agree.
func capture(t *testing.T, override string, mutate func(*http.Request)) string {
	t.Helper()
	var fromHeader, fromCtx string
	h := publicurl.Middleware(override)(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		fromHeader = publicurl.FromHeader(r.Header)
		fromCtx = publicurl.FromContext(r.Context())
	}))
	req := httptest.NewRequest(http.MethodGet, "http://example.test/anything", nil)
	if mutate != nil {
		mutate(req)
	}
	h.ServeHTTP(httptest.NewRecorder(), req)
	if fromHeader != fromCtx {
		t.Fatalf("header base %q != context base %q", fromHeader, fromCtx)
	}
	return fromHeader
}

func TestMiddlewareDerivesFromHost(t *testing.T) {
	if got := capture(t, "", nil); got != "http://example.test" {
		t.Fatalf("derived base = %q, want http://example.test", got)
	}
}

func TestMiddlewareHonorsForwardedHeaders(t *testing.T) {
	got := capture(t, "", func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https")
		r.Header.Set("X-Forwarded-Host", "radar.example.com")
	})
	if got != "https://radar.example.com" {
		t.Fatalf("forwarded base = %q, want https://radar.example.com", got)
	}
}

func TestMiddlewareForwardedHostListTakesFirst(t *testing.T) {
	got := capture(t, "", func(r *http.Request) {
		r.Header.Set("X-Forwarded-Proto", "https, http")
		r.Header.Set("X-Forwarded-Host", "radar.example.com, internal:9000")
	})
	if got != "https://radar.example.com" {
		t.Fatalf("chained-proxy base = %q, want the first token", got)
	}
}

func TestSecureFollowsTheClientFacingHop(t *testing.T) {
	cases := map[string]bool{"": false, "https": true, "http": false, "https, http": true, "http, https": false, "HTTPS": false}
	for proto, want := range cases {
		r := httptest.NewRequest(http.MethodGet, "http://example.test/", nil)
		if proto != "" {
			r.Header.Set("X-Forwarded-Proto", proto)
		}
		if got := publicurl.Secure(r); got != want {
			t.Errorf("Secure(X-Forwarded-Proto=%q) = %v, want %v", proto, got, want)
		}
	}
	r := httptest.NewRequest(http.MethodGet, "https://example.test/", nil)
	if !publicurl.Secure(r) {
		t.Fatalf("a TLS request without forwarding headers is not Secure")
	}
	r.Header.Set("X-Forwarded-Proto", "http")
	if publicurl.Secure(r) {
		t.Fatalf("an edge that says http overrides the hop's own TLS")
	}
}

func TestMiddlewareOverrideWins(t *testing.T) {
	got := capture(t, "https://override.example/", func(r *http.Request) {
		r.Header.Set("X-Forwarded-Host", "ignored.example")
	})
	if got != "https://override.example" {
		t.Fatalf("override base = %q, want the configured override with no trailing slash", got)
	}
}

func TestMiddlewareOverwritesClientSuppliedHeader(t *testing.T) {
	got := capture(t, "", func(r *http.Request) {
		r.Header.Set(publicurl.Header, "https://attacker.example")
	})
	if got != "http://example.test" {
		t.Fatalf("base = %q, want a client-supplied X-Public-Base to be overwritten", got)
	}
}

func TestMiddlewareIgnoresMalformedForwardedHeaders(t *testing.T) {
	tests := []struct{ proto, host, want string }{
		{"javascript", "evil.example/steal?x=", "http://example.test"},
		{"https", "evil.example/steal?x=", "https://example.test"},
		{"https", "user@evil.example", "https://example.test"},
		{"ftp", "radar.example.com:8443", "http://radar.example.com:8443"},
		{"https", "[::1]:8443", "https://[::1]:8443"},
	}
	for _, tt := range tests {
		got := capture(t, "", func(r *http.Request) {
			r.Header.Set("X-Forwarded-Proto", tt.proto)
			r.Header.Set("X-Forwarded-Host", tt.host)
		})
		if got != tt.want {
			t.Errorf("proto %q host %q: base = %q, want %q", tt.proto, tt.host, got, tt.want)
		}
	}
}
