package mcp_test

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/ingest"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/mcp"
	"github.com/davidteather/property-radar/internal/pgtest"
	"github.com/davidteather/property-radar/internal/photostore"
	"github.com/davidteather/property-radar/internal/shared/ptr"
	"github.com/davidteather/property-radar/internal/store"
)

func TestMain(m *testing.M) {
	os.Exit(pgtest.StartMain(m))
}

// localPhotos is the on-disk photo store the MCP tests read cached thumbnails through; test helpers write JPEG files under dir.
func localPhotos(t *testing.T, dir string) *photostore.Local {
	t.Helper()
	store, err := photostore.NewLocal(dir)
	if err != nil {
		t.Fatalf("local photo store: %v", err)
	}
	return store
}

const testProvider = "streeteasy"

// testPublicBase is the in-memory harness's fallback origin (no HTTP request carries one), so get_listing photo resource_link URLs are absolute in tests.
const testPublicBase = "https://radar.test"

var baseTime = time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)

// harness drives the registered tools through a real client/server session, so every test exercises schema validation and JSON encoding too.
type harness struct {
	t       *testing.T
	store   *store.Store
	thumbs  string
	runID   domain.IngestRunID
	session *mcpsdk.ClientSession
}

func newHarness(t *testing.T) *harness {
	t.Helper()
	return newHarnessWithAreas(t, nil)
}

func newHarnessWithAreas(t *testing.T, areas []domain.Area) *harness {
	t.Helper()
	pool := pgtest.Pool(t)
	pgtest.TruncateAll(t, pool)

	ctx := context.Background()
	st := store.New(pool)
	thumbs := t.TempDir()

	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	svc := listings.NewService(st, localPhotos(t, thumbs))
	svc.LoadAreas(areas)
	serverSession, err := mcp.NewServerFromService(svc, mcp.WithPublicBaseURL(testPublicBase)).MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "test-caller", Version: "0"}, nil)
	session, err := client.Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	t.Cleanup(func() {
		_ = session.Close()
		_ = serverSession.Close()
	})

	run, err := st.CreateRun(ctx, testProvider, "scope-a", false)
	if err != nil {
		t.Fatalf("create run: %v", err)
	}
	return &harness{t: t, store: st, thumbs: thumbs, runID: run.ID, session: session}
}

func (h *harness) call(name string, args any) *mcpsdk.CallToolResult {
	h.t.Helper()
	res, err := h.session.CallTool(context.Background(), &mcpsdk.CallToolParams{Name: name, Arguments: args})
	if err != nil {
		h.t.Fatalf("call %s: %v", name, err)
	}
	return res
}

func (h *harness) mustCall(name string, args any) *mcpsdk.CallToolResult {
	h.t.Helper()
	res := h.call(name, args)
	if res.IsError {
		h.t.Fatalf("call %s returned a tool error: %s", name, resultText(res))
	}
	return res
}

func (h *harness) mustFail(name string, args any) string {
	h.t.Helper()
	res := h.call(name, args)
	if !res.IsError {
		h.t.Fatalf("call %s succeeded, want a tool error", name)
	}
	return resultText(res)
}

func (h *harness) apply(sp ingest.SourceProperty, at time.Time) domain.PropertyID {
	h.t.Helper()
	res, err := h.store.ApplySourceProperty(context.Background(), h.runID, sp, at)
	if err != nil {
		h.t.Fatalf("apply source property %s: %v", sp.ProviderID, err)
	}
	return res.PropertyID
}

func decodeResult[T any](t *testing.T, res *mcpsdk.CallToolResult) T {
	t.Helper()
	raw, err := json.Marshal(res.StructuredContent)
	if err != nil {
		t.Fatalf("marshal structured content: %v", err)
	}
	var out T
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatalf("decode structured content %s: %v", raw, err)
	}
	return out
}

func resultText(res *mcpsdk.CallToolResult) string {
	var b strings.Builder
	for _, c := range res.Content {
		if text, ok := c.(*mcpsdk.TextContent); ok {
			b.WriteString(text.Text)
		}
	}
	return b.String()
}

func imageBlocks(res *mcpsdk.CallToolResult) []*mcpsdk.ImageContent {
	var out []*mcpsdk.ImageContent
	for _, c := range res.Content {
		if img, ok := c.(*mcpsdk.ImageContent); ok {
			out = append(out, img)
		}
	}
	return out
}

func resourceLinks(res *mcpsdk.CallToolResult) []*mcpsdk.ResourceLink {
	var out []*mcpsdk.ResourceLink
	for _, c := range res.Content {
		if link, ok := c.(*mcpsdk.ResourceLink); ok {
			out = append(out, link)
		}
	}
	return out
}

func newSource(providerID string) ingest.SourceProperty {
	return ingest.SourceProperty{
		Provider:     testProvider,
		ProviderID:   providerID,
		URL:          "https://streeteasy.com/sale/" + providerID,
		Raw:          json.RawMessage(`{"id":"` + providerID + `"}`),
		SourceStatus: "active",
		Property: domain.Property{
			Address: domain.Address{
				Street:       "123 Prospect Park West",
				Unit:         "4B",
				Neighborhood: "Park Slope",
				Zip:          "11215",
			},
			Geo:          &domain.GeoPoint{Latitude: 40.66, Longitude: -73.98},
			ListingType:  domain.ListingSale,
			PropertyType: domain.PropertyCoop,
			Status:       domain.StatusActive,
			Price:        money(900_000),
			Bedrooms:     ptr.To(2),
			Bathrooms:    ptr.To(1.5),
			Sqft:         ptr.To(900),
			Maintenance:  money(1200),
			TaxesMonthly: money(300),
			DaysOnMarket: ptr.To(10),
			Description:  "sunny corner unit",
		},
		PhotoURLs: []string{
			"https://photos.example/" + providerID + "/1.jpg",
			"https://photos.example/" + providerID + "/2.jpg",
		},
	}
}

func money(v int64) *domain.Money {
	m := domain.Money(v)
	return &m
}
