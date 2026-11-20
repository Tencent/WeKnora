package imageprep

import (
	"bytes"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"math/rand"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func encoded(
	t *testing.T, w, h int, encode func(*bytes.Buffer, image.Image) error, fill func(x, y int) color.Color,
) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, fill(x, y))
		}
	}
	var buf bytes.Buffer
	require.NoError(t, encode(&buf, img))
	return buf.Bytes()
}

func pngEncode(b *bytes.Buffer, img image.Image) error  { return png.Encode(b, img) }
func gifEncode(b *bytes.Buffer, img image.Image) error  { return gif.Encode(b, img, nil) }
func jpegEncode(b *bytes.Buffer, img image.Image) error { return jpeg.Encode(b, img, nil) }

// noise does not compress, so its size follows its pixel count.
func noise() func(x, y int) color.Color {
	r := rand.New(rand.NewSource(1))
	return func(int, int) color.Color {
		return color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255}
	}
}

func TestPrepareImagePassesThroughWhatTheModelTakes(t *testing.T) {
	data := encoded(t, 8, 8, pngEncode, noise())
	img, err := Prepare(data, Limits{MaxBytes: 1 << 20, MIMETypes: []string{"image/png"}})
	require.NoError(t, err)
	assert.Equal(t, "image/png", img.MIMEType)
	assert.Equal(t, data, img.Data, "an acceptable image is sent untouched")
}

func TestPrepareImageConvertsAFormatTheModelDoesNotTake(t *testing.T) {
	data := encoded(t, 8, 8, gifEncode, noise())
	img, err := Prepare(data, Limits{MIMETypes: []string{"image/png", "image/jpeg"}})
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", img.MIMEType)
	_, err = jpeg.Decode(bytes.NewReader(img.Data))
	assert.NoError(t, err)

	img, err = Prepare(data, Limits{MIMETypes: []string{"image/png"}})
	require.NoError(t, err)
	assert.Equal(t, "image/png", img.MIMEType, "PNG when the model takes no JPEG")
}

func TestPrepareImageShrinksUntilItFits(t *testing.T) {
	data := encoded(t, 600, 600, pngEncode, noise())
	limit := len(data) / 10
	img, err := Prepare(data, Limits{MaxBytes: limit})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(img.Data), limit)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	require.NoError(t, err)
	assert.Less(t, cfg.Width, 600)
	assert.Equal(t, cfg.Width, cfg.Height, "the aspect ratio is kept")

	_, err = Prepare(data, Limits{MaxBytes: 100})
	assert.ErrorContains(t, err, "does not fit")
}

func TestPrepareImageFlattensTransparencyOntoWhite(t *testing.T) {
	data := encoded(t, 8, 8, pngEncode, func(int, int) color.Color { return color.Transparent })
	img, err := Prepare(data, Limits{MIMETypes: []string{"image/jpeg"}})
	require.NoError(t, err)
	decoded, err := jpeg.Decode(bytes.NewReader(img.Data))
	require.NoError(t, err)
	r, g, b, _ := decoded.At(4, 4).RGBA()
	assert.Greater(t, r>>8, uint32(240))
	assert.Greater(t, g>>8, uint32(240))
	assert.Greater(t, b>>8, uint32(240))
}

func TestPrepareImageRejectsWhatIsNotAnImage(t *testing.T) {
	_, err := Prepare([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), Limits{})
	assert.ErrorContains(t, err, "not an image")
	_, err = Prepare(nil, Limits{})
	assert.ErrorContains(t, err, "empty")
}

// TestPrepareImageSendsColourChannels is the regression test for #5528: a
// vision processor that takes three channels fails on a grayscale or palette
// image, and Prepare used to pass those through untouched.
func TestPrepareImageSendsColourChannels(t *testing.T) {
	gray := image.NewGray(image.Rect(0, 0, 8, 8))
	for i := range gray.Pix {
		gray.Pix[i] = uint8(i * 4)
	}
	paletted := image.NewPaletted(gray.Bounds(), color.Palette{color.Black, color.White, color.RGBA{255, 0, 0, 255}})
	for i := range paletted.Pix {
		paletted.Pix[i] = uint8(i % 3)
	}
	for _, tc := range []struct {
		name     string
		src      image.Image
		encode   func(*bytes.Buffer, image.Image) error
		wantMIME string
		wantType string
	}{
		{"grayscale PNG", gray, pngEncode, "image/png", "*image.RGBA"},
		{"palette PNG", paletted, pngEncode, "image/png", "*image.RGBA"},
		{"grayscale JPEG", gray, jpegEncode, "image/jpeg", "*image.YCbCr"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			require.NoError(t, tc.encode(&buf, tc.src))
			img, err := Prepare(buf.Bytes(), Limits{})
			require.NoError(t, err)
			assert.Equal(t, tc.wantMIME, img.MIMEType)
			decoded, _, err := image.Decode(bytes.NewReader(img.Data))
			require.NoError(t, err)
			assert.Equal(t, tc.wantType, fmt.Sprintf("%T", decoded), "sent as three colour channels")
			if tc.wantMIME == "image/png" {
				for _, p := range []image.Point{{0, 0}, {4, 5}, {7, 7}} {
					assert.Equal(t, color.RGBAModel.Convert(tc.src.At(p.X, p.Y)), decoded.At(p.X, p.Y),
						"a PNG is redrawn losslessly")
				}
			}
		})
	}
}

func TestPrepareImagePassesColourImagesThrough(t *testing.T) {
	half := func(x, _ int) color.Color { return color.NRGBA{200, 30, 30, uint8(x * 30)} }
	for name, data := range map[string][]byte{
		"RGBA PNG":    encoded(t, 8, 8, pngEncode, half),
		"colour JPEG": encoded(t, 8, 8, jpegEncode, noise()),
	} {
		img, err := Prepare(data, Limits{})
		require.NoError(t, err, name)
		assert.Equal(t, data, img.Data, "%s is sent untouched", name)
	}
}
