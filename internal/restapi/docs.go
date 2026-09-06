package restapi

import (
	"net/http"

	"github.com/davidteather/property-radar/internal/shared/brand"
)

// scalarCDN is the Scalar API-reference standalone build, pinned and integrity-
// checked: /docs runs on the origin operators paste the bearer token into, so a
// tampered "latest" must not execute there. Bump both together.
const scalarCDN = "https://cdn.jsdelivr.net/npm/@scalar/api-reference@1.67.0/dist/browser/standalone.js"

const scalarSRI = "sha384-6c7Vmx+i0yi8gBbltn0x1cavD+zsMGw2xmXXVyacPJLIGBxwaVimW5TW0WiW17Ir"

const openAPIJSONPath = "/openapi.json"

// docsHTML is the shell Scalar mounts into: the empty <script id="api-reference"
// data-url> carries no code; the CDN standalone reads that url and renders.
const docsHTML = `<!doctype html>
<html lang="en">
<head>
<meta charset="utf-8">
<meta name="viewport" content="width=device-width, initial-scale=1">
<title>Property Radar API</title>
<link rel="icon" href="/favicon.svg" type="image/svg+xml">
</head>
<body>
<script id="api-reference" data-url="` + openAPIJSONPath + `"></script>
<script src="` + scalarCDN + `" integrity="` + scalarSRI + `" crossorigin="anonymous"></script>
</body>
</html>`

// registerDocs mounts the docs UI (GET /docs), unauthenticated, with a CSP that
// permits the Scalar CDN and same-origin spec fetch and nothing else.
func registerDocs(mux *http.ServeMux) {
	mux.HandleFunc("GET /docs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Content-Security-Policy", "script-src 'self' 'unsafe-eval' https://cdn.jsdelivr.net; style-src 'self' 'unsafe-inline' https://cdn.jsdelivr.net https://fonts.googleapis.com; img-src 'self' data:; font-src 'self' data: https://fonts.gstatic.com https://cdn.jsdelivr.net; connect-src 'self' https://cdn.jsdelivr.net; worker-src 'self' blob:; frame-ancestors 'none'")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(docsHTML))
	})
	favicon := func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "image/svg+xml")
		w.Header().Set("Cache-Control", "public, max-age=86400")
		_, _ = w.Write(brand.IconSVG)
	}
	mux.HandleFunc("GET /favicon.svg", favicon)
	mux.HandleFunc("GET /favicon.ico", favicon)
}
