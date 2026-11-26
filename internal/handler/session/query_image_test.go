package session

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"strings"
	"testing"
)

func samplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode png: %v", err)
	}
	return buf.Bytes()
}

func TestDecodeQueryImage(t *testing.T) {
	pngBytes := samplePNG(t)
	b64 := base64.StdEncoding.EncodeToString(pngBytes)

	t.Run("empty means no image", func(t *testing.T) {
		data, mediaType, err := decodeQueryImage("   ")
		if err != nil || data != nil || mediaType != "" {
			t.Fatalf("got (%v, %q, %v), want (nil, \"\", nil)", data, mediaType, err)
		}
	})

	t.Run("raw base64 is detected as png", func(t *testing.T) {
		data, mediaType, err := decodeQueryImage(b64)
		if err != nil {
			t.Fatalf("decodeQueryImage: %v", err)
		}
		if !bytes.Equal(data, pngBytes) {
			t.Fatalf("decoded bytes differ from the input image")
		}
		if mediaType != "image/png" {
			t.Fatalf("mediaType = %q, want image/png", mediaType)
		}
	})

	t.Run("data uri keeps its declared type", func(t *testing.T) {
		_, mediaType, err := decodeQueryImage("data:image/png;base64," + b64)
		if err != nil {
			t.Fatalf("decodeQueryImage: %v", err)
		}
		if mediaType != "image/png" {
			t.Fatalf("mediaType = %q, want image/png", mediaType)
		}
	})

	t.Run("non base64 data uri is rejected", func(t *testing.T) {
		if _, _, err := decodeQueryImage("data:image/png,notbase64"); err == nil {
			t.Fatal("expected an error for a non-base64 data URI")
		}
	})

	t.Run("invalid base64 is rejected", func(t *testing.T) {
		if _, _, err := decodeQueryImage("!!!not base64!!!"); err == nil {
			t.Fatal("expected an error for invalid base64")
		}
	})

	t.Run("unsupported image type is rejected", func(t *testing.T) {
		// "hello" decodes as text/plain, which no image embedder accepts.
		raw := base64.StdEncoding.EncodeToString([]byte("hello"))
		if _, _, err := decodeQueryImage(raw); err == nil {
			t.Fatal("expected an error for a non-image payload")
		}
	})

	t.Run("oversized image is rejected", func(t *testing.T) {
		huge := base64.StdEncoding.EncodeToString(bytes.Repeat([]byte{0}, maxQueryImageBytes+1))
		_, _, err := decodeQueryImage(huge)
		if err == nil {
			t.Fatal("expected an error for an oversized image")
		}
		if !strings.Contains(err.Error(), "exceeds") {
			t.Fatalf("error = %v, want it to mention the size limit", err)
		}
	})
}

// The bytes decide, not the declaration: a text payload labelled image/png
// used to pass this gate and fail downstream as a 500.
func TestDecodeQueryImageContentOverDeclaration(t *testing.T) {
	pngB64 := base64.StdEncoding.EncodeToString(samplePNG(t))
	textB64 := base64.StdEncoding.EncodeToString([]byte("hello"))

	t.Run("octet-stream label on a real png is a mismatch", func(t *testing.T) {
		_, _, err := decodeQueryImage("data:application/octet-stream;base64," + pngB64)
		if err == nil {
			t.Fatal("expected a mismatch error")
		}
		if !strings.Contains(err.Error(), "image/png") || !strings.Contains(err.Error(), "application/octet-stream") {
			t.Fatalf("error = %v, want it to name both types", err)
		}
	})

	t.Run("a wrong gif label on a png is a mismatch", func(t *testing.T) {
		if _, _, err := decodeQueryImage("data:image/gif;base64," + pngB64); err == nil {
			t.Fatal("expected a mismatch error for a gif label on png bytes")
		}
	})

	t.Run("text labelled as png is a mismatch at the gate", func(t *testing.T) {
		_, _, err := decodeQueryImage("data:image/png;base64," + textB64)
		if err == nil {
			t.Fatal("expected the gate to reject text labelled as png")
		}
	})

	t.Run("the harmless image/jpg alias matches jpeg content", func(t *testing.T) {
		var buf bytes.Buffer
		img := image.NewRGBA(image.Rect(0, 0, 2, 2))
		if err := jpeg.Encode(&buf, img, nil); err != nil {
			t.Fatalf("encode jpeg: %v", err)
		}
		_, mediaType, err := decodeQueryImage("data:image/jpg;base64," + base64.StdEncoding.EncodeToString(buf.Bytes()))
		if err != nil {
			t.Fatalf("decodeQueryImage: %v", err)
		}
		if mediaType != "image/jpeg" {
			t.Fatalf("mediaType = %q, want the sniffed image/jpeg", mediaType)
		}
	})
}
