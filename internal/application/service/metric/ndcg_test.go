package metric

import (
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/stretchr/testify/require"
)

func TestNDCGMetric_Compute(t *testing.T) {
	for _, tc := range []struct {
		name string
		k    int
		gt   [][]int
		ids  []int
		want float64
	}{
		{"one of three relevant documents", 3, [][]int{{1, 2, 3}}, []int{1}, 0.46927872602275644},
		{"two of three relevant documents", 3, [][]int{{1, 2, 3}}, []int{1, 2}, 0.7653606369886217},
		{"ideal ranking respects cutoff", 1, [][]int{{1, 2, 3}}, []int{1}, 1},
		{"complete retrieval below cutoff", 3, [][]int{{1, 2}}, []int{1, 2}, 1},
		{"relevant result beyond cutoff", 2, [][]int{{1}}, []int{9, 8, 1}, 0},
		{"rank discount", 3, [][]int{{1}}, []int{9, 1}, 0.6309297535714574},
		{"distinct relevant documents", 3, [][]int{{1, 1}, {1}}, []int{1, 9, 8}, 1},
		{"no ground truth", 3, nil, []int{1}, 0},
		{"empty ground truth set", 3, [][]int{{}}, []int{1}, 0},
		{"empty retrieval", 3, [][]int{{1, 2}}, nil, 0},
		{"zero cutoff", 0, [][]int{{1}}, []int{1}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &types.MetricInput{RetrievalGT: tc.gt, RetrievalIDs: tc.ids}
			require.InDelta(t, tc.want, NewNDCGMetric(tc.k).Compute(input), 1e-9)
		})
	}
}
