package metric

import (
	"github.com/Tencent/WeKnora/internal/types"
)

// PrecisionMetric calculates precision for retrieval evaluation
type PrecisionMetric struct{}

// NewPrecisionMetric creates a new PrecisionMetric instance
func NewPrecisionMetric() *PrecisionMetric {
	return &PrecisionMetric{}
}

// Compute calculates the precision score
func (r *PrecisionMetric) Compute(metricInput *types.MetricInput) float64 {
	// Get ground truth and predicted IDs
	gts := metricInput.RetrievalGT
	ids := metricInput.RetrievalIDs

	// Convert ground truth to sets for efficient lookup
	gtSets := SliceMap(gts, ToSet)
	// Precision = retrieved items that are in ground truth / total retrieved items
	// In the test cases, ground truth is a list of sets. 
	// We compute precision per ground truth set, and average them.
	// But actually, precision is typically |retrieved ∩ relevant| / |retrieved|.
	// Let's sum the precisions for each ground truth set and average them.
	
	if len(gts) == 0 {
		return 0.0
	}

	if len(ids) == 0 {
		return 0.0
	}

	var totalPrecision float64
	for _, gtSet := range gtSets {
		hits := Hit(ids, gtSet)
		totalPrecision += float64(hits) / float64(len(ids))
	}

	return totalPrecision / float64(len(gts))
}
