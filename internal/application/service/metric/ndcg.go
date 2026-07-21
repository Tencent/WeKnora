package metric

import (
	"math"

	"github.com/Tencent/WeKnora/internal/types"
)

// NDCGMetric calculates Normalized Discounted Cumulative Gain
type NDCGMetric struct {
	k int // Top k results to consider
}

// NewNDCGMetric creates a new NDCGMetric instance with given k value
func NewNDCGMetric(k int) *NDCGMetric {
	return &NDCGMetric{k: k}
}

// Compute calculates the NDCG score
func (n *NDCGMetric) Compute(metricInput *types.MetricInput) float64 {
	gts := metricInput.RetrievalGT
	ids := metricInput.RetrievalIDs

	// Limit results to top k
	if len(ids) > n.k {
		ids = ids[:n.k]
	}

	// Create set of relevant documents and count total relevant
	gtSets := make(map[int]struct{}, len(gts))
	countGt := 0
	for _, gt := range gts {
		countGt += len(gt)
		for _, g := range gt {
			gtSets[g] = struct{}{}
		}
	}

	// Assign relevance scores (1 for relevant, 0 otherwise)
	relevanceScores := make(map[int]int)
	for _, docID := range ids {
		if _, exist := gtSets[docID]; exist {
			relevanceScores[docID] = 1
		} else {
			relevanceScores[docID] = 0
		}
	}

	// Calculate DCG (Discounted Cumulative Gain)
	var dcg float64
	for i, docID := range ids {
		dcg += (math.Pow(2, float64(relevanceScores[docID])) - 1) / math.Log2(float64(i+2))
	}

	// Create ideal ranking (all relevant docs first)
	idealLen := minInt(countGt, len(ids))
	idealPred := make([]int, len(ids))
	for i := 0; i < len(ids); i++ {
		if i < idealLen {
			idealPred[i] = 1
		} else {
			idealPred[i] = 0
		}
	}

	// Calculate IDCG (Ideal DCG)
	var idcg float64
	for i, relevance := range idealPred {
		idcg += float64(relevance) / math.Log2(float64(i+2))
	}

	// Handle division by zero case
	if idcg == 0 {
		return 0
	}
	// NDCG = DCG / IDCG
	return dcg / idcg
}
