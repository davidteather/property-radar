package restapi_test

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/mock"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
	"github.com/davidteather/property-radar/internal/listings/mocks"
	"github.com/davidteather/property-radar/internal/restapi"

	"net/http/httptest"
)

// gateKey is the PUBLIC_IMG_TOKEN used to exercise the gated /img proxy; a lesser, read-only capability distinct from the operator bearer.
const gateKey = "img-gate-abc123"

// newGatedFixture is newFixture with a non-empty PUBLIC_IMG_TOKEN, so the /img proxy requires ?k=<gateKey> (or the operator bearer) and minted URLs carry it.
func newGatedFixture(t *testing.T, imgToken string) *fixture {
	t.Helper()
	st := mocks.NewMockStore(t)
	ph := mocks.NewMockPhotoStore(t)
	svc := listings.NewService(st, ph)
	srv := httptest.NewServer(restapi.NewHandler(svc, token, imgToken, ""))
	t.Cleanup(srv.Close)
	return &fixture{store: st, photos: ph, url: srv.URL}
}

func TestImageProxyGatedRequiresKey(t *testing.T) {
	f := newGatedFixture(t, gateKey)
	key := validImageKey()
	want := tinyJPEG(t)
	// Get is reachable only past the gate; the forbidden requests below must not touch the store, which the mock asserts by only expecting authorized calls.
	f.photos.EXPECT().Get(mock.Anything, key).Return(want, nil)

	cases := []struct {
		name, url, auth string
		want            int
	}{
		{"no key", f.url + "/img/" + key, "", http.StatusForbidden},
		{"wrong key", f.url + "/img/" + key + "?k=nope", "", http.StatusForbidden},
		{"correct key", f.url + "/img/" + key + "?k=" + gateKey, "", http.StatusOK},
		{"operator bearer", f.url + "/img/" + key, bearer(), http.StatusOK},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			resp := do(t, http.MethodGet, tc.url, tc.auth, nil)
			defer func() { _ = resp.Body.Close() }()
			if resp.StatusCode != tc.want {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.want)
			}
		})
	}
}

func TestImageByListingGatedRequiresKey(t *testing.T) {
	f := newGatedFixture(t, gateKey)
	key := validImageKey()
	want := tinyJPEG(t)
	f.store.EXPECT().PhotoKeyAt(mock.Anything, domain.PropertyID(5), 0).Return(key, nil)
	f.photos.EXPECT().Get(mock.Anything, key).Return(want, nil)

	// A gated resolver hides existence: no key is a 404, not a 403.
	resp := do(t, http.MethodGet, f.url+"/img/l/5/0", "", nil)
	status := resp.StatusCode
	_ = resp.Body.Close()
	if status != http.StatusNotFound {
		t.Fatalf("no-key status = %d, want 404", status)
	}

	resp = do(t, http.MethodGet, f.url+"/img/l/5/0?k="+gateKey, "", nil)
	status = resp.StatusCode
	_ = resp.Body.Close()
	if status != http.StatusOK {
		t.Fatalf("keyed status = %d, want 200", status)
	}
}

func TestListingsPhotosURLsCarryKeyWhenGated(t *testing.T) {
	f := newGatedFixture(t, gateKey)
	f.store.EXPECT().PhotoCounts(mock.Anything, mock.Anything).Return(map[domain.PropertyID]listings.PhotoCount{
		5: {Total: 1, Cached: 1},
	}, nil)

	resp := do(t, http.MethodPost, f.url+"/v1/listings/photos", bearer(), map[string]any{"ids": []int64{5}})
	body := decode(t, resp)
	uris := body["listings"].([]any)[0].(map[string]any)["image_uris"].([]any)
	if len(uris) != 1 {
		t.Fatalf("image_uris = %v, want 1 URL", uris)
	}
	// The minted gallery URL must carry the access key so it loads through the gate.
	if u, _ := uris[0].(string); !strings.Contains(u, "/img/l/5/0?k="+gateKey) {
		t.Fatalf("image uri = %q, want it to carry ?k=%s", uris[0], gateKey)
	}
}
