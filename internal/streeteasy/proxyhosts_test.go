package streeteasy

import (
	"net/http"
	"net/url"
	"testing"
)

func TestProxyForMode(t *testing.T) {
	pool := []*url.URL{{Scheme: "http", Host: "proxy.example:8080"}}

	urls := []string{
		"https://api-v6.streeteasy.com/",            // GraphQL API host
		"https://streeteasy.com/sale/123",           // site page
		"https://www.streeteasy.com/sale/123",       // site page
		"https://photos.zillowstatic.com/fp/x.webp", // CDN
	}

	// proxied[mode][i] = whether url i should get a proxy in that mode.
	cases := []struct {
		mode    string
		proxied []bool
	}{
		{ProxyOff, []bool{false, false, false, false}},
		{ProxySplit, []bool{false, true, true, false}},
		{ProxyAll, []bool{true, true, true, true}},
	}

	for _, tc := range cases {
		t.Run(tc.mode, func(t *testing.T) {
			fn, err := ProxyForMode(tc.mode, pool)
			if err != nil {
				t.Fatalf("ProxyForMode: %v", err)
			}
			for i, raw := range urls {
				req, err := http.NewRequest(http.MethodGet, raw, nil)
				if err != nil {
					t.Fatalf("build request: %v", err)
				}
				got, err := fn(req)
				if err != nil {
					t.Fatalf("proxy func: %v", err)
				}
				if (got != nil) != tc.proxied[i] {
					t.Errorf("%s: %s proxied = %v, want %v", tc.mode, raw, got != nil, tc.proxied[i])
				}
			}
		})
	}

	if _, err := ProxyForMode("bogus", pool); err == nil {
		t.Error("unknown mode should error")
	}
}

func TestProxyForHosts(t *testing.T) {
	proxy := &url.URL{Scheme: "http", Host: "proxy.example:8080"}
	fn := proxyForHosts(func(*http.Request) (*url.URL, error) { return proxy, nil },
		"streeteasy.com", "www.streeteasy.com")

	tests := []struct {
		url     string
		proxied bool
	}{
		{"https://streeteasy.com/sale/123", true},
		{"https://WWW.StreetEasy.com/sale/123", true},
		{"https://api-v6.streeteasy.com/", false},
		{"https://photos.zillowstatic.com/fp/x.webp", false},
	}
	for _, tt := range tests {
		req, err := http.NewRequest(http.MethodGet, tt.url, nil)
		if err != nil {
			t.Fatalf("build request: %v", err)
		}
		got, err := fn(req)
		if err != nil {
			t.Fatalf("proxy func: %v", err)
		}
		if (got != nil) != tt.proxied {
			t.Fatalf("%s: proxied = %v, want %v", tt.url, got != nil, tt.proxied)
		}
	}
}
