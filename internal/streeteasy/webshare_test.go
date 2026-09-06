package streeteasy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestFetchWebshareProxies(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"count":3,"results":[
			{"proxy_address":"10.0.0.1","port":6754,"username":"u1","password":"p1","valid":true},
			{"proxy_address":"10.0.0.2","port":6755,"username":"u2","password":"p2","valid":false},
			{"proxy_address":"10.0.0.3","port":6756,"username":"u3","password":"p3","valid":true}]}`))
	}))
	defer srv.Close()

	proxies, err := FetchWebshareProxies(t.Context(), srv.Client(), WebshareConfig{APIKey: "secret", ListURL: srv.URL})
	if err != nil {
		t.Fatalf("FetchWebshareProxies: %v", err)
	}
	if gotAuth != "Token secret" {
		t.Errorf("auth header = %q", gotAuth)
	}
	if len(proxies) != 2 {
		t.Fatalf("got %d proxies, want 2 (invalid ones dropped)", len(proxies))
	}
	if proxies[0].Host != "10.0.0.1:6754" || proxies[1].Host != "10.0.0.3:6756" {
		t.Errorf("hosts = %q %q", proxies[0].Host, proxies[1].Host)
	}
	if u := proxies[0].User; u == nil || u.Username() != "u1" {
		t.Errorf("user = %v", proxies[0].User)
	}
	if proxies[0].Scheme != "http" {
		t.Errorf("scheme = %q", proxies[0].Scheme)
	}
}

func TestFetchWebshareProxiesErrors(t *testing.T) {
	if _, err := FetchWebshareProxies(t.Context(), nil, WebshareConfig{}); err == nil {
		t.Error("missing api key should error")
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "nope", http.StatusUnauthorized)
	}))
	defer srv.Close()
	if _, err := FetchWebshareProxies(t.Context(), srv.Client(), WebshareConfig{APIKey: "k", ListURL: srv.URL}); err == nil {
		t.Error("401 should error")
	}

	empty := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer empty.Close()
	if _, err := FetchWebshareProxies(t.Context(), empty.Client(), WebshareConfig{APIKey: "k", ListURL: empty.URL}); err == nil {
		t.Error("empty list should error")
	}
}

func TestRotatingProxyFunc(t *testing.T) {
	a := &url.URL{Scheme: "http", Host: "a:1"}
	b := &url.URL{Scheme: "http", Host: "b:2"}
	next := rotatingProxyFunc([]*url.URL{a, b})

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"a:1", "b:2", "a:1", "b:2"}
	for i, w := range want {
		u, err := next(req)
		if err != nil {
			t.Fatalf("call %d: %v", i, err)
		}
		if u.Host != w {
			t.Errorf("call %d host = %q, want %q", i, u.Host, w)
		}
	}

	direct := rotatingProxyFunc(nil)
	u, err := direct(req)
	if err != nil || u != nil {
		t.Errorf("empty pool should mean direct, got %v %v", u, err)
	}
}

func TestRotatingProxyFuncCopiesPool(t *testing.T) {
	pool := []*url.URL{{Scheme: "http", Host: "a:1"}}
	next := rotatingProxyFunc(pool)
	pool[0] = &url.URL{Scheme: "http", Host: "mutated:9"}

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, "https://example.test", nil)
	if err != nil {
		t.Fatal(err)
	}
	u, _ := next(req)
	if u.Host != "a:1" {
		t.Errorf("caller mutation leaked into the proxy pool: %q", u.Host)
	}
}
