// Package publicurl derives the absolute, same-origin base URL a request came in on and threads it to handlers that mint public image links. The MCP tool handler reads it from the request header (go-sdk surfaces it as RequestExtra.Header); the REST handler reads it from the request context. Both are stamped by one Middleware.
package publicurl

import (
	"context"
	"net/http"
	"net/url"
	"strings"
)

// Header is the request header Middleware stamps the computed base URL into, for recovery via RequestExtra.Header. Always overwritten, so a client cannot spoof it.
const Header = "X-Public-Base"

type ctxKey struct{}

// Middleware computes the public base URL per request (an explicit override wins; otherwise derived from forwarded/host headers) and stamps it into both the request header and context.
func Middleware(override string) func(http.Handler) http.Handler {
	override = strings.TrimRight(strings.TrimSpace(override), "/")
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			base := override
			if base == "" {
				base = derive(r)
			}
			// Overwrite unconditionally so a client-supplied value cannot leak into minted links.
			r.Header.Set(Header, base)
			ctx := context.WithValue(r.Context(), ctxKey{}, base)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// ImageURL joins base and an absolute /img... path into a photo-proxy URL, appending the access token as ?k= when non-empty so it satisfies a gated proxy. base may be empty, yielding a relative URL as a last resort. The token is a read-only capability, safe to embed in <img src>.
func ImageURL(base, path, token string) string {
	u := base + path
	if token != "" {
		u += "?k=" + url.QueryEscape(token)
	}
	return u
}

// FromContext returns the base URL stamped by Middleware, or "" if absent.
func FromContext(ctx context.Context) string {
	base, _ := ctx.Value(ctxKey{}).(string)
	return base
}

// FromHeader returns the base URL stamped by Middleware into h, or "" if absent.
func FromHeader(h http.Header) string {
	if h == nil {
		return ""
	}
	return h.Get(Header)
}

// derive builds scheme://host from the request, honoring reverse-proxy forwarding headers a PaaS edge sets (Railway, Render, Fly) and falling back to the request's own scheme and Host. Forwarded values that are not a plain scheme or authority are ignored, so a client cannot mint a javascript: or foreign-path link into its own response.
func derive(r *http.Request) string {
	proto := "http"
	if Secure(r) {
		proto = "https"
	}
	host := firstToken(r.Header.Get("X-Forwarded-Host"))
	if !validAuthority(host) {
		host = r.Host
	}
	if !validAuthority(host) {
		return ""
	}
	return proto + "://" + host
}

// Secure reports whether the client reached us over https: the edge's X-Forwarded-Proto (first hop only, so a chained "https, http" still counts) or a TLS connection. Cookie Secure flags and self links must agree with it.
func Secure(r *http.Request) bool {
	switch firstToken(r.Header.Get("X-Forwarded-Proto")) {
	case "https":
		return true
	case "http":
		return false
	}
	return r.TLS != nil
}

// validAuthority reports whether host is a bare host[:port] with nothing that would change the URL's meaning (path, query, fragment, userinfo, whitespace).
func validAuthority(host string) bool {
	if host == "" || strings.ContainsAny(host, "/?#@\\ \t\r\n\"'<>") {
		return false
	}
	u, err := url.Parse("http://" + host)
	return err == nil && u.Host == host
}

// firstToken returns the first comma-separated value, trimmed: a forwarding header may carry a list when several proxies are chained.
func firstToken(v string) string {
	if i := strings.IndexByte(v, ','); i >= 0 {
		v = v[:i]
	}
	return strings.TrimSpace(v)
}
