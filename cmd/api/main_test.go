package main

import (
	"flag"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
)

func TestResolveAddr(t *testing.T) {
	cases := []struct {
		name     string
		explicit bool
		addr     string
		port     string
		want     string
	}{
		{"default when nothing set", false, ":8080", "", ":8080"},
		{"port applies when addr not passed", false, ":8080", "3000", ":3000"},
		{"explicit addr wins over port", true, ":9999", "3000", ":9999"},
		{"blank port ignored", false, ":8080", "  ", ":8080"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := resolveAddr(tc.explicit, tc.addr, tc.port); got != tc.want {
				t.Fatalf("resolveAddr(%v, %q, %q) = %q, want %q", tc.explicit, tc.addr, tc.port, got, tc.want)
			}
		})
	}
}

// run must refuse to start without the shared bearer token, before it ever
// touches the database — the wiring proof that does not need Postgres.
func TestRunRequiresBearerToken(t *testing.T) {
	// Isolate the global flag set from the test binary's flags.
	orig := flag.CommandLine
	origArgs := os.Args
	t.Cleanup(func() { flag.CommandLine = orig; os.Args = origArgs })
	flag.CommandLine = flag.NewFlagSet("apid", flag.ContinueOnError)
	os.Args = []string{"apid"}

	t.Setenv("DATABASE_URL", "postgres://user:pass@localhost:5432/db")
	t.Setenv("MCP_BEARER_TOKEN", "")
	t.Setenv("STORAGE_BACKEND", "local")
	t.Setenv("THUMBS_DIR", t.TempDir())

	err := run(slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err == nil {
		t.Fatal("expected run to fail without MCP_BEARER_TOKEN")
	}
	if !strings.Contains(err.Error(), "MCP_BEARER_TOKEN") {
		t.Fatalf("error = %v, want it to mention MCP_BEARER_TOKEN", err)
	}
}
