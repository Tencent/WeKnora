package embedding

import (
	"bytes"
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

func pngEncode(b *bytes.Buffer, img image.Image) error { return png.Encode(b, img) }
func gifEncode(b *bytes.Buffer, img image.Image) error { return gif.Encode(b, img, nil) }

// noise does not compress, so its size follows its pixel count.
func noise() func(x, y int) color.Color {
	r := rand.New(rand.NewSource(1))
	return func(int, int) color.Color {
		return color.RGBA{uint8(r.Intn(256)), uint8(r.Intn(256)), uint8(r.Intn(256)), 255}
	}
}

func TestPrepareImagePassesThroughWhatTheModelTakes(t *testing.T) {
	data := encoded(t, 8, 8, pngEncode, noise())
	img, err := PrepareImage(data, ImageLimits{MaxBytes: 1 << 20, MIMETypes: []string{"image/png"}})
	require.NoError(t, err)
	assert.Equal(t, "image/png", img.MIMEType)
	assert.Equal(t, data, img.Data, "an acceptable image is sent untouched")
}

func TestPrepareImageConvertsAFormatTheModelDoesNotTake(t *testing.T) {
	data := encoded(t, 8, 8, gifEncode, noise())
	img, err := PrepareImage(data, ImageLimits{MIMETypes: []string{"image/png", "image/jpeg"}})
	require.NoError(t, err)
	assert.Equal(t, "image/jpeg", img.MIMEType)
	_, err = jpeg.Decode(bytes.NewReader(img.Data))
	assert.NoError(t, err)

	img, err = PrepareImage(data, ImageLimits{MIMETypes: []string{"image/png"}})
	require.NoError(t, err)
	assert.Equal(t, "image/png", img.MIMEType, "PNG when the model takes no JPEG")
}

func TestPrepareImageShrinksUntilItFits(t *testing.T) {
	data := encoded(t, 600, 600, pngEncode, noise())
	limit := len(data) / 10
	img, err := PrepareImage(data, ImageLimits{MaxBytes: limit})
	require.NoError(t, err)
	assert.LessOrEqual(t, len(img.Data), limit)
	cfg, err := jpeg.DecodeConfig(bytes.NewReader(img.Data))
	require.NoError(t, err)
	assert.Less(t, cfg.Width, 600)
	assert.Equal(t, cfg.Width, cfg.Height, "the aspect ratio is kept")

	_, err = PrepareImage(data, ImageLimits{MaxBytes: 100})
	assert.ErrorContains(t, err, "does not fit")
}

func TestPrepareImageFlattensTransparencyOntoWhite(t *testing.T) {
	data := encoded(t, 8, 8, pngEncode, func(int, int) color.Color { return color.Transparent })
	img, err := PrepareImage(data, ImageLimits{MIMETypes: []string{"image/jpeg"}})
	require.NoError(t, err)
	decoded, err := jpeg.Decode(bytes.NewReader(img.Data))
	require.NoError(t, err)
	r, g, b, _ := decoded.At(4, 4).RGBA()
	assert.Greater(t, r>>8, uint32(240))
	assert.Greater(t, g>>8, uint32(240))
	assert.Greater(t, b>>8, uint32(240))
}

func TestPrepareImageRejectsWhatIsNotAnImage(t *testing.T) {
	_, err := PrepareImage([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"), ImageLimits{})
	assert.ErrorContains(t, err, "not an image")
	_, err = PrepareImage(nil, ImageLimits{})
	assert.ErrorContains(t, err, "empty")
}
