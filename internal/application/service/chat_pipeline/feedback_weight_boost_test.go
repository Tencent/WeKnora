package chatpipeline

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestFeedbackWeightBoostMultipliesScoresAndStableSorts(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}))
	require.NoError(t, db.Create([]*types.Chunk{
		{ID: "neutral", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "k1", Content: "neutral", RecallWeight: 1.0},
		{ID: "boosted", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "k1", Content: "boosted", RecallWeight: 1.2},
		{ID: "history", TenantID: 1, KnowledgeBaseID: "kb", KnowledgeID: "k1", Content: "history", RecallWeight: 1.2},
	}).Error)

	plugin := &PluginFeedbackWeightBoost{chunkRepo: repository.NewChunkRepository(db)}
	chatManage := &types.ChatManage{
		PipelineState: types.PipelineState{
			RerankResult: []*types.SearchResult{
				{ID: "neutral", Score: 0.80},
				{ID: "boosted", Score: 0.70},
				{ID: "history", Score: 0.90, MatchType: types.MatchTypeHistory},
			},
		},
	}

	errPlugin := plugin.OnEvent(context.Background(), types.CHUNK_RERANK, chatManage, func() *PluginError { return nil })
	require.Nil(t, errPlugin)
	require.Equal(t, "history", chatManage.RerankResult[0].ID)
	require.Equal(t, "boosted", chatManage.RerankResult[1].ID)
	require.InDelta(t, 0.84, chatManage.RerankResult[1].Score, 0.000001)
	require.Equal(t, "1.2000", chatManage.RerankResult[1].Metadata["feedback_recall_weight"])
	require.Equal(t, "neutral", chatManage.RerankResult[2].ID)
}
