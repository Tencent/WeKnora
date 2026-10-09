package service

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type galleryQueryModel struct {
	*imageModel
	query bool
}

func (m *galleryQueryModel) Embed(ctx context.Context, _ string) ([]float32, error) {
	m.query = types.IsEmbedQuery(ctx)
	return []float32{0.5, 0.5, 0.5}, nil
}

type galleryQueryEngine struct {
	indexRecorder
	scores map[string]float64
	seen   []string
}

func (e *galleryQueryEngine) Retrieve(_ context.Context, p types.RetrieveParams) ([]*types.RetrieveResult, error) {
	if len(p.ChunkIDs) == 0 {
		return nil, fmt.Errorf("gallery query must be restricted before ranking")
	}
	e.seen = append(e.seen, p.ChunkIDs...)
	hits := []*types.IndexWithScore{{KnowledgeBaseID: "kb", ChunkID: "unrequested", Score: 999}}
	for _, id := range p.ChunkIDs {
		hits = append(hits, &types.IndexWithScore{
			KnowledgeBaseID: "kb", KnowledgeID: "doc", ChunkID: id, Score: e.scores[id],
		})
	}
	return []*types.RetrieveResult{{Results: hits}}, nil
}

func TestGallerySemanticSearchFiltersBeforeRecallDeduplicatesAndPages(t *testing.T) {
	images, db, _, _, imageModel, _ := setupImageJobTest(t)
	ctx := types.WithExecutionTenant(t.Context(), 1)
	model := &galleryQueryModel{imageModel: imageModel}
	images.models.(*imageJobModels).model = model
	engine := &galleryQueryEngine{scores: map[string]float64{}}
	images.engines = parentChildRetrieveRegistry{engine: engine}
	var chunks []*types.Chunk
	for i := 0; i < 207; i++ {
		id := fmt.Sprintf("caption-%03d", i)
		url := fmt.Sprintf("resource://picture-%03d", i)
		// The best image is oldest and falls outside the first listing page.
		engine.scores[id] = 1 - float64(i)/1000
		if i >= 205 {
			engine.scores[id] = 100
		}
		info, err := json.Marshal([]map[string]any{{
			"url": url, "original_url": url, "caption": "photo without the query words",
			"attrs": map[string]any{"schema": "attrs/2", "attrs": map[string]any{"contain.text": i != 205}},
		}})
		require.NoError(t, err)
		chunks = append(chunks, &types.Chunk{
			ID: id, TenantID: 1, KnowledgeID: "doc", KnowledgeBaseID: "kb",
			ChunkType: types.ChunkTypeImageCaption, ImageInfo: string(info),
			Status: int(types.ChunkStatusIndexed), IsEnabled: i != 206,
			CreatedAt: time.Unix(int64(i), 0), UpdatedAt: time.Unix(int64(i), 0),
		})
	}
	// OCR is a second retrieval path for the same gallery image.
	ocr := *chunks[0]
	ocr.ID, ocr.ChunkType = "ocr-copy", types.ChunkTypeImageOCR
	chunks = append(chunks, &ocr)
	engine.scores[ocr.ID] = 0.99
	// An old completed image vector without a current job must not be served.
	stale := *chunks[1]
	stale.ID, stale.ChunkType = "stale-vector", types.ChunkTypeImageVector
	chunks = append(chunks, &stale)
	engine.scores[stale.ID] = 200
	require.NoError(t, images.chunks.CreateChunks(ctx, chunks))
	require.NoError(t, db.Model(&types.Chunk{}).Where("id = ?", "caption-206").Update("is_enabled", false).Error)
	svc := NewGallerySearchService(images, nil)
	filter := &types.ImageListFilter{
		Keyword: "汤圆", AttrRules: map[string]map[string]string{"system:contain.text": {"false": "off"}},
	}
	result, truncated, err := svc.Search(ctx, "kb", &types.Pagination{Page: 1, PageSize: 3}, filter)
	require.NoError(t, err)
	require.True(t, model.query)
	require.True(t, truncated)
	require.EqualValues(t, 200, result.Total)
	items := result.Data.([]types.ImageAsset)
	require.Len(t, items, 3)
	require.Equal(t, "resource://picture-000", items[0].URL)
	require.Equal(t, "resource://picture-001", items[1].URL)
	require.Equal(t, "resource://picture-002", items[2].URL)
	require.NotContains(t, engine.seen, "caption-205", "filtered assets must not enter retrieval")
	require.NotContains(t, engine.seen, "caption-206", "disabled assets must not enter retrieval")
	require.NotContains(t, engine.seen, "stale-vector")
	result, _, err = svc.Search(ctx, "kb", &types.Pagination{Page: 2, PageSize: 3}, filter)
	require.NoError(t, err)
	items = result.Data.([]types.ImageAsset)
	require.Equal(t, "resource://picture-003", items[0].URL)
	_, _, err = svc.Search(types.WithExecutionTenant(t.Context(), 2), "kb", &types.Pagination{}, filter)
	require.Error(t, err)
}

func TestGalleryFindsUncaptionedImageOnlyAfterIndexCompletes(t *testing.T) {
	images, _, _, queue, imageModel, _ := setupImageJobTest(t)
	ctx := types.WithExecutionTenant(t.Context(), 1)
	images.models.(*imageJobModels).model = &galleryQueryModel{imageModel: imageModel}
	engine := &galleryQueryEngine{scores: map[string]float64{}}
	images.engines = parentChildRetrieveRegistry{engine: engine}
	svc := NewGallerySearchService(images, nil)
	filter := &types.ImageListFilter{Keyword: "red bowl"}
	page := &types.Pagination{}
	result, _, err := svc.Search(ctx, "kb", page, filter)
	require.NoError(t, err)
	require.Zero(t, result.Total)
	require.NoError(t, images.Schedule(ctx, "kb", "", ""))
	require.NoError(t, images.Handle(ctx, queue.tasks[0]))
	require.NoError(t, images.Handle(ctx, queue.tasks[1]))
	result, _, err = svc.Search(ctx, "kb", page, filter)
	require.NoError(t, err)
	require.EqualValues(t, 1, result.Total)
	items := result.Data.([]types.ImageAsset)
	require.Equal(t, "resource://original", items[0].URL)
	require.Empty(t, items[0].Caption)
	images.models.(*imageJobModels).row.Name = "changed-space"
	result, _, err = svc.Search(ctx, "kb", page, filter)
	require.NoError(t, err)
	require.Zero(t, result.Total, "vectors of an old model configuration must not be recalled")
}
