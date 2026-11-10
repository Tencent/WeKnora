package metric

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// RecallMetric calculates recall for retrieval evaluation
type RecallMetric struct{}

// NewRecallMetric creates a new RecallMetric instance
func NewRecallMetric() *RecallMetric {
	return &RecallMetric{}
}

// Compute calculates the recall score
func (r *RecallMetric) Compute(metricInput *types.MetricInput) float64 {
	// Get ground truth and predicted IDs
	gts := metricInput.RetrievalGT
	ids := metricInput.RetrievalIDs

	// Convert ground truth to sets for efficient lookup
	gtSets := SliceMap(gts, ToSet)
	if len(gtSets) == 0 {
		return 0.0
	}

	if len(ids) == 0 {
		return 0.0
	}

	var totalRecall float64
	for _, gtSet := range gtSets {
		hits := Hit(ids, gtSet)
		if len(gtSet) > 0 {
			totalRecall += float64(hits) / float64(len(gtSet))
		}
	}

	// Recall = average recall across all ground truth sets
	return totalRecall / float64(len(gtSets))
}
