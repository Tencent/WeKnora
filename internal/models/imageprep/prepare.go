// Package imageprep turns stored image bytes into an image a model will take:
// converted to a format the vendor documents and shrunk to its size limit.
// Embedding and rerank both send images and share these rules.
package imageprep

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	_ "image/gif" // decode GIF sources
	"image/jpeg"
	"image/png"
	"net/http"
	"slices"
	"strings"

	"github.com/Tencent/WeKnora/internal/models/api"
	_ "golang.org/x/image/bmp" // decode BMP sources
	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp" // decode WebP sources
)

const (
	// prepJPEGQuality is the quality images are re-encoded at when they have
	// to be. Embedding models see the content, not the artefacts, so there is
	// nothing to gain from more.
	prepJPEGQuality = 85
	// prepScaleStep shrinks each side per attempt when an image is still
	// over the byte limit after re-encoding.
	prepScaleStep = 0.75
	// prepMinSide is where shrinking stops: below it an image no longer
	// carries anything worth a vector.
	prepMinSide = 64
)

// Limits describes what one image may be. Zero values mean the vendor
// documents no limit.
type Limits struct {
	MaxBytes  int
	MIMETypes []string
}

// LimitsOf renders an endpoint's image settings as Limits.
func LimitsOf(in api.ImageInput) Limits {
	return Limits{MaxBytes: in.MaxImageBytes, MIMETypes: slices.Clone(in.ImageMIMETypes)}
}

// Prepare turns stored image bytes into an image the model will take.
// An image already in an accepted format and within the byte limit goes as
// it is; anything else is re-encoded — as JPEG where the vendor takes it,
// PNG otherwise — and scaled down until it fits.
func Prepare(data []byte, limits Limits) (api.EmbedImage, error) {
	if len(data) == 0 {
		return api.EmbedImage{}, fmt.Errorf("image is empty")
	}
	mime := http.DetectContentType(data)
	if !strings.HasPrefix(mime, "image/") {
		return api.EmbedImage{}, fmt.Errorf("not an image (%s)", mime)
	}
	if acceptsMIME(limits, mime) && fitsBytes(limits, len(data)) {
		return api.EmbedImage{Data: data, MIMEType: mime}, nil
	}

	src, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return api.EmbedImage{}, fmt.Errorf("decode %s: %w", mime, err)
	}
	target := "image/jpeg"
	if !acceptsMIME(limits, target) {
		target = "image/png"
		if !acceptsMIME(limits, target) {
			return api.EmbedImage{}, fmt.Errorf(
				"the model takes none of the formats this can produce: %v", limits.MIMETypes)
		}
	}

	img := src
	for {
		out, err := encodeImage(img, target)
		if err != nil {
			return api.EmbedImage{}, err
		}
		if fitsBytes(limits, len(out)) {
			return api.EmbedImage{Data: out, MIMEType: target}, nil
		}
		b := img.Bounds()
		w, h := int(float64(b.Dx())*prepScaleStep), int(float64(b.Dy())*prepScaleStep)
		if w < prepMinSide || h < prepMinSide {
			return api.EmbedImage{}, fmt.Errorf("image does not fit %d bytes even scaled down", limits.MaxBytes)
		}
		scaled := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.ApproxBiLinear.Scale(scaled, scaled.Bounds(), img, b, draw.Src, nil)
		img = scaled
	}
}

func acceptsMIME(limits Limits, mime string) bool {
	return len(limits.MIMETypes) == 0 || slices.Contains(limits.MIMETypes, mime)
}

func fitsBytes(limits Limits, n int) bool {
	return limits.MaxBytes <= 0 || n <= limits.MaxBytes
}

func encodeImage(img image.Image, mime string) ([]byte, error) {
	var buf bytes.Buffer
	var err error
	if mime == "image/jpeg" {
		// JPEG has no alpha. Flatten onto white, which is what a transparent
		// diagram or logo looks like on the page it came from.
		b := img.Bounds()
		flat := image.NewRGBA(b)
		draw.Draw(flat, b, image.NewUniform(color.White), image.Point{}, draw.Src)
		draw.Draw(flat, b, img, b.Min, draw.Over)
		err = jpeg.Encode(&buf, flat, &jpeg.Options{Quality: prepJPEGQuality})
	} else {
		err = png.Encode(&buf, img)
	}
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", mime, err)
	}
	return buf.Bytes(), nil
}
