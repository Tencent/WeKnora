package embedding

import (
	"context"
	"encoding/json"
	"math"
	"os"
	"slices"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/models/imageprep"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/panjf2000/ants/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Opt-in integration check against an operator-selected Qwen3-VL endpoint.
// The fixture contains {"texts":[...],"image_paths":[...]} and stays outside
// the repository. This only calls the embedding model; it never writes an index.
// WEKNORA_EMBEDDING_TEST_URL is the base URL including /v1.
// Private/IP endpoints need the usual explicit SSRF_WHITELIST setting.
// A server without batch-invariant computation can fail the strict numerical
// bounds below even when it accepts the batch and returns correctly ordered vectors.
func TestLiveQwenMessagesBatchParity(t *testing.T) {
	base := os.Getenv("WEKNORA_EMBEDDING_TEST_URL")
	fixture := os.Getenv("WEKNORA_EMBEDDING_TEST_FIXTURE")
	if base == "" || fixture == "" {
		t.Skip("set WEKNORA_EMBEDDING_TEST_URL and WEKNORA_EMBEDDING_TEST_FIXTURE for live parity checks")
	}
	var inputs struct {
		Texts      []string `json:"texts"`
		ImagePaths []string `json:"image_paths"`
	}
	raw, err := os.ReadFile(fixture)
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(raw, &inputs))
	require.Greater(t, len(inputs.Texts), 1)
	require.Greater(t, len(inputs.ImagePaths), 1)
	t.Setenv("BATCH_EMBED_SIZE", "5")
	pool, err := ants.NewPool(5)
	require.NoError(t, err)
	defer pool.Release()
	config := Config{
		Source: types.ModelSourceRemote, Provider: "generic", ModelName: "Qwen/Qwen3-VL-Embedding-8B",
		BaseURL: base, APIKey: os.Getenv("WEKNORA_EMBEDDING_TEST_API_KEY"), Dimensions: 4096,
	}
	batched, err := NewEmbedder(config, NewBatchEmbedder(pool), nil)
	require.NoError(t, err)
	config.Spec = &types.ModelSpecOverride{Compat: map[string]any{"batch_messages": false}}
	serial, err := NewEmbedder(config, NewBatchEmbedder(pool), nil)
	require.NoError(t, err)
	config.Spec = &types.ModelSpecOverride{Compat: map[string]any{
		"batch_messages": true, "max_batch_size": 2, "max_image_batch_size": 2,
	}}
	smallBatch, err := NewEmbedder(config, NewBatchEmbedder(pool), nil)
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	t.Run("texts", func(t *testing.T) {
		start := time.Now()
		want, err := serial.BatchEmbed(ctx, inputs.Texts)
		require.NoError(t, err)
		serialTime := time.Since(start)
		start = time.Now()
		got, err := batched.BatchEmbedWithPool(ctx, batched, inputs.Texts)
		require.NoError(t, err)
		t.Logf("inputs=%d serial=%s batch=%s", len(want), serialTime, time.Since(start))
		assertLiveVectorParity(t, want, got)
		repeat, err := serial.BatchEmbed(ctx, inputs.Texts)
		require.NoError(t, err)
		t.Log("repeated serial control")
		assertLiveVectorParity(t, want, repeat)
		got, err = smallBatch.BatchEmbed(ctx, inputs.Texts)
		require.NoError(t, err)
		t.Log("batch size two")
		assertLiveVectorParity(t, want, got)
		reversed := slices.Clone(inputs.Texts)
		slices.Reverse(reversed)
		got, err = batched.BatchEmbedWithPool(ctx, batched, reversed)
		require.NoError(t, err)
		slices.Reverse(got)
		t.Log("reversed batch")
		assertLiveVectorParity(t, want, got)
	})
	t.Run("images", func(t *testing.T) {
		imageBatch, ok := AsImageEmbedder(batched)
		require.True(t, ok)
		imageSerial, ok := AsImageEmbedder(serial)
		require.True(t, ok)
		images := make([]Image, len(inputs.ImagePaths))
		for i, path := range inputs.ImagePaths {
			data, err := os.ReadFile(path)
			require.NoError(t, err)
			images[i], err = imageprep.Prepare(data, imageBatch.ImageLimits())
			require.NoError(t, err)
		}
		start := time.Now()
		want, err := imageSerial.BatchEmbedImages(ctx, images)
		require.NoError(t, err)
		serialTime := time.Since(start)
		start = time.Now()
		got, err := imageBatch.BatchEmbedImages(ctx, images)
		require.NoError(t, err)
		t.Logf("inputs=%d serial=%s batch=%s", len(want), serialTime, time.Since(start))
		assertLiveVectorParity(t, want, got)
		repeat, err := imageSerial.BatchEmbedImages(ctx, images)
		require.NoError(t, err)
		t.Log("repeated serial control")
		assertLiveVectorParity(t, want, repeat)
		imageSmall, ok := AsImageEmbedder(smallBatch)
		require.True(t, ok)
		got, err = imageSmall.BatchEmbedImages(ctx, images)
		require.NoError(t, err)
		t.Log("batch size two")
		assertLiveVectorParity(t, want, got)
		slices.Reverse(images)
		got, err = imageBatch.BatchEmbedImages(ctx, images)
		require.NoError(t, err)
		slices.Reverse(got)
		t.Log("reversed batch")
		assertLiveVectorParity(t, want, got)
	})
}

func assertLiveVectorParity(t *testing.T, want, got [][]float32) {
	t.Helper()
	require.Len(t, got, len(want))
	maxAbs, minCos, unequal := 0.0, 1.0, 0
	for i := range want {
		require.Len(t, want[i], 4096)
		require.Len(t, got[i], 4096)
		var dot, a2, b2 float64
		for j, a := range want[i] {
			b := got[i][j]
			af, bf := float64(a), float64(b)
			require.False(t, math.IsNaN(af) || math.IsNaN(bf) || math.IsInf(af, 0) || math.IsInf(bf, 0))
			maxAbs = math.Max(maxAbs, math.Abs(af-bf))
			if a != b {
				unequal++
			}
			dot += af * bf
			a2 += af * af
			b2 += bf * bf
		}
		require.Greater(t, a2, 0.0)
		require.Greater(t, b2, 0.0)
		minCos = math.Min(minCos, dot/math.Sqrt(a2*b2))
	}
	t.Logf("vectors=%d dimensions=4096 unequal_components=%d max_abs_diff=%.9g min_cosine=%.12f",
		len(got), unequal, maxAbs, minCos)
	// GPU batching may change low-order bits; do not confuse numerical
	// equivalence with bit-for-bit reproducibility.
	assert.GreaterOrEqual(t, minCos, 0.99999)
	assert.LessOrEqual(t, maxAbs, 0.001)
}
