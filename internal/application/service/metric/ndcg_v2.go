package metric

import (
	"math"
	"sort"

	"github.com/Tencent/WeKnora/internal/types"
)

// NDCGMetricV2 computes graded normalized discounted cumulative gain. The
// ideal ranking uses every labeled relevant passage up to k, so short result
// lists and omitted high-grade passages are penalized.
type NDCGMetricV2 struct{ k int }

// NewNDCGMetricV2 constructs the graded NDCG implementation for cutoff k.
func NewNDCGMetricV2(k int) *NDCGMetricV2 { return &NDCGMetricV2{k: k} }

// Compute returns graded NDCG using the full labeled ideal ranking up to k.
func (n *NDCGMetricV2) Compute(input *types.MetricInput) float64 {
	if input == nil || n.k <= 0 {
		return 0
	}
	grades := make(map[int]int, len(input.RetrievalGrades))
	for id, grade := range input.RetrievalGrades {
		if grade > 0 {
			grades[id] = grade
		}
	}
	if len(input.RetrievalGrades) == 0 {
		for _, group := range input.RetrievalGT {
			for _, id := range group {
				grades[id] = 1
			}
		}
	}
	if len(grades) == 0 {
		return 0
	}
	gain := func(grade int, rank int) float64 {
		return (math.Pow(2, float64(grade)) - 1) / math.Log2(float64(rank+2))
	}
	dcg := 0.0
	seen := make(map[int]struct{}, len(input.RetrievalIDs))
	for rank, id := range input.RetrievalIDs {
		if rank >= n.k {
			break
		}
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		dcg += gain(grades[id], rank)
	}
	ideal := make([]int, 0, len(grades))
	for _, grade := range grades {
		ideal = append(ideal, grade)
	}
	sort.Sort(sort.Reverse(sort.IntSlice(ideal)))
	idcg := 0.0
	for rank, grade := range ideal {
		if rank >= n.k {
			break
		}
		idcg += gain(grade, rank)
	}
	if idcg == 0 {
		return 0
	}
	return dcg / idcg
}
