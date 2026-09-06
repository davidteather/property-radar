package restapi

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// REST and the registry manifest share one version; .bumpversion.cfg rewrites both.
func TestAPIVersionMatchesRegistryManifest(t *testing.T) {
	raw, err := os.ReadFile("../../server.json")
	require.NoError(t, err)
	var manifest struct {
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, apiVersion, manifest.Version)
}
