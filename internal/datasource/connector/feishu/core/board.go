package core

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/draw"
	"image/jpeg"
	"image/png"
	"net/url"
)

// board.go covers the docx board block (block_type 43): whiteboards embedded in
// documents are exported to an image via the official download_as_image API and
// ride the embedded-image pipeline as a regular image sub-item
// (external_id "<parent>#board#<token>").
//
// API: GET /open-apis/board/v1/whiteboards/:whiteboard_id/download_as_image —
// responds with the raw image bytes (Content-Type image/png|jpeg|gif|svg+xml).
// The render covers the WHOLE board canvas, not the content bounding box, and
// editors routinely leave the canvas far larger than the drawing, so exports
// surface with large blank areas (mostly below the content). trimBoardImage
// crops those blank borders after download.
//
// Scope: board:whiteboard:node:read. 403 (code 2890005) means the app lacks
// read permission on the board; the caller degrades to a Markdown note rather
// than failing the document.

// blankTolerance is the per-channel delta under which a pixel counts as the
// background color — generous enough to absorb JPEG edge artifacts.
const blankTolerance = 14

// trimMargin keeps a small visual margin around the content bounding box.
const trimMargin = 8

// maxTrimPixels bounds the decode/scan cost; larger images pass through
// untrimmed rather than risking a huge RGBA allocation.
const maxTrimPixels = 64 << 20

type boardPixel struct{ r, g, b, a uint8 }

// downloadBoardAsImage fetches a whiteboard's thumbnail image bytes and crops
// blank canvas borders. Transient failures (429/5xx/transport) retry via the
// shared downloadRawBytes policy; the 512 MB maxFeishuDownloadBytes cap
// applies too (a real whiteboard PNG is orders of magnitude smaller).
func (c *Client) downloadBoardAsImage(ctx context.Context, boardToken string) ([]byte, error) {
	path := fmt.Sprintf("/open-apis/board/v1/whiteboards/%s/download_as_image", url.PathEscape(boardToken))
	data, err := c.downloadRawBytes(ctx, path)
	if err != nil {
		return nil, err
	}
	return trimBoardImage(data), nil
}

// trimBoardImage crops near-uniform blank borders from a board export (PNG or
// JPEG; anything undecodable or in another format returns unchanged). The
// bottom-right pixel seeds the background color; border rows/columns entirely
// within blankTolerance of it are trimmed, keeping trimMargin pixels around
// the content. The background color is the majority of the four corner
// pixels (>=3 must agree); on a split vote content reaches the corners and
// the image passes through untrimmed.
func trimBoardImage(data []byte) []byte {
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil || (format != "png" && format != "jpeg") {
		return data
	}
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	if w <= 0 || h <= 0 || w*h > maxTrimPixels {
		return data
	}
	at := func(x, y int) boardPixel {
		r, g, bl, a := img.At(x, y).RGBA()
		return boardPixel{uint8(r >> 8), uint8(g >> 8), uint8(bl >> 8), uint8(a >> 8)}
	}
	withinTolerance := func(p, q boardPixel) bool {
		d := func(a, c uint8) int {
			if d := int(a) - int(c); d < 0 {
				return -d
			} else {
				return d
			}
		}
		return d(p.r, q.r) <= blankTolerance && d(p.g, q.g) <= blankTolerance &&
			d(p.b, q.b) <= blankTolerance && d(p.a, q.a) <= blankTolerance
	}
	// The background color must be unambiguous: require at least three of the
	// four corners to agree on one color, else content reaches the corners and
	// trimming could eat it (rows fully matching the seed would read as blank).
	corners := [4]boardPixel{
		at(b.Min.X, b.Min.Y), at(b.Max.X-1, b.Min.Y),
		at(b.Min.X, b.Max.Y-1), at(b.Max.X-1, b.Max.Y-1),
	}
	bg, ok := boardPixel{}, false
	for _, c := range corners {
		n := 0
		for _, d := range corners {
			if withinTolerance(c, d) {
				n++
			}
		}
		if n >= 3 {
			bg, ok = c, true
			break
		}
	}
	if !ok {
		return data
	}
	rowBlank := func(y int) bool {
		for x := b.Min.X; x < b.Max.X; x++ {
			if !withinTolerance(at(x, y), bg) {
				return false
			}
		}
		return true
	}
	colBlank := func(x int) bool {
		for y := b.Min.Y; y < b.Max.Y; y++ {
			if !withinTolerance(at(x, y), bg) {
				return false
			}
		}
		return true
	}
	top, bottom := b.Min.Y, b.Max.Y-1
	for top < bottom && rowBlank(top) {
		top++
	}
	for bottom > top && rowBlank(bottom) {
		bottom--
	}
	left, right := b.Min.X, b.Max.X-1
	for left < right && colBlank(left) {
		left++
	}
	for right > left && colBlank(right) {
		right--
	}
	cw, ch := right-left+1, bottom-top+1
	if cw <= 2 || ch <= 2 {
		// Nothing but background (degenerate content box) — keep the original.
		return data
	}
	if cw == w && ch == h {
		return data
	}
	x0 := max(b.Min.X, left-trimMargin)
	y0 := max(b.Min.Y, top-trimMargin)
	x1 := min(b.Max.X, right+1+trimMargin)
	y1 := min(b.Max.Y, bottom+1+trimMargin)
	out := image.NewRGBA(image.Rect(0, 0, x1-x0, y1-y0))
	draw.Draw(out, out.Bounds(), img, image.Point{X: x0, Y: y0}, draw.Src)
	var buf bytes.Buffer
	switch format {
	case "jpeg":
		err = jpeg.Encode(&buf, out, &jpeg.Options{Quality: 90})
	case "png":
		err = png.Encode(&buf, out)
	}
	if err != nil {
		return data
	}
	return buf.Bytes()
}
