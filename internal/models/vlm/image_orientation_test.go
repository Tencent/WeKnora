package vlm

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// markedJPEG builds a JPEG whose only bright spot is a block in the top-left
// corner, then writes an EXIF Orientation into an APP1 segment in front of it —
// exactly how a camera or scanner stores a page it captured sideways.
func markedJPEG(t *testing.T, orientation int, order binary.ByteOrder) []byte {
	t.Helper()
	const width, height, block = 24, 8, 4
	src := image.NewRGBA(image.Rect(0, 0, width, height))
	for y := range height {
		for x := range width {
			src.Set(x, y, color.RGBA{A: 255})
		}
	}
	for y := range block {
		for x := range block {
			src.Set(x, y, color.RGBA{R: 255, G: 255, B: 255, A: 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, src, &jpeg.Options{Quality: 100}))
	raw := buf.Bytes()
	app1 := exifAPP1(orientation, order)
	out := make([]byte, 0, len(raw)+len(app1))
	out = append(out, raw[:2]...) // SOI
	out = append(out, app1...)
	out = append(out, raw[2:]...)
	return out
}

// exifAPP1 encodes a minimal APP1 segment ("Exif\0\0" + TIFF header + one IFD0
// entry) carrying only the Orientation tag.
func exifAPP1(orientation int, order binary.ByteOrder) []byte {
	tiff := make([]byte, 26)
	if order == binary.LittleEndian {
		copy(tiff, "II")
	} else {
		copy(tiff, "MM")
	}
	order.PutUint16(tiff[2:4], 42)                     // TIFF magic
	order.PutUint32(tiff[4:8], 8)                      // IFD0 offset
	order.PutUint16(tiff[8:10], 1)                     // one entry
	order.PutUint16(tiff[10:12], exifOrientationTag)   // 0x0112
	order.PutUint16(tiff[12:14], 3)                    // SHORT
	order.PutUint32(tiff[14:18], 1)                    // count
	order.PutUint16(tiff[18:20], uint16(orientation))  // value
	payload := append([]byte("Exif\x00\x00"), tiff...) // next IFD offset stays 0
	segmentLength := len(payload) + 2
	segment := []byte{0xFF, jpegMarkerAPP1, byte(segmentLength >> 8), byte(segmentLength)}
	return append(segment, payload...)
}

func decodeSize(t *testing.T, data []byte) (int, int) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	cfg := img.Bounds()
	return cfg.Dx(), cfg.Dy()
}

// brightestCorner reports which 4x4 corner of the decoded image is bright. The
// fixture paints exactly one corner, so this is how the tests tell a 90 degree
// clockwise turn from a counter-clockwise one.
func brightestCorner(t *testing.T, data []byte) string {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	bounds := img.Bounds()
	const block = 4
	corners := []struct {
		name   string
		x0, y0 int
	}{
		{"top-left", bounds.Min.X, bounds.Min.Y},
		{"top-right", bounds.Max.X - block, bounds.Min.Y},
		{"bottom-left", bounds.Min.X, bounds.Max.Y - block},
		{"bottom-right", bounds.Max.X - block, bounds.Max.Y - block},
	}
	best, bestMean := "", -1.0
	for _, corner := range corners {
		sum := 0.0
		for y := range block {
			for x := range block {
				r, g, b, _ := img.At(corner.x0+x, corner.y0+y).RGBA()
				sum += float64(r+g+b) / 3
			}
		}
		if mean := sum / (block * block); mean > bestMean {
			best, bestMean = corner.name, mean
		}
	}
	return best
}

func TestOrientImageBytesTurnsPixelsPerEXIFTag(t *testing.T) {
	for _, test := range []struct {
		orientation int
		width       int
		height      int
		brightest   string
	}{
		{orientation: 2, width: 24, height: 8, brightest: "top-right"},
		{orientation: 3, width: 24, height: 8, brightest: "bottom-right"},
		{orientation: 4, width: 24, height: 8, brightest: "bottom-left"},
		{orientation: 5, width: 8, height: 24, brightest: "top-left"},
		{orientation: 6, width: 8, height: 24, brightest: "top-right"},
		{orientation: 7, width: 8, height: 24, brightest: "bottom-right"},
		{orientation: 8, width: 8, height: 24, brightest: "bottom-left"},
	} {
		t.Run(string(rune('0'+test.orientation)), func(t *testing.T) {
			stored := markedJPEG(t, test.orientation, binary.BigEndian)
			require.Equal(t, test.orientation, jpegEXIFOrientation(stored))

			oriented := orientImageBytes(context.Background(), stored)

			width, height := decodeSize(t, oriented)
			assert.Equal(t, test.width, width, "width")
			assert.Equal(t, test.height, height, "height")
			assert.Equal(t, test.brightest, brightestCorner(t, oriented),
				"orientation %d must move the captured top-left corner to %s", test.orientation, test.brightest)
			// The pixels are upright now, so the tag must not survive: a viewer
			// that honoured it would turn the page a second time.
			assert.Zero(t, jpegEXIFOrientation(oriented), "the orientation tag must be gone")
		})
	}
}

func TestOrientImageBytesReadsBothTIFFByteOrders(t *testing.T) {
	for name, order := range map[string]binary.ByteOrder{
		"big-endian":    binary.BigEndian,
		"little-endian": binary.LittleEndian,
	} {
		t.Run(name, func(t *testing.T) {
			stored := markedJPEG(t, 6, order)
			require.Equal(t, 6, jpegEXIFOrientation(stored))

			width, height := decodeSize(t, orientImageBytes(context.Background(), stored))
			assert.Equal(t, 8, width)
			assert.Equal(t, 24, height)
		})
	}
}

func TestOrientImageBytesLeavesUntouchedPayloadsAlone(t *testing.T) {
	var pngBuf bytes.Buffer
	require.NoError(t, png.Encode(&pngBuf, image.NewRGBA(image.Rect(0, 0, 8, 4))))

	truncated := markedJPEG(t, 6, binary.BigEndian)[:40]
	corrupt := markedJPEG(t, 6, binary.BigEndian)
	copy(corrupt[24+6:24+10], []byte{0xDE, 0xAD, 0xBE, 0xEF}) // break the TIFF header

	for name, payload := range map[string][]byte{
		"orientation 1": markedJPEG(t, 1, binary.BigEndian),
		"no exif":       markedJPEGWithoutEXIF(t),
		"png":           pngBuf.Bytes(),
		"truncated":     truncated,
		"corrupt tiff":  corrupt,
		"empty":         {},
		"not an image":  []byte("this is not an image at all"),
		"orientation 0": markedJPEG(t, 0, binary.BigEndian),
		"orientation 9": markedJPEG(t, 9, binary.BigEndian),
	} {
		t.Run(name, func(t *testing.T) {
			out := orientImageBytes(context.Background(), payload)
			require.Len(t, out, len(payload))
			if len(payload) > 0 {
				assert.Same(t, &payload[0], &out[0], "the payload must be passed through by reference")
			}
		})
	}
}

func TestOrientImageBytesSkipsCanvasesOverThePixelBudget(t *testing.T) {
	previous := maxOrientationPixels
	maxOrientationPixels = 16
	t.Cleanup(func() { maxOrientationPixels = previous })

	stored := markedJPEG(t, 6, binary.BigEndian)
	out := orientImageBytes(context.Background(), stored)

	require.Len(t, out, len(stored))
	assert.Same(t, &stored[0], &out[0],
		"an oversized canvas keeps its stored orientation instead of exhausting the worker")
}

func TestOrientationVLMPredictsWithUprightPixels(t *testing.T) {
	inner := &recordingVLM{}
	model, err := wrapVLMImageOrientation(inner, nil)
	require.NoError(t, err)

	stored := markedJPEG(t, 6, binary.BigEndian)
	original := append([]byte(nil), stored...)
	_, err = model.Predict(context.Background(), [][]byte{stored}, "prompt")
	require.NoError(t, err)

	require.Len(t, inner.received, 1)
	width, height := decodeSize(t, inner.received[0])
	assert.Equal(t, 8, width, "the model must receive the rotated page")
	assert.Equal(t, 24, height)
	assert.Equal(t, "top-right", brightestCorner(t, inner.received[0]))
	assert.Equal(t, original, stored, "the caller's bytes must not be rewritten")
}

func TestOrientationVLMPassesPayloadsThroughUntouched(t *testing.T) {
	inner := &recordingVLM{}
	model, err := wrapVLMImageOrientation(inner, nil)
	require.NoError(t, err)

	stored := markedJPEGWithoutEXIF(t)
	_, err = model.Predict(context.Background(), [][]byte{stored}, "prompt")
	require.NoError(t, err)
	require.Len(t, inner.received, 1)
	assert.Same(t, &stored[0], &inner.received[0][0])

	_, err = model.Predict(context.Background(), nil, "prompt")
	require.NoError(t, err)
	assert.Nil(t, inner.received)
}

func markedJPEGWithoutEXIF(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, jpeg.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 8, 4)), &jpeg.Options{Quality: 100}))
	return buf.Bytes()
}

// markedJPEGAfterJFIFApp0 stores the EXIF block behind a JFIF APP0 — the layout
// every camera and scanner writes (Go's own encoder emits no APP0), and the one
// that catches a parser which assumes EXIF is the very first segment.
func markedJPEGAfterJFIFApp0(t *testing.T, orientation int, order binary.ByteOrder) []byte {
	t.Helper()
	raw := markedJPEG(t, orientation, order)
	app1 := exifAPP1(orientation, order)
	// Drop the APP1 the fixture above injected, then re-insert it behind APP0.
	withoutAPP1 := append(append([]byte{}, raw[:2]...), raw[2+len(app1):]...)
	app0 := []byte{
		0xFF, 0xE0, 0x00, 0x10, // APP0, 16 bytes including the length field
		'J', 'F', 'I', 'F', 0x00,
		0x01, 0x01, 0x00, 0x00, 0x01, 0x00, 0x01, 0x00, 0x00,
	}
	out := make([]byte, 0, len(withoutAPP1)+len(app0)+len(app1))
	out = append(out, withoutAPP1[:2]...)
	out = append(out, app0...)
	out = append(out, app1...)
	out = append(out, withoutAPP1[2:]...)
	return out
}

func TestOrientImageBytesFindsEXIFBehindJFIFApp0(t *testing.T) {
	stored := markedJPEGAfterJFIFApp0(t, 6, binary.BigEndian)
	require.Equal(t, byte(0xE0), stored[3], "APP0 must come first in this fixture")
	require.Equal(t, byte(0xE1), stored[21], "EXIF must sit behind APP0")
	require.Equal(t, 6, jpegEXIFOrientation(stored), "the tag must be found after APP0")

	width, height := decodeSize(t, orientImageBytes(context.Background(), stored))
	assert.Equal(t, 8, width)
	assert.Equal(t, 24, height)
}

// Normalising twice must be a passthrough the second time: the first pass
// removes the tag with the pixels, so a second decode would only burn CPU.
func TestOrientImageBytesIsIdempotent(t *testing.T) {
	stored := markedJPEG(t, 6, binary.BigEndian)

	once := orientImageBytes(context.Background(), stored)
	twice := orientImageBytes(context.Background(), once)

	require.Len(t, twice, len(once))
	assert.Same(t, &once[0], &twice[0], "the second pass must return the payload by reference")
	assert.Zero(t, jpegEXIFOrientation(twice))
}

type recordingVLM struct {
	received [][]byte
}

func (r *recordingVLM) Predict(_ context.Context, imgBytes [][]byte, _ string) (string, error) {
	r.received = imgBytes
	return "ok", nil
}

func (r *recordingVLM) GetModelName() string { return "recording" }
func (r *recordingVLM) GetModelID() string   { return "recording" }
