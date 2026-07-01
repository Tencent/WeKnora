package embedding_test

import (
	"encoding/hex"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/stretchr/testify/assert"
)

func TestComputeHash_Deterministic(t *testing.T) {
	cfg := embedding.HashChunkConfig(512, 80, "auto")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")
	h2 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")

	assert.Equal(t, h1, h2, "same inputs must produce same hash")
	assert.Len(t, h1, 64, "SHA-256 hex digest is 64 chars")
}

func TestComputeHash_DifferentInputs(t *testing.T) {
	cfg := embedding.HashChunkConfig(512, 80, "auto")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")
	h2 := embedding.ComputeHash("hello world!", "model-a", 768, cfg, "v1")

	assert.NotEqual(t, h1, h2, "different text must produce different hash")
}

func TestComputeHash_DifferentModel(t *testing.T) {
	cfg := embedding.HashChunkConfig(512, 80, "auto")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")
	h2 := embedding.ComputeHash("hello world", "model-b", 768, cfg, "v1")

	assert.NotEqual(t, h1, h2, "different model must produce different hash")
}

func TestComputeHash_DifferentDimensions(t *testing.T) {
	cfg := embedding.HashChunkConfig(512, 80, "auto")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")
	h2 := embedding.ComputeHash("hello world", "model-a", 1024, cfg, "v1")

	assert.NotEqual(t, h1, h2, "different dimensions must produce different hash")
}

func TestComputeHash_DifferentVersion(t *testing.T) {
	cfg := embedding.HashChunkConfig(512, 80, "auto")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v1")
	h2 := embedding.ComputeHash("hello world", "model-a", 768, cfg, "v2")

	assert.NotEqual(t, h1, h2, "different preprocessing version must produce different hash")
}

func TestComputeHash_DifferentChunkConfig(t *testing.T) {
	cfg1 := embedding.HashChunkConfig(512, 80, "auto")
	cfg2 := embedding.HashChunkConfig(256, 40, "heading")

	h1 := embedding.ComputeHash("hello world", "model-a", 768, cfg1, "v1")
	h2 := embedding.ComputeHash("hello world", "model-a", 768, cfg2, "v1")

	assert.NotEqual(t, h1, h2, "different chunk config must produce different hash")
}

func TestHashChunkConfig_Deterministic(t *testing.T) {
	b1 := embedding.HashChunkConfig(512, 80, "auto")
	b2 := embedding.HashChunkConfig(512, 80, "auto")

	assert.Equal(t, hex.EncodeToString(b1), hex.EncodeToString(b2),
		"chunk config hash must be deterministic")
}

func TestFloat32sToBytes_Roundtrip(t *testing.T) {
	original := []float32{0.1, 0.2, 0.3, -0.1, 1.0, -1.0}
	bytes := embedding.Float32sToBytes(original)
	result := embedding.BytesToFloat32s(bytes)

	assert.Equal(t, original, result, "float32 <-> bytes roundtrip must be lossless")
}

func TestFloat32sToBytes_Empty(t *testing.T) {
	bytes := embedding.Float32sToBytes([]float32{})
	assert.Len(t, bytes, 0)
}

func TestBytesToFloat32s_InvalidLength(t *testing.T) {
	result := embedding.BytesToFloat32s([]byte{0x01, 0x02, 0x03})
	assert.Nil(t, result, "bytes with non-multiple-of-4 length must return nil")
}
