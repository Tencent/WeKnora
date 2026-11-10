package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestPrecisionMetric_Compute(t *testing.T) {
	tests := []struct {
		name     string
		input    *types.MetricInput
		expected float64
	}{
		{
			name: "perfect match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 3, 5}},
				RetrievalIDs: []int{1, 3, 5},
			},
			expected: 1.0,
		},
		{
			name: "half match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3}},
				RetrievalIDs: []int{1, 4, 2},
			},
			expected: 0.6666666666666666,
		},
		{
			name: "no match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3}},
				RetrievalIDs: []int{4, 5, 6},
			},
			expected: 0.0,
		},
		{
			name: "empty retrieval",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3}},
				RetrievalIDs: []int{},
			},
			expected: 0.0,
		},
		{
			name: "multiple ground truths",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}, {3, 4}},
				RetrievalIDs: []int{1, 3, 5},
			},
			expected: 0.3333333333333333,
		},
	}

	pm := NewPrecisionMetric()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := pm.Compute(tt.input)
			if got != tt.expected {
				t.Errorf("Compute() = %v, want %v", got, tt.expected)
			}
		})
	}
}
