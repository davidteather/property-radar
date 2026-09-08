package streeteasy

import (
	"bufio"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
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

// Two fake forward proxies: the first always 403s. The caller must never see
// that 403 (the request is resent through the other proxy, body included) and
// the bad proxy must be hit exactly once.
func TestProxyTransportBenchesOn403(t *testing.T) {
	var badHits, goodHits atomic.Int32
	bodies := make(chan string, 8)
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		badHits.Add(1)
		w.WriteHeader(http.StatusForbidden)
	}))
	defer bad.Close()
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		b, _ := io.ReadAll(r.Body)
		bodies <- string(b)
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

	resp, err := client.Post("http://streeteasy.com/graphql", "application/json", strings.NewReader(`{"q":1}`))
	if err != nil {
		t.Fatal(err)
	}
	_ = resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Errorf("first request status = %d, want 200 via the second proxy", resp.StatusCode)
	}
	if got := <-bodies; got != `{"q":1}` {
		t.Errorf("resent body = %q, want the original", got)
	}
	for range 3 {
		resp, err := client.Get("http://streeteasy.com/sale/1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != 200 {
			t.Errorf("status = %d, want 200", resp.StatusCode)
		}
	}
	if badHits.Load() != 1 || goodHits.Load() != 4 {
		t.Errorf("hits bad=%d good=%d, want 1 and 4", badHits.Load(), goodHits.Load())
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
	if badHits.Load() != 1 || goodHits.Load() != 4 {
		t.Errorf("all benched still sent traffic: bad=%d good=%d", badHits.Load(), goodHits.Load())
	}
}

// connectProxy is a CONNECT proxy that tunnels every request to target,
// whatever host was asked for.
func connectProxy(t *testing.T, target string) *url.URL {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer func() { _ = c.Close() }()
				br := bufio.NewReader(c)
				if _, err := http.ReadRequest(br); err != nil {
					return
				}
				up, err := net.Dial("tcp", target)
				if err != nil {
					return
				}
				defer func() { _ = up.Close() }()
				_, _ = io.WriteString(c, "HTTP/1.1 200 OK\r\n\r\n")
				go func() { _, _ = io.Copy(up, br) }()
				_, _ = io.Copy(c, up)
			}()
		}
	}()
	return &url.URL{Scheme: "http", Host: ln.Addr().String()}
}

// Go's HTTP/2 pool keys connections by authority, not proxy: a single shared
// transport would send the second request down the first proxy's connection.
// Two proxies fronting two origins for the same URL must each see one request.
func TestProxyTransportIsolatesHTTP2PerProxy(t *testing.T) {
	var hits [2]atomic.Int32
	var origins [2]*httptest.Server
	var proxies []*url.URL
	for i := range origins {
		s := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			hits[i].Add(1)
			w.WriteHeader(http.StatusOK)
		}))
		s.EnableHTTP2 = true
		s.StartTLS()
		defer s.Close()
		origins[i] = s
		proxies = append(proxies, connectProxy(t, strings.TrimPrefix(s.URL, "https://")))
	}
	roots := x509.NewCertPool()
	roots.AddCert(origins[0].Certificate())

	pool := NewProxyPool(proxies)
	pool.minGap = 0
	base := &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots}, ForceAttemptHTTP2: true}
	rt, err := NewProxyTransport(base, ProxyAll, pool)
	if err != nil {
		t.Fatal(err)
	}
	client := &http.Client{Transport: rt}
	for range 2 {
		resp, err := client.Get("https://example.com/sale/1")
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.ProtoMajor != 2 {
			t.Fatalf("proto = %s, want HTTP/2 so the shared-pool bug is exercised", resp.Proto)
		}
	}
	if hits[0].Load() != 1 || hits[1].Load() != 1 {
		t.Errorf("origin hits = %d/%d, want one each", hits[0].Load(), hits[1].Load())
	}
}
