package streeteasy

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"
)

func TestNewProxyTransport(t *testing.T) {
	pool := NewProxyPool([]*url.URL{{Scheme: "http", Host: "proxy.example:8080"}})

	urls := []string{
		"https://api-v6.streeteasy.com/",            // GraphQL API host
		"https://streeteasy.com/sale/123",           // site page
		"https://WWW.StreetEasy.com/sale/123",       // site page, any case
		"https://photos.zillowstatic.com/fp/x.webp", // CDN
	}

	// proxied[mode][i] = whether url i should get a proxy in that mode.
	cases := []struct {
		mode    string
		proxied []bool
	}{
		{ProxySplit, []bool{false, true, true, false}},
		{ProxyAll, []bool{true, true, true, true}},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			rt, err := NewProxyTransport(&http.Transport{}, tc.mode, pool)
			if err != nil {
				t.Fatalf("NewProxyTransport: %v", err)
			}
			pt, ok := rt.(*proxyTransport)
			if !ok {
				t.Fatalf("got %T, want *proxyTransport", rt)
			}
			for i, raw := range urls {
				req, err := http.NewRequest(http.MethodGet, raw, nil)
				if err != nil {
					t.Fatalf("build request: %v", err)
				}
				if got := pt.proxied(req); got != tc.proxied[i] {
					t.Errorf("%s: %s proxied = %v, want %v", tc.mode, raw, got, tc.proxied[i])
				}
			}
		})
	}

	base := &http.Transport{Proxy: http.ProxyFromEnvironment}
	rt, err := NewProxyTransport(base, ProxyOff, pool)
	if err != nil {
		t.Fatalf("off: %v", err)
	}
	if rt != base || base.Proxy != nil {
		t.Error("off mode should return the base transport with no proxy func")
	}
	if _, err := NewProxyTransport(&http.Transport{}, "bogus", pool); err == nil {
		t.Error("unknown mode should error")
	}
}

// Two fake forward proxies: the first always 403s. After one request through
// it the transport must stop picking it.
func TestProxyTransportBenchesOn403(t *testing.T) {
	var badHits, goodHits atomic.Int32
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		badHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		goodHits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	defer good.Close()

	badURL, _ := url.Parse(bad.URL)
	goodURL, _ := url.Parse(good.URL)
	pool := NewProxyPool([]*url.URL{badURL, goodURL})
	pool.minGap = 0
	rt, err := NewProxyTransport(&http.Transport{}, ProxyAll, pool)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rt}

	codes := []int{}
	for range 4 {
		resp, err := client.Get("http://streeteasy.com/sale/1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		codes = append(codes, resp.StatusCode)
	}
	if badHits.Load() != 1 || goodHits.Load() != 3 {
		t.Errorf("hits bad=%d good=%d, want 1 and 3", badHits.Load(), goodHits.Load())
	}
	if codes[0] != 403 || codes[1] != 200 || codes[3] != 200 {
		t.Errorf("status codes = %v", codes)
	}
	if h, n := pool.Healthy(); h != 1 || n != 2 {
		t.Errorf("healthy = %d/%d, want 1/2", h, n)
	}

	// Bench the last healthy proxy: the next request must fail without a hit.
	pool.Bench(goodURL)
	_, err = client.Get("http://streeteasy.com/sale/2")
	if !errors.Is(err, ErrProxiesBenched) {
		t.Errorf("all benched: err = %v, want ErrProxiesBenched", err)
	}
	if badHits.Load() != 1 || goodHits.Load() != 3 {
		t.Errorf("all benched still sent traffic: bad=%d good=%d", badHits.Load(), goodHits.Load())
	}
}
