package metric

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// MRRMetric calculates Mean Reciprocal Rank for retrieval evaluation
type MRRMetric struct{}

// NewMRRMetric creates a new MRRMetric instance
func NewMRRMetric() *MRRMetric {
	return &MRRMetric{}
}

// Compute calculates the Mean Reciprocal Rank score
func (m *MRRMetric) Compute(metricInput *types.MetricInput) float64 {
	// Get ground truth and predicted IDs
	gts := metricInput.RetrievalGT
	ids := metricInput.RetrievalIDs

	// Convert ground truth to sets for efficient lookup
	gtSets := make([]map[int]struct{}, len(gts))
	for i, gt := range gts {
		gtSets[i] = make(map[int]struct{})
		for _, docID := range gt {
			gtSets[i][docID] = struct{}{}
		}
	}

	var sumRR float64 // Sum of reciprocal ranks
	// Calculate reciprocal rank for each query
	for _, gtSet := range gtSets {
		// Find first relevant document in results
		for i, predID := range ids {
			if _, ok := gtSet[predID]; ok {
				// Reciprocal rank is 1/position (1-based)
				sumRR += 1.0 / float64(i+1)
				break // Only consider first relevant document
			}
		}
	}
	// Handle case with no ground truth
	if len(gtSets) == 0 {
		return 0
	}
	// Return mean of reciprocal ranks
	return sumRR / float64(len(gtSets))
}
