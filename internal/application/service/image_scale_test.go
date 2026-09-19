package service

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// encodeTestPNG renders a deterministic gradient so the downscaled result is a
// real image rather than a flat colour (a flat image would hide orientation or
// aspect-ratio mistakes).
func encodeTestPNG(t *testing.T, width, height int, opaque bool) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, width, height))
	if opaque {
		for y := 0; y < height; y++ {
			for x := 0; x < width; x++ {
				img.Set(x, y, color.RGBA{R: uint8(x % 256), G: uint8(y % 256), B: 128, A: 255})
			}
		}
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("encode test png: %v", err)
	}
	return buf.Bytes()
}

func TestDownscaleForDescribe(t *testing.T) {
	t.Parallel()

	t.Run("shrinks the long edge and re-encodes as jpeg", func(t *testing.T) {
		original := encodeTestPNG(t, 1280, 960, true)
		got, changed := downscaleForDescribe(original, 640)
		if !changed {
			t.Fatal("an oversized image should be downscaled")
		}
		cfg, format, err := image.DecodeConfig(bytes.NewReader(got))
		if err != nil {
			t.Fatalf("decode downscaled bytes: %v", err)
		}
		if format != "jpeg" {
			t.Errorf("format = %q, want jpeg", format)
		}
		if cfg.Width != 640 || cfg.Height != 480 {
			t.Errorf("got %dx%d, want 640x480", cfg.Width, cfg.Height)
		}
	})

	t.Run("portrait orientation keys off the height", func(t *testing.T) {
		original := encodeTestPNG(t, 480, 1200, true)
		got, changed := downscaleForDescribe(original, 640)
		if !changed {
			t.Fatal("an oversized portrait image should be downscaled")
		}
		cfg, _, err := image.DecodeConfig(bytes.NewReader(got))
		if err != nil {
			t.Fatalf("decode downscaled bytes: %v", err)
		}
		if cfg.Width != 256 || cfg.Height != 640 {
			t.Errorf("got %dx%d, want 256x640", cfg.Width, cfg.Height)
		}
	})

	t.Run("leaves an image within the limit untouched", func(t *testing.T) {
		original := encodeTestPNG(t, 320, 240, true)
		got, changed := downscaleForDescribe(original, 640)
		if changed {
			t.Error("an image within the limit must not be re-encoded")
		}
		if !bytes.Equal(got, original) {
			t.Error("bytes should be returned unchanged")
		}
	})

	t.Run("disabled when the edge is zero or negative", func(t *testing.T) {
		original := encodeTestPNG(t, 2000, 1000, true)
		for _, edge := range []int{0, -1} {
			got, changed := downscaleForDescribe(original, edge)
			if changed || !bytes.Equal(got, original) {
				t.Errorf("edge %d should disable downscaling", edge)
			}
		}
	})

	t.Run("passes through undecodable input", func(t *testing.T) {
		garbage := []byte("this is not an image")
		got, changed := downscaleForDescribe(garbage, 640)
		if changed || !bytes.Equal(got, garbage) {
			t.Error("undecodable input should pass through so the image is still described")
		}
	})

	t.Run("transparent pixels become light, not black", func(t *testing.T) {
		// A fully transparent oversized PNG: without the white backdrop the
		// JPEG would encode black, which could bias both the description and
		// the classification of a transparent screenshot.
		transparent := encodeTestPNG(t, 1280, 1280, false)
		got, changed := downscaleForDescribe(transparent, 320)
		if !changed {
			t.Fatal("expected a downscale")
		}
		decoded, _, err := image.Decode(bytes.NewReader(got))
		if err != nil {
			t.Fatalf("decode downscaled bytes: %v", err)
		}
		r, g, b, _ := decoded.At(10, 10).RGBA()
		// JPEG is lossy, so accept "clearly light" rather than exact white.
		if r < 0x8000 || g < 0x8000 || b < 0x8000 {
			t.Errorf("transparent area encoded as r=%d g=%d b=%d, want a light backdrop", r, g, b)
		}
	})
}

func TestNormalizeClassifyMaxEdge(t *testing.T) {
	t.Parallel()
	cases := map[string]struct{ in, want int }{
		"unset selects the default": {0, types.DefaultClassifyMaxEdge},
		"negative disables":         {-1, 0},
		"explicit value is kept":    {1024, 1024},
	}
	for name, tc := range cases {
		if got := types.NormalizeImageClassifyMaxEdge(tc.in); got != tc.want {
			t.Errorf("%s: NormalizeImageClassifyMaxEdge(%d) = %d, want %d", name, tc.in, got, tc.want)
		}
	}
}
