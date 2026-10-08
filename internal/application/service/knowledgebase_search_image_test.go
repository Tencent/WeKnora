package service

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/embedding"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// hit is a hit in knowledge base "kb".
func hit(id string, score float64, source types.SourceType) *types.IndexWithScore {
	return kbHit("kb", id, score, source)
}

func kbHit(kbID, id string, score float64, source types.SourceType) *types.IndexWithScore {
	return &types.IndexWithScore{
		ChunkID: id, SourceID: id, Score: score, SourceType: source, KnowledgeBaseID: kbID,
	}
}

// recalls is the ImageKBIDs of a group whose KBs recall images.
func recalls(ids ...string) map[string]struct{} {
	out := make(map[string]struct{}, len(ids))
	for _, id := range ids {
		out[id] = struct{}{}
	}
	return out
}

func ids(hits []*types.IndexWithScore) []string {
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.ChunkID
	}
	return out
}

func TestParamsWithTopKWidensOnlyTheDocumentVectorSearchForImages(t *testing.T) {
	g := &storeGroup{
		TopK: 100, ImageKBIDs: recalls("kb"), VectorThreshold: 0.3,
		BaseParams: []types.RetrieveParams{
			{RetrieverType: types.VectorRetrieverType, Threshold: 0.3},
			{RetrieverType: types.VectorRetrieverType, Threshold: 0.3, KnowledgeType: types.KnowledgeTypeFAQ},
			{RetrieverType: types.KeywordsRetrieverType, Threshold: 0.5},
		},
	}
	got := paramsWithTopK(g)
	assert.Equal(t, 150, got[0].TopK)
	assert.Equal(t, imageVectorThreshold, got[0].Threshold)
	assert.Equal(t, 100, got[1].TopK, "FAQ indexes hold no images")
	assert.Equal(t, 0.3, got[1].Threshold)
	assert.Equal(t, 100, got[2].TopK, "keyword search is left alone")
	assert.Equal(t, 0.5, got[2].Threshold)
	assert.Equal(t, 0.3, g.BaseParams[0].Threshold, "base params stay immutable")

	// A text threshold already under the image one is not raised, and the
	// pool never grows past the global cap.
	g = &storeGroup{TopK: maxRetrievalPoolSize, ImageKBIDs: recalls("kb"), BaseParams: []types.RetrieveParams{
		{RetrieverType: types.VectorRetrieverType, Threshold: 0.05},
	}}
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, maxRetrievalPoolSize, got[0].TopK)

	g.ImageKBIDs, g.TopK = nil, 100
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, 100, got[0].TopK, "without images the group TopK is used as is")
}

func TestFilterImageHitsHoldsEachKindToItsOwnThreshold(t *testing.T) {
	g := &storeGroup{ImageKBIDs: recalls("kb"), VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("text-strong", 0.6, types.ChunkSourceType),
		hit("image-strong", 0.25, types.ImageSourceType),
		hit("text-weak", 0.2, types.ChunkSourceType), // only reached because the query threshold was lowered
		hit("image-weak", 0.05, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("text-kw", 0.9, types.ChunkSourceType),
		hit("image-kw", 0.9, types.ImageSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, keyword}, g)
	assert.Equal(t, []string{"text-strong", "image-strong"}, ids(vector.Results))
	assert.Equal(t, []string{"text-kw"}, ids(keyword.Results),
		"an image row's Content is the caption, already indexed as text")
}

func TestFilterImageHitsLeavesTextOnlySearchesAlone(t *testing.T) {
	// Without image recall the query ran at the text threshold, so every
	// text hit already passed it. Image rows a KB indexed while it was
	// opted in are dropped once it opts out, however well they score.
	g := &storeGroup{VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("a", 0.31, types.ChunkSourceType), hit("b", 0.9, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("c", 0.9, types.ImageSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, nil, keyword}, g)
	assert.Equal(t, []string{"a"}, ids(vector.Results))
	assert.Empty(t, keyword.Results)
}

func TestFilterImageHitsRecallsImagesOnlyOfOptedInKBs(t *testing.T) {
	// One store group can hold KBs on both sides of the switch.
	g := &storeGroup{ImageKBIDs: recalls("kb-on"), VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		kbHit("kb-on", "image-on", 0.25, types.ImageSourceType),
		kbHit("kb-off", "image-off", 0.9, types.ImageSourceType),
		kbHit("kb-off", "text-off", 0.6, types.ChunkSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector}, g)
	assert.Equal(t, []string{"image-on", "text-off"}, ids(vector.Results))
}

// imageRecallModels hands out one embedding model and records each lookup.
type imageRecallModels struct {
	interfaces.ModelService
	model     embedding.Embedder
	requested []string
}

func (m *imageRecallModels) GetEmbeddingModel(_ context.Context, id string) (embedding.Embedder, error) {
	m.requested = append(m.requested, id)
	return m.model, nil
}

func TestApplyImageRecallNeedsTheKBSwitchAndAnImageModel(t *testing.T) {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(1))
	vectorKB := func(id string, imageVectors bool) *types.KnowledgeBase {
		return &types.KnowledgeBase{
			ID: id, TenantID: 1, EmbeddingModelID: "embed-1",
			IndexingStrategy:      types.IndexingStrategy{VectorEnabled: true},
			ImageProcessingConfig: types.ImageProcessingConfig{ImageVectorEnabled: imageVectors},
		}
	}
	params := types.SearchParams{VectorThreshold: 0.3}

	cases := []struct {
		name          string
		kbs           []*types.KnowledgeBase
		model         embedding.Embedder
		params        types.SearchParams
		want          map[string]struct{}
		modelResolved bool
	}{
		{
			// Every KB from before the switch: the image-capable model is
			// not even looked up.
			name:  "switch off, image model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", false)},
			model: &imageModel{dims: 3}, params: params,
		},
		{
			name:  "switch on, text-only model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true)},
			model: &textModel{}, params: params, modelResolved: true,
		},
		{
			name:  "switch on, vector match disabled",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true)},
			model: &imageModel{dims: 3}, params: types.SearchParams{DisableVectorMatch: true},
		},
		{
			name:  "switch on, image model",
			kbs:   []*types.KnowledgeBase{vectorKB("kb-a", true), vectorKB("kb-b", false)},
			model: &imageModel{dims: 3}, params: params,
			want: recalls("kb-a"), modelResolved: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			models := &imageRecallModels{model: tc.model}
			s := &knowledgeBaseService{modelService: models}
			g := &storeGroup{KBIDs: []string{"kb-a", "kb-b"}, TopK: 100, BaseParams: []types.RetrieveParams{
				{RetrieverType: types.VectorRetrieverType, Threshold: 0.3},
			}}
			s.applyImageRecall(ctx, tc.kbs, []*storeGroup{g}, tc.params)

			assert.Equal(t, tc.modelResolved, len(models.requested) > 0)
			if tc.want == nil {
				assert.False(t, g.imageRecall())
				got := paramsWithTopK(g)
				require.Len(t, got, 1)
				assert.Equal(t, 100, got[0].TopK, "the pool is not widened")
				assert.Equal(t, 0.3, got[0].Threshold, "the threshold is not lowered")
				return
			}
			assert.Equal(t, tc.want, g.ImageKBIDs)
			assert.Equal(t, 0.3, g.VectorThreshold)
			got := paramsWithTopK(g)
			assert.Equal(t, 150, got[0].TopK)
			assert.Equal(t, imageVectorThreshold, got[0].Threshold)
		})
	}
}
