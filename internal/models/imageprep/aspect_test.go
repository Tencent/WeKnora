package imageprep

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func pngOf(t *testing.T, w, h int) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{uint8(x % 256), uint8(y % 256), uint8((x + y) % 256), 255})
		}
	}
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))
	return buf.Bytes()
}

func decodedSize(t *testing.T, data []byte) (w, h int) {
	t.Helper()
	img, _, err := image.Decode(bytes.NewReader(data))
	require.NoError(t, err)
	b := img.Bounds()
	return b.Dx(), b.Dy()
}

func TestClampAspectRatioLeavesNormalImagesAlone(t *testing.T) {
	for _, size := range [][2]int{{64, 64}, {640, 480}, {100, 5000}} { // 100×5000 is 50:1, still under 200
		data := pngOf(t, size[0], size[1])
		out, err := ClampAspectRatio(data)
		require.NoError(t, err)
		assert.Equal(t, data, out, "%d×%d should pass through untouched", size[0], size[1])
	}
}

func TestClampAspectRatioDownscalesTallImage(t *testing.T) {
	// 1×526 has a ratio of 526, far over the limit. The short side (1px) is a
	// degenerate strip, so it must be reported as such rather than sent.
	data := pngOf(t, 1, 526)
	_, err := ClampAspectRatio(data)
	require.ErrorIs(t, err, ErrDegenerateImage)
}

func TestClampAspectRatioDownscalesWideImage(t *testing.T) {
	data := pngOf(t, 526, 1)
	_, err := ClampAspectRatio(data)
	require.ErrorIs(t, err, ErrDegenerateImage)
}

func TestClampAspectRatioDownscalesLongContent(t *testing.T) {
	// A 40×10000 image (250:1) has a real short side (40px) — a tall table
	// exported as one image — so it is downscaled rather than dropped.
	data := pngOf(t, 40, 10000)
	out, err := ClampAspectRatio(data)
	require.NoError(t, err)
	w, h := decodedSize(t, out)
	// The long side is shrunk so the ratio falls under the limit.
	ratio := float64(h) / float64(w)
	assert.Less(t, ratio, MaxSafeAspectRatio, "clamped ratio %f must stay under %f", ratio, MaxSafeAspectRatio)
	assert.Equal(t, 40, w, "short side is preserved")
}

func TestClampAspectRatioDownscalesWideContent(t *testing.T) {
	data := pngOf(t, 10000, 40)
	out, err := ClampAspectRatio(data)
	require.NoError(t, err)
	w, h := decodedSize(t, out)
	ratio := float64(w) / float64(h)
	assert.Less(t, ratio, MaxSafeAspectRatio)
	assert.Equal(t, 40, h, "short side is preserved")
}

func TestClampAspectRatioPassesUndecodableThrough(t *testing.T) {
	out, err := ClampAspectRatio([]byte("not an image"))
	require.NoError(t, err)
	assert.Equal(t, []byte("not an image"), out, "undecodable bytes are left for the model to judge")
}
