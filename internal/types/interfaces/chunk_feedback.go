package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type MessageChunkRefRepository interface {
	UpsertBatch(ctx context.Context, refs []*types.MessageChunkRef) error
	ListChunkIDsByMessage(ctx context.Context, tenantID uint64, messageID string) ([]string, error)
}

type UserMessageFeedbackRepository interface {
	GetByUserMessage(ctx context.Context, tenantID uint64, userID, messageID string) (*types.UserMessageFeedback, error)
	Upsert(ctx context.Context, feedback *types.UserMessageFeedback) error
	DeleteByUserMessage(ctx context.Context, tenantID uint64, userID, messageID string) error
}

type ChunkRecallWeightLogRepository interface {
	Create(ctx context.Context, log *types.ChunkRecallWeightLog) error
	ListByChunkID(ctx context.Context, tenantID uint64, chunkID string, limit int) ([]*types.ChunkRecallWeightLog, error)
}

type ChunkFeedbackService interface {
	PersistMessageChunkRefs(ctx context.Context, message *types.Message) error
	SetMessageFeedback(ctx context.Context, sessionID, messageID, userID string, tenantID uint64, vote types.UserMessageFeedbackVote, dislikeReason string) error
	CancelMessageFeedback(ctx context.Context, sessionID, messageID, userID string, tenantID uint64) error
	ListKnowledgeBaseChunkFeedbackStats(ctx context.Context, tenantID uint64, knowledgeBaseID string, pagination *types.Pagination, maxPositiveRate *float64, needsOptimization *bool) (*types.PageResult, error)
	ListChunkRecallWeightLogs(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID string, limit int) ([]*types.ChunkRecallWeightLog, error)
	ResetChunkFeedback(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID, userID string) error
	UpdateChunkWeight(ctx context.Context, tenantID uint64, knowledgeBaseID, chunkID, userID string, weight float64) error
}
