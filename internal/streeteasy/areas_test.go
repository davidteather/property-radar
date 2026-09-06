package streeteasy

import "testing"

func TestEmbeddedAreasParse(t *testing.T) {
	areas, err := Areas()
	if err != nil {
		t.Fatalf("Areas: %v", err)
	}
	if len(areas) < 300 {
		t.Fatalf("expected the full NYC taxonomy, got %d areas", len(areas))
	}
	var found bool
	for _, a := range areas {
		if a.Provider != ProviderName {
			t.Fatalf("area %s tagged provider %q, want %q", a.ID, a.Provider, ProviderName)
		}
		if a.Name == "West Village" && a.ID == "157" {
			found = true
		}
	}
	if !found {
		t.Error("expected West Village (id 157) in the embedded catalog")
	}
}
