package reproduce

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestValidateThresholdsRequiresCompleteValidConfiguration(t *testing.T) {
	metrics := []MetricResult{{Name: "quality"}}
	require.ErrorContains(t, validateThresholds(metrics, ThresholdConfig{}), "contain 0 metrics")
	require.ErrorContains(t, validateThresholds(metrics, ThresholdConfig{Metrics: map[string]Threshold{
		"quality": {Direction: "lower_is_better"},
	}}), "unsupported direction")
	require.ErrorContains(t, validateThresholds(metrics, ThresholdConfig{Metrics: map[string]Threshold{
		"quality": {Direction: "higher_is_better", MaxAbsoluteDegradation: -0.1},
	}}), "must be non-negative")
}

func TestBuildProducesDeterministicCompleteReport(t *testing.T) {
	options := BuildOptions{
		DatasetPath:   "../../../dataset/golden/v1/dataset.json",
		ThresholdPath: "../../../evaluation/regression/thresholds.json",
		Commit:        "0123456789abcdef",
		Environment:   Environment{GoVersion: "go1.26.0", GOOS: "linux", GOARCH: "amd64", CPUs: 4},
	}
	first, err := Build(context.Background(), options)
	require.NoError(t, err)
	second, err := Build(context.Background(), options)
	require.NoError(t, err)
	firstJSON, err := JSON(first)
	require.NoError(t, err)
	secondJSON, err := JSON(second)
	require.NoError(t, err)
	require.Equal(t, firstJSON, secondJSON)
	require.Equal(t, ReportSchemaVersion, first.SchemaVersion)
	require.Equal(t, "d8deca2c2ab7b77123130788c9082d0998ecbf76c1d8556952703045cc2556e1", first.Dataset.ContentSHA256)
	require.Len(t, first.MetricPlan.Metrics, 13)
	require.Len(t, first.Metrics, 13)
	require.True(t, first.Regression.Passed)
	for _, metric := range first.Metrics {
		require.Equal(t, 2, metric.NTotal)
		require.Equal(t, 2, metric.NValid)
		require.Zero(t, metric.NMissing)
	}
}

func TestThresholdBoundaryAndDegradation(t *testing.T) {
	valueAtBoundary := 0.7
	metrics := []MetricResult{{Name: "quality", Value: &valueAtBoundary}}
	config := ThresholdConfig{Metrics: map[string]Threshold{
		"quality": {Baseline: 0.8, MaxAbsoluteDegradation: 0.1, Direction: "higher_is_better"},
	}}
	require.True(t, CheckThresholds(metrics, config).Passed)

	degraded := 0.699
	metrics[0].Value = &degraded
	result := CheckThresholds(metrics, config)
	require.False(t, result.Passed)
	require.Len(t, result.Checks, 1)
	require.InDelta(t, 0.101, result.Checks[0].AbsoluteDelta, 1e-12)
}

func TestMarkdownIsDerivedFromReport(t *testing.T) {
	value := 0.75
	report := &Report{
		Dataset: DatasetIdentity{ID: "golden-v1", Version: 1, ContentSHA256: "abc"}, Commit: "commit-a",
		MetricPlan: &types.EvaluationMetricPlanSnapshot{Metrics: make([]types.EvaluationMetricSpecSnapshot, 1)},
		Metrics:    []MetricResult{{Name: "retrieval.recall", Value: &value, NValid: 2, NTotal: 2}},
		Regression: RegressionResult{Passed: true, Checks: []RegressionCheck{{
			Metric: "retrieval.recall", Baseline: 0.75, Threshold: 0, Passed: true,
		}}},
		Environment: Environment{GoVersion: "go1.26", GOOS: "linux", GOARCH: "amd64", CPUs: 2},
	}
	output := string(Markdown(report))
	require.Contains(t, output, "`retrieval.recall`")
	require.Contains(t, output, "**PASS**")
}
