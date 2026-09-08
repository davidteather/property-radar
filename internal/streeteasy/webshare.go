package streeteasy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultWebshareListURL = "https://proxy.webshare.io/api/v2/proxy/list/?mode=direct&page=1&page_size=25"

type WebshareConfig struct {
	APIKey  string
	ListURL string
}

type webshareList struct {
	Results []struct {
		ProxyAddress string `json:"proxy_address"`
		Port         int    `json:"port"`
		Username     string `json:"username"`
		Password     string `json:"password"`
		Valid        bool   `json:"valid"`
	} `json:"results"`
}

// FetchWebshareProxies returns proxy URLs with embedded credentials. Never log the
// result: url.URL.String() renders the password.
func FetchWebshareProxies(ctx context.Context, httpClient *http.Client, cfg WebshareConfig) ([]*url.URL, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("webshare: missing api key")
	}
	if httpClient == nil {
		httpClient = http.DefaultClient
	}
	listURL := cfg.ListURL
	if listURL == "" {
		listURL = defaultWebshareListURL
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, listURL, nil)
	if err != nil {
		return nil, fmt.Errorf("webshare: build request: %w", err)
	}
	req.Header.Set("Authorization", "Token "+cfg.APIKey)
	req.Header.Set("accept", "application/json")

	resp, err := httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("webshare: request proxy list: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("webshare: proxy list: %w", &HTTPError{StatusCode: resp.StatusCode})
	}

	var list webshareList
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxPageBodySize)).Decode(&list); err != nil {
		return nil, fmt.Errorf("webshare: decode proxy list: %w", err)
	}

	proxies := make([]*url.URL, 0, len(list.Results))
	addressless := 0
	for _, r := range list.Results {
		if r.Valid && r.ProxyAddress == "" {
			addressless++
		}
		if !r.Valid || r.ProxyAddress == "" || r.Port == 0 {
			continue
		}
		proxies = append(proxies, &url.URL{
			Scheme: "http",
			User:   url.UserPassword(r.Username, r.Password),
			Host:   r.ProxyAddress + ":" + strconv.Itoa(r.Port),
		})
	}
	if len(proxies) == 0 {
		if addressless > 0 {
			return nil, errors.New("webshare: plan returns proxies without addresses (rotating residential needs backbone mode, which is not supported); use a static residential plan")
		}
		return nil, errors.New("webshare: no valid proxies returned")
	}
	return proxies, nil
}

const (
	ProxyOff   = "off"
	ProxySplit = "split"
	ProxyAll   = "all"
)

// The GraphQL API host is deliberately absent: it 403s datacenter proxy IPs.
var siteHosts = []string{"streeteasy.com", "www.streeteasy.com"}

// proxyBenchFor is how long a proxy that answered 403 sits out. A PX ban is per
// IP, clears within minutes if left alone, and lengthens on every re-hit.
const proxyBenchFor = 30 * time.Minute

// proxyMinGap spaces hits through one IP: ~10 within 15 s earn a block, 10 s
// apart never did.
const proxyMinGap = 10 * time.Second

// ProxyPool hands out the least recently used proxy that is not benched.
// Hitting a banned IP again lengthens its ban, so a fully benched pool hands
// out nothing.
type ProxyPool struct {
	mu       sync.Mutex
	proxies  []*url.URL
	benched  map[string]time.Time
	lastUsed map[string]time.Time
	minGap   time.Duration
	now      func() time.Time
}

func NewProxyPool(proxies []*url.URL) *ProxyPool {
	p := &ProxyPool{
		benched:  make(map[string]time.Time),
		lastUsed: make(map[string]time.Time),
		minGap:   proxyMinGap,
		now:      time.Now,
	}
	p.Replace(proxies)
	return p
}

// Replace swaps in a refreshed list; a proxy still listed keeps its bench.
func (p *ProxyPool) Replace(proxies []*url.URL) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxies = slices.Clone(proxies)
	keep := make(map[string]time.Time, len(p.benched))
	for _, u := range p.proxies {
		if until, ok := p.benched[u.Host]; ok {
			keep[u.Host] = until
		}
	}
	p.benched = keep
}

// Pick returns the proxy to use next and how long to wait before using it;
// nil means every proxy is benched.
func (p *ProxyPool) Pick() (*url.URL, time.Duration) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	var pick *url.URL
	for _, u := range p.proxies {
		if until, ok := p.benched[u.Host]; ok {
			if until.After(now) {
				continue
			}
			delete(p.benched, u.Host)
		}
		if pick == nil || p.lastUsed[u.Host].Before(p.lastUsed[pick.Host]) {
			pick = u
		}
	}
	if pick == nil {
		return nil, 0
	}
	wait := max(0, p.minGap-now.Sub(p.lastUsed[pick.Host]))
	p.lastUsed[pick.Host] = now.Add(wait)
	return pick, wait
}

func (p *ProxyPool) Bench(u *url.URL) {
	if u == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	p.benched[u.Host] = p.now().Add(proxyBenchFor)
}

// Healthy reports how many proxies are currently usable, for logs.
func (p *ProxyPool) Healthy() (healthy, total int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	now := p.now()
	for _, u := range p.proxies {
		if until, ok := p.benched[u.Host]; !ok || !until.After(now) {
			healthy++
		}
	}
	return healthy, len(p.proxies)
}

// proxyTransport gives each proxy its own transport: Go's HTTP/2 pool keys
// connections by authority, not proxy, so one shared transport would funnel
// every host through whichever proxy dialed it first.
type proxyTransport struct {
	base  *http.Transport
	pool  *ProxyPool
	hosts map[string]struct{} // nil proxies every host

	mu       sync.Mutex
	perProxy map[string]*http.Transport
}

// NewProxyTransport wires base to route through pool per mode; base is the
// template every per-proxy transport is cloned from.
func NewProxyTransport(base *http.Transport, mode string, pool *ProxyPool) (http.RoundTripper, error) {
	t := &proxyTransport{base: base, pool: pool, perProxy: make(map[string]*http.Transport)}
	switch mode {
	case ProxyOff:
		base.Proxy = nil
		return base, nil
	case ProxySplit:
		t.hosts = make(map[string]struct{}, len(siteHosts))
		for _, h := range siteHosts {
			t.hosts[h] = struct{}{}
		}
	case ProxyAll:
	default:
		return nil, fmt.Errorf("unknown proxy mode %q", mode)
	}
	base.Proxy = nil
	return t, nil
}

// ErrProxiesBenched is returned instead of sending through a banned IP.
var ErrProxiesBenched = errors.New("every proxy is benched")

func (t *proxyTransport) proxied(req *http.Request) bool {
	if t.hosts == nil {
		return true
	}
	_, ok := t.hosts[strings.ToLower(req.URL.Hostname())]
	return ok
}

func (t *proxyTransport) via(u *url.URL) *http.Transport {
	t.mu.Lock()
	defer t.mu.Unlock()
	tr, ok := t.perProxy[u.String()]
	if !ok {
		tr = t.base.Clone()
		tr.Proxy = http.ProxyURL(u)
		t.perProxy[u.String()] = tr
	}
	return tr
}

// A 403 is the picked IP's problem, not the request's: bench it and resend
// through another until the pool runs dry.
func (t *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if !t.proxied(req) {
		return t.base.RoundTrip(req)
	}
	for {
		u, wait := t.pool.Pick()
		if u == nil {
			return nil, ErrProxiesBenched
		}
		if wait > 0 {
			if err := sleepCtx(req.Context(), wait); err != nil {
				return nil, err
			}
		}
		resp, err := t.via(u).RoundTrip(req)
		if err != nil || resp.StatusCode != http.StatusForbidden {
			return resp, err
		}
		t.pool.Bench(u)
		if req.Body != nil && req.GetBody == nil {
			return resp, nil // body already consumed and not replayable
		}
		_ = resp.Body.Close()
		if req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req = req.Clone(req.Context())
			req.Body = body
		}
	}
}
