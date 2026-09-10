package v8

import (
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
		"Math.max(cosineSimilarity(params.query_vector, 'embedding'), 0.0)",
		vectorScoreScriptSource)
}

func TestBuildVectorScriptScoreQuery(t *testing.T) {
	e := &elasticsearchRepository{}
	params := typesLocal.RetrieveParams{
		Embedding:        []float32{1, 0},
		Threshold:        0.15,
		TopK:             10,
		KnowledgeBaseIDs: []string{"kb-1"},
	}

	q, err := e.buildVectorScriptScoreQuery(params)
	require.NoError(t, err)
	require.NotNil(t, q)

	require.NotNil(t, q.Script.Source)
	require.Equal(t, vectorScoreScriptSource, *q.Script.Source)

	require.NotNil(t, q.MinScore)
	require.InDelta(t, 0.15, float64(*q.MinScore), 1e-6)

	require.NotNil(t, q.Query.Bool)
	require.Len(t, q.Query.Bool.Filter, 1)

	var queryVector []float32
	require.NotNil(t, q.Script.Params["query_vector"])
	require.NoError(t, json.Unmarshal(q.Script.Params["query_vector"], &queryVector))
	require.Equal(t, []float32{1, 0}, queryVector)
}
