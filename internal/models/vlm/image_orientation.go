package vlm

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"

	"github.com/Tencent/WeKnora/internal/logger"
)

const (
	// exifOrientationTag is TIFF tag 0x0112 (Orientation).
	exifOrientationTag = 0x0112
	// jpegMarkerAPP1 carries the EXIF payload.
	jpegMarkerAPP1 = 0xE1
	jpegMarkerSOS  = 0xDA
	jpegMarkerEOI  = 0xD9
	// orientationJPEGQuality re-encodes a rotated page. Only images that
	// actually carry a rotation tag are re-encoded; the rest keep their bytes.
	orientationJPEGQuality = 90
)

// maxOrientationPixels bounds the canvases this decorator is willing to decode.
// Rotation needs the whole image in memory (about four bytes per pixel), so an
// absurd canvas is left untouched rather than risking the worker. A 600 DPI A3
// scan (about 70 megapixels) still fits.
var maxOrientationPixels = 80 << 20

// orientationVLM applies the EXIF Orientation tag to the pixels before an image
// reaches a model.
//
// Cameras and scanners store the sensor's pixel matrix as captured and record
// how it must be turned for display in EXIF tag 0x0112. Browsers and image
// viewers honour that tag, so an upload looks upright everywhere in the UI, but
// vision models read the pixel matrix and generally ignore the tag: a portrait
// page stored sideways (orientation 6/8) or upside down (3) reaches the model
// rotated. The model then has to recognise rotated glyphs and run table layout
// reasoning in a rotated frame, which is where merged-cell anchors, names and
// numbers start drifting.
type orientationVLM struct {
	inner VLM
}

func (w *orientationVLM) GetModelName() string { return w.inner.GetModelName() }
func (w *orientationVLM) GetModelID() string   { return w.inner.GetModelID() }

func (w *orientationVLM) Predict(ctx context.Context, imgBytes [][]byte, prompt string) (string, error) {
	if len(imgBytes) == 0 {
		return w.inner.Predict(ctx, imgBytes, prompt)
	}
	oriented := make([][]byte, len(imgBytes))
	for i, img := range imgBytes {
		oriented[i] = orientImageBytes(ctx, img)
	}
	return w.inner.Predict(ctx, oriented, prompt)
}

// wrapVLMImageOrientation installs the EXIF orientation normaliser as the
// innermost VLM decorator, so every implementation (remote API, Ollama, cloud)
// and every caller sees upright pixels while the debug and Langfuse layers
// record what was really sent.
func wrapVLMImageOrientation(v VLM, err error) (VLM, error) {
	if err != nil || v == nil {
		return v, err
	}
	return &orientationVLM{inner: v}, nil
}

// orientImageBytes returns image bytes whose pixels match their EXIF
// orientation. Images without a rotation tag, non-JPEG payloads, unreadable
// files and canvases above maxOrientationPixels are returned unchanged — the
// decorator never blocks a call it cannot improve.
func orientImageBytes(ctx context.Context, data []byte) []byte {
	orientation := jpegEXIFOrientation(data)
	if orientation <= 1 || orientation > 8 {
		return data
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width <= 0 || cfg.Height <= 0 {
		return data
	}
	if cfg.Width*cfg.Height > maxOrientationPixels {
		logger.Warnf(ctx,
			"[VLM] Image %dx%d exceeds the %d pixel rotation budget; sending it in its stored orientation",
			cfg.Width, cfg.Height, maxOrientationPixels)
		return data
	}
	img, _, err := image.Decode(bytes.NewReader(data))
	if err != nil {
		return data
	}
	rotated := applyEXIFOrientation(img, orientation)
	if rotated == nil {
		return data
	}
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, rotated, &jpeg.Options{Quality: orientationJPEGQuality}); err != nil {
		return data
	}
	logger.Infof(ctx, "[VLM] Applied EXIF orientation %d: %dx%d -> %dx%d, %d -> %d bytes",
		orientation, cfg.Width, cfg.Height, rotated.Bounds().Dx(), rotated.Bounds().Dy(), len(data), buf.Len())
	return buf.Bytes()
}

// applyEXIFOrientation rebuilds the image so that no rotation or mirroring is
// left to the viewer. The mapping is the one EXIF defines for each value:
// 2 mirrors horizontally, 4 vertically, 3 turns 180 degrees, 6 turns 90 degrees
// clockwise, 8 turns 90 degrees counter-clockwise, and 5/7 are the transposed
// (mirrored diagonal) variants. Values outside 2..8 return nil.
func applyEXIFOrientation(src image.Image, orientation int) image.Image {
	bounds := src.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width == 0 || height == 0 {
		return nil
	}
	dstWidth, dstHeight := width, height
	if orientation >= 5 {
		dstWidth, dstHeight = height, width
	}
	dst := image.NewRGBA(image.Rect(0, 0, dstWidth, dstHeight))
	for y := range dstHeight {
		for x := range dstWidth {
			var sx, sy int
			switch orientation {
			case 2:
				sx, sy = width-1-x, y
			case 3:
				sx, sy = width-1-x, height-1-y
			case 4:
				sx, sy = x, height-1-y
			case 5:
				sx, sy = y, x
			case 6:
				sx, sy = y, height-1-x
			case 7:
				sx, sy = width-1-y, height-1-x
			case 8:
				sx, sy = width-1-y, x
			default:
				return nil
			}
			dst.Set(x, y, src.At(bounds.Min.X+sx, bounds.Min.Y+sy))
		}
	}
	return dst
}

// jpegEXIFOrientation returns the EXIF Orientation of a JPEG, or 0 when the
// payload is not a JPEG, carries no EXIF block, or the tag cannot be read
// without guessing. Only segment markers are walked — image data is never
// touched — so the cost is proportional to the headers, not the file.
func jpegEXIFOrientation(data []byte) int {
	if len(data) < 4 || data[0] != 0xFF || data[1] != 0xD8 {
		return 0
	}
	for pos := 2; pos+4 <= len(data); {
		if data[pos] != 0xFF {
			return 0 // Desynchronised: refuse to interpret the rest.
		}
		marker := data[pos+1]
		switch {
		case marker == jpegMarkerSOS || marker == jpegMarkerEOI:
			return 0
		case marker == 0x00 || marker == 0xFF:
			pos++ // Padding or a stuffed byte.
			continue
		case marker >= 0xD0 && marker <= 0xD8:
			pos += 2 // Standalone marker without a payload.
			continue
		}
		segLen := int(binary.BigEndian.Uint16(data[pos+2 : pos+4]))
		if segLen < 2 || pos+2+segLen > len(data) {
			return 0
		}
		if marker == jpegMarkerAPP1 {
			if orientation := exifTIFFOrientation(data[pos+4 : pos+2+segLen]); orientation != 0 {
				return orientation
			}
		}
		pos += 2 + segLen
	}
	return 0
}

// exifTIFFOrientation reads tag 0x0112 out of an APP1 payload ("Exif\0\0"
// followed by a TIFF header and IFD0).
func exifTIFFOrientation(segment []byte) int {
	const exifHeader = "Exif\x00\x00"
	if len(segment) < len(exifHeader)+8 || string(segment[:len(exifHeader)]) != exifHeader {
		return 0
	}
	tiff := segment[len(exifHeader):]
	var order binary.ByteOrder
	switch {
	case tiff[0] == 'I' && tiff[1] == 'I':
		order = binary.LittleEndian
	case tiff[0] == 'M' && tiff[1] == 'M':
		order = binary.BigEndian
	default:
		return 0
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0
	}
	offset := int(order.Uint32(tiff[4:8]))
	if offset < 8 || offset+2 > len(tiff) {
		return 0
	}
	entries := int(order.Uint16(tiff[offset : offset+2]))
	for i := range entries {
		entry := offset + 2 + i*12
		if entry+12 > len(tiff) {
			return 0
		}
		if order.Uint16(tiff[entry:entry+2]) != exifOrientationTag {
			continue
		}
		// Orientation is a SHORT, so its value sits in the first two bytes of
		// the entry's value field.
		if order.Uint16(tiff[entry+2:entry+4]) != 3 {
			return 0
		}
		value := int(order.Uint16(tiff[entry+8 : entry+10]))
		if value >= 1 && value <= 8 {
			return value
		}
		return 0
	}
	return 0
}
