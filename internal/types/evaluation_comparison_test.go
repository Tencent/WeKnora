package types

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeEvaluationComparisonTaskIDsPreservesFirstSeenOrder(t *testing.T) {
	ids, err := NormalizeEvaluationComparisonTaskIDs([]string{" task-b ", "task-a", "task-b"})
	require.NoError(t, err)
	require.Equal(t, []string{"task-b", "task-a"}, ids)
}

func TestFlattenEvaluationComparisonParametersUsesStableAllowedPointers(t *testing.T) {
	snapshot := JSON(`{
		"dataset":{"dataset_id":"dataset-a"},
		"models":{"chat":{"id":"chat-a"}},
		"configuration":{"retrieval":{"top_k":5},"chunking":{"size":512}},
		"metric_plan":{"metrics":[]},
		"code":{"commit_id":"excluded"}
	}`)
	leaves, err := FlattenEvaluationComparisonParameters(snapshot)
	require.NoError(t, err)
	require.JSONEq(t, `5`, string(leaves["/configuration/retrieval/top_k"]))
	require.JSONEq(t, `"chat-a"`, string(leaves["/models/chat/id"]))
	require.NotContains(t, leaves, "/configuration/chunking/size")
	require.NotContains(t, leaves, "/code/commit_id")
}

func TestFlattenEvaluationNumericMetricsKeepsEveryNumericLeaf(t *testing.T) {
	leaves, err := FlattenEvaluationNumericMetrics(JSON(`{
		"retrieval_metrics":{"precision":0,"recall":0.5},
		"scores":{"custom@1#hash":{"value":0.8,"status":"valid"}}
	}`))
	require.NoError(t, err)
	require.Equal(t, 0.0, leaves["/retrieval_metrics/precision"])
	require.Equal(t, 0.5, leaves["/retrieval_metrics/recall"])
	require.Equal(t, 0.8, leaves["/scores/custom@1#hash/value"])
	require.Len(t, leaves, 3)
}

func TestEvaluationComparisonMetricIdentityForFixedPathUsesFrozenPlan(t *testing.T) {
	plan, err := DefaultEvaluationMetricPlan()
	require.NoError(t, err)
	experiment := &EvaluationExperimentSnapshot{MetricPlan: plan}
	identity, ok := EvaluationComparisonMetricIdentityForPath(experiment, "/retrieval_metrics/ndcg3")
	require.True(t, ok)
	require.Equal(t, "retrieval.ndcg", identity.Key)
	require.Equal(t, "2.0.0", identity.Version)
	require.Equal(t,
		CanonicalEvaluationMetricConfigSHA256(json.RawMessage(`{"k":3}`)),
		identity.ConfigSHA256,
	)
}

func TestNormalizeEvaluationComparisonTaskIDsRequiresTwoDistinctRuns(t *testing.T) {
	_, err := NormalizeEvaluationComparisonTaskIDs([]string{"task-a", "task-a"})
	require.ErrorIs(t, err, ErrEvaluationComparisonInvalid)
}

func TestNormalizeEvaluationComparisonTaskIDsAppliesMaximumAfterDeduplication(t *testing.T) {
	ids, err := NormalizeEvaluationComparisonTaskIDs([]string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "a",
	})
	require.NoError(t, err)
	require.Len(t, ids, EvaluationComparisonMaxRuns)

	_, err = NormalizeEvaluationComparisonTaskIDs([]string{
		"a", "b", "c", "d", "e", "f", "g", "h", "i", "j", "k",
	})
	require.ErrorIs(t, err, ErrEvaluationComparisonInvalid)
}
