package metricregistry

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type semanticJudgeStub struct{ request SemanticJudgeRequest }

func (s *semanticJudgeStub) Judge(_ context.Context, request SemanticJudgeRequest) (float64, error) {
	s.request = request
	return 0.75, nil
}

func TestSemanticJudgeIsOptInAndComputesThroughRegistry(t *testing.T) {
	defaults, err := NewDefaultRegistry()
	require.NoError(t, err)
	for _, definition := range defaults.Definitions() {
		require.NotEqual(t, "generation.semantic_judge", definition.Key)
	}

	judge := &semanticJudgeStub{}
	plugin, err := NewSemanticJudgeMetric(judge)
	require.NoError(t, err)
	registry, err := New(plugin)
	require.NoError(t, err)
	plan, err := registry.Resolve([]Spec{{
		Key: "generation.semantic_judge", Version: "1.0.0", Required: true,
		Config: json.RawMessage(
			`{"rubric":"Check meaning","prompt_version":"2.1.0",` +
				`"prompt_sha256":"2cc96f8253c179826836047ef9e0757b8bd2e585b1f414776e8c01784541beee",` +
				`"judge_model_snapshot":{"id":"judge-1","fingerprint":"frozen"},` +
				`"temperature":0,"seed":17,"retry_limit":2}`,
		),
	}})
	require.NoError(t, err)
	result, observations, err := plan.Compute(context.Background(), &types.MetricInput{
		GeneratedTexts: "answer", GeneratedGT: "reference",
	})
	require.NoError(t, err)
	require.Len(t, observations, 1)
	require.Equal(t, 0.75, *observations[0].Value)
	require.Equal(t, "Check meaning", judge.request.Rubric)
	require.Equal(t, "2.1.0", judge.request.PromptVersion)
	require.Equal(t, int64(17), judge.request.Seed)
	require.Equal(t, 2, judge.request.RetryLimit)
	require.JSONEq(t, `{"id":"judge-1","fingerprint":"frozen"}`, string(judge.request.JudgeModelSnapshot))
	require.Len(t, result.Scores, 1)
}

func TestSemanticJudgeRejectsPromptHashMismatch(t *testing.T) {
	plugin, err := NewSemanticJudgeMetric(&semanticJudgeStub{})
	require.NoError(t, err)
	err = plugin.Validate(json.RawMessage(
		`{"rubric":"Check meaning","prompt_version":"2.1.0",` +
			`"prompt_sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",` +
			`"judge_model_snapshot":{"id":"judge-1"},"temperature":0,"seed":17,"retry_limit":2}`,
	))
	require.ErrorContains(t, err, "does not match")
}
