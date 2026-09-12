package main

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestRunProductionPipelineGateMeetsExpectedQuality(t *testing.T) {
	result, err := runProductionPipelineGate(context.Background())
	require.NoError(t, err)
	require.True(t, result.Success)
	require.Equal(t, types.EvaluationStatueSuccess, result.Data.Task.Status)
	require.Equal(t, result.Data.Task.Total, result.Data.Task.Finished)
	require.GreaterOrEqual(t, result.Data.Metric.Retrieval.Precision, 0.5)
	require.GreaterOrEqual(t, result.Data.Metric.Retrieval.Recall, 0.5)
	require.GreaterOrEqual(t, result.Data.Metric.Retrieval.NDCG10, 0.5)
	require.GreaterOrEqual(t, result.Data.Metric.Retrieval.MRR, 0.5)
	require.GreaterOrEqual(t, result.Data.Metric.Generation.ROUGEL, 0.2)
}
