package session

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
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
