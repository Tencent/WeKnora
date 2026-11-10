package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// A follow-up question shares a few terms with last turn's long reference.
// Jaccard over the union never reached the threshold for chunk-sized text,
// so no history reference was ever injected.
func TestFilterHistoryResultsKeepsReferenceCoveringTheQuery(t *testing.T) {
	longChunk := "refund policy: customers may request a refund within seven days of delivery. " +
		strings.Repeat("unrelated operational detail about warehouses and shipping partners. ", 30)
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			RewriteQuery: "refund within seven days delivery",
			History: []*types.History{{
				KnowledgeReferences: []*types.SearchResult{
					{ID: "ref-1", Content: longChunk, Score: 0.8},
					{ID: "ref-2", Content: strings.Repeat("completely different topic text. ", 30), Score: 0.9},
				},
			}},
		},
	}
	got := filterHistoryResults(context.Background(), cm, nil)
	if len(got) != 1 || got[0].ID != "ref-1" {
		ids := make([]string, 0, len(got))
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		t.Fatalf("history references kept = %v, want [ref-1]", ids)
	}
}
