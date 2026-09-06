package logging

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
)

func TestSplitRoutesByLevel(t *testing.T) {
	var lo, hi bytes.Buffer
	h := split{
		lo: slog.NewTextHandler(&lo, nil),
		hi: slog.NewTextHandler(&hi, nil),
	}
	log := slog.New(h)

	log.Info("info line")
	log.Warn("warn line")
	log.Error("error line")

	if !strings.Contains(lo.String(), "info line") {
		t.Errorf("info did not go to the stdout stream: %q", lo.String())
	}
	if strings.Contains(lo.String(), "warn line") || strings.Contains(lo.String(), "error line") {
		t.Errorf("warn/error leaked to the stdout stream: %q", lo.String())
	}
	if !strings.Contains(hi.String(), "warn line") || !strings.Contains(hi.String(), "error line") {
		t.Errorf("warn/error did not go to the stderr stream: %q", hi.String())
	}
	if strings.Contains(hi.String(), "info line") {
		t.Errorf("info leaked to the stderr stream: %q", hi.String())
	}

	// Attrs and groups propagate to both streams.
	log.With("k", "v").Info("attr line")
	if !strings.Contains(lo.String(), "k=v") {
		t.Errorf("WithAttrs not applied to stdout stream: %q", lo.String())
	}
}
