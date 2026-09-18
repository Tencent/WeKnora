package tools

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestNormalizeSearchMode(t *testing.T) {
	for in, want := range map[string]string{
		"": SearchModeHybrid, "hybrid": SearchModeHybrid,
		" Semantic ": SearchModeSemantic, "KEYWORD": SearchModeKeyword,
	} {
		got, err := normalizeSearchMode(in)
		if err != nil || got != want {
			t.Errorf("normalizeSearchMode(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
	if _, err := normalizeSearchMode("regex"); err == nil {
		t.Fatal("unknown mode must be rejected")
	}
}

func TestClampSearchLimit(t *testing.T) {
	if got := clampSearchLimit(0); got != searchKnowledgeDefaultLimit {
		t.Fatalf("default limit = %d", got)
	}
	if got := clampSearchLimit(500); got != searchKnowledgeMaxLimit {
		t.Fatalf("max limit = %d", got)
	}
	if got := clampSearchLimit(7); got != 7 {
		t.Fatalf("explicit limit = %d", got)
	}
}

func kbWithIndexes(id string, vector, keyword bool, kbType string) *types.KnowledgeBase {
	kb := &types.KnowledgeBase{ID: id, Type: kbType}
	kb.IndexingStrategy.VectorEnabled = vector
	kb.IndexingStrategy.KeywordEnabled = keyword
	return kb
}

func TestSearchModeUnavailableReason(t *testing.T) {
	vectorOnly := []*types.KnowledgeBase{kbWithIndexes("kb-v", true, false, "")}
	keywordOnly := []*types.KnowledgeBase{kbWithIndexes("kb-k", false, true, "")}
	faqOnly := []*types.KnowledgeBase{kbWithIndexes("kb-faq", true, true, types.KnowledgeBaseTypeFAQ)}
	wikiOnly := []*types.KnowledgeBase{kbWithIndexes("kb-w", false, false, "")}

	msg := searchModeUnavailableReason(SearchModeKeyword, vectorOnly)
	if !strings.Contains(msg, "keyword mode is unavailable") {
		t.Fatalf("keyword on vector-only KB: %q", msg)
	}
	msg = searchModeUnavailableReason(SearchModeKeyword, faqOnly)
	if !strings.Contains(msg, "keyword mode is unavailable") {
		t.Fatalf("FAQ bases have no keyword index: %q", msg)
	}
	msg = searchModeUnavailableReason(SearchModeSemantic, keywordOnly)
	if !strings.Contains(msg, "semantic mode is unavailable") {
		t.Fatalf("semantic on keyword-only KB: %q", msg)
	}
	if msg := searchModeUnavailableReason(SearchModeHybrid, wikiOnly); msg == "" {
		t.Fatal("hybrid on a wiki-only KB must be refused")
	}
	if msg := searchModeUnavailableReason(SearchModeHybrid, keywordOnly); msg != "" {
		t.Fatalf("hybrid works with any chunk index, got %q", msg)
	}
	if msg := searchModeUnavailableReason(SearchModeKeyword, nil); msg != "" {
		t.Fatalf("an unknown KB list must not refuse the call, got %q", msg)
	}
}

func TestSearchKnowledgeRejectsMissingQueryAndBadMode(t *testing.T) {
	tool := &SearchKnowledgeTool{BaseTool: searchKnowledgeTool}
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"  "}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "query is required") {
		t.Fatalf("blank query: res=%+v err=%v", res, err)
	}
	res, err = tool.Execute(context.Background(), json.RawMessage(`{"query":"x","mode":"grep"}`))
	if err == nil || res.Success || !strings.Contains(res.Error, "mode must be one of") {
		t.Fatalf("bad mode: res=%+v err=%v", res, err)
	}
}

func TestSearchKnowledgeRejectsOutOfScopeKnowledgeBase(t *testing.T) {
	tool := NewSearchKnowledgeTool(nil, nil, nil, types.SearchTargets{{
		Type: types.SearchTargetTypeKnowledgeBase, KnowledgeBaseID: "kb-1", TenantID: 1,
	}}, nil, nil)
	res, err := tool.Execute(context.Background(), json.RawMessage(`{"query":"x","knowledge_base_ids":["kb-9"]}`))
	if err == nil || res.Success {
		t.Fatalf("out-of-scope KB must be rejected: res=%+v err=%v", res, err)
	}
}

func TestRetrievalParamsFallBackAndNeverUndercutLimit(t *testing.T) {
	tool := &SearchKnowledgeTool{config: &config.Config{Conversation: &config.ConversationConfig{EmbeddingTopK: 5}}}
	topK, v, k := tool.retrievalParams(12)
	if topK != 12 || v != 0.6 || k != 0.5 {
		t.Fatalf("retrievalParams = %d %.2f %.2f", topK, v, k)
	}
	bare := &SearchKnowledgeTool{}
	if topK, _, _ := bare.retrievalParams(3); topK != searchKnowledgeMaxLimit {
		t.Fatalf("missing config should fall back to %d, got %d", searchKnowledgeMaxLimit, topK)
	}
}

func TestDeduplicateResultsKeepsFirstOccurrenceInOrder(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	in := []*searchResultWithMeta{
		{SearchResult: &types.SearchResult{ID: "c1", KnowledgeID: "d1", ChunkIndex: 0, Content: "alpha", Score: 0.9}},
		// same document + index as c1
		{SearchResult: &types.SearchResult{ID: "c2", KnowledgeID: "d1", ChunkIndex: 0, Content: "other", Score: 0.8}},
		// same content as c1
		{SearchResult: &types.SearchResult{ID: "c3", KnowledgeID: "d2", ChunkIndex: 1, Content: "alpha", Score: 0.7}},
		{SearchResult: &types.SearchResult{ID: "c4", KnowledgeID: "d3", ChunkIndex: 2, Content: "gamma", Score: 0.6}},
		nil,
	}
	out := tool.deduplicateResults(in)
	if len(out) != 2 || out[0].ID != "c1" || out[1].ID != "c4" {
		ids := make([]string, 0, len(out))
		for _, r := range out {
			ids = append(ids, r.ID)
		}
		t.Fatalf("deduplicateResults = %v, want [c1 c4]", ids)
	}
}

func TestFormatOutputEmptyResultIsPlainStatement(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	res := tool.formatOutput(
		context.Background(), nil, []string{"kb-1", "kb-2"}, "why is the sky blue", SearchModeSemantic,
	)
	if !res.Success {
		t.Fatal("empty search is not an error")
	}
	if strings.Contains(res.Output, "CRITICAL") || strings.Contains(res.Output, "DO NOT") {
		t.Fatalf("empty result must not carry behavioural instructions: %q", res.Output)
	}
	if !strings.Contains(res.Output, "No matching chunks") || !strings.Contains(res.Output, "mode=semantic") {
		t.Fatalf("empty result statement = %q", res.Output)
	}
	if res.Data["display_type"] != "search_results" || res.Data["count"] != 0 ||
		res.Data["mode"] != SearchModeSemantic {
		t.Fatalf("empty result data = %+v", res.Data)
	}
}

func TestFormatOutputRowsCarryHandlesAndSnippets(t *testing.T) {
	tool := &SearchKnowledgeTool{}
	results := []*searchResultWithMeta{
		{
			SearchResult: &types.SearchResult{
				ID: "chunk-1", KnowledgeID: "doc-1", ChunkIndex: 3, KnowledgeTitle: "Guide <v2>",
				Content: "Install the engine, then configure psionic drive parameters.", Score: 0.91,
				KnowledgeCustomMetadata: "region: EU",
			},
			SourceQuery: "psionic drive", QueryType: SearchModeHybrid, KnowledgeBaseID: "kb-1",
		},
	}
	res := tool.formatOutput(context.Background(), results, []string{"kb-1"}, "psionic drive", SearchModeHybrid)
	if !res.Success {
		t.Fatal("format failed")
	}
	rows, ok := res.Data["results"].([]map[string]interface{})
	if !ok || len(rows) != 1 {
		t.Fatalf("results rows = %#v", res.Data["results"])
	}
	row := rows[0]
	if row["chunk_id"] != "chunk-1" || row["knowledge_id"] != "doc-1" || row["chunk_index"] != 3 {
		t.Fatalf("row identity = %+v", row)
	}
	if snippet, _ := row["match_snippet"].(string); !strings.Contains(snippet, "psionic") {
		t.Fatalf("snippet should surface the query term: %q", snippet)
	}
	if res.Data["mode"] != SearchModeHybrid || res.Data["query"] != "psionic drive" {
		t.Fatalf("data = %+v", res.Data)
	}
	if !strings.Contains(res.Output, `<search_results count="1" mode="hybrid">`) ||
		!strings.Contains(res.Output, `title="Guide &lt;v2&gt;"`) ||
		!strings.Contains(res.Output, "<metadata>region: EU</metadata>") {
		t.Fatalf("output = %s", res.Output)
	}
	if strings.Contains(res.Output, "already_seen") || strings.Contains(res.Output, "retrieval_statistics") {
		t.Fatalf("legacy annotations must be gone: %s", res.Output)
	}
}
