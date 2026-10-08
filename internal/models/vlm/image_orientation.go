package vlm

import (
	"bytes"
	"context"
	"encoding/binary"
	"image"
	"image/jpeg"
	"os"
	"strings"
	"sync"

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

// imageOrientationEnabled turns the EXIF orientation pass ON.
//
// The pass is OFF by default, on the same rule the other image-pipeline
// additions follow (see ImageProcessingConfig.ImageAttrsEnabled): upgrading a
// deployment must not silently change what happens to documents it already
// ingests. That rule does real work here, because the pass can go either way:
//
//   - It fixes what it was written for. A page stored sideways carries the
//     rotation in its EXIF tag, and models read the pixel matrix while ignoring
//     that tag, so the page reaches them rotated. Measured on a real API with
//     the production OCR prompt, a 180-degree page read 9/9 rounds wrong while
//     the rotated-back bytes read 9/9 rounds correct.
//   - It can also break a page that was already fine. The tag is the only
//     signal it has, and a camera can write it wrong at capture time: a
//     landscape-held phone shot can store upright pixels under tag 6, and
//     honouring that tag turns CORRECT pixels sideways. Those two inputs are
//     indistinguishable from the bytes alone.
//   - And it only covers part of the problem. It needs a JPEG that still
//     carries a tag 2..8; pages that lost their metadata (re-encoded, PNG,
//     re-saved) are left exactly as they were, which is where the worst
//     measured failures live (a 0.35 MP upside-down page lost 22.6 percentage
//     points of cell accuracy, and the pass never sees it).
//
// Asking the model to straighten the page instead is not a substitute: measured
// under the same prompt, that recovers a 180-degree page's table structure but
// misreads individual glyphs on a 90-degree one.
//
// So a deployment opts in with VLM_IMAGE_ORIENTATION=on when its sources are
// known to keep trustworthy tags on JPEGs; everything else keeps the bytes
// exactly as stored, byte for byte.
func imageOrientationEnabled() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("VLM_IMAGE_ORIENTATION"))) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// maxConcurrentOrientationConversions caps how many EXIF conversions this
// process runs at the same time. It covers every VLM instance and both the
// interactive and the background path.
//
// One conversion holds three full-size buffers at once: the decoded image
// (about 1.5 bytes per pixel for a JPEG), the RGBA canvas it is turned onto
// (4 bytes per pixel) and the re-encoded JPEG. At the pixel budget above that
// is on the order of 0.5 GiB of transient memory, so a single slot is what
// bounds the total: however many models, uploads or workers are in flight, the
// process never holds more than one conversion's worth of it. The limit is the
// only knob here; raise it if conversion throughput ever outweighs that.
const maxConcurrentOrientationConversions = 1

// orientationSlots is the process-wide conversion budget. It is deliberately
// package-level rather than a field of orientationVLM: a process has one memory
// budget, while VLM instances are created per model and re-created on every
// configuration change, so a per-instance budget would multiply the bound by
// the number of live instances — the exact stacking this gate exists to stop.
var orientationSlots = make(chan struct{}, maxConcurrentOrientationConversions)

// acquireOrientationSlot takes one conversion slot without waiting. ok is false
// when the budget is exhausted, and the caller then sends the image as stored,
// exactly as it does for a canvas above maxOrientationPixels. Waiting is
// deliberately not an option: the gate also sits on the interactive chat path,
// which must not queue behind someone else's scan when it can simply forward
// the bytes it was given.
func acquireOrientationSlot() (release func(), ok bool) {
	select {
	case orientationSlots <- struct{}{}:
		var once sync.Once
		return func() { once.Do(func() { <-orientationSlots }) }, true
	default:
		return nil, false
	}
}

// decodeImage is image.Decode behind a variable so the concurrency tests can
// keep a conversion inside the decoder — the only point where a slot stays held
// long enough for another call to observe — and count how often the decoder was
// reached.
var decodeImage = image.Decode

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
// files, canvases above maxOrientationPixels and calls that find every
// conversion slot busy are returned unchanged — the decorator never blocks a
// call it cannot improve.
//
// The whole pass is behind imageOrientationEnabled, which is off unless the
// deployment opts in with VLM_IMAGE_ORIENTATION=on. See the rationale there.
func orientImageBytes(ctx context.Context, data []byte) []byte {
	if !imageOrientationEnabled() {
		return data
	}
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
	// The header probe above allocates nothing; the slot is taken for the
	// expensive window only (decode -> turn -> re-encode) and held until that
	// window closes, on every return path below.
	release, acquired := acquireOrientationSlot()
	if !acquired {
		logger.Warnf(ctx,
			"[VLM] All %d rotation slots are busy; sending the %dx%d image in its stored orientation",
			maxConcurrentOrientationConversions, cfg.Width, cfg.Height)
		return data
	}
	defer release()
	img, _, err := decodeImage(bytes.NewReader(data))
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
