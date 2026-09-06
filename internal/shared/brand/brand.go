// Package brand holds the shared Property Radar icon, used both as the served
// favicon and as the MCP server icon a host (e.g. Claude) displays.
package brand

import (
	_ "embed"
	"encoding/base64"
)

//go:embed icon.svg
var IconSVG []byte

// IconDataURI returns the icon as a base64 data URI, so it can be advertised in
// the MCP Implementation (which a stdio server cannot serve over HTTP).
func IconDataURI() string {
	return "data:image/svg+xml;base64," + base64.StdEncoding.EncodeToString(IconSVG)
}
