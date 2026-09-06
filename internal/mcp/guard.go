package mcp

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"runtime/debug"
	"slices"
	"strings"

	"github.com/google/jsonschema-go/jsonschema"
	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"
)

// Tool effect classes, surfaced to clients as MCP ToolAnnotations so they can
// skip approval prompts for reads and demand them for destructive calls.
const (
	readOnly    effect = iota // no state change
	additive                  // creates or appends; never removes
	destructive               // removes or resets state
)

type effect int

type toolSpec struct {
	name, title, description string
	effect                   effect
	// idempotent: repeating the same call changes nothing further.
	idempotent bool
	// enums constrains string properties by JSON name; "a.b" reaches into
	// array items or nested objects.
	enums map[string][]string
}

// addTool is the single registration path: it stamps annotations, tightens the
// inferred input schema with enums, and wraps the handler in guard.
func addTool[In, Out any](s *Server, spec toolSpec, h mcpsdk.ToolHandlerFor[In, Out]) {
	schema, err := jsonschema.For[In](nil)
	if err != nil {
		panic(fmt.Sprintf("mcp: schema for %s: %v", spec.name, err))
	}
	for path, values := range spec.enums {
		prop := propertyAt(schema, path)
		if prop == nil {
			panic(fmt.Sprintf("mcp: tool %s enum %q names no property", spec.name, path))
		}
		setEnum(prop, values)
	}
	mcpsdk.AddTool(s.mcp, &mcpsdk.Tool{
		Name:        spec.name,
		Title:       spec.title,
		Description: spec.description,
		InputSchema: schema,
		Annotations: &mcpsdk.ToolAnnotations{
			Title:           spec.title,
			ReadOnlyHint:    spec.effect == readOnly,
			DestructiveHint: ptrBool(spec.effect == destructive),
			IdempotentHint:  spec.effect == readOnly || spec.idempotent,
			OpenWorldHint:   ptrBool(false),
		},
	}, guard(spec.name, h))
}

// propertyAt walks "a.b.c" through object properties and array items.
func propertyAt(s *jsonschema.Schema, path string) *jsonschema.Schema {
	cur := s
	for _, seg := range strings.Split(path, ".") {
		if cur.Items != nil {
			cur = cur.Items
		}
		next, ok := cur.Properties[seg]
		if !ok {
			return nil
		}
		cur = next
	}
	return cur
}

// setEnum constrains a string property, or each element of an array property.
// Nullable properties keep accepting null: explicit null means "clear" in set_profile.
func setEnum(prop *jsonschema.Schema, values []string) {
	if prop.Items != nil {
		prop = prop.Items
	}
	prop.Enum = make([]any, 0, len(values)+1)
	for _, v := range values {
		prop.Enum = append(prop.Enum, v)
	}
	if slices.Contains(prop.Types, "null") {
		prop.Enum = append(prop.Enum, nil)
	}
}

func ptrBool(b bool) *bool { return &b }

const internalErrorMsg = "internal error while serving this tool; the operator can find details in the server logs"

// guard wraps a tool handler so infrastructure faults (database, network) are
// logged server-side and replaced with a generic message; client-input and
// listings sentinel errors pass through verbatim since they guide the agent.
// A panic is also caught: the SDK runs handlers on its own goroutine, so an
// uncaught one would take the whole server down.
func guard[In, Out any](name string, f mcpsdk.ToolHandlerFor[In, Out]) mcpsdk.ToolHandlerFor[In, Out] {
	return func(ctx context.Context, req *mcpsdk.CallToolRequest, in In) (res *mcpsdk.CallToolResult, out Out, err error) {
		defer func() {
			if r := recover(); r != nil {
				slog.Error("mcp: tool handler panicked", "tool", name, "panic", r, "stack", string(debug.Stack()))
				res, err = nil, errors.New(internalErrorMsg)
			}
		}()
		res, out, err = f(ctx, req, in)
		if err != nil && isInfrastructureError(err) {
			slog.Error("mcp: tool failed", "tool", name, "err", err)
			return nil, out, errors.New(internalErrorMsg)
		}
		return res, out, err
	}
}

// isInfrastructureError recognises Postgres (anything carrying an SQLSTATE) and
// network faults without importing their packages into the transport. Deadline
// errors also satisfy net.Error, so a timed-out call is masked too; canceled is not.
func isInfrastructureError(err error) bool {
	var pg interface{ SQLState() string }
	var netErr net.Error
	return errors.As(err, &pg) || errors.As(err, &netErr)
}
