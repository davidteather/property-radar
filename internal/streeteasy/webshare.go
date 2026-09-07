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

// proxyBenchFor is how long a proxy that answered 403 sits out. PX bans are per
// IP and sticky, so re-sending through it only keeps it burned.
const proxyBenchFor = 30 * time.Minute

// ProxyPool round-robins the proxies that are not benched; when every proxy is
// benched it hands out the one whose bench ends soonest rather than none.
type ProxyPool struct {
	mu      sync.Mutex
	proxies []*url.URL
	benched map[string]time.Time
	next    int
	now     func() time.Time
}

func NewProxyPool(proxies []*url.URL) *ProxyPool {
	p := &ProxyPool{benched: make(map[string]time.Time), now: time.Now}
	p.Replace(proxies)
	return p
}

// Replace swaps in a refreshed list; a proxy still listed keeps its bench.
func (p *ProxyPool) Replace(proxies []*url.URL) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.proxies = slices.Clone(proxies)
	p.next = 0
	keep := make(map[string]time.Time, len(p.benched))
	for _, u := range p.proxies {
		if until, ok := p.benched[u.Host]; ok {
			keep[u.Host] = until
		}
	}
	p.benched = keep
}

func (p *ProxyPool) Pick() *url.URL {
	p.mu.Lock()
	defer p.mu.Unlock()
	n := len(p.proxies)
	if n == 0 {
		return nil
	}
	now := p.now()
	var soonest *url.URL
	for range n {
		u := p.proxies[p.next%n]
		p.next++
		until, ok := p.benched[u.Host]
		if !ok || !until.After(now) {
			delete(p.benched, u.Host)
			return u
		}
		if soonest == nil || until.Before(p.benched[soonest.Host]) {
			soonest = u
		}
	}
	return soonest
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

type pickedProxyKey struct{}

// proxyTransport picks a pool proxy per request and benches the one that
// answers 403; the base transport reads the pick back out of the context.
type proxyTransport struct {
	base  *http.Transport
	pool  *ProxyPool
	hosts map[string]struct{} // nil proxies every host
}

// NewProxyTransport wires base to route through pool per mode. base.Proxy is
// overwritten; pass a transport nothing else shares.
func NewProxyTransport(base *http.Transport, mode string, pool *ProxyPool) (http.RoundTripper, error) {
	t := &proxyTransport{base: base, pool: pool}
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
	base.Proxy = func(r *http.Request) (*url.URL, error) {
		u, _ := r.Context().Value(pickedProxyKey{}).(*url.URL)
		return u, nil
	}
	return t, nil
}

func (t *proxyTransport) proxyFor(req *http.Request) *url.URL {
	if t.hosts != nil {
		if _, ok := t.hosts[strings.ToLower(req.URL.Hostname())]; !ok {
			return nil
		}
	}
	return t.pool.Pick()
}

func (t *proxyTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	u := t.proxyFor(req)
	if u == nil {
		return t.base.RoundTrip(req)
	}
	resp, err := t.base.RoundTrip(req.WithContext(context.WithValue(req.Context(), pickedProxyKey{}, u)))
	if err == nil && resp.StatusCode == http.StatusForbidden {
		t.pool.Bench(u)
	}
	return resp, err
}
