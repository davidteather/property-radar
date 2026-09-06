package mcp

import (
	"context"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

func (s *Server) getCrawlStatus(ctx context.Context, _ *mcpsdk.CallToolRequest, in getCrawlStatusInput) (*mcpsdk.CallToolResult, getCrawlStatusOutput, error) {
	status, err := s.svc.CrawlStatus(ctx, domain.CrawlTargetID(in.JobID))
	if err != nil {
		return nil, getCrawlStatusOutput{}, crawlTargetErr(in.JobID, err)
	}
	return nil, getCrawlStatusOutput{
		JobID:  int64(status.Target.ID),
		Status: string(status.Target.Status),
		Target: toCrawlTarget(status.Target),
		Run:    toCrawlRun(status.Run),
	}, nil
}

func (s *Server) getCorpusStats(ctx context.Context, _ *mcpsdk.CallToolRequest, _ getCorpusStatsInput) (*mcpsdk.CallToolResult, corpusStatsOutput, error) {
	stats, err := s.svc.CorpusStats(ctx)
	if err != nil {
		return nil, corpusStatsOutput{}, err
	}
	return nil, toCorpusStats(stats), nil
}

func toCrawlRun(r *domain.IngestRun) *crawlRunDTO {
	if r == nil {
		return nil
	}
	dto := &crawlRunDTO{
		ID:            int64(r.ID),
		StartedAt:     timestamp(r.StartedAt),
		Complete:      r.Complete,
		ListingsSeen:  r.ListingsSeen,
		Created:       r.Created,
		Updated:       r.Updated,
		PhotoFailures: r.PhotoFailures,
		Suspect:       r.Suspect,
	}
	if r.FinishedAt != nil {
		dto.FinishedAt = timestamp(*r.FinishedAt)
	}
	return dto
}

func toCorpusStats(s domain.CorpusStats) corpusStatsOutput {
	out := corpusStatsOutput{
		Listings:       s.Listings,
		ActiveListings: s.ActiveListings,
		ActiveSale:     s.ActiveSale,
		ActiveRent:     s.ActiveRent,
		PhotosCached:   s.PhotosCached,
		Verdicts:       s.Verdicts,
		Lists:          s.Lists,
		PendingCrawls:  s.PendingCrawls,
		StandingScopes: s.StandingScopes,
	}
	if s.LastCrawlAt != nil {
		out.LastCrawlAt = timestamp(*s.LastCrawlAt)
	}
	out.Neighborhoods = make([]neighborhoodCountDTO, 0, len(s.Neighborhoods))
	for _, n := range s.Neighborhoods {
		out.Neighborhoods = append(out.Neighborhoods, neighborhoodCountDTO{Name: n.Name, Active: n.Active})
	}
	return out
}

// crawlTargetErr replaces the bare not-found sentinel with a message that
// says where valid ids come from; other errors pass through untouched.
func crawlTargetErr(id int64, err error) error {
	if errors.Is(err, listings.ErrCrawlTargetNotFound) {
		return fmt.Errorf("crawl request %d does not exist (settled requests are pruned after about a week); ids come from request_crawl or list_crawl_targets", id)
	}
	return err
}
