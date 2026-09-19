package mastery

import (
	"math"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func refs(knowledgeIDs ...string) []types.MemoryDocAffinity {
	out := make([]types.MemoryDocAffinity, 0, len(knowledgeIDs))
	for _, id := range knowledgeIDs {
		out = append(out, types.MemoryDocAffinity{KnowledgeID: id, KnowledgeBaseID: "kb-1"})
	}
	return out
}

func TestAllocateLikesSingleSource(t *testing.T) {
	allocs := AllocateLikes(refs("doc-a"))
	if len(allocs) != 1 {
		t.Fatalf("expected 1 allocation, got %d", len(allocs))
	}
	if allocs[0].KnowledgeID != "doc-a" || allocs[0].CitationPosition != 1 {
		t.Fatalf("unexpected allocation: %+v", allocs[0])
	}
	if allocs[0].KnowledgeBaseID != "kb-1" {
		t.Fatalf("allocation should keep its knowledge_base_id: %+v", allocs[0])
	}
	if math.Abs(allocs[0].CreditedWeight-1.0) > 1e-9 {
		t.Fatalf("single source should get full 1.0, got %v", allocs[0].CreditedWeight)
	}
}

func TestAllocateLikesSublinearCap(t *testing.T) {
	cases := []struct {
		n     int
		total float64
	}{
		{1, 1.0},
		{2, 1.3},
		{3, 1.3},
		{4, 1.5},
		{8, 1.5},
	}
	for _, c := range cases {
		distinct := make([]string, c.n)
		for i := range distinct {
			distinct[i] = string(rune('a'+i)) + "-doc"
		}
		allocs := AllocateLikes(refs(distinct...))
		got := 0.0
		for _, a := range allocs {
			got += a.CreditedWeight
		}
		if math.Abs(got-c.total) > 1e-6 {
			t.Fatalf("n=%d total should be %.1f, got %v", c.n, c.total, got)
		}
	}
}

func TestAllocateLikesPositionDecay(t *testing.T) {
	allocs := AllocateLikes(refs("first", "second", "third"))
	if len(allocs) != 3 {
		t.Fatalf("expected 3 allocations, got %d", len(allocs))
	}
	if allocs[0].CreditedWeight <= allocs[1].CreditedWeight {
		t.Fatalf("first citation should get more credit than second: %v <= %v",
			allocs[0].CreditedWeight, allocs[1].CreditedWeight)
	}
}

func TestAllocateLikesDeduplicates(t *testing.T) {
	allocs := AllocateLikes([]types.MemoryDocAffinity{
		{KnowledgeID: "a", KnowledgeBaseID: "kb-1"},
		{KnowledgeID: "a", KnowledgeBaseID: "kb-2"},
		{KnowledgeID: "b", KnowledgeBaseID: "kb-1"},
	})
	if len(allocs) != 2 {
		t.Fatalf("expected dedupe to 2 allocations, got %d", len(allocs))
	}
}

func TestAllocateLikesEmpty(t *testing.T) {
	if allocs := AllocateLikes(nil); allocs != nil {
		t.Fatalf("nil input should return nil, got %v", allocs)
	}
	if allocs := AllocateLikes([]types.MemoryDocAffinity{{}}); allocs != nil {
		t.Fatalf("blank-only input should return nil, got %v", allocs)
	}
}
