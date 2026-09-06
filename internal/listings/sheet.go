package listings

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/draw"
	"image/jpeg"
	_ "image/png"
	"math"

	xdraw "golang.org/x/image/draw"
)

// Contact sheets are application presentation shared by every transport: ingest stores individual thumbnails and knows nothing about this layout.
const (
	sheetColumns = 3
	sheetRows    = 4
	sheetTiles   = sheetColumns * sheetRows
	sheetCellW   = 400
	sheetCellH   = 300
	sheetWidth   = sheetColumns * sheetCellW
	sheetQuality = 80

	// SheetMIME is the media type of every stitched sheet; transports label the image block with it.
	SheetMIME = "image/jpeg"
)

var sheetBackground = color.RGBA{R: 0x18, G: 0x18, B: 0x18, A: 0xff}

// Tile is one decoded thumbnail placed into a sheet, keyed by photo position.
type Tile struct {
	position int
	img      image.Image
}

// Sheet is one stitched grid image plus the photo positions it covers.
type Sheet struct {
	Data  []byte
	First int
	Last  int
	Tiles int
}

// PlanSheets lays out readable links into sheets without rendering: the same
// chunking BuildSheets uses, so a detail can advertise sheet indices cheaply.
func PlanSheets(links []PhotoLink) []Sheet {
	sheets := make([]Sheet, 0, sheetCount(len(links)))
	for start := 0; start < len(links); start += sheetTiles {
		chunk := links[start:min(start+sheetTiles, len(links))]
		sheets = append(sheets, Sheet{First: chunk[0].Position, Last: chunk[len(chunk)-1].Position, Tiles: len(chunk)})
	}
	return sheets
}

func BuildSheets(tiles []Tile) ([]Sheet, error) {
	sheets := make([]Sheet, 0, sheetCount(len(tiles)))
	for start := 0; start < len(tiles); start += sheetTiles {
		chunk := tiles[start:min(start+sheetTiles, len(tiles))]
		data, err := encodeSheet(chunk)
		if err != nil {
			return nil, err
		}
		sheets = append(sheets, Sheet{
			Data:  data,
			First: chunk[0].position,
			Last:  chunk[len(chunk)-1].position,
			Tiles: len(chunk),
		})
	}
	return sheets, nil
}

// Fixed encoder options keep the same thumbnails byte-identical across calls.
func encodeSheet(tiles []Tile) ([]byte, error) {
	canvas := image.NewRGBA(image.Rect(0, 0, sheetWidth, sheetRowsFor(len(tiles))*sheetCellH))
	draw.Draw(canvas, canvas.Bounds(), image.NewUniform(sheetBackground), image.Point{}, draw.Src)
	for i, tile := range tiles {
		src := tile.img.Bounds()
		xdraw.CatmullRom.Scale(canvas, fitRect(src, cellRect(i)), tile.img, src, xdraw.Src, nil)
	}

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, canvas, &jpeg.Options{Quality: sheetQuality}); err != nil {
		return nil, fmt.Errorf("encode contact sheet: %w", err)
	}
	return buf.Bytes(), nil
}

func sheetCount(tiles int) int {
	return (tiles + sheetTiles - 1) / sheetTiles
}

func sheetRowsFor(tiles int) int {
	rows := (tiles + sheetColumns - 1) / sheetColumns
	return min(max(rows, 1), sheetRows)
}

// Row-major, matching photo position order.
func cellRect(i int) image.Rectangle {
	x := (i % sheetColumns) * sheetCellW
	y := (i / sheetColumns) * sheetCellH
	return image.Rect(x, y, x+sheetCellW, y+sheetCellH)
}

// Letterbox: the whole photo fits inside the cell, centred, never cropped.
func fitRect(src, cell image.Rectangle) image.Rectangle {
	w, h := src.Dx(), src.Dy()
	if w <= 0 || h <= 0 {
		return cell
	}
	scale := math.Min(float64(cell.Dx())/float64(w), float64(cell.Dy())/float64(h))
	dw := min(max(1, int(math.Round(float64(w)*scale))), cell.Dx())
	dh := min(max(1, int(math.Round(float64(h)*scale))), cell.Dy())
	x := cell.Min.X + (cell.Dx()-dw)/2
	y := cell.Min.Y + (cell.Dy()-dh)/2
	return image.Rect(x, y, x+dw, y+dh)
}
