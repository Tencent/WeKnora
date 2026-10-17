package chatpipeline

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
)

type wikiBoostKBService struct {
	interfaces.KnowledgeBaseService
}

func (wikiBoostKBService) GetKnowledgeBaseByIDOnly(context.Context, string) (*types.KnowledgeBase, error) {
	return &types.KnowledgeBase{IndexingStrategy: types.IndexingStrategy{WikiEnabled: true}}, nil
}

func TestWikiBoostRanksRetrievedWikiPageAboveDocument(t *testing.T) {
	plugin := &PluginWikiBoost{kbService: wikiBoostKBService{}}
	manage := &types.ChatManage{}
	manage.SearchTargets = types.SearchTargets{{KnowledgeBaseID: "wiki-kb"}}
	manage.RerankResult = []*types.SearchResult{
		{ID: "document", ChunkType: types.ChunkTypeText, Score: 0.9},
		{ID: "wiki", ChunkType: types.ChunkTypeWikiPage, Score: 0.8},
	}
	err := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, manage, func() *PluginError { return nil })
	require.Nil(t, err)
	require.Equal(t, "wiki", manage.RerankResult[0].ID)
	require.InDelta(t, 1.04, manage.RerankResult[0].Score, 0.000001)
	require.Equal(t, 0.9, manage.RerankResult[1].Score)
}
