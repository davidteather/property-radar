package listings

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"

	"github.com/davidteather/property-radar/internal/domain"
	"github.com/davidteather/property-radar/internal/photostore"
)

const (
	DefaultPhotoCount   = 6
	MaxPhotosPerListing = 10
	MaxSheetPhotos      = 30
	defaultPhotoMIME    = "image/jpeg"

	// MaxBulkPhotoListings caps how many listings one bulk photo-URL call may resolve, keeping it a single cheap DB read.
	MaxBulkPhotoListings = 30

	LayoutIndividual   = "individual"
	LayoutContactSheet = "contact_sheet"
)

// PhotoStore is the read side of the thumbnail byte store: fetches cached bytes by their content-addressed cached_path key.
type PhotoStore interface {
	Get(ctx context.Context, key string) ([]byte, error)
	Stat(ctx context.Context, key string) (photostore.Info, error)
}

// PhotoImage is one returned thumbnail: bytes plus the media type a transport stamps on its image block.
type PhotoImage struct {
	Position int
	Data     []byte
	MIME     string
}

// PhotoLink is one readable cached thumbnail addressed by its store key rather than carried as bytes: the transport turns Key into a public /img URL so the client fetches the image itself instead of paying for base64.
type PhotoLink struct {
	Position int
	Key      string
	MIME     string
}

// PhotoCounts is the photo accounting for one listing detail. Cached counts readable thumbnails, Missing counts cached thumbnails that would not read.
type PhotoCounts struct {
	Total    int
	Cached   int
	Returned int
	Missing  int
}

type photoTally struct {
	cached  int
	missing int
}

// A photo with no cached path was never thumbnailed (ingest caps how many are cached) and is neither cached nor missing; missing means a cached thumbnail that will not read or decode, which is skipped, never a Service error.
func (s *Service) walkPhotos(ctx context.Context, photos []domain.Photo, budget int, take func(position int, data []byte, mime string) bool) photoTally {
	var tally photoTally
	used := 0
	for _, p := range photos {
		if p.CachedPath == "" {
			continue
		}
		if used >= budget {
			if s.thumbnailReadable(ctx, p.CachedPath) {
				tally.cached++
			} else {
				tally.missing++
			}
			continue
		}
		data, err := s.readThumbnail(ctx, p.CachedPath)
		if err != nil {
			tally.missing++
			continue
		}
		mime := p.MIMEType
		if mime == "" {
			mime = defaultPhotoMIME
		}
		if !take(p.Position, data, mime) {
			tally.missing++
			continue
		}
		tally.cached++
		used++
	}
	return tally
}

func (s *Service) collectPhotos(ctx context.Context, photos []domain.Photo, budget int) ([]PhotoImage, photoTally) {
	var images []PhotoImage
	tally := s.walkPhotos(ctx, photos, budget, func(position int, data []byte, mime string) bool {
		images = append(images, PhotoImage{Position: position, Data: data, MIME: mime})
		return true
	})
	return images, tally
}

func (s *Service) collectTiles(ctx context.Context, photos []domain.Photo, budget int) ([]Tile, photoTally) {
	var tiles []Tile
	tally := s.walkPhotos(ctx, photos, budget, func(position int, data []byte, _ string) bool {
		img, _, err := image.Decode(bytes.NewReader(data))
		if err != nil {
			return false
		}
		tiles = append(tiles, Tile{position: position, img: img})
		return true
	})
	return tiles, tally
}

// collectLinks gathers readable cached thumbnails as store-key links up to budget, statting rather than reading bytes to avoid loading the image. The tally matches collectPhotos: cached counts every readable thumbnail (inside and beyond budget), missing counts cached paths whose thumbnail will not read.
func (s *Service) collectLinks(ctx context.Context, photos []domain.Photo, budget int) ([]PhotoLink, photoTally) {
	var links []PhotoLink
	var tally photoTally
	used := 0
	for _, p := range photos {
		if p.CachedPath == "" {
			continue
		}
		readable := s.thumbnailReadable(ctx, p.CachedPath)
		if used >= budget {
			if readable {
				tally.cached++
			} else {
				tally.missing++
			}
			continue
		}
		if !readable {
			tally.missing++
			continue
		}
		mime := p.MIMEType
		if mime == "" {
			mime = defaultPhotoMIME
		}
		links = append(links, PhotoLink{Position: p.Position, Key: p.CachedPath, MIME: mime})
		tally.cached++
		used++
	}
	return links, tally
}

func (s *Service) readThumbnail(ctx context.Context, cachedPath string) ([]byte, error) {
	if s.photos == nil {
		return nil, errors.New("read thumbnail: no photo store configured")
	}
	data, err := s.photos.Get(ctx, cachedPath)
	if err != nil {
		return nil, fmt.Errorf("read thumbnail: %w", err)
	}
	return data, nil
}

func (s *Service) thumbnailReadable(ctx context.Context, cachedPath string) bool {
	if s.photos == nil {
		return false
	}
	info, err := s.photos.Stat(ctx, cachedPath)
	return err == nil && info.Exists
}

func photoLayout(layout string) (string, error) {
	switch layout {
	case "", LayoutIndividual:
		return LayoutIndividual, nil
	case LayoutContactSheet:
		return LayoutContactSheet, nil
	default:
		return "", Invalidf("photo_layout %q is not one of individual, contact_sheet", layout)
	}
}

func photoBudget(max int, layout string) int {
	hard := MaxPhotosPerListing
	if layout == LayoutContactSheet {
		hard = MaxSheetPhotos
	}
	switch {
	case max <= 0:
		return DefaultPhotoCount
	case max > hard:
		return hard
	default:
		return max
	}
}
