package listings

import (
	"context"
	"fmt"

	"github.com/davidteather/property-radar/internal/domain"
)

// ListWithMembers is a list together with its listings as compact rows.
type ListWithMembers struct {
	List    domain.List
	Members []Row
}

func (s *Service) Lists(ctx context.Context) ([]domain.List, error) {
	lists, err := s.store.Lists(ctx)
	if err != nil {
		return nil, fmt.Errorf("load lists: %w", err)
	}
	return lists, nil
}

// CreateList returns the new list, or the existing one with ErrDuplicateList when the derived slug already exists (so callers can treat it as idempotent).
func (s *Service) CreateList(ctx context.Context, name, emoji string) (domain.List, error) {
	if err := checkText("name", name, MaxListName, "use a short label"); err != nil {
		return domain.List{}, err
	}
	if err := checkText("emoji", emoji, MaxListEmoji, "one emoji is plenty"); err != nil {
		return domain.List{}, err
	}
	return s.store.CreateList(ctx, name, emoji)
}

// DeleteList passes ErrListNotFound / ErrDefaultList through for transport mapping.
func (s *Service) DeleteList(ctx context.Context, id domain.ListID) error {
	return s.store.DeleteList(ctx, id)
}

// AddToList adds a listing to a list, reporting false when it was already a member.
func (s *Service) AddToList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID, note string) (bool, error) {
	if err := checkNote(note); err != nil {
		return false, err
	}
	return s.store.AddToList(ctx, listID, propertyID, note)
}

// AddToListBySlug resolves a list by slug (it does not create one) then adds a listing, returning the resolved list and whether the listing was newly added. The caller-friendly MCP path.
func (s *Service) AddToListBySlug(ctx context.Context, slug string, propertyID domain.PropertyID, note string) (domain.List, bool, error) {
	if err := checkNote(note); err != nil {
		return domain.List{}, false, err
	}
	list, err := s.store.GetListBySlug(ctx, slug)
	if err != nil {
		return domain.List{}, false, err
	}
	added, err := s.store.AddToList(ctx, list.ID, propertyID, note)
	if err != nil {
		return domain.List{}, false, err
	}
	return list, added, nil
}

// RemoveFromList drops a listing from a list and reports whether it was a member.
func (s *Service) RemoveFromList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID) (bool, error) {
	return s.store.RemoveFromList(ctx, listID, propertyID)
}

// ListMembers returns a list plus its listings as compact rows (with photo counts).
func (s *Service) ListMembers(ctx context.Context, id domain.ListID) (ListWithMembers, error) {
	list, err := s.store.GetList(ctx, id)
	if err != nil {
		return ListWithMembers{}, err
	}
	props, err := s.store.ListMembers(ctx, id)
	if err != nil {
		return ListWithMembers{}, fmt.Errorf("load list %d members: %w", id, err)
	}
	rows, err := s.rows(ctx, props)
	if err != nil {
		return ListWithMembers{}, err
	}
	return ListWithMembers{List: list, Members: rows}, nil
}

// ListsForProperty reports which lists a listing belongs to.
func (s *Service) ListsForProperty(ctx context.Context, id domain.PropertyID) ([]domain.List, error) {
	return s.store.ListsForProperty(ctx, id)
}

// ListBySlug resolves a list by its slug (ErrListNotFound if none).
func (s *Service) ListBySlug(ctx context.Context, slug string) (domain.List, error) {
	return s.store.GetListBySlug(ctx, slug)
}

// ListMembersBySlug is ListMembers keyed by slug, for the MCP path.
func (s *Service) ListMembersBySlug(ctx context.Context, slug string) (ListWithMembers, error) {
	list, err := s.store.GetListBySlug(ctx, slug)
	if err != nil {
		return ListWithMembers{}, err
	}
	return s.ListMembers(ctx, list.ID)
}

// RemoveFromListBySlug is RemoveFromList keyed by slug, for the MCP path.
func (s *Service) RemoveFromListBySlug(ctx context.Context, slug string, propertyID domain.PropertyID) (bool, error) {
	list, err := s.store.GetListBySlug(ctx, slug)
	if err != nil {
		return false, err
	}
	return s.store.RemoveFromList(ctx, list.ID, propertyID)
}
