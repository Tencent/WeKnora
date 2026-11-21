package chatpipeline

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/models/rerank"
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

// contentScoredReranker stands in for a semantic rerank model: it scores a
// passage 0.9 when it mentions the topic of the follow-up query and 0.05
// otherwise, which the token-overlap filter cannot distinguish.
type contentScoredReranker struct{ err error }

func (r *contentScoredReranker) Rerank(_ context.Context, _ string, documents []string) ([]rerank.RankResult, error) {
	if r.err != nil {
		return nil, r.err
	}
	out := make([]rerank.RankResult, len(documents))
	for i, doc := range documents {
		score := 0.05
		if strings.Contains(doc, "psychological") {
			score = 0.9
		}
		out[i] = rerank.RankResult{Index: i, RelevanceScore: score}
	}
	return out, nil
}

func (r *contentScoredReranker) GetModelName() string { return "content-scored" }
func (r *contentScoredReranker) GetModelID() string   { return "content-scored" }

// historyRerankChatManage models the #5613 turn pair: turn 1 retrieved a
// food-safety regulation, turn 2 asks about psychological screening. The
// food-safety text shares generic school words (schools/students/yearly)
// with the rewritten query, so it clears the 0.3 token-overlap filter, while
// a mental-health chunk is a genuinely relevant prior reference.
func historyRerankChatManage() *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{RerankModelID: "rr-1", RerankThreshold: 0.3},
		PipelineState: types.PipelineState{
			RewriteQuery: "do schools give students yearly psychological screening",
			RerankResult: []*types.SearchResult{
				{
					ID:      "fresh-1",
					Content: "implementation rules of the provincial school safety regulation notice",
					Score:   0.7,
				},
			},
			History: []*types.History{{
				KnowledgeReferences: []*types.SearchResult{
					{
						ID:    "hist-food",
						Score: 0.8,
						Content: "school food safety and nutrition management regulation: schools must provide " +
							"safe meals to students and run yearly canteen inspections",
					},
					{
						ID:    "hist-mental",
						Score: 0.8,
						Content: "mental health plan: schools give students yearly psychological screening " +
							"and keep records confidential",
					},
				},
			}},
		},
	}
}

func mergeResultIDs(results []*types.SearchResult) []string {
	ids := make([]string, 0, len(results))
	for _, r := range results {
		ids = append(ids, r.ID)
	}
	return ids
}

// A history reference that only shares generic domain words with the follow-up
// query must not enter the merged results once a rerank model scores it far
// below the threshold, while a genuinely relevant prior reference stays (#5613).
func TestInjectHistoryResultsReranksHistoryCandidates(t *testing.T) {
	plugin := &PluginMerge{modelService: &rerankOnlyModelService{
		reranker: &contentScoredReranker{},
	}}
	cm := historyRerankChatManage()

	if err := plugin.OnEvent(context.Background(), types.CHUNK_MERGE, cm,
		func() *PluginError { return nil }); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	ids := mergeResultIDs(cm.MergeResult)
	for _, id := range ids {
		if id == "hist-food" {
			t.Fatalf("history reference hist-food was injected after rerank scored it below the threshold: %v", ids)
		}
	}
	if !containsIDString(ids, "hist-mental") {
		t.Fatalf("relevant history reference hist-mental missing from merged results: %v", ids)
	}
	if !containsIDString(ids, "fresh-1") {
		t.Fatalf("current retrieval result fresh-1 missing from merged results: %v", ids)
	}
}

// Without a rerank model configured the token-overlap filter keeps deciding,
// so the legacy injection behavior is unchanged for those deployments.
func TestInjectHistoryResultsWithoutRerankModelKeepsLegacyFilter(t *testing.T) {
	plugin := &PluginMerge{}
	cm := historyRerankChatManage()
	cm.RerankModelID = ""

	if err := plugin.OnEvent(context.Background(), types.CHUNK_MERGE, cm,
		func() *PluginError { return nil }); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	ids := mergeResultIDs(cm.MergeResult)
	if !containsIDString(ids, "hist-food") || !containsIDString(ids, "hist-mental") {
		t.Fatalf("legacy token-overlap injection changed without a rerank model: %v", ids)
	}
}

// A failing rerank call degrades to the legacy filter instead of dropping all
// history references, mirroring how the rerank stage itself degrades.
func TestInjectHistoryResultsModelFailureKeepsLegacyFilter(t *testing.T) {
	plugin := &PluginMerge{modelService: &rerankOnlyModelService{
		reranker: &contentScoredReranker{err: errors.New("model down")},
	}}
	cm := historyRerankChatManage()

	if err := plugin.OnEvent(context.Background(), types.CHUNK_MERGE, cm,
		func() *PluginError { return nil }); err != nil {
		t.Fatalf("OnEvent: %v", err)
	}

	ids := mergeResultIDs(cm.MergeResult)
	if !containsIDString(ids, "hist-food") || !containsIDString(ids, "hist-mental") {
		t.Fatalf("model failure must fall back to the token-overlap filter: %v", ids)
	}
}

func containsIDString(ids []string, want string) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}
