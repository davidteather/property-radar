package restapi

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
)

func (h *handler) registerAreas(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "resolve-areas",
		Method:      http.MethodGet,
		Path:        "/v1/areas",
		Summary:     "Resolve a place name to crawl area ids",
		Description: "Turns a plain-English neighborhood, borough, or city phrase into provider area ids for crawling. 'all of Manhattan' resolves to the borough; 'the whole city' resolves to all five NYC boroughs. Empty area_ids means nothing matched.",
		Tags:        []string{"Crawl"},
	}, h.resolveAreas)
}

func (h *handler) resolveAreas(ctx context.Context, in *areasInput) (*areasOutput, error) {
	r, err := h.svc.ResolveAreas(ctx, in.Q)
	if err != nil {
		return nil, mapServiceError(err)
	}
	areas := make([]areaDTO, len(r.Areas))
	for i, a := range r.Areas {
		areas[i] = areaDTO{ID: a.ID, Name: a.Name, Borough: a.Borough, Level: a.Level}
	}
	return &areasOutput{Body: areasResponse{
		Query: r.Query, CityWide: r.CityWide, Areas: areas, AreaIDs: nonNil(r.AreaIDs),
	}}, nil
}
