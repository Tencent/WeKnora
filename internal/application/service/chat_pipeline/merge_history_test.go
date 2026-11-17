package chatpipeline

import (
	"context"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func boolPtr(b bool) *bool { return &b }

// Issue #5613: a quick-answer agent with retain_retrieval_history=false must
// not see the previous turn's documents in this turn's retrieval results.
// The merge stage is the only place history references re-enter a turn, so
// opting out has to skip the injection entirely — the overlap filter alone
// is not enough, since same-domain chunks easily clear its 0.3 threshold.
func TestInjectHistoryResultsSkippedWhenRetainRetrievalHistoryFalse(t *testing.T) {
	longChunk := "refund policy: customers may request a refund within seven days of delivery. " +
		strings.Repeat("unrelated operational detail about warehouses and shipping partners. ", 30)
	cm := &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			RetainRetrievalHistory: boolPtr(false),
		},
		PipelineState: types.PipelineState{
			RewriteQuery: "refund within seven days delivery",
			History: []*types.History{{
				KnowledgeReferences: []*types.SearchResult{
					{ID: "ref-1", Content: longChunk, Score: 0.8},
				},
			}},
		},
	}
	current := []*types.SearchResult{{ID: "fresh-1", Content: "fresh result about refunds", Score: 0.9}}
	got := (&PluginMerge{}).injectHistoryResults(context.Background(), cm, current)
	if len(got) != 1 || got[0].ID != "fresh-1" {
		ids := make([]string, 0, len(got))
		for _, r := range got {
			ids = append(ids, r.ID)
		}
		t.Fatalf("injected results = %v, want [fresh-1]", ids)
	}
}

// The same turn with the flag unset keeps the legacy behavior: history
// references covering the query are still injected.
func TestInjectHistoryResultsKeepsReferenceWhenRetainUnset(t *testing.T) {
	longChunk := "refund policy: customers may request a refund within seven days of delivery. " +
		strings.Repeat("unrelated operational detail about warehouses and shipping partners. ", 30)
	cm := &types.ChatManage{
		PipelineState: types.PipelineState{
			RewriteQuery: "refund within seven days delivery",
			History: []*types.History{{
				KnowledgeReferences: []*types.SearchResult{
					{ID: "ref-1", Content: longChunk, Score: 0.8},
				},
			}},
		},
	}
	current := []*types.SearchResult{{ID: "fresh-1", Content: "fresh result about refunds", Score: 0.9}}
	got := (&PluginMerge{}).injectHistoryResults(context.Background(), cm, current)
	ids := make(map[string]bool, len(got))
	for _, r := range got {
		ids[r.ID] = true
	}
	if !ids["fresh-1"] || !ids["ref-1"] {
		t.Fatalf("injected results = %v, want [fresh-1 ref-1]", ids)
	}
}

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
