package metricregistry

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

type testMetric struct {
	definition Definition
	value      float64
	err        error
}

func (m testMetric) Definition() Definition { return m.definition }

func (m testMetric) Validate(json.RawMessage) error { return nil }

func (m testMetric) Compute(context.Context, *types.MetricInput, json.RawMessage) (Observation, error) {
	if m.err != nil {
		return Observation{Status: types.EvaluationMetricObservationFailed, ErrorCode: "test_failed"}, m.err
	}
	value := m.value
	return Observation{Value: &value, Status: types.EvaluationMetricObservationValid}, nil
}

func TestRegistryRejectsDuplicateKeyAndVersion(t *testing.T) {
	definition := Definition{
		Key: "custom.score", Version: "1.0.0", Kind: KindEndToEnd,
		DefaultConfig: json.RawMessage(`{}`), ConfigSchema: json.RawMessage(`{"type":"object"}`),
	}
	_, err := New(testMetric{definition: definition}, testMetric{definition: definition})
	require.ErrorIs(t, err, ErrDuplicateMetric)
}

func TestRegistryResolveUsesM3SnapshotProtocolAndComputesCustomMetric(t *testing.T) {
	registry, err := New(testMetric{
		definition: Definition{
			Key: "custom.score", Version: "1.0.0", Kind: KindEndToEnd,
			Description: "deterministic custom score", DefaultConfig: json.RawMessage(`{"weight":1}`),
			ConfigSchema: json.RawMessage(`{"type":"object"}`),
		},
		value: 0.75,
	})
	require.NoError(t, err)

	resolved, err := registry.Resolve([]Spec{{
		Key: "custom.score", Version: "1.0.0", Config: json.RawMessage(`{"weight": 2}`), Required: true,
	}})
	require.NoError(t, err)
	require.Len(t, resolved.Snapshot.Metrics, 1)
	spec := resolved.Snapshot.Metrics[0]
	require.Equal(t, types.EvaluationMetricInstanceID(spec.Key, spec.Version, spec.ConfigSHA256), spec.InstanceID)
	require.JSONEq(t, `{"weight":2}`, string(spec.Config))

	result, observations, err := resolved.Compute(context.Background(), &types.MetricInput{})
	require.NoError(t, err)
	require.Equal(t, 0.75, *result.Scores[spec.InstanceID].Value)
	require.Len(t, observations, 1)
	require.Equal(t, types.EvaluationMetricObservationValid, observations[0].Status)
}

func TestResolvedPlanPreservesOptionalFailureAndRejectsRequiredFailure(t *testing.T) {
	failed := testMetric{
		definition: Definition{
			Key: "custom.failed", Version: "1.0.0", Kind: KindGeneration,
			DefaultConfig: json.RawMessage(`{}`), ConfigSchema: json.RawMessage(`{"type":"object"}`),
		},
		err: errors.New("compute failed"),
	}
	registry, err := New(failed)
	require.NoError(t, err)

	optional, err := registry.Resolve([]Spec{{Key: "custom.failed", Version: "1.0.0", Required: false}})
	require.NoError(t, err)
	result, observations, err := optional.Compute(context.Background(), &types.MetricInput{})
	require.NoError(t, err)
	require.Equal(
		t, types.EvaluationMetricObservationFailed,
		result.Scores[optional.Snapshot.Metrics[0].InstanceID].Status,
	)
	require.Equal(t, "test_failed", observations[0].ErrorCode)

	required, err := registry.Resolve([]Spec{{Key: "custom.failed", Version: "1.0.0", Required: true}})
	require.NoError(t, err)
	_, observations, err = required.Compute(context.Background(), &types.MetricInput{})
	require.ErrorIs(t, err, ErrRequiredMetricFailed)
	require.Equal(t, types.EvaluationMetricObservationFailed, observations[0].Status)
}

func TestDefaultRegistryResolvesTwelveMetricsAndFillsCompatibilityFields(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)
	resolved, err := registry.Resolve(DefaultSpecs())
	require.NoError(t, err)
	require.Len(t, resolved.Snapshot.Metrics, 12)

	result, observations, err := resolved.Compute(context.Background(), &types.MetricInput{
		RetrievalGT:              [][]int{{3}},
		RetrievalGrades:          map[int]int{3: 2},
		RetrievalLabelsAvailable: true,
		RetrievalIDs:             []int{9, 3},
		GeneratedTexts:           "known answer",
		GeneratedGT:              "known answer",
	})
	require.NoError(t, err)
	require.Len(t, observations, 12)
	require.Len(t, result.Scores, 12)
	require.Equal(t, 0.5, result.RetrievalMetrics.Precision)
	require.Equal(t, 1.0, result.RetrievalMetrics.Recall)
}

func TestDefaultRetrievalMetricsMarkMissingLabels(t *testing.T) {
	registry, err := NewDefaultRegistry()
	require.NoError(t, err)
	resolved, err := registry.Resolve(DefaultSpecs())
	require.NoError(t, err)

	result, observations, err := resolved.Compute(context.Background(), &types.MetricInput{
		RetrievalIDs: []int{1, 2},
	})
	require.NoError(t, err)
	for _, observation := range observations {
		if strings.HasPrefix(observation.InstanceID, "retrieval.map@2.0.0") ||
			strings.HasPrefix(observation.InstanceID, "retrieval.ndcg@2.0.0") {
			require.Equal(t, types.EvaluationMetricObservationMissing, observation.Status)
			require.Nil(t, observation.Value)
			require.Equal(t, "relevance_labels_missing", observation.ErrorCode)
		}
	}
	require.NotNil(t, result)
}
