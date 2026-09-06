package streeteasy

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestParseDetailPage(t *testing.T) {
	listing, raw, err := parseDetailPage(readFixture(t, "detail_page_coop_1777453.html"))
	if err != nil {
		t.Fatalf("parseDetailPage: %v", err)
	}
	if listing.ID != "1777453" {
		t.Errorf("id = %q", listing.ID)
	}
	if listing.Status != "ACTIVE" {
		t.Errorf("status = %q", listing.Status)
	}
	assertInt(t, "daysOnMarket", positive(listing.DaysOnMarket), 426)

	// description is emitted as a `$<hexid>` reference into a T chunk; an
	// unresolved payload would leave the literal "$b7" here.
	if strings.HasPrefix(listing.Description, "$") {
		t.Fatalf("description reference not resolved: %q", listing.Description)
	}
	if !strings.HasPrefix(listing.Description, "Sunny one-bedroom co-op") {
		t.Errorf("description = %q", listing.Description)
	}

	if got := money(listing.Pricing.MonthlyMaintenance); got == nil || *got != 912 {
		t.Errorf("monthlyMaintenance = %v", got)
	}
	if listing.PropertyDetails.Address.Street != "288 Larkspur Street" {
		t.Errorf("street = %q", listing.PropertyDetails.Address.Street)
	}
	assertInt(t, "livingAreaSize", positive(listing.PropertyDetails.LivingAreaSize), 711)
	if len(listing.PropertyDetails.Amenities.List) == 0 {
		t.Error("amenities not parsed")
	}
	if len(listing.Media.Photos) != 8 {
		t.Errorf("photos = %d, want 8", len(listing.Media.Photos))
	}

	var round map[string]any
	if err := json.Unmarshal(raw, &round); err != nil {
		t.Fatalf("raw provenance is not valid json: %v", err)
	}
	if _, ok := round["propertyHistory"]; !ok {
		t.Error("raw provenance should retain fields the wire struct ignores")
	}
}

func TestParseDetailPageMatchesExtractedFixture(t *testing.T) {
	fromHTML, _, err := parseDetailPage(readFixture(t, "detail_page_coop_1777453.html"))
	if err != nil {
		t.Fatalf("parseDetailPage: %v", err)
	}
	fromJSON := decodeDetailFixture(t, "detail_coop_1777453.json")

	if fromHTML.Description != fromJSON.Description {
		t.Error("description differs between html and extracted-json fixtures")
	}
	if fromHTML.ID != fromJSON.ID || len(fromHTML.Media.Photos) != len(fromJSON.Media.Photos) {
		t.Error("html and extracted-json fixtures disagree")
	}
}

func TestParseDetailPageErrors(t *testing.T) {
	cases := map[string][]byte{
		"no flight payload": []byte(`<html><body>nothing here</body></html>`),
		"flight without listing": []byte(
			`<script>self.__next_f.push([1,"0:{\"foo\":1}\n"])</script>`),
		"listing without id": []byte(
			`<script>self.__next_f.push([1,"0:{\"listing\":{\"id\":\"\"}}\n"])</script>`),
	}
	for name, page := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := parseDetailPage(page); !errors.Is(err, ErrListingNotFound) {
				t.Errorf("err = %v, want ErrListingNotFound", err)
			}
		})
	}
}

func TestFlightChunks(t *testing.T) {
	// A T chunk length is a byte count and its body may contain newlines.
	flight := "3:HL[\"/a.css\"]\nb7:Tb,line1\nline2\nc0:{\"x\":1}\n"
	chunks := flightChunks(flight)
	if got := chunks["b7"]; got != "line1\nline2" {
		t.Errorf("T chunk = %q", got)
	}
	if got := chunks["3"]; got != `HL["/a.css"]` {
		t.Errorf("newline chunk = %q", got)
	}
	if got := chunks["c0"]; got != `{"x":1}` {
		t.Errorf("json chunk = %q", got)
	}

	// T lengths count bytes, not runes.
	multibyte := flightChunks("a1:T7,café\nx\n")
	if got := multibyte["a1"]; got != "café\nx" {
		t.Errorf("multibyte T chunk = %q", got)
	}

	// A hostile length must clamp rather than overflow i+n and panic.
	huge := flightChunks("d2:T7fffffffffffffff,rest\n")
	if got := huge["d2"]; got != "rest\n" {
		t.Errorf("overflowing T chunk = %q", got)
	}
}

func TestFlightPayloadConcatenatesSplitPushes(t *testing.T) {
	page := []byte(
		`<script>self.__next_f.push([1,"0:{\"lis"])</script>` +
			`<script>self.__next_f.push([1,"ting\":{\"id\":\"7\"}}\n"])</script>`)
	got := flightPayload(page)
	if got != "0:{\"listing\":{\"id\":\"7\"}}\n" {
		t.Fatalf("flightPayload = %q", got)
	}
}

func TestResolveRefsNested(t *testing.T) {
	chunks := map[string]string{"b7": "long text", "ff": "other"}
	in := map[string]any{
		"a": "$b7",
		"b": []any{"$ff", "$missing", "plain"},
		"c": map[string]any{"d": "$b7"},
		"e": "$notlowerhex!",
		"f": float64(3),
	}
	out, ok := resolveRefs(in, chunks).(map[string]any)
	if !ok {
		t.Fatal("resolveRefs did not return a map")
	}
	if out["a"] != "long text" {
		t.Errorf("a = %v", out["a"])
	}
	list := out["b"].([]any)
	if list[0] != "other" || list[1] != "$missing" || list[2] != "plain" {
		t.Errorf("b = %v", list)
	}
	if out["c"].(map[string]any)["d"] != "long text" {
		t.Errorf("c.d = %v", out["c"])
	}
	if out["e"] != "$notlowerhex!" || out["f"] != float64(3) {
		t.Errorf("e/f mangled: %v %v", out["e"], out["f"])
	}
}

// A wrong-typed non-numeric field on the detail page costs that field, not the
// enrichment: failing it would leave the listing re-fetched on every run.
func TestParseDetailPageKeepsAListingPastAWrongTypedField(t *testing.T) {
	page := []byte(`<script>self.__next_f.push([1,"0:{\"listing\":{\"id\":\"42\",\"status\":\"ACTIVE\",\"description\":{\"nested\":true},\"daysOnMarket\":12}}\n"])</script>`)
	listing, _, err := parseDetailPage(page)
	if err != nil {
		t.Fatalf("err = %v, want the listing minus the bad field", err)
	}
	if listing.ID != "42" || listing.Status != "ACTIVE" || listing.Description != "" {
		t.Errorf("listing = %+v", listing)
	}
	assertInt(t, "daysOnMarket", positive(listing.DaysOnMarket), 12)
}
