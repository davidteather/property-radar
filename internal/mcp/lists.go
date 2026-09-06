package mcp

import (
	"context"
	"errors"
	"fmt"

	mcpsdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

func (s *Server) getLists(ctx context.Context, _ *mcpsdk.CallToolRequest, _ getListsInput) (*mcpsdk.CallToolResult, getListsOutput, error) {
	ls, err := s.svc.Lists(ctx)
	if err != nil {
		return nil, getListsOutput{}, err
	}
	return nil, getListsOutput{Lists: toListDTOs(ls), Count: len(ls)}, nil
}

func (s *Server) createList(ctx context.Context, _ *mcpsdk.CallToolRequest, in createListInput) (*mcpsdk.CallToolResult, createListOutput, error) {
	l, err := s.svc.CreateList(ctx, in.Name, in.Emoji)
	already := errors.Is(err, listings.ErrDuplicateList)
	if err != nil && !already {
		return nil, createListOutput{}, err
	}
	return nil, createListOutput{List: toListDTO(l), AlreadyExists: already}, nil
}

func (s *Server) addToList(ctx context.Context, _ *mcpsdk.CallToolRequest, in addToListInput) (*mcpsdk.CallToolResult, addToListOutput, error) {
	list, added, err := s.svc.AddToListBySlug(ctx, in.List, domain.PropertyID(in.ListingID), in.Note)
	if errors.Is(err, listings.ErrListNotFound) {
		return nil, addToListOutput{}, fmt.Errorf("no list named %q; create it first with create_list, or call get_lists to see existing lists", in.List)
	}
	if errors.Is(err, listings.ErrNotFound) {
		return nil, addToListOutput{}, fmt.Errorf("listing %d does not exist; ids come from search_listings or get_candidates", in.ListingID)
	}
	if err != nil {
		return nil, addToListOutput{}, err
	}
	// Refetch so the returned count reflects the add.
	if updated, err := s.svc.ListBySlug(ctx, list.Slug); err == nil {
		list = updated
	}
	return nil, addToListOutput{List: toListDTO(list), Added: added}, nil
}

func (s *Server) removeFromList(ctx context.Context, _ *mcpsdk.CallToolRequest, in removeFromListInput) (*mcpsdk.CallToolResult, removeFromListOutput, error) {
	removed, err := s.svc.RemoveFromListBySlug(ctx, in.List, domain.PropertyID(in.ListingID))
	if errors.Is(err, listings.ErrListNotFound) {
		return nil, removeFromListOutput{}, fmt.Errorf("no list named %q; call get_lists to see existing lists", in.List)
	}
	if err != nil {
		return nil, removeFromListOutput{}, err
	}
	return nil, removeFromListOutput{OK: true, Removed: removed}, nil
}

func (s *Server) getList(ctx context.Context, _ *mcpsdk.CallToolRequest, in getListInput) (*mcpsdk.CallToolResult, getListOutput, error) {
	lm, err := s.svc.ListMembersBySlug(ctx, in.List)
	if errors.Is(err, listings.ErrListNotFound) {
		return nil, getListOutput{}, fmt.Errorf("no list named %q; call get_lists to see existing lists", in.List)
	}
	if err != nil {
		return nil, getListOutput{}, err
	}
	return nil, getListOutput{List: toListDTO(lm.List), Count: len(lm.Members), Listings: toListingRows(lm.Members)}, nil
}

func (s *Server) getConsoleURL(_ context.Context, _ *mcpsdk.CallToolRequest, _ getConsoleURLInput) (*mcpsdk.CallToolResult, consoleURLOutput, error) {
	if s.consoleURL == "" {
		return nil, consoleURLOutput{Available: false, Message: "No web console is configured for this deployment."}, nil
	}
	return nil, consoleURLOutput{URL: s.consoleURL, Available: true, Message: "Open this link in a browser for a web view of the corpus and taste state."}, nil
}
