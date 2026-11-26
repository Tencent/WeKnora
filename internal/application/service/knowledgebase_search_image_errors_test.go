package service

import (
	"context"
	"net/http"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

// textOnlyEmbedder is a complete text embedder with no image side; its
// GetModelName is safe to call on the failure path.
type textOnlyEmbedder struct{}

func (textOnlyEmbedder) Embed(context.Context, string) ([]float32, error) {
	return []float32{1}, nil
}

func (textOnlyEmbedder) BatchEmbed(context.Context, []string) ([][]float32, error) {
	return nil, nil
}
func (textOnlyEmbedder) GetModelName() string { return "text-embed-1" }
func (textOnlyEmbedder) GetDimensions() int   { return 1 }
func (textOnlyEmbedder) GetModelID() string   { return "text-embed-1" }
func (textOnlyEmbedder) BatchEmbedWithPool(
	_ context.Context, _ embedding.Embedder, _ []string,
) ([][]float32, error) {
	return nil, nil
}

// An image query against a KB whose embedding model has no image side is the
// caller's mistake to fix (pick another KB, search with text, or enable image
// vectors), so it must carry a 400 the client can act on — not a 500.
func TestGetQueryImageEmbeddingTextOnlyModelIsBadRequest(t *testing.T) {
	svc := &knowledgeBaseService{
		repo: &fakeKBRepo{rows: map[string]*types.KnowledgeBase{
			"kb-text": {ID: "kb-text", TenantID: 1, EmbeddingModelID: "embed-1"},
		}},
		modelService: &imageRecallModels{model: textOnlyEmbedder{}},
	}

	_, err := svc.GetQueryImageEmbedding(ctxWithTenant(1), "kb-text", []byte("png-bytes"))

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Contains(t, appErr.Message, "cannot embed images")
}

// Bytes the embedder cannot take are the caller's image, not a service fault.
func TestGetQueryImageEmbeddingUnembeddableImageIsBadRequest(t *testing.T) {
	svc := &knowledgeBaseService{
		repo: &fakeKBRepo{rows: map[string]*types.KnowledgeBase{
			"kb-img": {ID: "kb-img", TenantID: 1, EmbeddingModelID: "embed-1"},
		}},
		modelService: &imageRecallModels{model: &imageModel{Embedder: textOnlyEmbedder{}, dims: 3}},
	}

	_, err := svc.GetQueryImageEmbedding(ctxWithTenant(1), "kb-img", []byte("not really an image"))

	var appErr *apperrors.AppError
	require.ErrorAs(t, err, &appErr)
	require.Equal(t, http.StatusBadRequest, appErr.HTTPCode)
	require.Contains(t, appErr.Message, "not embeddable")
}
