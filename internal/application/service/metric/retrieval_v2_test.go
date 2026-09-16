package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestMAPMetricV2_PenalizesMissingRelevantPassage(t *testing.T) {
	value := NewMAPMetricV2().Compute(&types.MetricInput{
		RetrievalGrades: map[int]int{1: 1, 2: 2},
		RetrievalIDs:    []int{1},
	})
	require.InDelta(t, 0.5, value, 1e-12)
}

func TestNDCGMetricV2_UsesGradedRelevance(t *testing.T) {
	metric := NewNDCGMetricV2(2)
	ideal := metric.Compute(&types.MetricInput{
		RetrievalGrades: map[int]int{1: 1, 2: 2},
		RetrievalIDs:    []int{2, 1},
	})
	reversed := metric.Compute(&types.MetricInput{
		RetrievalGrades: map[int]int{1: 1, 2: 2},
		RetrievalIDs:    []int{1, 2},
	})
	require.InDelta(t, 1, ideal, 1e-12)
	require.Less(t, reversed, ideal)
}

func TestRetrievalV2_NoPositiveLabelsReturnsZero(t *testing.T) {
	input := &types.MetricInput{
		RetrievalGrades: map[int]int{1: 0},
		RetrievalIDs:    []int{1},
	}
	require.Zero(t, NewMAPMetricV2().Compute(input))
	require.Zero(t, NewNDCGMetricV2(3).Compute(input))
}
