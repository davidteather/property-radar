package restapi

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

func (h *handler) registerLists(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-lists",
		Method:      http.MethodGet,
		Path:        "/v1/lists",
		Summary:     "List all lists",
		Description: "Returns every user-curated list (favorites plus any named feature buckets) with its listing count.",
		Tags:        []string{"Lists"},
	}, h.listLists)

	huma.Register(api, huma.Operation{
		OperationID: "create-list",
		Method:      http.MethodPost,
		Path:        "/v1/lists",
		Summary:     "Create a list",
		Description: "Creates a list from a display name (the slug is derived from it) plus an optional emoji. Idempotent: if the slug already exists the existing list is returned unchanged with already_exists=true.",
		Tags:        []string{"Lists"},
	}, h.createList)

	huma.Register(api, huma.Operation{
		OperationID: "get-list",
		Method:      http.MethodGet,
		Path:        "/v1/lists/{id}",
		Summary:     "Get a list and its listings",
		Description: "Returns the list plus its member listings as compact rows, most-recently-added first.",
		Tags:        []string{"Lists"},
	}, h.getList)

	huma.Register(api, huma.Operation{
		OperationID:   "delete-list",
		Method:        http.MethodDelete,
		Path:          "/v1/lists/{id}",
		Summary:       "Delete a list",
		Description:   "Deletes a list and its membership. 422 for the built-in favorites list, which cannot be deleted; 404 if the list does not exist.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusOK,
	}, h.deleteList)

	huma.Register(api, huma.Operation{
		OperationID: "add-to-list",
		Method:      http.MethodPost,
		Path:        "/v1/lists/{id}/items",
		Summary:     "Add a listing to a list",
		Description: "Adds a listing to the list (idempotent; added reports whether it was new). 404 if the list or the listing does not exist.",
		Tags:        []string{"Lists"},
	}, h.addToList)

	huma.Register(api, huma.Operation{
		OperationID:   "remove-from-list",
		Method:        http.MethodDelete,
		Path:          "/v1/lists/{id}/items/{listing_id}",
		Summary:       "Remove a listing from a list",
		Description:   "Removes a listing from the list; removed is false when it was not a member. 404 if the list does not exist.",
		Tags:          []string{"Lists"},
		DefaultStatus: http.StatusOK,
	}, h.removeFromList)

	huma.Register(api, huma.Operation{
		OperationID: "get-verdicts",
		Method:      http.MethodGet,
		Path:        "/v1/verdicts",
		Summary:     "List rated listings and the rubric",
		Description: "Returns the current taste rubric plus every listing that has a verdict, most recently judged first, each with its current verdict, note, and full verdict history.",
		Tags:        []string{"State"},
	}, h.getVerdicts)
}

func (h *handler) getVerdicts(ctx context.Context, _ *verdictsInput) (*verdictsOutput, error) {
	rs, err := h.svc.RatedListings(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &verdictsOutput{Body: verdictsResponse{
		Rubric:      toRubric(rs.Rubric),
		RubricStale: rs.Stale,
		Count:       len(rs.Rated),
		Rated:       nonNil(xslicesMapRated(rs.Rated)),
	}}, nil
}

func xslicesMapRated(rs []listings.RatedListing) []ratedDTO {
	out := make([]ratedDTO, len(rs))
	for i, r := range rs {
		out[i] = toRated(r)
	}
	return out
}

func (h *handler) listLists(ctx context.Context, _ *listsInput) (*listsOutput, error) {
	ls, err := h.svc.Lists(ctx)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &listsOutput{Body: listsResponse{Count: len(ls), Lists: toLists(ls)}}, nil
}

func (h *handler) createList(ctx context.Context, in *createListInput) (*createListOutput, error) {
	l, err := h.svc.CreateList(ctx, in.Body.Name, in.Body.Emoji)
	already := errors.Is(err, listings.ErrDuplicateList)
	if err != nil && !already {
		return nil, mapServiceError(err)
	}
	return &createListOutput{Body: createListResponse{List: toList(l), AlreadyExists: already}}, nil
}

func (h *handler) getList(ctx context.Context, in *listIDInput) (*listDetailOutput, error) {
	lm, err := h.svc.ListMembers(ctx, domain.ListID(in.ID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &listDetailOutput{Body: listDetailResponse{
		List: toList(lm.List), Count: len(lm.Members), Listings: toListingRows(lm.Members),
	}}, nil
}

func (h *handler) deleteList(ctx context.Context, in *listIDInput) (*listOKOutput, error) {
	if err := h.svc.DeleteList(ctx, domain.ListID(in.ID)); err != nil {
		return nil, mapServiceError(err)
	}
	return &listOKOutput{Body: listOKResponse{OK: true}}, nil
}

func (h *handler) addToList(ctx context.Context, in *addToListInput) (*addToListOutput, error) {
	added, err := h.svc.AddToList(ctx, domain.ListID(in.ID), domain.PropertyID(in.Body.ListingID), in.Body.Note)
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &addToListOutput{Body: addToListResponse{OK: true, Added: added}}, nil
}

func (h *handler) removeFromList(ctx context.Context, in *removeFromListInput) (*removeFromListOutput, error) {
	removed, err := h.svc.RemoveFromList(ctx, domain.ListID(in.ID), domain.PropertyID(in.ListingID))
	if err != nil {
		return nil, mapServiceError(err)
	}
	return &removeFromListOutput{Body: removeFromListResponse{OK: true, Removed: removed}}, nil
}
