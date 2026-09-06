package main

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/davidteather/property-radar/internal/domain"
)

func TestRenderStatus(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	finished := started.Add(45 * time.Second)
	report := statusReport{
		runs: []domain.IngestRun{
			{
				ID: 9, Provider: "streeteasy", ScopeHash: "9f2c1ab77d0e4455",
				StartedAt: started.Add(time.Hour), Complete: false,
			},
			{
				ID: 8, Provider: "streeteasy", ScopeHash: "9f2c1ab77d0e4455",
				StartedAt: started, FinishedAt: &finished, Complete: true,
				ListingsSeen: 412, Created: 12, Updated: 30, PhotoFailures: 2,
				ItemErrors: []domain.RunError{{ProviderID: "abc", Message: "no price"}},
				Suspect:    true,
			},
		},
		activeListings: 388,
		missingSources: 4,
	}

	var buf bytes.Buffer
	if err := renderStatus(&buf, report); err != nil {
		t.Fatalf("renderStatus: %v", err)
	}
	want := `Last 10 ingest runs (times UTC)
ID  PROVIDER    SCOPE       STARTED            DURATION  COMPLETE  SEEN  CREATED  UPDATED  PHOTOFAIL  ITEMERR  SUSPECT
9   streeteasy  9f2c1ab77…  2026-08-01 13:00Z  running   no        0     0        0        0          0        no
8   streeteasy  9f2c1ab77…  2026-08-01 12:00Z  45s       yes       412   12       30       2          1        yes

Corpus
  active listings                388
  sources with missing_runs > 0  4

Warnings
  1 of the last 2 runs are suspect; lifecycle advancement is blocked for them
  4 sources were absent from at least one clean run
`
	if got := buf.String(); got != want {
		t.Errorf("renderStatus mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestRenderStatusSurfacesIncompleteReason(t *testing.T) {
	started := time.Date(2026, 8, 1, 12, 0, 0, 0, time.UTC)
	finished := started.Add(time.Second)
	report := statusReport{
		runs: []domain.IngestRun{{
			ID: 6, Provider: "streeteasy", ScopeHash: "preflight",
			StartedAt: started, FinishedAt: &finished, Complete: false,
			ItemErrors: []domain.RunError{{Message: "WEBSHARE_API_KEY not set: -proxy-mode all needs a residential Webshare plan"}},
		}},
	}
	var buf bytes.Buffer
	if err := renderStatus(&buf, report); err != nil {
		t.Fatalf("renderStatus: %v", err)
	}
	if got := buf.String(); !strings.Contains(got, "run 6 reason: WEBSHARE_API_KEY not set") {
		t.Errorf("status should surface the incomplete run's reason, got:\n%s", got)
	}
}

func TestRenderStatusNoRuns(t *testing.T) {
	var buf bytes.Buffer
	if err := renderStatus(&buf, statusReport{}); err != nil {
		t.Fatalf("renderStatus: %v", err)
	}
	got := buf.String()
	for _, want := range []string{"no ingest runs recorded", "active listings", "sources with missing_runs > 0  0"} {
		if !strings.Contains(got, want) {
			t.Errorf("renderStatus missing %q in:\n%s", want, got)
		}
	}
	if strings.Contains(got, "Warnings") {
		t.Errorf("clean empty status should not warn:\n%s", got)
	}
}
