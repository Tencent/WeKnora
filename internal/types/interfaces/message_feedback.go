package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type MessageFeedbackService interface {
	UpsertFeedback(ctx context.Context, sessionID, messageID string, req *types.MessageFeedbackRequest) (*types.MessageFeedback, error)
	CancelFeedback(ctx context.Context, sessionID, messageID string) error
	RecordMessageChunkReferences(ctx context.Context, message *types.Message) error
}

type MessageFeedbackRepository interface {
	UpsertFeedback(ctx context.Context, feedback *types.MessageFeedback) (*types.MessageFeedback, *types.MessageFeedback, error)
	UpsertFeedbackAndRefreshChunks(ctx context.Context, feedback *types.MessageFeedback, chunkIDs []string, cfg types.ChunkFeedbackConfig) (*types.MessageFeedback, error)
	DeleteFeedback(ctx context.Context, tenantID uint64, sessionID, messageID, userID string) (*types.MessageFeedback, error)
	DeleteFeedbackAndRefreshChunks(ctx context.Context, tenantID uint64, sessionID, messageID, userID string, chunkIDs []string, cfg types.ChunkFeedbackConfig) error
	ListFeedbacksByMessageIDs(ctx context.Context, tenantID uint64, sessionID, userID string, messageIDs []string) ([]*types.MessageFeedback, error)
	CreateMessageChunkReferences(ctx context.Context, refs []*types.MessageChunkReference) error
	ListMessageChunkReferences(ctx context.Context, tenantID uint64, sessionID, messageID string) ([]*types.MessageChunkReference, error)
	ApplyChunkFeedbackDelta(ctx context.Context, tenantID uint64, chunkIDs []string, likeDelta, dislikeDelta int64, messageID string, cfg types.ChunkFeedbackConfig) error
}
