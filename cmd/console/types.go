package main

import (
	"fmt"
	"html/template"
	"strings"
)

// Response subsets we actually render. Extra API fields are ignored by json.

type stateResp struct {
	Profile     profile   `json:"profile"`
	Rubric      *rubric   `json:"rubric"`
	RubricStale bool      `json:"rubric_stale"`
	Verdicts    []verdict `json:"verdicts"`
}

type profile struct {
	MaxPrice      *int64   `json:"max_price"`
	MinBeds       *int     `json:"min_beds"`
	MinBaths      *float64 `json:"min_baths"`
	ListingType   string   `json:"listing_type"`
	Neighborhoods []string `json:"neighborhoods"`
	PropertyTypes []string `json:"property_types"`
}

type rubric struct {
	Content   string `json:"content"`
	CreatedAt string `json:"created_at"`
}

type verdict struct {
	ID        int64  `json:"id"`
	ListingID int64  `json:"listing_id"`
	Verdict   string `json:"verdict"`
	Note      string `json:"note"`
	CreatedAt string `json:"created_at"`
}

type listResp struct {
	Total    int          `json:"total"`
	Count    int          `json:"count"`
	Offset   int          `json:"offset"`
	Limit    int          `json:"limit"`
	Listings []listingRow `json:"listings"`
}

type listingRow struct {
	ID           int64    `json:"id"`
	Address      string   `json:"address"`
	Neighborhood string   `json:"neighborhood"`
	Price        *int64   `json:"price"`
	Beds         *int     `json:"beds"`
	Baths        *float64 `json:"baths"`
	Sqft         *int     `json:"sqft"`
	PropertyType string   `json:"property_type"`
	ListingType  string   `json:"listing_type"`
	DOM          *int     `json:"dom"`
	PriceDrop    bool     `json:"price_drop"`
	Status       string   `json:"status"`
	URL          string   `json:"url"`
	PhotoCount   int      `json:"photo_count"`
	PhotosCached int      `json:"photos_cached"`
}

type detailResp struct {
	ID            int64         `json:"id"`
	Address       string        `json:"address"`
	Neighborhood  string        `json:"neighborhood"`
	Zip           string        `json:"zip"`
	ListingType   string        `json:"listing_type"`
	PropertyType  string        `json:"property_type"`
	Status        string        `json:"status"`
	Price         *int64        `json:"price"`
	Beds          *int          `json:"beds"`
	Baths         *float64      `json:"baths"`
	Sqft          *int          `json:"sqft"`
	Maintenance   *int64        `json:"maintenance"`
	CommonCharges *int64        `json:"common_charges"`
	TaxesMonthly  *int64        `json:"taxes_monthly"`
	DOM           *int          `json:"dom"`
	Description   string        `json:"description"`
	FirstSeen     string        `json:"first_seen"`
	LastSeen      string        `json:"last_seen"`
	PhotoCount    int           `json:"photo_count"`
	PhotosCached  int           `json:"photos_cached"`
	PhotoMissing  int           `json:"photo_missing"`
	Photos        []photoRef    `json:"photos"`
	PriceHistory  []priceEvent  `json:"price_history"`
	Lists         []consoleList `json:"lists"`
	Verdicts      []verdict     `json:"verdicts"`
	Provenance    struct {
		URL string `json:"url"`
	} `json:"provenance"`
}

type photoRef struct {
	Position       int    `json:"position"`
	HostedImageURI string `json:"hosted_image_uri"`
}

type priceEvent struct {
	Price      int64  `json:"price"`
	ObservedAt string `json:"observed_at"`
}

type targetsResp struct {
	Targets []crawlTarget `json:"targets"`
}

func (t targetsResp) list() []crawlTarget { return t.Targets }

type crawlTarget struct {
	ID          int64    `json:"id"`
	Kind        string   `json:"kind"`
	Status      string   `json:"status"`
	Enabled     bool     `json:"enabled"`
	Areas       []string `json:"areas"`
	ListingType string   `json:"listing_type"`
	MaxPrice    *int64   `json:"max_price"`
	MinBeds     *int     `json:"min_beds"`
	Note        string   `json:"note"`
	LastRunAt   string   `json:"last_run_at"`
	LastError   string   `json:"last_error"`
}

// InFlight reports whether a target is waiting or mid-crawl, so the page can
// auto-refresh until it settles. A standing scope's status is its latest pass,
// so only running counts there (pending just means no pass yet).
func (t crawlTarget) InFlight() bool {
	if t.Kind == "standing" {
		return t.Status == "running"
	}
	return t.Status == "pending" || t.Status == "running"
}

type corpusStats struct {
	Listings       int    `json:"listings"`
	ActiveListings int    `json:"active_listings"`
	ActiveSale     int    `json:"active_sale"`
	ActiveRent     int    `json:"active_rent"`
	PhotosCached   int    `json:"photos_cached"`
	PendingCrawls  int    `json:"pending_crawls"`
	StandingScopes int    `json:"standing_scopes"`
	LastCrawlAt    string `json:"last_crawl_at"`
}

var funcs = template.FuncMap{
	"money": func(v *int64) string {
		if v == nil {
			return "—"
		}
		return "$" + commas(*v)
	},
	"money0": func(v int64) string { return "$" + commas(v) },
	"num": func(v *int) string {
		if v == nil {
			return "—"
		}
		return fmt.Sprintf("%d", *v)
	},
	"baths": func(v *float64) string {
		if v == nil {
			return "—"
		}
		return strings.TrimSuffix(fmt.Sprintf("%.1f", *v), ".0")
	},
	"title": func(s string) string {
		if s == "" {
			return "—"
		}
		return strings.ToUpper(s[:1]) + s[1:]
	},
	"join": strings.Join,
	"short": func(s string, n int) string {
		r := []rune(s)
		if len(r) <= n {
			return s
		}
		return string(r[:n]) + "…"
	},
	"date": func(s string) string {
		if len(s) >= 10 {
			return s[:10]
		}
		return s
	},
	"add": func(a, b int) int { return a + b },
	"sub": func(a, b int) int { return a - b },
}

func commas(v int64) string {
	s := fmt.Sprintf("%d", v)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []byte
	for i, c := range []byte(s) {
		if i > 0 && (len(s)-i)%3 == 0 {
			out = append(out, ',')
		}
		out = append(out, c)
	}
	if neg {
		return "-" + string(out)
	}
	return string(out)
}

type consoleList struct {
	ID        int64  `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Emoji     string `json:"emoji"`
	IsDefault bool   `json:"is_default"`
	Count     int    `json:"count"`
}

type listsPayload struct {
	Lists []consoleList `json:"lists"`
}

type listDetailPayload struct {
	List     consoleList  `json:"list"`
	Listings []listingRow `json:"listings"`
}

type ratedEntry struct {
	Listing listingRow `json:"listing"`
	Verdict string     `json:"verdict"`
	Note    string     `json:"note"`
	History []verdict  `json:"history"`
}

type ratedPayload struct {
	Rubric      *rubric      `json:"rubric"`
	RubricStale bool         `json:"rubric_stale"`
	Count       int          `json:"count"`
	Rated       []ratedEntry `json:"rated"`
}
