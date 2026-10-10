package service

import (
	"encoding/json"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestImagePoolDiagnosticsUseActualFilterRulesWithoutMutatingHits(t *testing.T) {
	g := &storeGroup{ImageKBIDs: recalls("kb"), VectorThreshold: 0.5}
	rr := &types.RetrieveResult{RetrieverType: types.VectorRetrieverType, Results: []*types.IndexWithScore{
		hit("text-low", 0.4, types.ChunkSourceType),
		hit("image-low", 0.199, types.ImageSourceType),
		hit("image-boundary", 0.2, types.ImageSourceType),
		kbHit("disabled", "image-disabled", 0.9, types.ImageSourceType),
	}}
	out := summarizeImagePool(rr, g)
	require.Equal(t, 3, out["returned_image_count"])
	require.Equal(t, 1, out["image_count_passing_filter"])
	require.Equal(t, map[string]int{
		"below_text_threshold": 1, "below_image_threshold": 1, "kept": 1, "image_recall_disabled": 1,
	}, out["filter_decision_counts"])
	require.Len(t, rr.Results, 4)
	require.Zero(t, rr.Results[2].VectorScore, "observability must not alter raw scores")
	filterImageHits([]*types.RetrieveResult{rr}, g)
	require.Equal(t, []string{"image-boundary"}, ids(rr.Results))
	require.Equal(t, 0.2, rr.Results[0].VectorScore)
}

func TestImagePoolDiagnosticsCoverKeywordExclusionAndBoundSamples(t *testing.T) {
	g := &storeGroup{ImageKBIDs: recalls("kb")}
	rr := &types.RetrieveResult{RetrieverType: types.KeywordsRetrieverType}
	for i := 0; i < 13; i++ {
		h := hit(fmt.Sprint(i), 10, types.ImageSourceType)
		h.Content = "private image description"
		rr.Results = append(rr.Results, h)
	}
	out := summarizeImagePool(rr, g)
	require.Equal(t, 13, out["returned_image_count"])
	require.Equal(t, 0, out["image_count_passing_filter"])
	require.Equal(t, 3, out["image_samples_truncated"])
	require.Equal(t, map[string]int{"duplicate_image_keyword": 13}, out["filter_decision_counts"])
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "private image description")
}

func TestImageCandidateCutDiagnosticsDistinguishFusionCutFromVectorThreshold(t *testing.T) {
	image := hit("image", 0.3, types.ImageSourceType)
	image.VectorScore = 0.2688
	hits := []*types.IndexWithScore{hit("text", 0.9, types.ChunkSourceType), image}
	out := summarizeImageCandidateCut(hits, 1)
	require.Equal(t, 1, out["input_image_count"])
	require.Equal(t, 0, out["output_image_count"])
	rows := out["image_samples"].([]map[string]interface{})
	require.Equal(t, "match_count_cut", rows[0]["decision"])
	require.Equal(t, "0.2688", rows[0]["vector_score"])
	require.Equal(t, "0.3000", rows[0]["fusion_score"])
	require.Equal(t, 2, rows[0]["rank"])
	require.Len(t, hits, 2)
	require.Equal(t, 1, summarizeImageCandidateCut(hits, 2)["output_image_count"])
}
