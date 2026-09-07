package streeteasy

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
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

	// Rotating residential plans list proxies with no address (backbone only).
	rotating := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[{"proxy_address":null,"port":0,"username":"u","password":"p","valid":true}]}`))
	}))
	defer rotating.Close()
	_, err := FetchWebshareProxies(t.Context(), rotating.Client(), WebshareConfig{APIKey: "k", ListURL: rotating.URL})
	if err == nil || !strings.Contains(err.Error(), "backbone") {
		t.Errorf("addressless list should name the rotating plan, got %v", err)
	}
}

func TestProxyPoolPick(t *testing.T) {
	a := &url.URL{Scheme: "http", Host: "a:1"}
	b := &url.URL{Scheme: "http", Host: "b:2"}
	pool := NewProxyPool([]*url.URL{a, b})

	want := []string{"a:1", "b:2", "a:1", "b:2"}
	for i, w := range want {
		if u := pool.Pick(); u.Host != w {
			t.Errorf("pick %d host = %q, want %q", i, u.Host, w)
		}
	}
	if u := NewProxyPool(nil).Pick(); u != nil {
		t.Errorf("empty pool should mean direct, got %v", u)
	}
}

func TestProxyPoolCopiesInput(t *testing.T) {
	in := []*url.URL{{Scheme: "http", Host: "a:1"}}
	pool := NewProxyPool(in)
	in[0] = &url.URL{Scheme: "http", Host: "mutated:9"}
	if u := pool.Pick(); u.Host != "a:1" {
		t.Errorf("caller mutation leaked into the proxy pool: %q", u.Host)
	}
}

func TestProxyPoolBench(t *testing.T) {
	a := &url.URL{Scheme: "http", Host: "a:1"}
	b := &url.URL{Scheme: "http", Host: "b:2"}
	pool := NewProxyPool([]*url.URL{a, b})
	now := time.Unix(1_700_000_000, 0)
	pool.now = func() time.Time { return now }

	pool.Bench(a)
	for i := range 3 {
		if u := pool.Pick(); u.Host != "b:2" {
			t.Fatalf("pick %d after benching a = %q, want b:2", i, u.Host)
		}
	}
	if h, n := pool.Healthy(); h != 1 || n != 2 {
		t.Errorf("healthy = %d/%d, want 1/2", h, n)
	}

	// Everyone benched: hand out the proxy whose bench ends first, not nothing.
	now = now.Add(time.Minute)
	pool.Bench(b)
	if u := pool.Pick(); u.Host != "a:1" {
		t.Errorf("all benched: picked %q, want the soonest-expiring a:1", u.Host)
	}

	now = now.Add(proxyBenchFor)
	if u := pool.Pick(); u.Host != "a:1" {
		t.Errorf("after cooldown: picked %q, want a:1 back in rotation", u.Host)
	}
	if u := pool.Pick(); u.Host != "b:2" {
		t.Errorf("b should still be benched for a minute, got %q", u.Host)
	}
}

func TestProxyPoolReplaceKeepsBenches(t *testing.T) {
	a := &url.URL{Scheme: "http", Host: "a:1"}
	b := &url.URL{Scheme: "http", Host: "b:2"}
	c := &url.URL{Scheme: "http", Host: "c:3"}
	pool := NewProxyPool([]*url.URL{a, b})
	pool.Bench(a)
	pool.Bench(b)

	pool.Replace([]*url.URL{a, c})
	if u := pool.Pick(); u.Host != "c:3" {
		t.Errorf("a should stay benched across a refresh, got %q", u.Host)
	}
	if h, n := pool.Healthy(); h != 1 || n != 2 {
		t.Errorf("healthy = %d/%d, want 1/2", h, n)
	}
	if len(pool.benched) != 1 {
		t.Errorf("bench for the dropped proxy b should be forgotten, have %d", len(pool.benched))
	}
}
