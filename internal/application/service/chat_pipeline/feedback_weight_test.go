package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type weightChunkRepo struct {
	interfaces.ChunkRepository
	chunks []*types.Chunk
}

func (r *weightChunkRepo) ListChunksByIDOnly(context.Context, []string) ([]*types.Chunk, error) {
	return r.chunks, nil
}

func TestPluginFeedbackWeightAdjustsRerankScores(t *testing.T) {
	plugin := &PluginFeedbackWeight{chunks: &weightChunkRepo{chunks: []*types.Chunk{
		{ID: "boosted", RecallWeight: 1.2},
		{ID: "penalized", RecallWeight: 0.8},
	}}}
	manage := &types.ChatManage{}
	manage.RerankResult = []*types.SearchResult{
		{ID: "penalized", KnowledgeID: "doc", Score: 1, MatchType: types.MatchTypeKeywords},
		{ID: "boosted", KnowledgeID: "doc", Score: 0.9, MatchType: types.MatchTypeKeywords},
		{ID: "page", Score: 2, MatchType: types.MatchTypeWebSearch, KnowledgeSource: "web_search"},
	}
	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, manage, func() *PluginError { return nil })
	if err != nil {
		t.Fatal(err)
	}
	got := []string{manage.RerankResult[0].ID, manage.RerankResult[1].ID, manage.RerankResult[2].ID}
	if got[0] != "page" || got[1] != "boosted" || got[2] != "penalized" {
		t.Fatalf("order = %v", got)
	}
	if manage.RerankResult[1].Score != 0.9*1.2 || manage.RerankResult[2].Score != 0.8 {
		t.Fatalf("scores = %v %v", manage.RerankResult[1].Score, manage.RerankResult[2].Score)
	}
}
