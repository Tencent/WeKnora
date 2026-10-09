package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

// imageModel is an embedding model with an image side.
type imageModel struct {
	embedding.Embedder
	dims   int
	images []embedding.Image
}

func (m *imageModel) GetDimensions() int                 { return m.dims }
func (m *imageModel) AcceptsImages() bool                { return true }
func (m *imageModel) ImageLimits() embedding.ImageLimits { return embedding.ImageLimits{} }

func (m *imageModel) BatchEmbedImages(_ context.Context, images []embedding.Image) ([][]float32, error) {
	m.images = append(m.images, images...)
	return [][]float32{{0.5, 0.5, 0.5}}, nil
}

// textModel has no image side.
type textModel struct{ embedding.Embedder }

// indexRecorder records what reaches the vector store.
type indexRecorder struct {
	interfaces.RetrieveEngineService
	rows    []*types.IndexInfo
	vectors [][]float32
}

func (r *indexRecorder) EngineType() types.RetrieverEngineType {
	return types.PostgresRetrieverEngineType
}

func (r *indexRecorder) Support() []types.RetrieverType {
	return []types.RetrieverType{types.VectorRetrieverType}
}

func (r *indexRecorder) BatchIndex(ctx context.Context, e embedding.Embedder,
	rows []*types.IndexInfo, _ []types.RetrieverType,
) error {
	vectors, err := e.BatchEmbedWithPool(ctx, e, make([]string, len(rows)))
	if err != nil {
		return err
	}
	r.rows, r.vectors = append(r.rows, rows...), append(r.vectors, vectors...)
	return nil
}

func testPNG(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 4, 4))))
	return buf.Bytes()
}

// imageVectorKB has opted in to image vectors.
func imageVectorKB() *types.KnowledgeBase {
	return &types.KnowledgeBase{
		ID:                    "kb",
		IndexingStrategy:      types.IndexingStrategy{VectorEnabled: true},
		ImageProcessingConfig: types.ImageProcessingConfig{ImageVectorEnabled: true},
	}
}
