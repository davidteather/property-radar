package mcp_test

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/mcp"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/store"
)

const testBearerToken = "test-token-6f1c9d"

func newTestServer(t *testing.T) *mcp.Server {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.TruncateAll(t, pool)
	return mcp.NewServer(store.New(pool), localPhotos(t, t.TempDir()))
}

func testHTTPConfig() mcp.HTTPConfig {
	return mcp.HTTPConfig{
		BearerToken: testBearerToken,
		Logger:      slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
}

func newHTTPServer(t *testing.T) string {
	t.Helper()
	handler, err := newTestServer(t).HTTPHandler(testHTTPConfig())
	if err != nil {
		t.Fatalf("build http handler: %v", err)
	}
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return srv.URL
}

func TestHTTPHandlerRequiresBearerToken(t *testing.T) {
	base := newHTTPServer(t)

	tests := map[string]string{
		"no authorization header":  "",
		"wrong token":              "Bearer not-the-token",
		"right token wrong scheme": "Basic " + testBearerToken,
		"bare token":               testBearerToken,
		"empty bearer":             "Bearer ",
	}
	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, base, strings.NewReader(`{}`))
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if header != "" {
				req.Header.Set("Authorization", header)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("post: %v", err)
			}
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", resp.StatusCode)
			}
			if got := resp.Header.Get("WWW-Authenticate"); !strings.HasPrefix(got, "Bearer") {
				t.Fatalf("WWW-Authenticate = %q, want a Bearer challenge", got)
			}
		})
	}
}

func TestHTTPHandlerRejectsEmptyToken(t *testing.T) {
	cfg := testHTTPConfig()
	cfg.BearerToken = "   "
	if _, err := newTestServer(t).HTTPHandler(cfg); err == nil {
		t.Fatal("expected an error for a blank bearer token")
	}
}

func TestHTTPHealthzNeedsNoAuth(t *testing.T) {
	base := newHTTPServer(t)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, base+"/healthz", nil)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("get healthz: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 220", resp.StatusCode)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	if strings.TrimSpace(string(body)) != "ok" {
		t.Fatalf("body = %q, want ok", body)
	}
}

func TestHTTPServesMCPSessionWithBearerToken(t *testing.T) {
	session := connectHTTP(t, newHTTPServer(t), testBearerToken)

	tools, err := session.ListTools(t.Context(), nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 23 {
		t.Fatalf("advertised %d tools, want 23", len(tools.Tools))
	}

	res, err := session.CallTool(t.Context(), &mcpsdk.CallToolParams{Name: "get_state"})
	if err != nil {
		t.Fatalf("call get_state: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_state returned a tool error: %s", resultText(res))
	}
	state, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("get_state structured content = %T, want an object", res.StructuredContent)
	}
	if _, ok := state["profile"]; !ok {
		t.Fatalf("get_state result has no profile: %v", state)
	}
}

func TestRunHTTPServesAndShutsDown(t *testing.T) {
	server := newTestServer(t)
	cfg := testHTTPConfig()
	cfg.Addr = freeAddr(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() { done <- server.RunHTTP(ctx, cfg) }()

	waitHealthy(t, "http://"+cfg.Addr+"/healthz")
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("RunHTTP: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("RunHTTP did not return after context cancellation")
	}
}

func connectHTTP(t *testing.T, base, token string) *mcpsdk.ClientSession {
	t.Helper()
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "http-test-caller", Version: "0"}, nil)
	session, err := client.Connect(t.Context(), &mcpsdk.StreamableClientTransport{
		Endpoint:   base,
		HTTPClient: &http.Client{Transport: bearerRoundTripper{token: token, base: http.DefaultTransport}},
	}, nil)
	if err != nil {
		t.Fatalf("connect over streamable http: %v", err)
	}
	t.Cleanup(func() { _ = session.Close() })
	return session
}

type bearerRoundTripper struct {
	token string
	base  http.RoundTripper
}

func (b bearerRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	clone := req.Clone(req.Context())
	clone.Header.Set("Authorization", "Bearer "+b.token)
	return b.base.RoundTrip(clone)
}

// RunHTTP owns its listener, so the test reserves a port and releases it.
func freeAddr(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatalf("release port: %v", err)
	}
	return addr
}

func waitHealthy(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, url, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		resp, err := http.DefaultClient.Do(req)
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(25 * time.Millisecond)
	}
	t.Fatalf("%s never became healthy", url)
}
