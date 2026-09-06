package streeteasy

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
)

var (
	flightPushRE = regexp.MustCompile(`self\.__next_f\.push\(\[1,("(?:[^"\\]|\\.)*")\]\)`)
	chunkHeadRE  = regexp.MustCompile(`^([0-9a-f]+):`)
	chunkRefRE   = regexp.MustCompile(`^\$([0-9a-f]+)$`)
	listingKeyRE = regexp.MustCompile(`"listing":\s*\{"id":"`)
)

func (c *Client) fetchDetail(ctx context.Context, urlPath string) (*detailListing, json.RawMessage, error) {
	if !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	page, err := c.do(ctx, func() (*http.Request, error) {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.SiteURL+urlPath, nil)
		if err != nil {
			return nil, err
		}
		req.Header.Set("accept", "text/html,application/xhtml+xml,application/xml;q=0.9,*/*;q=0.8")
		req.Header.Set("accept-language", "en-US,en;q=0.9")
		req.Header.Set("user-agent", c.cfg.UserAgent)
		return req, nil
	})
	if err != nil {
		return nil, nil, err
	}
	return parseDetailPage(page)
}

func parseDetailPage(page []byte) (*detailListing, json.RawMessage, error) {
	flight := flightPayload(page)
	if flight == "" {
		return nil, nil, ErrListingNotFound
	}
	obj, err := extractListingObject(flight)
	if err != nil {
		return nil, nil, err
	}
	resolved := resolveRefs(obj, flightChunks(flight))

	raw, err := json.Marshal(resolved)
	if err != nil {
		return nil, nil, fmt.Errorf("encode listing: %w", err)
	}
	var listing detailListing
	if err := json.Unmarshal(raw, &listing); err != nil {
		// The decoder fills every other field past a wrong-typed one; with an
		// id in hand the listing is still worth keeping, minus that field.
		var typeErr *json.UnmarshalTypeError
		if !errors.As(err, &typeErr) || listing.ID == "" {
			return nil, nil, fmt.Errorf("decode listing: %w", err)
		}
	}
	if listing.ID == "" {
		return nil, nil, ErrListingNotFound
	}
	return &listing, raw, nil
}

// The RSC payload is split across many push() calls mid-token; only the concatenation parses.
func flightPayload(page []byte) string {
	matches := flightPushRE.FindAllSubmatch(page, -1)
	var b strings.Builder
	for _, m := range matches {
		var s string
		if err := json.Unmarshal(m[1], &s); err != nil {
			continue
		}
		b.WriteString(s)
	}
	return b.String()
}

// Chunk records are `<hexid>:<body>`; a `T<hexlen>,` body is exactly hexlen bytes
// of raw text (newlines included), anything else ends at the newline.
func flightChunks(flight string) map[string]string {
	chunks := make(map[string]string)
	for i := 0; i < len(flight); {
		m := chunkHeadRE.FindStringSubmatch(flight[i:min(i+20, len(flight))])
		if m == nil {
			nl := strings.IndexByte(flight[i:], '\n')
			if nl < 0 {
				break
			}
			i += nl + 1
			continue
		}
		id := m[1]
		i += len(m[0])
		if i < len(flight) && flight[i] == 'T' {
			comma := strings.IndexByte(flight[i:], ',')
			if comma < 0 {
				break
			}
			n, err := strconv.ParseInt(flight[i+1:i+comma], 16, 64)
			if err != nil || n < 0 {
				break
			}
			i += comma + 1
			// Clamp before adding: a hostile length would wrap i+n negative.
			end := len(flight)
			if n < int64(len(flight)-i) {
				end = i + int(n)
			}
			chunks[id] = flight[i:end]
			i = end
			if i < len(flight) && flight[i] == '\n' {
				i++
			}
			continue
		}
		nl := strings.IndexByte(flight[i:], '\n')
		if nl < 0 {
			chunks[id] = flight[i:]
			break
		}
		chunks[id] = flight[i : i+nl]
		i += nl + 1
	}
	return chunks
}

func extractListingObject(flight string) (map[string]any, error) {
	loc := listingKeyRE.FindStringIndex(flight)
	if loc == nil {
		return nil, ErrListingNotFound
	}
	start := strings.IndexByte(flight[loc[0]:loc[1]], '{') + loc[0]
	dec := json.NewDecoder(strings.NewReader(flight[start:]))
	var obj map[string]any
	if err := dec.Decode(&obj); err != nil {
		return nil, fmt.Errorf("decode listing object: %w", err)
	}
	return obj, nil
}

func resolveRefs(v any, chunks map[string]string) any {
	switch t := v.(type) {
	case string:
		if m := chunkRefRE.FindStringSubmatch(t); m != nil {
			if s, ok := chunks[m[1]]; ok {
				return s
			}
		}
		return t
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, val := range t {
			out[k] = resolveRefs(val, chunks)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, val := range t {
			out[i] = resolveRefs(val, chunks)
		}
		return out
	default:
		return v
	}
}
