package main

import (
	"context"
	"os/exec"
	"testing"
	"time"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/pgtest"
)

const smokeTimeout = 2 * time.Minute

// The one test that exercises the real process: a client speaks MCP to a built
// mcpd over its stdin/stdout, which only works if nothing else writes there.
func TestMcpdServesMCPOverStdio(t *testing.T) {
	if testing.Short() {
		t.Skip("mcpd stdio smoke needs Postgres and a build; skipped with -short")
	}
	ctx, cancel := context.WithTimeout(context.Background(), smokeTimeout)
	defer cancel()

	dsn := migratedPostgres(ctx, t)
	binary := buildMcpd(t)

	cmd := exec.Command(binary)
	cmd.Env = append(cmd.Environ(), "DATABASE_URL="+dsn, "THUMBS_DIR="+t.TempDir())

	client := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "smoke-caller", Version: "0"}, nil)
	session, err := client.Connect(ctx, &mcpsdk.CommandTransport{Command: cmd}, nil)
	if err != nil {
		t.Fatalf("connect to mcpd over stdio: %v", err)
	}
	defer func() { _ = session.Close() }()

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(tools.Tools) != 23 {
		t.Fatalf("mcpd advertised %d tools, want 23", len(tools.Tools))
	}

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_state"})
	if err != nil {
		t.Fatalf("call get_state: %v", err)
	}
	if res.IsError {
		t.Fatalf("get_state returned a tool error: %+v", res.Content)
	}
	state, ok := res.StructuredContent.(map[string]any)
	if !ok {
		t.Fatalf("get_state structured content = %T, want an object", res.StructuredContent)
	}
	if _, ok := state["profile"]; !ok {
		t.Fatalf("get_state result has no profile: %v", state)
	}
}

func migratedPostgres(ctx context.Context, t *testing.T) string {
	t.Helper()
	dsn := pgtest.RunContainer(t, ctx)
	pgtest.Migrate(t, dsn)
	return dsn
}

func buildMcpd(t *testing.T) string {
	t.Helper()
	binary := t.TempDir() + "/mcpd"
	out, err := exec.Command("go", "build", "-o", binary, ".").CombinedOutput()
	if err != nil {
		t.Fatalf("build mcpd: %v\n%s", err, out)
	}
	return binary
}
