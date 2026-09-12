package metric

import "github.com/Tencent/WeKnora/internal/types"

// MAPMetricV2 computes binary average precision with the complete relevant
// ground-truth set as the denominator, so omitted relevant passages reduce the
// score.
type MAPMetricV2 struct{}

// NewMAPMetricV2 constructs the complete-ground-truth MAP implementation.
func NewMAPMetricV2() *MAPMetricV2 { return &MAPMetricV2{} }

// Compute returns average precision using every labeled relevant passage as the denominator.
func (m *MAPMetricV2) Compute(input *types.MetricInput) float64 {
	if input == nil {
		return 0
	}
	relevant := make(map[int]struct{})
	for id, grade := range input.RetrievalGrades {
		if grade > 0 {
			relevant[id] = struct{}{}
		}
	}
	if len(input.RetrievalGrades) == 0 {
		for _, group := range input.RetrievalGT {
			for _, id := range group {
				relevant[id] = struct{}{}
			}
		}
	}
	if len(relevant) == 0 {
		return 0
	}
	seen := make(map[int]struct{}, len(input.RetrievalIDs))
	hits := 0
	sum := 0.0
	for rank, id := range input.RetrievalIDs {
		if _, duplicate := seen[id]; duplicate {
			continue
		}
		seen[id] = struct{}{}
		if _, ok := relevant[id]; ok {
			hits++
			sum += float64(hits) / float64(rank+1)
		}
	}
	return sum / float64(len(relevant))
}
