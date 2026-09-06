// Package migrations embeds the goose SQL migrations so binaries and tests can
// apply the schema without depending on the source tree layout.
package migrations

import "embed"

// The migration dir inside FS is the root "."; pass "." as goose's directory.
//
//go:embed *.sql
var FS embed.FS
