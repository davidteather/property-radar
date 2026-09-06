package main

import (
	"testing"
	"time"
)

// A short local -timeout must not shrink the stale window below the default,
// or a one-shot would "recover" a target a cloud worker is still crawling.
func TestStaleWindowIsFlooredAtTheDefaultTimeout(t *testing.T) {
	if got := staleAfter(20 * time.Minute); got != defaultTimeout {
		t.Fatalf("staleAfter(20m) = %s, want %s", got, defaultTimeout)
	}
	if got := staleAfter(4 * time.Hour); got != 4*time.Hour {
		t.Fatalf("staleAfter(4h) = %s, want 4h", got)
	}
}
