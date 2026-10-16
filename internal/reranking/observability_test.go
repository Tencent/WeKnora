package reranking

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestSelectionDiagnosticsExplainRejectedAndProtectedImages(t *testing.T) {
	input := []*types.SearchResult{
		imageHit("weak", "private image caption", 0.21, 0),
		imageHit("protected", "another caption", 0.4, 0),
	}
	opts := Options{Threshold: 0.3, FallbackMinScore: 0.15, ImageKeepScore: 0.25}
	res := Rerank(context.Background(), &textScorer{}, "q", input, opts)
	out := summarizeSelection(input, res, opts, nil)
	require.Equal(t, map[string]int{"rerank_rejected": 1, "image_vector_fallback": 1}, out["image_decision_counts"])
	require.Equal(t, map[string]int{"image_vector": 2}, out["input_chunk_types"])
	require.Equal(t, map[string]int{"image_vector": 1}, out["output_chunk_types"])
	rows := out["image_samples"].([]map[string]interface{})
	require.Equal(t, "0.2100", rows[0]["vector_score"])
	require.Equal(t, "0.0000", rows[0]["model_score"])
	require.Equal(t, "text", rows[0]["scored_as"])
	b, err := json.Marshal(out)
	require.NoError(t, err)
	require.NotContains(t, string(b), "private image caption")
}

func TestSelectionDiagnosticsExplainModelErrorAndAllowNonfiniteThresholds(t *testing.T) {
	input := []*types.SearchResult{imageHit("image", "caption", 0.3, 0)}
	opts := Options{Threshold: math.Inf(-1), FallbackMinScore: math.Inf(-1)}
	res := Rerank(context.Background(), &textScorer{err: errors.New("offline")}, "q", input, opts)
	out := summarizeSelection(input, res, opts, nil)
	require.Equal(t, map[string]int{"model_error_fallback": 1}, out["image_decision_counts"])
	require.Equal(t, "-Inf", out["threshold"])
	_, err := json.Marshal(out)
	require.NoError(t, err)
}

func TestSelectionDiagnosticsDistinguishTopKFromModelRejection(t *testing.T) {
	input := []*types.SearchResult{
		imageHit("first", "first", 0.4, 0), imageHit("second", "second", 0.3, 0),
	}
	opts := Options{Threshold: 0.3, TopK: 1}
	model := &textScorer{scores: map[string]float64{"first": 0.9, "second": 0.8}}
	res := Rerank(context.Background(), model, "q", input, opts)
	out := summarizeSelection(input, res, opts, map[int]bool{0: true})
	require.Equal(t, map[string]int{"kept": 1, "top_k_mmr_cut": 1}, out["image_decision_counts"])
	require.Equal(t, "image", out["image_samples"].([]map[string]interface{})[0]["scored_as"])
}
