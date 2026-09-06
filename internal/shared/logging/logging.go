// Package logging builds the process logger with the conventional stream split:
// info to stdout, warnings/errors to stderr, so collectors (and Railway, which
// classifies severity by stream) label them correctly.
package logging

import (
	"context"
	"log/slog"
	"os"
)

// New returns the logger. With stdoutProtocol true (an MCP server using stdout
// as its JSON-RPC channel) everything goes to stderr to avoid corrupting the
// protocol; otherwise below-Warn goes to stdout and Warn+ to stderr.
func New(stdoutProtocol bool) *slog.Logger {
	if stdoutProtocol {
		return slog.New(slog.NewTextHandler(os.Stderr, nil))
	}
	return slog.New(split{
		lo: slog.NewTextHandler(os.Stdout, nil),
		hi: slog.NewTextHandler(os.Stderr, nil),
	})
}

// split routes each record to lo (stdout) or hi (stderr) by level; both share
// the same attrs and groups.
type split struct {
	lo, hi slog.Handler
}

func (s split) Enabled(ctx context.Context, l slog.Level) bool {
	return s.lo.Enabled(ctx, l) || s.hi.Enabled(ctx, l)
}

func (s split) Handle(ctx context.Context, r slog.Record) error {
	if r.Level >= slog.LevelWarn {
		return s.hi.Handle(ctx, r)
	}
	return s.lo.Handle(ctx, r)
}

func (s split) WithAttrs(as []slog.Attr) slog.Handler {
	return split{lo: s.lo.WithAttrs(as), hi: s.hi.WithAttrs(as)}
}

func (s split) WithGroup(name string) slog.Handler {
	return split{lo: s.lo.WithGroup(name), hi: s.hi.WithGroup(name)}
}
