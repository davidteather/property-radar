package mcp

import (
	"context"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/listings"
)

func (s *Server) resolveAreas(ctx context.Context, _ *mcpsdk.CallToolRequest, in resolveAreasInput) (*mcpsdk.CallToolResult, resolveAreasOutput, error) {
	r, err := s.svc.ResolveAreas(ctx, in.Query)
	if err != nil {
		return nil, resolveAreasOutput{}, err
	}
	return nil, resolveAreasOutput{
		Query: in.Query, CityWide: r.CityWide,
		Areas: toResolvedAreaDTOs(r.Areas), AreaIDs: nonNil(r.AreaIDs),
	}, nil
}

func toResolvedAreaDTOs(as []listings.ResolvedArea) []resolvedAreaDTO {
	out := make([]resolvedAreaDTO, len(as))
	for i, a := range as {
		out[i] = resolvedAreaDTO{ID: a.ID, Name: a.Name, Borough: a.Borough, Level: a.Level}
	}
	return out
}
