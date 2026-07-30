package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkFeedbackServiceInterface defines the interface for chunk feedback operations
type ChunkFeedbackServiceInterface interface {
	// CreateMessageChunkRelations 创建问答回复与片段的关联关系
	CreateMessageChunkRelations(ctx context.Context, messageID, sessionID string, chunks []types.SearchResult, tenantID uint64) error

	// SubmitFeedback 提交用户反馈
	SubmitFeedback(ctx context.Context, userID, sessionID, messageID string, feedbackType types.FeedbackType, dislikeReason *types.DislikeReason, dislikeReasonDetail *string, tenantID uint64) error

	// GetChunkStats 获取片段统计信息
	GetChunkStats(ctx context.Context, chunkID string, tenantID uint64) (*types.ChunkStatsDetail, error)

	// ListChunksByStats 按统计信息筛选片段
	ListChunksByStats(ctx context.Context, kbID string, params *ListChunksByStatsParams, tenantID uint64) ([]*types.Chunk, int64, error)

	// GetChunkWeightLogs 获取片段权重变更日志
	GetChunkWeightLogs(ctx context.Context, chunkID string, limit, offset int, tenantID uint64) ([]*types.ChunkWeightLog, int64, error)

	// ResetChunkFeedback 重置片段的评价数据和权重
	ResetChunkFeedback(ctx context.Context, chunkID string, operatorID string, tenantID uint64) error

	// AdjustChunkWeight 调整片段权重
	AdjustChunkWeight(ctx context.Context, chunkID string, triggerType types.TriggerType, triggerReason string, feedbackID *string, tenantID uint64) error

	// GetChunkFeedbackConfig 获取配置
	GetChunkFeedbackConfig(ctx context.Context, key types.ChunkFeedbackConfigKey, tenantID uint64) (string, error)

	// UpdateChunkFeedbackConfig 更新配置
	UpdateChunkFeedbackConfig(ctx context.Context, key types.ChunkFeedbackConfigKey, value string, tenantID uint64) error

	// GetFeedbackSummary 获取知识库反馈汇总统计
	GetFeedbackSummary(ctx context.Context, kbID string, tenantID uint64) (*types.ChunkFeedbackSummary, error)

	// BatchAdjustWeights 批量调整权重
	BatchAdjustWeights(ctx context.Context, kbID string, tenantID uint64) error
}

// ListChunksByStatsParams defines parameters for listing chunks by statistics
type ListChunksByStatsParams struct {
	Keyword             string
	MinLikeRate         *float64
	MaxLikeRate         *float64
	PendingOptimization *bool
	SortBy             string // like_count, dislike_count, like_rate, recall_weight, related_session_count
	SortOrder          string // asc, desc
	Page               int
	PageSize           int
}
