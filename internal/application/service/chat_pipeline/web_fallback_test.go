package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// rerankStub simulates the CHUNK_RERANK stage: it keeps only the candidates
// whose Content is not marked "irrelevant", mirroring how the real rerank
// discards everything below the relevance threshold.
func rerankStub(cm *types.ChatManage) *PluginError {
	cm.RerankResult = nil
	for _, r := range cm.SearchResult {
		if r.Content != "irrelevant" {
			cm.RerankResult = append(cm.RerankResult, r)
		}
	}
	if len(cm.RerankResult) == 0 {
		return ErrSearchNothing
	}
	return nil
}

func kbThenWebManage(candidates ...*types.SearchResult) *types.ChatManage {
	return &types.ChatManage{
		PipelineRequest: types.PipelineRequest{
			EmbeddingTopK: 10,
		},
		PipelineState: types.PipelineState{
			RetrievalPlan: types.RetrievalPlan{Mode: types.RetrievalPlanKBThenWeb},
			SearchResult:  candidates,
		},
	}
}

func TestWebFallback_SkipsForNonKBThenWebPlan(t *testing.T) {
	cm := kbThenWebManage(&types.SearchResult{Content: "irrelevant"})
	cm.RetrievalPlan = types.RetrievalPlan{Mode: types.RetrievalPlanKBOnly}

	webCalled := false
	p := &PluginWebFallback{fetchWeb: func(context.Context, *types.ChatManage) []*types.SearchResult {
		webCalled = true
		return []*types.SearchResult{{Content: "web"}}
	}}

	err := p.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return rerankStub(cm) })
	if err != ErrSearchNothing {
		t.Fatalf("expected original ErrSearchNothing, got %v", err)
	}
	if webCalled {
		t.Fatal("web fallback must not run for non kb_then_web plans")
	}
}

func TestWebFallback_SkipsWhenKBSufficient(t *testing.T) {
	cm := kbThenWebManage(
		&types.SearchResult{Content: "a"},
		&types.SearchResult{Content: "b"},
		&types.SearchResult{Content: "c"},
	)

	webCalled := false
	p := &PluginWebFallback{fetchWeb: func(context.Context, *types.ChatManage) []*types.SearchResult {
		webCalled = true
		return nil
	}}

	err := p.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return rerankStub(cm) })
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if webCalled {
		t.Fatal("web fallback must not run when KB recall is relevant enough")
	}
	if len(cm.RerankResult) != 3 {
		t.Fatalf("expected 3 KB rerank results, got %d", len(cm.RerankResult))
	}
}

func TestWebFallback_TriggersWhenRerankDiscardsAllKB(t *testing.T) {
	// KB recalled candidates but rerank finds them all irrelevant — this is the
	// reported bug scenario that the old count-based gate missed.
	cm := kbThenWebManage(
		&types.SearchResult{ID: "kb-1", Content: "irrelevant"},
		&types.SearchResult{ID: "kb-2", Content: "irrelevant"},
	)

	p := &PluginWebFallback{fetchWeb: func(context.Context, *types.ChatManage) []*types.SearchResult {
		return []*types.SearchResult{{ID: "web-1", Content: "web-answer"}}
	}}

	err := p.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return rerankStub(cm) })
	if err != nil {
		t.Fatalf("unexpected error after web fallback: %v", err)
	}
	if len(cm.RerankResult) != 1 || cm.RerankResult[0].Content != "web-answer" {
		t.Fatalf("expected reranked web result, got %+v", cm.RerankResult)
	}
}

func TestWebFallback_PreservesErrorWhenWebEmpty(t *testing.T) {
	cm := kbThenWebManage(&types.SearchResult{Content: "irrelevant"})

	p := &PluginWebFallback{fetchWeb: func(context.Context, *types.ChatManage) []*types.SearchResult {
		return nil
	}}

	err := p.OnEvent(context.Background(), types.CHUNK_RERANK, cm, func() *PluginError { return rerankStub(cm) })
	if err != ErrSearchNothing {
		t.Fatalf("expected ErrSearchNothing preserved, got %v", err)
	}
}
