package mcp

import (
	"context"
	"errors"
	"strings"
	"testing"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/stretchr/testify/mock"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings/mocks"
	"github.com/davidteather/property-radar/internal/photostore"
)

type sqlStateErr struct{}

func (sqlStateErr) Error() string    { return "ERROR: relation \"x\" does not exist (SQLSTATE 42P01)" }
func (sqlStateErr) SQLState() string { return "42P01" }

func TestGuardMasksInfrastructureErrorsOnly(t *testing.T) {
	req := &mcpsdk.CallToolRequest{Params: &mcpsdk.CallToolParamsRaw{Name: "get_state"}}
	call := func(err error) error {
		h := guard("get_state", func(context.Context, *mcpsdk.CallToolRequest, struct{}) (*mcpsdk.CallToolResult, struct{}, error) {
			return nil, struct{}{}, err
		})
		_, _, got := h(context.Background(), req, struct{}{})
		return got
	}

	if got := call(sqlStateErr{}); got == nil || strings.Contains(got.Error(), "SQLSTATE") {
		t.Fatalf("pg error reached the agent: %v", got)
	}
	client := errors.New("unknown action \"x\": use pause, resume")
	if got := call(client); got != client {
		t.Fatalf("client error was rewritten: %v", got)
	}
	if got := call(nil); got != nil {
		t.Fatalf("nil error became %v", got)
	}
}

// Drives a registered tool through a real session so a regression that
// registers a handler without guard is caught, not just the wrapper in isolation.
func TestGuardIsWiredIntoRegisteredTools(t *testing.T) {
	st := mocks.NewMockStore(t)
	st.EXPECT().State(mock.Anything).Return(domain.Profile{}, nil, false, nil, sqlStateErr{})
	photos, err := photostore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := NewServer(st, photos).MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "guard-test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_state", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	if !res.IsError || strings.Contains(text, "SQLSTATE") || !strings.Contains(text, "internal error") {
		t.Fatalf("get_state over a failing store = %q, want the generic masked error", text)
	}
}

// A panicking handler must come back as a tool error and leave the session
// usable; the SDK runs handlers on its own goroutine, so without recover the
// process would die.
func TestGuardRecoversAPanickingTool(t *testing.T) {
	st := mocks.NewMockStore(t)
	calls := 0
	st.EXPECT().State(mock.Anything).RunAndReturn(func(context.Context) (domain.Profile, *domain.Rubric, bool, []domain.Verdict, error) {
		calls++
		if calls == 1 {
			panic("boom: secret internals")
		}
		return domain.Profile{}, nil, false, nil, nil
	})
	photos, err := photostore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := NewServer(st, photos).MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "guard-test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_state", Arguments: map[string]any{}})
	if err != nil {
		t.Fatalf("first call: %v", err)
	}
	text := textOf(res)
	if !res.IsError || strings.Contains(text, "secret internals") || !strings.Contains(text, "internal error") {
		t.Fatalf("panicking get_state = %q, want the generic masked error", text)
	}

	res, err = session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "get_state", Arguments: map[string]any{}})
	if err != nil || res.IsError {
		t.Fatalf("session did not survive the panic: err=%v res=%q", err, textOf(res))
	}
}

// A store that dies mid-batch must not hide which verdicts already landed
// behind the generic mask, or the model resubmits them all.
func TestRecordVerdictsReportsWhatLandedBeforeAStoreFailure(t *testing.T) {
	st := mocks.NewMockStore(t)
	st.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(1), domain.VerdictLove, "").
		Return(domain.Verdict{ID: 10, PropertyID: 1, Kind: domain.VerdictLove}, nil).Once()
	st.EXPECT().RecordVerdict(mock.Anything, domain.PropertyID(2), domain.VerdictLove, "").
		Return(domain.Verdict{}, sqlStateErr{}).Once()
	photos, err := photostore.NewLocal(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	ctx := context.Background()
	serverTransport, clientTransport := mcpsdk.NewInMemoryTransports()
	serverSession, err := NewServer(st, photos).MCP().Connect(ctx, serverTransport, nil)
	if err != nil {
		t.Fatalf("connect server: %v", err)
	}
	defer func() { _ = serverSession.Close() }()
	session, err := mcpsdk.NewClient(&mcpsdk.Implementation{Name: "guard-test", Version: "0"}, nil).Connect(ctx, clientTransport, nil)
	if err != nil {
		t.Fatalf("connect client: %v", err)
	}
	defer func() { _ = session.Close() }()

	res, err := session.CallTool(ctx, &mcpsdk.CallToolParams{Name: "record_verdicts", Arguments: map[string]any{
		"verdicts": []map[string]any{{"listing_id": 1, "verdict": "love"}, {"listing_id": 2, "verdict": "love"}, {"listing_id": 3, "verdict": "love"}},
	}})
	if err != nil {
		t.Fatalf("call: %v", err)
	}
	text := textOf(res)
	if !res.IsError || strings.Contains(text, "SQLSTATE") || !strings.Contains(text, "1 of 3") || !strings.Contains(text, "[1]") {
		t.Fatalf("mid-batch failure = %q, want the landed ids without the SQLSTATE", text)
	}
}

func textOf(res *mcpsdk.CallToolResult) string {
	if res == nil {
		return ""
	}
	var text string
	for _, c := range res.Content {
		if tc, ok := c.(*mcpsdk.TextContent); ok {
			text += tc.Text
		}
	}
	return text
}
