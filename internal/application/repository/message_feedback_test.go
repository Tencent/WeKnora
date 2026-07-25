package repository

import (
	"context"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func newFeedbackTestRepository(t *testing.T) (*messageFeedbackRepository, *gorm.DB) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file:"+t.Name()+"?mode=memory&cache=shared"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(`
		CREATE TABLE tenants (
			id INTEGER PRIMARY KEY,
			retrieval_config TEXT,
			deleted_at DATETIME
		);
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE chunks (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			knowledge_base_id VARCHAR(36) NOT NULL,
			knowledge_id VARCHAR(36) NOT NULL,
			content TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			positive_feedback_count INTEGER NOT NULL DEFAULT 0,
			negative_feedback_count INTEGER NOT NULL DEFAULT 0,
			positive_feedback_rate REAL NOT NULL DEFAULT 0,
			recall_weight REAL NOT NULL DEFAULT 1,
			feedback_status VARCHAR(32) NOT NULL DEFAULT 'normal',
			created_at DATETIME,
			updated_at DATETIME,
			deleted_at DATETIME
		);
	`).Error)
	require.NoError(t, db.Exec(`
		CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			title VARCHAR(255),
			deleted_at DATETIME
		);
	`).Error)
	require.NoError(t, db.AutoMigrate(
		&types.MessageChunkReference{},
		&types.MessageFeedback{},
		&types.MessageFeedbackAttribution{},
		&types.ChunkFeedbackWeightLog{},
	))
	require.NoError(t, db.Exec(
		`INSERT INTO tenants(id, retrieval_config) VALUES (?, ?)`,
		1,
		`{"feedback_positive_threshold":0.8,"feedback_negative_threshold":0.5,"feedback_optimization_threshold":0.3,"feedback_boost_weight":1.2,"feedback_reduce_weight":0.8,"feedback_min_count":1}`,
	).Error)
	require.NoError(t, db.Exec(`
		INSERT INTO chunks(
			id, tenant_id, knowledge_base_id, knowledge_id, content, chunk_index,
			recall_weight, feedback_status
		) VALUES ('chunk-1', 1, 'kb-1', 'knowledge-1', 'source content', 0, 1, 'normal')
	`).Error)
	require.NoError(t, db.Exec(`INSERT INTO knowledges(id, title) VALUES ('knowledge-1', 'Source document')`).Error)
	return &messageFeedbackRepository{db: db}, db
}

func TestMessageFeedbackLifecycleRecomputesChunkMetrics(t *testing.T) {
	repo, db := newFeedbackTestRepository(t)
	ctx := context.Background()

	apply := func(index int, actor string, rating int, reason string) {
		message := &types.Message{
			ID:        fmt.Sprintf("message-%d", index),
			SessionID: fmt.Sprintf("session-%d", index),
			RequestID: fmt.Sprintf("request-%d", index),
			KnowledgeReferences: types.References{{
				ID:              "chunk-1",
				KnowledgeBaseID: "kb-1",
				KnowledgeID:     "knowledge-1",
			}},
		}
		require.NoError(t, repo.SyncMessageChunkReferences(ctx, 1, message))
		_, err := repo.ApplyFeedback(ctx, interfaces.MessageFeedbackMutation{
			TenantID:   1,
			ActorID:    actor,
			Message:    message,
			Rating:     rating,
			ReasonCode: reason,
		})
		require.NoError(t, err)
	}

	apply(1, "user-1", types.MessageFeedbackDislike, "inaccurate")
	chunk := loadFeedbackTestChunk(t, db)
	require.Equal(t, int64(0), chunk.PositiveFeedbackCount)
	require.Equal(t, int64(1), chunk.NegativeFeedbackCount)
	require.Equal(t, 0.8, chunk.RecallWeight)
	require.Equal(t, types.ChunkFeedbackStatusPendingOptimization, chunk.FeedbackStatus)

	for i := 2; i <= 5; i++ {
		apply(i, fmt.Sprintf("user-%d", i), types.MessageFeedbackLike, "")
	}
	chunk = loadFeedbackTestChunk(t, db)
	require.Equal(t, int64(4), chunk.PositiveFeedbackCount)
	require.Equal(t, int64(1), chunk.NegativeFeedbackCount)
	require.InDelta(t, 0.8, chunk.PositiveFeedbackRate, 1e-9)
	require.Equal(t, 1.2, chunk.RecallWeight)
	require.Equal(t, types.ChunkFeedbackStatusNormal, chunk.FeedbackStatus)

	stats, err := repo.ListChunkStats(ctx, 1, "kb-1", types.ChunkFeedbackStatsFilter{
		Page: 1, PageSize: 20, SortBy: "positive_rate", SortOrder: "asc",
	})
	require.NoError(t, err)
	require.Equal(t, int64(1), stats.Total)
	require.Len(t, stats.Data, 1)
	require.Equal(t, int64(5), stats.Data[0].RelatedSessionCount)
	require.Equal(t, []types.FeedbackReasonCount{{ReasonCode: "inaccurate", Count: 1}}, stats.Data[0].ReasonCounts)

	message := &types.Message{ID: "message-1", SessionID: "session-1"}
	require.NoError(t, repo.CancelFeedback(ctx, 1, "user-1", message))
	chunk = loadFeedbackTestChunk(t, db)
	require.Equal(t, int64(4), chunk.PositiveFeedbackCount)
	require.Equal(t, int64(0), chunk.NegativeFeedbackCount)
	require.Equal(t, 1.2, chunk.RecallWeight)

	require.NoError(t, repo.ResetChunk(ctx, 1, "kb-1", "chunk-1", "admin-1"))
	chunk = loadFeedbackTestChunk(t, db)
	require.Zero(t, chunk.PositiveFeedbackCount)
	require.Zero(t, chunk.NegativeFeedbackCount)
	require.Zero(t, chunk.PositiveFeedbackRate)
	require.Equal(t, 1.0, chunk.RecallWeight)
	require.Equal(t, types.ChunkFeedbackStatusNormal, chunk.FeedbackStatus)

	var attributionCount int64
	require.NoError(t, db.Model(&types.MessageFeedbackAttribution{}).Count(&attributionCount).Error)
	require.Zero(t, attributionCount)

	var logs []types.ChunkFeedbackWeightLog
	require.NoError(t, db.Order("id ASC").Find(&logs).Error)
	require.NotEmpty(t, logs)
	require.Equal(t, types.ChunkWeightTriggerAdminReset, logs[len(logs)-1].TriggerSource)
	require.Equal(t, "reset", logs[len(logs)-1].TriggerAction)
}

func TestCalculateChunkFeedbackMetricsHonorsMinimumCount(t *testing.T) {
	config := &types.RetrievalConfig{
		FeedbackPositiveThreshold:     0.8,
		FeedbackNegativeThreshold:     0.5,
		FeedbackOptimizationThreshold: 0.3,
		FeedbackBoostWeight:           1.3,
		FeedbackReduceWeight:          0.7,
		FeedbackMinCount:              3,
	}

	rate, weight, status := calculateChunkFeedbackMetrics(0, 1, config)
	require.Zero(t, rate)
	require.Equal(t, 1.0, weight)
	require.Equal(t, types.ChunkFeedbackStatusNormal, status)

	rate, weight, status = calculateChunkFeedbackMetrics(0, 3, config)
	require.Zero(t, rate)
	require.Equal(t, 0.7, weight)
	require.Equal(t, types.ChunkFeedbackStatusPendingOptimization, status)

	rate, weight, status = calculateChunkFeedbackMetrics(4, 1, config)
	require.InDelta(t, 0.8, rate, 1e-9)
	require.Equal(t, 1.3, weight)
	require.Equal(t, types.ChunkFeedbackStatusNormal, status)
}

func loadFeedbackTestChunk(t *testing.T, db *gorm.DB) *types.Chunk {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, db.Where("id = ?", "chunk-1").First(&chunk).Error)
	return &chunk
}
