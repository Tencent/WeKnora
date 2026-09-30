package service

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
)

func hit(id string, score float64, source types.SourceType) *types.IndexWithScore {
	return &types.IndexWithScore{ChunkID: id, SourceID: id, Score: score, SourceType: source}
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
		TopK: 100, ImageRecall: true, VectorThreshold: 0.3,
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
	g = &storeGroup{TopK: maxRetrievalPoolSize, ImageRecall: true, BaseParams: []types.RetrieveParams{
		{RetrieverType: types.VectorRetrieverType, Threshold: 0.05},
	}}
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, maxRetrievalPoolSize, got[0].TopK)

	g.ImageRecall, g.TopK = false, 100
	got = paramsWithTopK(g)
	assert.Equal(t, 0.05, got[0].Threshold)
	assert.Equal(t, 100, got[0].TopK, "without images the group TopK is used as is")
}

func TestFilterImageHitsHoldsEachKindToItsOwnThreshold(t *testing.T) {
	g := &storeGroup{ImageRecall: true, VectorThreshold: 0.3}
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
	// vector hit already passed it; keyword hits on image rows still go.
	g := &storeGroup{VectorThreshold: 0.3}
	vector := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("a", 0.31, types.ChunkSourceType), hit("b", 0.31, types.ImageSourceType),
	}}
	keyword := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType, Results: []*types.IndexWithScore{
		hit("c", 0.9, types.ImageSourceType),
	}}
	filterImageHits([]*types.RetrieveResult{vector, nil, keyword}, g)
	assert.Equal(t, []string{"a", "b"}, ids(vector.Results))
	assert.Empty(t, keyword.Results)
}
