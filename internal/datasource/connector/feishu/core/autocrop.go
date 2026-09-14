package core

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"

	_ "image/gif"
)

const (
	autocropBGThreshold    = 248
	autocropMinInkPerLine  = 3
	autocropMarginPx       = 12
	autocropMinShrinkRatio = 0.08
)

// AutocropImage trims near-white margins from a Feishu whiteboard thumbnail.
// SVG is returned unchanged. Decode/encode failures return the original bytes
// (never fail the parent document sync).
func AutocropImage(data []byte) ([]byte, error) {
	if looksLikeSVG(data) {
		return data, nil
	}
	img, format, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, nil
	}
	rgb := flattenToRGB(img)
	box, reason := contentBBox(rgb)
	if reason != "" {
		return data, nil
	}
	cropped := cropRGBA(rgb, box)
	var buf bytes.Buffer
	switch strings.ToLower(format) {
	case "jpeg", "jpg":
		if err := jpeg.Encode(&buf, cropped, &jpeg.Options{Quality: 92}); err != nil {
			return data, fmt.Errorf("jpeg encode cropped: %w", err)
		}
	default:
		if err := png.Encode(&buf, cropped); err != nil {
			return data, fmt.Errorf("png encode cropped: %w", err)
		}
	}
	return buf.Bytes(), nil
}

func looksLikeSVG(data []byte) bool {
	s := strings.TrimSpace(string(data))
	return strings.HasPrefix(s, "<svg") || strings.HasPrefix(s, "<?xml")
}

func flattenToRGB(src image.Image) *image.RGBA {
	b := src.Bounds()
	dst := image.NewRGBA(image.Rect(0, 0, b.Dx(), b.Dy()))
	for y := b.Min.Y; y < b.Max.Y; y++ {
		for x := b.Min.X; x < b.Max.X; x++ {
			r, g, bl, a := src.At(x, y).RGBA()
			r8, g8, b8, a8 := uint8(r>>8), uint8(g>>8), uint8(bl>>8), uint8(a>>8)
			if a8 < 255 {
				alpha := float64(a8) / 255
				r8 = uint8(float64(r8)*alpha + 255*(1-alpha))
				g8 = uint8(float64(g8)*alpha + 255*(1-alpha))
				b8 = uint8(float64(b8)*alpha + 255*(1-alpha))
			}
			dst.SetRGBA(x-b.Min.X, y-b.Min.Y, color.RGBA{R: r8, G: g8, B: b8, A: 255})
		}
	}
	return dst
}

func isInk(c color.RGBA) bool {
	return c.R < autocropBGThreshold || c.G < autocropBGThreshold || c.B < autocropBGThreshold
}

func contentBBox(img *image.RGBA) (image.Rectangle, string) {
	b := img.Bounds()
	w, h := b.Dx(), b.Dy()
	rowHas := make([]bool, h)
	for y := 0; y < h; y++ {
		ink := 0
		for x := 0; x < w; x++ {
			if isInk(img.RGBAAt(x, y)) {
				ink++
				if ink >= autocropMinInkPerLine {
					rowHas[y] = true
					break
				}
			}
		}
	}
	top, bottom := -1, -1
	for y := 0; y < h; y++ {
		if rowHas[y] {
			top = y
			break
		}
	}
	for y := h - 1; y >= 0; y-- {
		if rowHas[y] {
			bottom = y
			break
		}
	}
	if top < 0 {
		return image.Rectangle{}, "no-content"
	}

	left, right := w, -1
	for y := top; y <= bottom; y++ {
		for x := 0; x < w; x++ {
			if isInk(img.RGBAAt(x, y)) {
				if x < left {
					left = x
				}
				if x > right {
					right = x
				}
			}
		}
	}
	if right < 0 {
		return image.Rectangle{}, "no-content"
	}

	l := max(0, left-autocropMarginPx)
	t := max(0, top-autocropMarginPx)
	r := min(w, right+1+autocropMarginPx)
	bot := min(h, bottom+1+autocropMarginPx)
	newW, newH := r-l, bot-t
	if newW <= 0 || newH <= 0 {
		return image.Rectangle{}, "no-content"
	}
	origArea := float64(w * h)
	newArea := float64(newW * newH)
	areaShrink := 1 - newArea/origArea
	wShrink := 1 - float64(newW)/float64(w)
	hShrink := 1 - float64(newH)/float64(h)
	if areaShrink < autocropMinShrinkRatio && wShrink < autocropMinShrinkRatio && hShrink < autocropMinShrinkRatio {
		return image.Rectangle{}, "already-tight"
	}
	return image.Rect(l, t, r, bot), ""
}

func cropRGBA(src *image.RGBA, box image.Rectangle) *image.RGBA {
	dst := image.NewRGBA(image.Rect(0, 0, box.Dx(), box.Dy()))
	for y := box.Min.Y; y < box.Max.Y; y++ {
		for x := box.Min.X; x < box.Max.X; x++ {
			dst.SetRGBA(x-box.Min.X, y-box.Min.Y, src.RGBAAt(x, y))
		}
	}
	return dst
}
