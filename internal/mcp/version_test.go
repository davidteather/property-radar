package mcp

import (
	"encoding/json"
	"os"
	"testing"

	"github.com/stretchr/testify/require"
)

// The registry manifest and the MCP server must advertise the same version;
// .bumpversion.cfg rewrites both, so a drift here means the config lost a file.
func TestServerVersionMatchesRegistryManifest(t *testing.T) {
	raw, err := os.ReadFile("../../server.json")
	require.NoError(t, err)
	var manifest struct {
		Name    string `json:"name"`
		Version string `json:"version"`
	}
	require.NoError(t, json.Unmarshal(raw, &manifest))
	require.Equal(t, "io.github.davidteather/property-radar", manifest.Name)
	require.Equal(t, serverVersion, manifest.Version)
}
