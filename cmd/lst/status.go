package main

import (
	"fmt"
	"io"
	"strconv"

	"github.com/spf13/cobra"

	"github.com/davidteather/property-radar/internal/domain"
)

const statusRunLimit = 10

type statusReport struct {
	runs           []domain.IngestRun
	activeListings int
	missingSources int
}

func newStatusCmd(a *app) *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show provider health: recent ingest runs and corpus counts",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			ctx, cancel := commandContext(cmd)
			defer cancel()

			s, err := a.store(ctx)
			if err != nil {
				return err
			}
			var report statusReport
			if report.runs, err = s.RecentRuns(ctx, statusRunLimit); err != nil {
				return err
			}
			if report.activeListings, err = s.ActiveCount(ctx); err != nil {
				return err
			}
			if report.missingSources, err = s.MissingSourceCount(ctx); err != nil {
				return err
			}
			return renderStatus(cmd.OutOrStdout(), report)
		},
	}
}

var runHeaders = []string{
	"ID", "PROVIDER", "SCOPE", "STARTED", "DURATION", "COMPLETE",
	"SEEN", "CREATED", "UPDATED", "PHOTOFAIL", "ITEMERR", "SUSPECT",
}

func runRow(r domain.IngestRun) []string {
	return []string{
		strconv.FormatInt(int64(r.ID), 10),
		formatText(r.Provider),
		shortHash(r.ScopeHash),
		formatTime(r.StartedAt),
		formatDuration(r.StartedAt, r.FinishedAt),
		formatBool(r.Complete),
		strconv.Itoa(r.ListingsSeen),
		strconv.Itoa(r.Created),
		strconv.Itoa(r.Updated),
		strconv.Itoa(r.PhotoFailures),
		strconv.Itoa(len(r.ItemErrors)),
		formatBool(r.Suspect),
	}
}

// Newest run first, matching store.RecentRuns.
func renderStatus(w io.Writer, r statusReport) error {
	if _, err := fmt.Fprintf(w, "Last %d ingest runs (times UTC)\n", statusRunLimit); err != nil {
		return err
	}
	if len(r.runs) == 0 {
		if _, err := fmt.Fprintln(w, "  no ingest runs recorded"); err != nil {
			return err
		}
	} else {
		rows := make([][]string, 0, len(r.runs))
		for _, run := range r.runs {
			rows = append(rows, runRow(run))
		}
		if err := renderTable(w, runHeaders, rows); err != nil {
			return err
		}
	}

	if err := section(w, "Corpus"); err != nil {
		return err
	}
	if err := renderFields(w, [][2]string{
		{"active listings", strconv.Itoa(r.activeListings)},
		{"sources with missing_runs > 0", strconv.Itoa(r.missingSources)},
	}); err != nil {
		return err
	}
	return renderStatusWarnings(w, r)
}

func renderStatusWarnings(w io.Writer, r statusReport) error {
	var warnings []string
	if suspect := countRuns(r.runs, func(run domain.IngestRun) bool { return run.Suspect }); suspect > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of the last %d runs are suspect; lifecycle advancement is blocked for them", suspect, len(r.runs)))
	}
	if incomplete := countRuns(r.runs, func(run domain.IngestRun) bool {
		return run.FinishedAt != nil && !run.Complete
	}); incomplete > 0 {
		warnings = append(warnings, fmt.Sprintf("%d of the last %d runs finished incomplete", incomplete, len(r.runs)))
	}
	// Surface why the most recent incomplete run stopped, not just the count.
	for _, run := range r.runs {
		if run.FinishedAt != nil && !run.Complete && len(run.ItemErrors) > 0 {
			warnings = append(warnings, fmt.Sprintf("run %d reason: %s", run.ID, run.ItemErrors[0].Message))
			break
		}
	}
	if r.missingSources > 0 {
		warnings = append(warnings, fmt.Sprintf("%d sources were absent from at least one clean run", r.missingSources))
	}
	if len(warnings) == 0 {
		return nil
	}
	if err := section(w, "Warnings"); err != nil {
		return err
	}
	for _, msg := range warnings {
		if _, err := fmt.Fprintf(w, "  %s\n", msg); err != nil {
			return err
		}
	}
	return nil
}

func countRuns(runs []domain.IngestRun, match func(domain.IngestRun) bool) int {
	n := 0
	for _, r := range runs {
		if match(r) {
			n++
		}
	}
	return n
}
