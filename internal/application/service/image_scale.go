package service

import (
	"bytes"
	"image"
	"image/color"
	_ "image/gif"
	"image/jpeg"
	_ "image/png"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

// downscaleForDescribe shrinks an encoded image so its longest edge is at most
// maxEdge, re-encoding it as JPEG. The bytes stay in memory — nothing is
// written to storage, and the stored object is never modified.
//
// It returns the input unchanged (and reports false) when no work is needed:
//   - maxEdge <= 0 disables downscaling;
//   - an image already within the limit keeps its exact bytes, so a re-encode
//     never costs quality on images that do not need it;
//   - a format the decoder cannot read also passes through, which means an
//     unusual image still gets described instead of being dropped.
//
// Only the describe round uses this. OCR keeps the original bytes: fine detail
// decides the outcome there, and the measurements showed that downscaling
// before OCR makes the model invent text.
func downscaleForDescribe(data []byte, maxEdge int) ([]byte, bool) {
	if maxEdge <= 0 || len(data) == 0 {
		return data, false
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data, false
	}

	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= maxEdge && height <= maxEdge {
		return data, false
	}

	scale := float64(maxEdge) / float64(width)
	if height > width {
		scale = float64(maxEdge) / float64(height)
	}
	targetW := int(float64(width)*scale + 0.5)
	targetH := int(float64(height)*scale + 0.5)
	if targetW < 1 {
		targetW = 1
	}
	if targetH < 1 {
		targetH = 1
	}

	dst := image.NewRGBA(image.Rect(0, 0, targetW, targetH))
	// JPEG has no alpha channel, so lay down an opaque white backdrop first.
	// Without it, transparent regions encode as black, which can bias both the
	// classification and the description of, for example, a transparent
	// screenshot or a diagram exported with a transparent background.
	draw.Draw(dst, dst.Bounds(), image.NewUniform(color.White), image.Point{}, draw.Src)
	// CatmullRom is the closest match in this package to the LANCZOS-class
	// resampling the measurements were taken with. The cheaper ApproxBiLinear
	// was never validated for classification accuracy, so it is not used.
	draw.CatmullRom.Scale(dst, dst.Bounds(), src, bounds, draw.Over, nil)

	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, dst, &jpeg.Options{Quality: 85}); err != nil {
		return data, false
	}
	return buf.Bytes(), true
}
