package service

import (
	"context"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type dimensionTestEmbedder struct {
	embedding.Embedder
	name       string
	dimensions int
}

func (e dimensionTestEmbedder) GetModelName() string { return e.name }
func (e dimensionTestEmbedder) GetDimensions() int   { return e.dimensions }
func (e dimensionTestEmbedder) Embed(context.Context, string) ([]float32, error) {
	return make([]float32, 768), nil
}

func TestValidateQueryEmbeddingDimension(t *testing.T) {
	err := validateQueryEmbeddingDimension(dimensionTestEmbedder{
		name:       "configured-embedding",
		dimensions: 1536,
	}, 768)
	require.Error(t, err)
	appErr, ok := err.(*apperrors.AppError)
	require.True(t, ok)
	assert.Equal(t, apperrors.ErrVectorStoreUnavailable, appErr.Code)
	assert.Equal(t, "configured-embedding", appErr.Details.(map[string]any)["model"])
	assert.Equal(t, 1536, appErr.Details.(map[string]any)["expected_dimension"])
	assert.Equal(t, 768, appErr.Details.(map[string]any)["actual_dimension"])
}

func TestValidateQueryEmbeddingDimensionAllowsUnknownOrMatchingSize(t *testing.T) {
	assert.NoError(t, validateQueryEmbeddingDimension(dimensionTestEmbedder{dimensions: 768}, 768))
	assert.NoError(t, validateQueryEmbeddingDimension(dimensionTestEmbedder{dimensions: 0}, 768))
	assert.NoError(t, validateQueryEmbeddingDimension(dimensionTestEmbedder{dimensions: 1536}, 0))
}
