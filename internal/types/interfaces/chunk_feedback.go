package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// QAReplyChunkRefRepository stores the chunks used to build an assistant reply.
type QAReplyChunkRefRepository interface {
	Create(ctx context.Context, ref *types.QAReplyChunkRef) error
	CreateBatch(ctx context.Context, refs []*types.QAReplyChunkRef) error
	GetByMessageID(ctx context.Context, tenantID uint64, messageID string) ([]*types.QAReplyChunkRef, error)
	GetByChunkID(ctx context.Context, tenantID uint64, chunkID string) ([]*types.QAReplyChunkRef, error)
	DeleteByMessageID(ctx context.Context, tenantID uint64, messageID string) error
	CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error)
}

// ChunkFeedbackRepository stores a user's current vote for an assistant reply.
type ChunkFeedbackRepository interface {
	Create(ctx context.Context, feedback *types.ChunkFeedback) error
	Update(ctx context.Context, feedback *types.ChunkFeedback) error
	Upsert(ctx context.Context, messageID, sessionID, userID string, tenantID uint64, isPositive bool, dislikeReason string) (*types.ChunkFeedback, error)
	GetByMessageID(ctx context.Context, tenantID uint64, messageID string) (*types.ChunkFeedback, error)
	GetByMessageAndUser(ctx context.Context, tenantID uint64, messageID, userID string) (*types.ChunkFeedback, error)
	Delete(ctx context.Context, tenantID uint64, id string) error
	GetDislikeReasonsByChunkIDs(ctx context.Context, tenantID uint64, chunkIDs []string) (map[string][]string, error)
}

// ChunkWeightLogRepository stores recall-weight audit entries for chunks.
type ChunkWeightLogRepository interface {
	Create(ctx context.Context, log *types.ChunkWeightLog) error
	GetByChunkID(ctx context.Context, tenantID uint64, chunkID string, limit int) ([]*types.ChunkWeightLog, error)
	CountByChunkID(ctx context.Context, tenantID uint64, chunkID string) (int64, error)
}
