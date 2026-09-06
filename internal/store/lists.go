package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/listings"
)

// Re-exported so transports map them without importing store.
var (
	ErrListNotFound  = listings.ErrListNotFound
	ErrDefaultList   = listings.ErrDefaultList
	ErrDuplicateList = listings.ErrDuplicateList
)

type listRow struct {
	ID        int64     `db:"id"`
	Slug      string    `db:"slug"`
	Name      string    `db:"name"`
	Emoji     *string   `db:"emoji"`
	IsDefault bool      `db:"is_default"`
	CreatedAt time.Time `db:"created_at"`
	Count     int       `db:"count"`
}

func (r listRow) toDomain() domain.List {
	l := domain.List{
		ID: domain.ListID(r.ID), Slug: r.Slug, Name: r.Name,
		IsDefault: r.IsDefault, CreatedAt: r.CreatedAt, Count: r.Count,
	}
	if r.Emoji != nil {
		l.Emoji = *r.Emoji
	}
	return l
}

const listSelect = `
SELECT l.id, l.slug, l.name, l.emoji, l.is_default, l.created_at,
       count(li.listing_id) AS count
FROM lists l LEFT JOIN list_items li ON li.list_id = l.id`

// EnsureDefaultList idempotently guarantees the favorites list exists. The
// migration seeds it; this is a startup backstop (and re-creates it for the
// truncate-based test harness).
func (s *Store) EnsureDefaultList(ctx context.Context) error {
	const q = `INSERT INTO lists (slug, name, emoji, is_default)
VALUES ('favorites', 'Favorites', '⭐', true)
ON CONFLICT (slug) DO NOTHING`
	if _, err := s.q.Exec(ctx, q); err != nil {
		return fmt.Errorf("ensure default list: %w", err)
	}
	return nil
}

// Lists returns every list with its item count, default first then by name.
func (s *Store) Lists(ctx context.Context) ([]domain.List, error) {
	rows, err := s.q.Query(ctx, listSelect+`
GROUP BY l.id ORDER BY l.is_default DESC, lower(l.name)`)
	if err != nil {
		return nil, fmt.Errorf("query lists: %w", err)
	}
	collected, err := pgx.CollectRows(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		return nil, fmt.Errorf("scan lists: %w", err)
	}
	out := make([]domain.List, len(collected))
	for i, r := range collected {
		out[i] = r.toDomain()
	}
	return out, nil
}

func (s *Store) getListWhere(ctx context.Context, cond string, arg any) (domain.List, error) {
	rows, err := s.q.Query(ctx, listSelect+`
WHERE `+cond+`
GROUP BY l.id`, arg)
	if err != nil {
		return domain.List{}, fmt.Errorf("query list: %w", err)
	}
	row, err := pgx.CollectExactlyOneRow(rows, pgx.RowToStructByName[listRow])
	if errors.Is(err, pgx.ErrNoRows) {
		return domain.List{}, ErrListNotFound
	}
	if err != nil {
		return domain.List{}, fmt.Errorf("scan list: %w", err)
	}
	return row.toDomain(), nil
}

func (s *Store) GetList(ctx context.Context, id domain.ListID) (domain.List, error) {
	return s.getListWhere(ctx, "l.id = $1", int64(id))
}

func (s *Store) GetListBySlug(ctx context.Context, slug string) (domain.List, error) {
	return s.getListWhere(ctx, "l.slug = $1", Slugify(slug))
}

// CreateList inserts a list from a display name (slug derived from it). On a
// slug collision it returns the existing list with ErrDuplicateList.
func (s *Store) CreateList(ctx context.Context, name, emoji string) (domain.List, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return domain.List{}, listings.Invalidf("list name is empty")
	}
	slug := Slugify(name)
	if slug == "" {
		return domain.List{}, listings.Invalidf("list name %q has no usable characters", name)
	}
	const insert = `INSERT INTO lists (slug, name, emoji) VALUES ($1, $2, $3) RETURNING id`
	var id int64
	err := s.q.QueryRow(ctx, insert, slug, name, nullText(strings.TrimSpace(emoji))).Scan(&id)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" { // unique_violation
			existing, getErr := s.GetListBySlug(ctx, slug)
			if getErr != nil {
				return domain.List{}, getErr
			}
			return existing, ErrDuplicateList
		}
		return domain.List{}, fmt.Errorf("insert list: %w", err)
	}
	return s.GetList(ctx, domain.ListID(id))
}

// DeleteList removes a list and its items; the default favorites list is
// protected. Returns ErrListNotFound when nothing matches.
func (s *Store) DeleteList(ctx context.Context, id domain.ListID) error {
	l, err := s.GetList(ctx, id)
	if err != nil {
		return err
	}
	if l.IsDefault {
		return ErrDefaultList
	}
	tag, err := s.q.Exec(ctx, `DELETE FROM lists WHERE id = $1 AND NOT is_default`, int64(id))
	if err != nil {
		return fmt.Errorf("delete list %d: %w", id, err)
	}
	if tag.RowsAffected() == 0 {
		return ErrListNotFound
	}
	return nil
}

// AddToList upserts a listing into a list and reports whether it was newly
// added (false when it was already a member; a non-empty note still updates).
// ErrListNotFound if the list is gone, ErrPropertyNotFound if the listing is.
func (s *Store) AddToList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID, note string) (bool, error) {
	const q = `INSERT INTO list_items (list_id, listing_id, note) VALUES ($1, $2, $3)
ON CONFLICT (list_id, listing_id) DO UPDATE SET note = COALESCE(EXCLUDED.note, list_items.note)
RETURNING (xmax = 0)`
	var added bool
	err := s.q.QueryRow(ctx, q, int64(listID), int64(propertyID), nullText(strings.TrimSpace(note))).Scan(&added)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23503" { // foreign_key_violation
			if strings.Contains(pgErr.ConstraintName, "list_id") {
				return false, ErrListNotFound
			}
			return false, ErrPropertyNotFound
		}
		return false, fmt.Errorf("add listing %d to list %d: %w", propertyID, listID, err)
	}
	return added, nil
}

// RemoveFromList drops a listing from a list and reports whether it was a
// member; removing a non-member is a no-op. ErrListNotFound if the list is gone.
func (s *Store) RemoveFromList(ctx context.Context, listID domain.ListID, propertyID domain.PropertyID) (bool, error) {
	tag, err := s.q.Exec(ctx, `DELETE FROM list_items WHERE list_id = $1 AND listing_id = $2`,
		int64(listID), int64(propertyID))
	if err != nil {
		return false, fmt.Errorf("remove listing %d from list %d: %w", propertyID, listID, err)
	}
	if tag.RowsAffected() == 0 {
		_, err = s.GetList(ctx, listID)
		return false, err
	}
	return true, nil
}

// ListMembers returns the listings in a list, most-recently-added first.
func (s *Store) ListMembers(ctx context.Context, listID domain.ListID) ([]domain.Property, error) {
	if _, err := s.GetList(ctx, listID); err != nil {
		return nil, err
	}
	const q = `
SELECT ` + propertyColumns + `
FROM listings l
LEFT JOIN listing_current c ON c.listing_id = l.id` + sourceJoin + `
JOIN list_items li ON li.listing_id = l.id
WHERE li.list_id = $1
ORDER BY li.added_at DESC, l.id DESC`
	rows, err := s.q.Query(ctx, q, int64(listID))
	if err != nil {
		return nil, fmt.Errorf("query list %d members: %w", listID, err)
	}
	return collectProperties(rows)
}

// ListsForProperty returns the lists a listing belongs to (default first).
func (s *Store) ListsForProperty(ctx context.Context, propertyID domain.PropertyID) ([]domain.List, error) {
	const q = `
SELECT l.id, l.slug, l.name, l.emoji, l.is_default, l.created_at, 0 AS count
FROM lists l JOIN list_items li ON li.list_id = l.id
WHERE li.listing_id = $1
ORDER BY l.is_default DESC, lower(l.name)`
	rows, err := s.q.Query(ctx, q, int64(propertyID))
	if err != nil {
		return nil, fmt.Errorf("query lists for listing %d: %w", propertyID, err)
	}
	collected, err := pgx.CollectRows(rows, pgx.RowToStructByName[listRow])
	if err != nil {
		return nil, fmt.Errorf("scan lists for listing: %w", err)
	}
	out := make([]domain.List, len(collected))
	for i, r := range collected {
		out[i] = r.toDomain()
	}
	return out, nil
}

var slugNonWord = regexp.MustCompile(`[^\p{L}\p{N}]+`)

// Slugify lowercases and hyphenates a display name into a stable list slug.
// Any script's letters and digits count; a name with none (emoji only) gets a
// hash-derived slug so it can still be created and found again.
func Slugify(s string) string {
	name := strings.TrimSpace(s)
	slug := strings.Trim(slugNonWord.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if slug == "" && name != "" {
		sum := sha256.Sum256([]byte(name))
		slug = "list-" + hex.EncodeToString(sum[:4])
	}
	return slug
}
