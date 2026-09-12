package service

import (
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestEvaluationConfigurationOverridesPreserveUnspecifiedFields(t *testing.T) {
	defaults := types.EvaluationRetrievalSnapshot{VectorThreshold: 0.7, KeywordThreshold: 0.5, EmbeddingTopK: 10}
	defaultRerank := types.EvaluationRerankSnapshot{RerankTopK: 5, RerankThreshold: 0.6}
	for _, test := range []struct {
		name      string
		json      string
		retrieval types.EvaluationRetrievalSnapshot
		rerank    types.EvaluationRerankSnapshot
	}{
		{"omitted", `null`, defaults, defaultRerank},
		{"empty config", `{}`, defaults, defaultRerank},
		{"empty groups", `{"retrieval":{},"rerank":{}}`, defaults, defaultRerank},
		{
			"null fields", `{"retrieval":{"embedding_top_k":null},"rerank":{"rerank_threshold":null}}`,
			defaults, defaultRerank,
		},
		{
			"retrieval topk", `{"retrieval":{"embedding_top_k":23}}`,
			types.EvaluationRetrievalSnapshot{VectorThreshold: 0.7, KeywordThreshold: 0.5, EmbeddingTopK: 23},
			defaultRerank,
		},
		{
			"vector threshold", `{"retrieval":{"vector_threshold":0}}`,
			types.EvaluationRetrievalSnapshot{VectorThreshold: 0, KeywordThreshold: 0.5, EmbeddingTopK: 10},
			defaultRerank,
		},
		{
			"keyword threshold", `{"retrieval":{"keyword_threshold":0}}`,
			types.EvaluationRetrievalSnapshot{VectorThreshold: 0.7, KeywordThreshold: 0, EmbeddingTopK: 10},
			defaultRerank,
		},
		{
			"rerank topk", `{"rerank":{"rerank_top_k":3}}`, defaults,
			types.EvaluationRerankSnapshot{RerankTopK: 3, RerankThreshold: 0.6},
		},
		{
			"rerank threshold", `{"rerank":{"rerank_threshold":0}}`, defaults,
			types.EvaluationRerankSnapshot{RerankTopK: 5, RerankThreshold: 0},
		},
		{
			"explicit zero", `{"retrieval":{"vector_threshold":0,"keyword_threshold":0,"embedding_top_k":0},` +
				`"rerank":{"rerank_top_k":0,"rerank_threshold":0}}`,
			types.EvaluationRetrievalSnapshot{},
			types.EvaluationRerankSnapshot{},
		},
		{
			"full override", `{"retrieval":{"vector_threshold":0.2,"keyword_threshold":0.3,"embedding_top_k":20},` +
				`"rerank":{"rerank_top_k":4,"rerank_threshold":0.8}}`,
			types.EvaluationRetrievalSnapshot{VectorThreshold: 0.2, KeywordThreshold: 0.3, EmbeddingTopK: 20},
			types.EvaluationRerankSnapshot{RerankTopK: 4, RerankThreshold: 0.8},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			input := evaluationExperimentInputFixture()
			var overrides *types.EvaluationConfigurationOverrides
			require.NoError(t, json.Unmarshal([]byte(test.json), &overrides))
			require.NoError(t, applyEvaluationConfigurationOverrides(input.Params, overrides))
			snapshot, _, err := BuildEvaluationExperimentSnapshot(input)
			require.NoError(t, err)
			assert.Equal(t, test.retrieval, snapshot.Configuration.Retrieval)
			assert.Equal(t, test.rerank, snapshot.Configuration.Rerank)
			assert.Equal(t, 0.7, snapshot.Configuration.Generation.Temperature)
			assert.Equal(t, 0.9, snapshot.Configuration.Generation.TopP)
			assert.Equal(t, 2048, snapshot.Configuration.Generation.MaxTokens)

			// Persisted snapshots contain resolved numbers, including zero,
			// and retain the schema-version-1 JSON shape.
			frozen, err := json.Marshal(snapshot.Configuration)
			require.NoError(t, err)
			var wire struct {
				Retrieval map[string]json.RawMessage `json:"retrieval"`
				Rerank    map[string]json.RawMessage `json:"rerank"`
			}
			require.NoError(t, json.Unmarshal(frozen, &wire))
			assert.Len(t, wire.Retrieval, 3)
			assert.Len(t, wire.Rerank, 2)
			assert.Equal(t, 1, snapshot.SchemaVersion)
		})
	}
}

func TestEvaluationConfigurationPartialAndResolvedRequestsFreezeIdentically(t *testing.T) {
	partial := evaluationExperimentInputFixture()
	full := evaluationExperimentInputFixture()
	topK := 23
	require.NoError(t, applyEvaluationConfigurationOverrides(partial.Params, &types.EvaluationConfigurationOverrides{
		Retrieval: &types.EvaluationRetrievalOverrides{EmbeddingTopK: &topK},
	}))
	var complete types.EvaluationConfigurationOverrides
	require.NoError(t, json.Unmarshal([]byte(`{
		"retrieval":{"vector_threshold":0.7,"keyword_threshold":0.5,"embedding_top_k":23},
		"rerank":{"rerank_top_k":5,"rerank_threshold":0.6}
	}`), &complete))
	require.NoError(t, applyEvaluationConfigurationOverrides(full.Params, &complete))
	partialSnapshot, partialHash, err := BuildEvaluationExperimentSnapshot(partial)
	require.NoError(t, err)
	fullSnapshot, fullHash, err := BuildEvaluationExperimentSnapshot(full)
	require.NoError(t, err)
	assert.Equal(t, fullSnapshot.Configuration, partialSnapshot.Configuration)
	assert.Equal(t, fullHash, partialHash)
}

func TestEvaluationOutputOverrideReplacesInheritedCompletionBudget(t *testing.T) {
	input := evaluationExperimentInputFixture()
	input.Params.SummaryConfig.MaxCompletionTokens = 2048
	requested := 256
	require.NoError(t, applyEvaluationConfigurationOverrides(input.Params, &types.EvaluationConfigurationOverrides{
		Generation: &types.EvaluationGenerationOverrides{MaxTokens: &requested},
	}))
	snapshot, _, err := BuildEvaluationExperimentSnapshot(input)
	require.NoError(t, err)
	assert.Equal(t, requested, snapshot.Configuration.Generation.MaxTokens)
	assert.Equal(t, requested, snapshot.Configuration.Generation.MaxCompletionTokens)
}
