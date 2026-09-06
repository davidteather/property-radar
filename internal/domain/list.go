package domain

import "time"

type ListID int64

// List is a user-curated bucket of listings. Slug is the stable identifier
// (e.g. "favorites", "big-windows"); the default favorites list cannot be
// deleted. Count is populated by read paths, zero otherwise.
type List struct {
	ID        ListID
	Slug      string
	Name      string
	Emoji     string
	IsDefault bool
	CreatedAt time.Time
	Count     int
}

type ListItem struct {
	ListID     ListID
	PropertyID PropertyID
	Note       string
	AddedAt    time.Time
}
