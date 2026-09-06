package main

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/listings/mocks"
	"github.com/davidteather/property-radar/internal/mcp"
)

// MCP-only mode still has to serve the /img links its tools mint, and must not
// expose the REST surface.
func TestMCPOnlyModeServesImagesButNotREST(t *testing.T) {
	st := mocks.NewMockStore(t)
	ph := mocks.NewMockPhotoStore(t)
	key := "streeteasy/ab/abcdef0123456789abcdef0123456789abcdef0123456789abcdef0123456789.jpg"
	st.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil).Times(2)
	ph.EXPECT().Get(mock.Anything, key).Return([]byte{0xFF, 0xD8, 0xFF, 0xD9}, nil).Times(2)

	svc := listings.NewService(st, ph)
	server := mcp.NewServerFromService(svc)
	const token = "0123456789abcdef0123456789abcdef"
	const imgToken = "img-key"
	srv := httptest.NewServer(combinedHandler(server, svc, token, imgToken, "", false, false, slog.Default()))
	defer srv.Close()

	get := func(path string) *http.Response {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		t.Cleanup(func() { _ = resp.Body.Close() })
		return resp
	}
	if resp := get("/healthz"); resp.StatusCode != http.StatusOK {
		t.Fatalf("/healthz = %d, want 200", resp.StatusCode)
	}
	resp := get("/img/l/5/0?k=" + imgToken)
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK || len(body) != 4 {
		t.Fatalf("/img/l/5/0 with key = %d (%d bytes), want the cached thumbnail", resp.StatusCode, len(body))
	}
	if resp := get("/img/l/5/0"); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("/img/l/5/0 without key = %d, want 404 (gate hides existence)", resp.StatusCode)
	}
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/img/l/5/0", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	withBearer, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = withBearer.Body.Close() }()
	if withBearer.StatusCode != http.StatusOK {
		t.Fatalf("/img/l/5/0 with operator bearer = %d, want 200", withBearer.StatusCode)
	}
	if resp := get("/v1/listings"); resp.StatusCode == http.StatusOK {
		t.Fatalf("/v1/listings served in MCP-only mode")
	}
	if resp := get("/"); resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("root without bearer = %d, want 401", resp.StatusCode)
	}
}

// /connect prints the operator's public origin, not whatever Host a client
// sent, and is never cached: the page changes with PUBLIC_BASE_URL.
func TestConnectPageUsesThePublicOrigin(t *testing.T) {
	svc := listings.NewService(mocks.NewMockStore(t), mocks.NewMockPhotoStore(t))
	h := combinedHandler(mcp.NewServerFromService(svc), svc, "0123456789abcdef0123456789abcdef", "", "https://radar.example.com", true, false, slog.Default())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "http://evil.example/connect", nil)
	h.ServeHTTP(rr, req)
	body, _ := io.ReadAll(rr.Result().Body)
	if rr.Code != http.StatusOK || !strings.Contains(string(body), "https://radar.example.com/docs") || strings.Contains(string(body), "evil.example") {
		t.Fatalf("/connect = %d; body should name the configured origin only: %s", rr.Code, body)
	}
	if cc := rr.Header().Get("Cache-Control"); cc != "no-store" {
		t.Fatalf("Cache-Control = %q, want no-store", cc)
	}
}
