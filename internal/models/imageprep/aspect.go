// Package imageprep normalizes images before they are handed to a vision model.
//
// Some vision preprocessors reject images whose aspect ratio is extreme:
// vLLM, for one, fails an image whose absolute aspect ratio reaches 200 with
// "absolute aspect ratio must be smaller than 200". Such images are common in
// documents: LibreOffice and MarkItDown extract thin table borders, page rules
// and decorative strips as their own resources (1×526, 1×1006, …). Handed to
// the model untouched they fail on every retry and burn the task budget.
package imageprep

import (
	"bytes"
	"fmt"
	"image"

	"golang.org/x/image/draw"
)

// MaxSafeAspectRatio is the tightest common vision-preprocessor limit. vLLM
// documents "absolute aspect ratio must be smaller than 200", so an image is
// only left alone when its ratio stays comfortably below that.
const MaxSafeAspectRatio = 200.0

// MinUsefulSide is the short-side threshold below which an extreme image is
// treated as a decorative strip rather than content. A 1- or 2-pixel-wide
// rule is a table border, not something a caption could describe.
const MinUsefulSide = 8

// ErrDegenerateImage marks an image whose short side is too small to be
// content. The caller should skip it rather than send it to the model.
var ErrDegenerateImage = fmt.Errorf("degenerate image: short side too small to be content")

// ClampAspectRatio returns image bytes whose aspect ratio stays within
// MaxSafeAspectRatio, downscaling the long side of an over-tall or over-wide
// image so it no longer trips a vision preprocessor's ratio limit.
//
// An image already within bounds is returned untouched. An extreme image
// whose short side is below MinUsefulSide is a decorative strip (a table
// border or page rule) and is reported with ErrDegenerateImage so the caller
// can skip it instead of sending it to the model.
//
// A clamped image is re-encoded as PNG: the downscale must be written out in
// some format, and PNG keeps the page detail lossless.
func ClampAspectRatio(data []byte) ([]byte, error) {
	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		// Not something we can measure; leave the bytes for the model to judge.
		return data, nil
	}
	b := src.Bounds()
	w, h := b.Dx(), b.Dy()
	if w == 0 || h == 0 {
		return data, nil
	}

	ratio := float64(w) / float64(h)
	if ratio < 1 {
		ratio = 1 / ratio
	}
	if ratio < MaxSafeAspectRatio {
		return data, nil
	}

	short := w
	if w >= h {
		short = h
	}
	if short < MinUsefulSide {
		return nil, fmt.Errorf("%w (%d×%d)", ErrDegenerateImage, w, h)
	}

	// Shrink the long side so the ratio comes back under the limit. Keep a
	// one-pixel margin so a downstream re-encode or rounding does not push it
	// back over.
	targetLong := int(float64(short) * (MaxSafeAspectRatio - 1))
	if targetLong < 1 {
		targetLong = 1
	}
	var out *image.RGBA
	if w >= h {
		out = image.NewRGBA(image.Rect(0, 0, targetLong, h))
	} else {
		out = image.NewRGBA(image.Rect(0, 0, w, targetLong))
	}
	draw.ApproxBiLinear.Scale(out, out.Bounds(), src, b, draw.Src, nil)

	outBytes, err := encodeImage(out, "image/png")
	if err != nil {
		return nil, fmt.Errorf("encode clamped image: %w", err)
	}
	return outBytes, nil
}
