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

func TestResolveKBSearchModesFallsBackPerKnowledgeBase(t *testing.T) {
	vectorOnly := kbWithIndexes("kb-v", true, false, "")
	keywordOnly := kbWithIndexes("kb-k", false, true, "")
	both := kbWithIndexes("kb-b", true, true, "")
	faq := kbWithIndexes("kb-faq", true, true, types.KnowledgeBaseTypeFAQ)
	wikiOnly := kbWithIndexes("kb-w", false, false, "")

	modes, msg := resolveKBSearchModes(SearchModeKeyword, []*types.KnowledgeBase{vectorOnly, both, faq, wikiOnly})
	if msg != "" {
		t.Fatalf("keyword must not be refused while some base can serve it: %q", msg)
	}
	if m := modes["kb-b"]; m.mode != SearchModeKeyword || m.fallback {
		t.Fatalf("keyword-capable base = %+v", m)
	}
	if m := modes["kb-v"]; m.mode != SearchModeSemantic || !m.fallback || m.reason != "no keyword index" {
		t.Fatalf("vector-only base must fall back to semantic: %+v", m)
	}
	if m := modes["kb-faq"]; m.mode != SearchModeSemantic || !m.fallback || !strings.Contains(m.reason, "FAQ") {
		t.Fatalf("FAQ base must fall back to semantic: %+v", m)
	}
	if _, ok := modes["kb-w"]; ok {
		t.Fatal("a wiki-only base has no chunk index and must be left out")
	}

	modes, msg = resolveKBSearchModes(SearchModeKeyword, []*types.KnowledgeBase{faq})
	if msg != "" || modes["kb-faq"].mode != SearchModeSemantic {
		t.Fatalf("an FAQ-only scope must still be searchable: modes=%+v msg=%q", modes, msg)
	}
	modes, _ = resolveKBSearchModes(SearchModeSemantic, []*types.KnowledgeBase{keywordOnly})
	if m := modes["kb-k"]; m.mode != SearchModeKeyword || !m.fallback {
		t.Fatalf("keyword-only base must serve semantic requests with keyword: %+v", m)
	}
	modes, _ = resolveKBSearchModes(SearchModeHybrid, []*types.KnowledgeBase{keywordOnly})
	if m := modes["kb-k"]; m.mode != SearchModeHybrid || m.fallback {
		t.Fatalf("hybrid already adapts per index: %+v", m)
	}
	if _, msg := resolveKBSearchModes(SearchModeHybrid, []*types.KnowledgeBase{wikiOnly}); msg == "" {
		t.Fatal("a scope with no chunk index must be refused")
	}
	if modes, msg := resolveKBSearchModes(SearchModeKeyword, nil); modes != nil || msg != "" {
		t.Fatalf("an unknown KB list must not refuse the call: %+v %q", modes, msg)
	}
}

// When the rerank model rejected every candidate, the statement must not
// read as "nothing was found": repeating the same query in another mode is
// usually wasted, but keyword mode remains the retry for exact terms.
func TestEmptySearchStatementReportsRerankRejection(t *testing.T) {
	data := map[string]interface{}{"mode": SearchModeHybrid, "rerank_rejected": 18}
	msg := emptySearchStatement("Echo open-weight cost", data, 20)
	for _, want := range []string{"found 18 candidate chunks", "Repeating the same query", "mode=keyword"} {
		if !strings.Contains(msg, want) {
			t.Fatalf("statement missing %q: %q", want, msg)
		}
	}
	if strings.Contains(msg, "switching mode will not help") {
		t.Fatalf("statement must not forbid switching to keyword: %q", msg)
	}
	if plain := emptySearchStatement("x", map[string]interface{}{"mode": SearchModeHybrid}, 1); !strings.HasPrefix(
		plain, "No matching chunks") {
		t.Fatalf("no rerank rejection keeps the plain statement: %q", plain)
	}
}

func TestAnnotateModeFallbackReportsEffectiveMode(t *testing.T) {
	all := map[string]kbSearchMode{"kb-faq": {mode: SearchModeSemantic, fallback: true, reason: "FAQ"}}
	data := map[string]interface{}{"mode": SearchModeKeyword}
	annotateModeFallback(data, SearchModeKeyword, all)
	if data["mode"] != SearchModeSemantic || data["requested_mode"] != SearchModeKeyword {
		t.Fatalf("a scope that fell back entirely must report the mode it used: %+v", data)
	}
	fallbacks, _ := data["mode_fallbacks"].([]map[string]interface{})
	if len(fallbacks) != 1 || fallbacks[0]["knowledge_base_id"] != "kb-faq" {
		t.Fatalf("fallbacks = %+v", data["mode_fallbacks"])
	}
	if msg := emptySearchStatement("E1001", data, 1); !strings.Contains(msg, "mode=semantic") ||
		!strings.Contains(msg, "kb-faq was searched with mode=semantic (FAQ)") {
		t.Fatalf("empty statement must explain the fallback: %q", msg)
	}

	partial := map[string]kbSearchMode{
		"kb-b": {mode: SearchModeKeyword},
		"kb-v": {mode: SearchModeSemantic, fallback: true, reason: "no keyword index"},
	}
	data = map[string]interface{}{"mode": SearchModeKeyword}
	annotateModeFallback(data, SearchModeKeyword, partial)
	if data["mode"] != SearchModeKeyword {
		t.Fatalf("a partial fallback keeps the requested mode: %+v", data)
	}
	data = map[string]interface{}{"mode": SearchModeKeyword}
	annotateModeFallback(data, SearchModeKeyword, map[string]kbSearchMode{"kb-b": {mode: SearchModeKeyword}})
	if _, ok := data["mode_fallbacks"]; ok {
		t.Fatalf("no fallback, no annotation: %+v", data)
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
