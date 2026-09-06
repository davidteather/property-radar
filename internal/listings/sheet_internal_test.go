package listings

import (
	"bytes"
	"image"
	"image/color"
	"image/jpeg"
	"testing"
)

func TestSheetGridMath(t *testing.T) {
	tests := []struct {
		tiles  int
		sheets int
		rows   int
	}{
		{tiles: 0, sheets: 0, rows: 1},
		{tiles: 1, sheets: 1, rows: 1},
		{tiles: 3, sheets: 1, rows: 1},
		{tiles: 4, sheets: 1, rows: 2},
		{tiles: 6, sheets: 1, rows: 2},
		{tiles: 12, sheets: 1, rows: 4},
		{tiles: 13, sheets: 2, rows: 4},
		{tiles: 24, sheets: 2, rows: 4},
		{tiles: 30, sheets: 3, rows: 4},
	}
	for _, tt := range tests {
		if got := sheetCount(tt.tiles); got != tt.sheets {
			t.Errorf("sheetCount(%d) = %d, want %d", tt.tiles, got, tt.sheets)
		}
		if got := sheetRowsFor(min(tt.tiles, sheetTiles)); got != tt.rows {
			t.Errorf("sheetRowsFor(%d) = %d, want %d", tt.tiles, got, tt.rows)
		}
	}
}

func TestCellRectIsRowMajor(t *testing.T) {
	tests := []struct {
		i    int
		x, y int
	}{
		{i: 0, x: 0, y: 0},
		{i: 2, x: 800, y: 0},
		{i: 3, x: 0, y: 300},
		{i: 11, x: 800, y: 900},
	}
	for _, tt := range tests {
		got := cellRect(tt.i)
		want := image.Rect(tt.x, tt.y, tt.x+sheetCellW, tt.y+sheetCellH)
		if got != want {
			t.Errorf("cellRect(%d) = %v, want %v", tt.i, got, want)
		}
	}
}

func TestFitRectLetterboxesWithoutCropping(t *testing.T) {
	cell := cellRect(0)
	tests := []struct {
		name         string
		src          image.Rectangle
		want         image.Rectangle
		wantAspectOf image.Rectangle
	}{
		{name: "4:3 fills the cell", src: image.Rect(0, 0, 800, 600), want: image.Rect(0, 0, 400, 300)},
		{name: "wide letterboxes vertically", src: image.Rect(0, 0, 800, 400), want: image.Rect(0, 50, 400, 250)},
		{name: "tall letterboxes horizontally", src: image.Rect(0, 0, 400, 800), want: image.Rect(125, 0, 275, 300)},
		{name: "small upscales to fit", src: image.Rect(0, 0, 40, 30), want: image.Rect(0, 0, 400, 300)},
		{name: "square", src: image.Rect(0, 0, 500, 500), want: image.Rect(50, 0, 350, 300)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := fitRect(tt.src, cell)
			if got != tt.want {
				t.Fatalf("fitRect(%v) = %v, want %v", tt.src, got, tt.want)
			}
			if !got.In(cell) {
				t.Fatalf("fitRect(%v) = %v, which leaves the cell %v", tt.src, got, cell)
			}
		})
	}

	// A different cell offsets the same fit.
	if got := fitRect(image.Rect(0, 0, 800, 400), cellRect(4)); got != image.Rect(400, 350, 800, 550) {
		t.Errorf("fitRect into cell 4 = %v", got)
	}
}

func TestBuildSheetsSplitsAtTwelveAndIsDeterministic(t *testing.T) {
	tiles := make([]Tile, 14)
	for i := range tiles {
		tiles[i] = Tile{position: i, img: solidImage(200+i, 150)}
	}

	sheets, err := BuildSheets(tiles)
	if err != nil {
		t.Fatalf("build sheets: %v", err)
	}
	if len(sheets) != 2 {
		t.Fatalf("got %d sheets for 14 tiles, want 2", len(sheets))
	}
	if sheets[0].First != 0 || sheets[0].Last != 11 || sheets[0].Tiles != 12 {
		t.Errorf("sheet 1 = %+v, want positions 0..11", sheets[0])
	}
	if sheets[1].First != 12 || sheets[1].Last != 13 || sheets[1].Tiles != 2 {
		t.Errorf("sheet 2 = %+v, want positions 12..13", sheets[1])
	}

	for i, want := range []int{4 * sheetCellH, 1 * sheetCellH} {
		cfg, err := jpeg.DecodeConfig(bytes.NewReader(sheets[i].Data))
		if err != nil {
			t.Fatalf("decode sheet %d: %v", i+1, err)
		}
		if cfg.Width != sheetWidth || cfg.Height != want {
			t.Errorf("sheet %d = %dx%d, want %dx%d", i+1, cfg.Width, cfg.Height, sheetWidth, want)
		}
	}

	again, err := BuildSheets(tiles)
	if err != nil {
		t.Fatalf("rebuild sheets: %v", err)
	}
	for i := range sheets {
		if !bytes.Equal(sheets[i].Data, again[i].Data) {
			t.Errorf("sheet %d is not byte-identical for the same tiles", i+1)
		}
	}

	if sheets, err := BuildSheets(nil); err != nil || len(sheets) != 0 {
		t.Errorf("BuildSheets(nil) = %v, %v, want no sheets and no error", sheets, err)
	}
}

func solidImage(w, h int) image.Image {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := range h {
		for x := range w {
			img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: uint8(w % 256), A: 255})
		}
	}
	return img
}
