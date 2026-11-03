package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestMAPMetric_Compute(t *testing.T) {
	tests := []struct {
		name     string
		input    *types.MetricInput
		expected float64
	}{
		{
			name: "total match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{2, 4, 6}},
				RetrievalIDs: []int{2, 4, 6},
			},
			expected: 1.0,
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
			name: "partial match",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3}},
				RetrievalIDs: []int{2, 5, 1, 3},
			},
			// AP = (1/1 + 2/3 + 3/4)/3 ≈ 0.80555555
			expected: 0.8055555555555555,
		},
		{
			name: "empty ground truth",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{},
				RetrievalIDs: []int{1, 2},
			},
			expected: 0.0,
		},
		{
			name: "only one of four relevant documents retrieved",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3, 4}},
				RetrievalIDs: []int{1},
			},
			expected: 0.25,
		},
		{
			name: "incomplete recall with a nonrelevant result first",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2, 3}},
				RetrievalIDs: []int{99, 1, 2},
			},
			expected: 7.0 / 18,
		},
		{
			name: "duplicate retrieved documents count once",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}},
				RetrievalIDs: []int{1, 1, 2},
			},
			expected: 5.0 / 6,
		},
		{
			name: "duplicate hits cannot inflate the normalized score",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1}},
				RetrievalIDs: []int{1, 1},
			},
			expected: 1.0,
		},
		{
			name: "duplicate ground truth documents count once",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 1, 2}},
				RetrievalIDs: []int{1},
			},
			expected: 0.5,
		},
		{
			name: "empty ground truth group",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{}},
				RetrievalIDs: []int{1},
			},
			expected: 0.0,
		},
		{
			name: "empty retrieved results",
			input: &types.MetricInput{
				RetrievalGT: [][]int{{1, 2}},
			},
			expected: 0.0,
		},
		{
			name: "multiple queries with incomplete recall",
			input: &types.MetricInput{
				RetrievalGT:  [][]int{{1, 2}, {3, 4}},
				RetrievalIDs: []int{1, 3},
			},
			expected: 0.375,
		},
		{
			name: "multiple queries",
			input: &types.MetricInput{
				RetrievalGT: [][]int{
					{1, 2},
					{3, 4},
				},
				RetrievalIDs: []int{1, 3, 2, 4},
			},
			// Query1 AP: (1/1 + 2/3)/2 ≈ 0.8333
			// Query2 AP: (1/2 + 2/4)/2 = 0.5
			// MAP: (0.8333 + 0.5)/2 ≈ 0.6667
			expected: 0.6666666666666666,
		},
	}

	metric := NewMAPMetric()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := metric.Compute(tt.input)
			if !almostEqual(got, tt.expected, 1e-6) {
				t.Errorf("Compute() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// Helper function to compare floating point numbers with tolerance
func almostEqual(a, b, tolerance float64) bool {
	if a == b {
		return true
	}
	diff := a - b
	if diff < 0 {
		diff = -diff
	}
	return diff < tolerance
}
