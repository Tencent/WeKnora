package core

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

// squareWhiteWithTopBar is a 200×200 white PNG with a dark 160×20 bar near the
// top — the Feishu-whiteboard "content stuck at the top of a white square" case.
func squareWhiteWithTopBar() []byte {
	const n = 200
	img := image.NewRGBA(image.Rect(0, 0, n, n))
	white := color.RGBA{255, 255, 255, 255}
	ink := color.RGBA{20, 20, 20, 255}
	for y := 0; y < n; y++ {
		for x := 0; x < n; x++ {
			img.Set(x, y, white)
		}
	}
	for y := 8; y < 28; y++ {
		for x := 20; x < 180; x++ {
			img.Set(x, y, ink)
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	return buf.Bytes()
}

func TestAutocropImage_CropsTallWhitespace(t *testing.T) {
	orig := squareWhiteWithTopBar()
	out, err := AutocropImage(orig)
	if err != nil {
		t.Fatalf("AutocropImage: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("decode cropped: %v", err)
	}
	if cfg.Height >= 200 {
		t.Errorf("height still %d, want a crop that shortens the 200px square", cfg.Height)
	}
	if cfg.Height > 80 {
		t.Errorf("height %d still too tall; content bar is only ~20px plus margin", cfg.Height)
	}
	if cfg.Width < 140 || cfg.Width > 200 {
		t.Errorf("width %d out of expected cropped range", cfg.Width)
	}
}

func TestAutocropImage_SkipSVG(t *testing.T) {
	svg := []byte(`<svg xmlns="http://www.w3.org/2000/svg" width="10" height="10"></svg>`)
	out, err := AutocropImage(svg)
	if err != nil {
		t.Fatalf("svg: %v", err)
	}
	if !bytes.Equal(out, svg) {
		t.Error("svg must be returned unchanged")
	}
}

func TestAutocropImage_NoContent(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 32, 32))
	for y := 0; y < 32; y++ {
		for x := 0; x < 32; x++ {
			img.Set(x, y, color.RGBA{255, 255, 255, 255})
		}
	}
	var buf bytes.Buffer
	_ = png.Encode(&buf, img)
	out, err := AutocropImage(buf.Bytes())
	if err != nil {
		t.Fatalf("blank: %v", err)
	}
	if !bytes.Equal(out, buf.Bytes()) {
		t.Error("blank image must be left as-is (no-content)")
	}
}
