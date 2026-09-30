package mastery

import "github.com/Tencent/WeKnora/internal/types"

// AllocateLikes splits one answer like across its cited documents. The total
// gain is capped sub-linearly (so a long answer cannot light up the whole map),
// then divided across citations by position so earlier citations (usually the
// more central sources) get more credit.
//
//	1 source   -> total 1.0
//	2..3 sources -> total 1.3
//	4+ sources  -> total 1.5 (cap)
//
// Each allocation keeps its source's knowledge_base_id so the like event can be
// scoped to the right KB in the daily aggregation bucket.
func AllocateLikes(refs []types.MemoryDocAffinity) types.LikeAllocations {
	cleaned := make([]types.MemoryDocAffinity, 0, len(refs))
	seen := make(map[string]struct{}, len(refs))
	for _, r := range refs {
		if r.KnowledgeID == "" {
			continue
		}
		if _, dup := seen[r.KnowledgeID]; dup {
			continue
		}
		seen[r.KnowledgeID] = struct{}{}
		cleaned = append(cleaned, r)
	}
	if len(cleaned) == 0 {
		return nil
	}

	var total float64
	switch {
	case len(cleaned) == 1:
		total = 1.0
	case len(cleaned) <= 3:
		total = 1.3
	default:
		total = 1.5
	}

	weights := make([]float64, len(cleaned))
	var sum float64
	for i := range cleaned {
		weights[i] = 1.0 / float64(i+1)
		sum += weights[i]
	}

	allocations := make(types.LikeAllocations, 0, len(cleaned))
	for i, r := range cleaned {
		allocations = append(allocations, types.LikeAllocation{
			KnowledgeID:      r.KnowledgeID,
			KnowledgeBaseID:  r.KnowledgeBaseID,
			CitationPosition: i + 1,
			CreditedWeight:   total * weights[i] / sum,
		})
	}
	return allocations
}
