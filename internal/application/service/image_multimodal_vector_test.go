package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service/retriever"
	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// vectorChunkRepo keeps created chunks in memory.
type vectorChunkRepo struct {
	interfaces.ChunkRepository
	chunks map[string]*types.Chunk
}

func (r *vectorChunkRepo) CreateChunks(_ context.Context, chunks []*types.Chunk) error {
	for _, c := range chunks {
		r.chunks[c.ID] = c
	}
	return nil
}

func (r *vectorChunkRepo) UpdateChunk(_ context.Context, c *types.Chunk) error {
	r.chunks[c.ID] = c
	return nil
}

type vectorChunkService struct {
	interfaces.ChunkService
	repo *vectorChunkRepo
}

func (s *vectorChunkService) GetRepository() interfaces.ChunkRepository { return s.repo }

func (s *vectorChunkService) GetChunkByIDOnly(_ context.Context, id string) (*types.Chunk, error) {
	c := *s.repo.chunks[id]
	return &c, nil
}

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

func imageVectorFixture(t *testing.T) (
	*ImageMultimodalService, *vectorChunkRepo, *indexRecorder, *retriever.CompositeRetrieveEngine,
) {
	t.Helper()
	repo := &vectorChunkRepo{chunks: map[string]*types.Chunk{}}
	recorder := &indexRecorder{}
	engine, err := retriever.NewCompositeRetrieveEngine(parentChildRetrieveRegistry{engine: recorder},
		[]types.RetrieverEngineParams{{
			RetrieverEngineType: types.PostgresRetrieverEngineType, RetrieverType: types.VectorRetrieverType,
		}})
	require.NoError(t, err)
	return &ImageMultimodalService{chunkService: &vectorChunkService{repo: repo}}, repo, recorder, engine
}

func multimodalChunks() []*types.Chunk {
	const info = `[{"url":"u"}]`
	return []*types.Chunk{
		{ID: "ocr", ChunkType: types.ChunkTypeImageOCR, Content: "SALES 2025", ImageInfo: info},
		{ID: "cap", ChunkType: types.ChunkTypeImageCaption, Content: "a bar chart of sales", ImageInfo: info},
	}
}

func TestIndexImageVectorStoresTheImagesOwnVector(t *testing.T) {
	svc, repo, recorder, engine := imageVectorFixture(t)
	model := &imageModel{dims: 3}
	payload := types.ImageMultimodalPayload{
		ChunkID: "text-parent", KnowledgeID: "k", KnowledgeBaseID: "kb", TenantID: 1, ImageURL: "u",
	}
	status := svc.indexImageVector(context.Background(), payload, testPNG(t), multimodalChunks(), model, engine)
	require.Equal(t, "indexed", status)

	require.Len(t, model.images, 1)
	assert.Equal(t, "image/png", model.images[0].MIMEType)

	require.Len(t, repo.chunks, 1)
	var chunk *types.Chunk
	for _, c := range repo.chunks {
		chunk = c
	}
	assert.Equal(t, types.ChunkTypeImageVector, chunk.ChunkType)
	assert.Equal(t, "a bar chart of sales", chunk.Content, "the caption is preferred over OCR")
	assert.Equal(t, "text-parent", chunk.ParentChunkID)
	assert.True(t, chunk.IsEnabled)
	assert.Equal(t, int(types.ChunkStatusIndexed), chunk.Status)

	require.Len(t, recorder.rows, 1)
	row := recorder.rows[0]
	assert.Equal(t, types.ImageSourceType, row.SourceType)
	assert.Equal(t, chunk.ID, row.ChunkID)
	assert.Equal(t, chunk.ID, row.SourceID)
	assert.True(t, row.IsEnabled)
	assert.Equal(t, [][]float32{{0.5, 0.5, 0.5}}, recorder.vectors, "the image's vector, not the caption's")
}

func TestIndexImageVectorFallsBackToOCRText(t *testing.T) {
	svc, repo, _, engine := imageVectorFixture(t)
	status := svc.indexImageVector(context.Background(), types.ImageMultimodalPayload{}, testPNG(t),
		multimodalChunks()[:1], &imageModel{dims: 3}, engine)
	require.Equal(t, "indexed", status)
	for _, c := range repo.chunks {
		assert.Equal(t, "SALES 2025", c.Content)
	}
}

func TestIndexImageVectorSkips(t *testing.T) {
	cases := []struct {
		name    string
		model   embedding.Embedder
		payload types.ImageMultimodalPayload
		img     []byte
		want    string
	}{
		{"text-only model", &textModel{}, types.ImageMultimodalPayload{}, nil, ""},
		{
			"scanned page", &imageModel{dims: 3},
			types.ImageMultimodalPayload{ImageSourceType: "scanned_pdf"},
			nil,
			"skipped: scanned page",
		},
		{
			"not an image", &imageModel{dims: 3},
			types.ImageMultimodalPayload{},
			[]byte("%PDF-1.7"),
			"failed: not an image (application/pdf)",
		},
		{
			"vector width differs from the index", &imageModel{dims: 1024},
			types.ImageMultimodalPayload{},
			nil,
			"failed: 3 dimensions, index has 1024",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			svc, repo, recorder, engine := imageVectorFixture(t)
			img := tc.img
			if img == nil {
				img = testPNG(t)
			}
			assert.Equal(t, tc.want, svc.indexImageVector(context.Background(), tc.payload, img,
				multimodalChunks(), tc.model, engine))
			assert.Empty(t, repo.chunks)
			assert.Empty(t, recorder.rows)
		})
	}
}
