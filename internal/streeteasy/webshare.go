package streeteasy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync/atomic"
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
	for _, r := range list.Results {
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

func ProxyForMode(mode string, proxies []*url.URL) (func(*http.Request) (*url.URL, error), error) {
	switch mode {
	case ProxyOff:
		return func(*http.Request) (*url.URL, error) { return nil, nil }, nil
	case ProxySplit:
		return proxyForHosts(rotatingProxyFunc(proxies), siteHosts...), nil
	case ProxyAll:
		return rotatingProxyFunc(proxies), nil
	default:
		return nil, fmt.Errorf("unknown proxy mode %q", mode)
	}
}

// Proxies only the given hostnames: the API 403s datacenter proxy IPs while
// HTML pages 403 re-used direct IPs.
func proxyForHosts(next func(*http.Request) (*url.URL, error), hosts ...string) func(*http.Request) (*url.URL, error) {
	set := make(map[string]struct{}, len(hosts))
	for _, h := range hosts {
		set[strings.ToLower(h)] = struct{}{}
	}
	return func(req *http.Request) (*url.URL, error) {
		if _, ok := set[strings.ToLower(req.URL.Hostname())]; ok {
			return next(req)
		}
		return nil, nil
	}
}

// Round-robins for http.Transport.Proxy; an empty list means direct connection.
func rotatingProxyFunc(proxies []*url.URL) func(*http.Request) (*url.URL, error) {
	if len(proxies) == 0 {
		return func(*http.Request) (*url.URL, error) { return nil, nil }
	}
	pool := make([]*url.URL, len(proxies))
	copy(pool, proxies)
	var n atomic.Uint64
	return func(*http.Request) (*url.URL, error) {
		i := n.Add(1) - 1
		return pool[i%uint64(len(pool))], nil
	}
}
