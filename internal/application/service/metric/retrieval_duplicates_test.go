package metric

import (
	"math"
	"reflect"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestRetrievalMetrics_DuplicateDocuments(t *testing.T) {
	idealTwo := 1 + 1/math.Log2(3)
	for _, tc := range []struct {
		name          string
		gt            [][]int
		ids           []int
		k             int
		wantPrecision float64
		wantRecall    float64
		wantNDCG      float64
	}{
		{"duplicate cannot exceed full credit", [][]int{{1}}, []int{1, 1}, 3, 0.5, 1, 1},
		{"duplicate does not cover missing evidence", [][]int{{1, 2}}, []int{1, 1}, 3, 0.5, 0.5, 1 / idealTwo},
		{"duplicate preserves later document rank", [][]int{{1, 2}}, []int{1, 1, 2}, 3, 2.0 / 3, 1, 1.5 / idealTwo},
		{"nonrelevant duplicates preserve rank", [][]int{{1}}, []int{9, 9, 1}, 3, 1.0 / 3, 1, 0.5},
		{"duplicates do not move evidence into cutoff", [][]int{{1, 2}}, []int{1, 1, 2}, 2, 2.0 / 3, 1, 1 / idealTwo},
		{"ground truth groups stay independent", [][]int{{1, 1}, {1, 2}}, []int{1, 1, 2}, 3, 0.5, 1, 1.5 / idealTwo},
	} {
		t.Run(tc.name, func(t *testing.T) {
			input := &types.MetricInput{RetrievalGT: tc.gt, RetrievalIDs: tc.ids}
			if got := NewPrecisionMetric().Compute(input); !(math.Abs(got-tc.wantPrecision) <= 1e-9) {
				t.Errorf("Precision = %v, want %v", got, tc.wantPrecision)
			}
			if got := NewRecallMetric().Compute(input); !(math.Abs(got-tc.wantRecall) <= 1e-9) {
				t.Errorf("Recall = %v, want %v", got, tc.wantRecall)
			}
			if got := NewNDCGMetric(tc.k).Compute(input); !(math.Abs(got-tc.wantNDCG) <= 1e-9) {
				t.Errorf("NDCG@%d = %v, want %v", tc.k, got, tc.wantNDCG)
			}
		})
	}
}

func TestRetrievalMetrics_DuplicateInputsUnchanged(t *testing.T) {
	input := &types.MetricInput{
		RetrievalGT:  [][]int{{1, 1}, {1, 2}},
		RetrievalIDs: []int{1, 1, 2, 9},
	}
	wantGT := [][]int{{1, 1}, {1, 2}}
	wantIDs := []int{1, 1, 2, 9}
	precision, recall, ndcg := NewPrecisionMetric(), NewRecallMetric(), NewNDCGMetric(3)
	firstPrecision, firstRecall, firstNDCG := precision.Compute(input), recall.Compute(input), ndcg.Compute(input)
	if precision.Compute(input) != firstPrecision ||
		recall.Compute(input) != firstRecall ||
		ndcg.Compute(input) != firstNDCG {
		t.Error("reusing the same input changed the metric result")
	}
	if !reflect.DeepEqual(input.RetrievalGT, wantGT) || !reflect.DeepEqual(input.RetrievalIDs, wantIDs) {
		t.Error("metric calculation mutated the caller's input")
	}
}
