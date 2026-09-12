package v7

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	typesLocal "github.com/Tencent/WeKnora/internal/types"
)

// Regression test for #3156: the script_score source must floor the cosine
// similarity at 0. An unclamped negative cosine makes Lucene reject the whole
// search request ("script_score script returned an invalid score ... Must be a
// non-negative score!"), turning one dissimilar document into a full vector
// retrieval outage (all shards failed).
func TestVectorScoreScriptSourceClampsNegativeCosine(t *testing.T) {
	require.Equal(t,
		"Math.max(cosineSimilarity(params.query_vector,'embedding'), 0.0)",
		vectorScoreScriptSource)
}

// The marshaled query body must carry the clamped script source end to end.
func TestBuildVectorSearchQueryClampsScriptScore(t *testing.T) {
	e := &elasticsearchRepository{}
	params := typesLocal.RetrieveParams{
		Embedding:        []float32{-1, 0},
		Threshold:        0.15,
		TopK:             5,
		KnowledgeBaseIDs: []string{"kb-1"},
	}

	query, err := e.buildVectorSearchQuery(context.Background(), params)
	require.NoError(t, err)

	var body struct {
		Query struct {
			ScriptScore struct {
				Script struct {
					Source string `json:"source"`
				} `json:"script"`
				MinScore float64 `json:"min_score"`
			} `json:"script_score"`
		} `json:"query"`
	}
	require.NoError(t, json.Unmarshal([]byte(query), &body))
	require.Equal(t,
		"Math.max(cosineSimilarity(params.query_vector,'embedding'), 0.0)",
		body.Query.ScriptScore.Script.Source)
	require.InDelta(t, 0.15, body.Query.ScriptScore.MinScore, 1e-6)
}
