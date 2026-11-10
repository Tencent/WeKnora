package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMRRMetric_Compute(t *testing.T) {
	tests := []struct {
		name     string
		input    *types.MetricInput
		expected float64
	}{
		{
			name: "perfect match - first position",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}},
				RetrievalIDs: []int{1, 2, 3},
			},
			// RR = 1/1 = 1.0
			expected: 1.0,
		},
		{
			name: "match at second position",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}},
				RetrievalIDs: []int{3, 1, 2},
			},
			// RR = 1/2 = 0.5
			expected: 0.5,
		},
		{
			name: "no match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}},
				RetrievalIDs: []int{3, 4},
			},
			expected: 0.0,
		},
		{
			name: "multiple queries",
			input: &types.MetricInput{
				RetrievalGT: [][]int{
					{1, 2}, // RR = 1/1 = 1.0
					{3, 4}, // RR = 1/2 = 0.5
				},
				RetrievalIDs: []int{1, 3, 2, 4},
			},
			// MRR = (1.0 + 0.5)/2 = 0.75
			expected: 0.75,
		},
		{
			name: "empty ground truth",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{},
				RetrievalIDs: []int{1, 2},
			},
			expected: 0.0,
		},
	}

	metric := NewMRRMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metric.Compute(tt.input)
			if !almostEqual(got, tt.expected, 1e-6) {
				t.Errorf("Compute() = %v, want %v", got, tt.expected)
			}
		})
	}
}
