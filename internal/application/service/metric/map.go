package metric

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// MAPMetric calculates Mean Average Precision for retrieval evaluation
type MAPMetric struct{}

// NewMAPMetric creates a new MAPMetric instance
func NewMAPMetric() *MAPMetric {
	return &MAPMetric{}
}

// Compute calculates the Mean Average Precision score
func (m *MAPMetric) Compute(metricInput *types.MetricInput) float64 {
	// Convert ground truth to sets for efficient lookup
	gts := metricInput.RetrievalGT
	ids := metricInput.RetrievalIDs

	// Create sets of relevant document IDs for each query
	gtSets := make([]map[int]struct{}, len(gts))
	for i, gt := range gts {
		gtSets[i] = make(map[int]struct{})
		for _, docID := range gt {
			gtSets[i][docID] = struct{}{}
		}
	}

	var apSum float64 // Sum of average precision for all queries

	// Calculate average precision for each query
	for _, gtSet := range gtSets {
		relevantCount := len(gtSet)
		// Mark which predicted documents are relevant
		predHits := make([]bool, len(ids))
		for i, predID := range ids {
			if _, ok := gtSet[predID]; ok {
				predHits[i] = true
				delete(gtSet, predID) // Count each relevant document only at its first retrieved rank.
			} else {
				predHits[i] = false
			}
		}

		var (
			ap       float64 // Average precision for current query
			hitCount int     // Number of relevant documents found
		)

		// Calculate precision at each rank position
		for k := 0; k < len(predHits); k++ {
			if predHits[k] {
				hitCount++
				// Precision at k: relevant docs found up to k / k
				ap += float64(hitCount) / float64(k+1)
			}
		}
		// Normalize by number of relevant documents
		if hitCount > 0 {
			ap /= float64(relevantCount)
		}
		apSum += ap
	}

	// Handle case with no ground truth
	if len(gtSets) == 0 {
		return 0
	}
	// Return mean of average precision across all queries
	return apSum / float64(len(gtSets))
}
