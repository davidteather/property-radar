package store

import (
	"context"
	"fmt"

	"github.com/davidteather/property-radar/internal/listings"
)

// ResetCounts is re-exported so transports never import store.
type ResetCounts = listings.ResetCounts

// ResetTasteState clears the taste + area-preference tables in one transaction
// (rubric_versions before verdicts for the FK; custom lists too, keeping empty
// favorites; crawl_targets, so a reset stops crawling old areas). Corpus kept.
func (s *Store) ResetTasteState(ctx context.Context) (ResetCounts, error) {
	var counts ResetCounts
	err := s.InTx(ctx, func(tx *Store) error {
		var err error
		if counts.Rubrics, err = tx.deleteAll(ctx, `DELETE FROM rubric_versions`); err != nil {
			return fmt.Errorf("clear rubric_versions: %w", err)
		}
		if counts.Verdicts, err = tx.deleteAll(ctx, `DELETE FROM verdicts`); err != nil {
			return fmt.Errorf("clear verdicts: %w", err)
		}
		if counts.Shown, err = tx.deleteAll(ctx, `DELETE FROM shown`); err != nil {
			return fmt.Errorf("clear shown: %w", err)
		}
		if counts.Profile, err = tx.deleteAll(ctx, `DELETE FROM profile`); err != nil {
			return fmt.Errorf("clear profile: %w", err)
		}
		// list_items cascade from lists, so delete custom lists after counting
		// the items removed.
		if counts.ListItems, err = tx.deleteAll(ctx, `DELETE FROM list_items`); err != nil {
			return fmt.Errorf("clear list_items: %w", err)
		}
		if _, err = tx.deleteAll(ctx, `DELETE FROM lists WHERE NOT is_default`); err != nil {
			return fmt.Errorf("clear lists: %w", err)
		}
		// Area preferences: standing crawl scopes and any queued once-jobs, so a
		// reset never resumes crawling the old areas.
		if counts.CrawlTargets, err = tx.deleteAll(ctx, `DELETE FROM crawl_targets`); err != nil {
			return fmt.Errorf("clear crawl_targets: %w", err)
		}
		return nil
	})
	if err != nil {
		return ResetCounts{}, fmt.Errorf("reset taste state: %w", err)
	}
	return counts, nil
}

func (s *Store) deleteAll(ctx context.Context, query string) (int, error) {
	tag, err := s.q.Exec(ctx, query)
	if err != nil {
		return 0, err
	}
	return int(tag.RowsAffected()), nil
}
