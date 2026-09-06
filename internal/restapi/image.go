package restapi

import (
	"errors"
	"log/slog"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"github.com/davidteather/property-radar/internal/bearerauth"
	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

// Content-addressed thumbnail key ({provider}/{2-hex shard}/{sha256}.jpg). The public /img proxy validates against this before touching the store, rejecting "..", absolute paths, and anything the ingester would not have written.
var imageKeyPattern = regexp.MustCompile(`^[a-z0-9_-]+/[0-9a-f]{2}/[0-9a-f]{64}\.jpg$`)

// imageAuthorized gates the public /img routes. With PUBLIC_IMG_TOKEN unset the proxy stays fully public, the sha256 key being the only capability. With it set, a request must present the token as ?k= (used by <img src> in artifacts and the console) or the operator bearer via the Authorization header.
func (h *handler) imageAuthorized(r *http.Request) bool {
	if h.imgToken == "" {
		return true
	}
	if bearerauth.TokenMatches(r.URL.Query().Get("k"), h.imgToken) {
		return true
	}
	return bearerauth.TokenMatches(bearerauth.ParseBearer(r.Header.Get("Authorization")), h.bearerToken)
}

// Photo proxy: the unguessable sha256 key is the capability. When PUBLIC_IMG_TOKEN is set the request must also carry it (see imageAuthorized).
func (h *handler) image(w http.ResponseWriter, r *http.Request) {
	if !h.imageAuthorized(r) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	key := r.PathValue("key")
	if !imageKeyPattern.MatchString(key) {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	data, err := h.svc.PhotoObject(r.Context(), key)
	if err != nil {
		if errors.Is(err, listings.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("rest: read photo object", "key", key, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.writeImage(w, key, true, data)
}

// imageByListing is the public constructable photo resolver GET /img/l/{id}/{n}. It maps a (listing id, 0-based position) pair — which a caller has from any compact row — to the cached thumbnail, so a many-listing gallery's URLs need zero tool calls. It resolves the content-addressed key from the DB, re-validates its shape before touching the store, and serves the same bytes with a short cache life, since a re-crawl can reorder or replace a position. Any miss (unknown listing, out-of-range position, uncached photo, bad key shape) is a 404.
func (h *handler) imageByListing(w http.ResponseWriter, r *http.Request) {
	if !h.imageAuthorized(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	id, err := strconv.Atoi(r.PathValue("id"))
	if err != nil || id <= 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	n, err := strconv.Atoi(r.PathValue("n"))
	if err != nil || n < 0 {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	key, err := h.svc.PhotoKeyAt(r.Context(), domain.PropertyID(id), n)
	if err != nil {
		// Any miss (unknown listing, out-of-range position, uncached photo) is 404; only a store outage is 500.
		if errors.Is(err, listings.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("rest: resolve listing photo", "listing", id, "position", n, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	// Re-validate the resolved key against the content-addressed shape before touching the store, as /img/{key} does.
	if !imageKeyPattern.MatchString(key) {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}
	// The key doubles as the ETag: a tile only changes when its key does.
	if etagMatches(r.Header.Get("If-None-Match"), key) {
		h.cacheHeaders(w, key, false)
		w.WriteHeader(http.StatusNotModified)
		return
	}
	data, err := h.svc.PhotoObject(r.Context(), key)
	if err != nil {
		if errors.Is(err, listings.ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)
			return
		}
		slog.Error("rest: read listing photo", "key", key, "err", err)
		http.Error(w, "internal server error", http.StatusInternalServerError)
		return
	}
	h.writeImage(w, key, false, data)
}

// resolverMaxAge bounds how stale a /img/l/{id}/{n} tile may get after a re-crawl reorders photos.
const resolverMaxAge = "300"

// writeImage streams cached thumbnail bytes. A content-addressed /img/{key} is
// immutable; the resolver is not, so it revalidates via the key as ETag. A gated
// proxy stays in private caches so a shared cache never outlives a token rotation.
func (h *handler) writeImage(w http.ResponseWriter, key string, immutable bool, data []byte) {
	h.cacheHeaders(w, key, immutable)
	w.Header().Set("Content-Type", "image/jpeg")
	w.Header().Set("Content-Length", strconv.Itoa(len(data)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	_, _ = w.Write(data)
}

// etagMatches reads If-None-Match as RFC 9110 does: a comma list, each entry
// possibly weakened with W/, or a bare * (any current representation).
func etagMatches(header, key string) bool {
	want := etagFor(key)
	for tag := range strings.SplitSeq(header, ",") {
		tag = strings.TrimSpace(tag)
		if tag == "*" || strings.TrimPrefix(tag, "W/") == want {
			return true
		}
	}
	return false
}

func (h *handler) cacheHeaders(w http.ResponseWriter, key string, immutable bool) {
	scope := "public"
	if h.imgToken != "" {
		scope = "private"
	}
	w.Header().Set("Vary", "Authorization")
	if immutable {
		w.Header().Set("Cache-Control", scope+", max-age=31536000, immutable")
		return
	}
	w.Header().Set("Cache-Control", scope+", max-age="+resolverMaxAge)
	w.Header().Set("ETag", etagFor(key))
}

func etagFor(key string) string { return `"` + key + `"` }
