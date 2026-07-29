package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type FeedbackRepository interface {
	CompleteAssistantMessageWithReferences(
		ctx context.Context, messageTenantID uint64, message *types.Message,
		references types.References,
	) (bool, error)
	HydrateMessages(ctx context.Context, tenantID uint64, userID string, messages []*types.Message) error
	HydrateChunks(ctx context.Context, chunks []*types.Chunk, optimizationThreshold float64) error
	ApplyMessageFeedback(ctx context.Context, input types.ApplyMessageFeedbackInput) (*types.MessageFeedbackState, error)
	ResetChunkFeedback(ctx context.Context, input types.ResetChunkFeedbackInput) error
	GetChunkFeedbackDetails(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetails, error)
	DeleteMessageWithFeedback(ctx context.Context, tenantID uint64, sessionID, messageID, actorUserID string) error
	DeleteSessionMessagesWithFeedback(
		ctx context.Context, tenantID uint64, sessionIDs []string, actorUserID string, deleteSessions bool,
	) error
}

type FeedbackService interface {
	ApplyMessageFeedback(ctx context.Context, sessionID, messageID string, feedbackType types.FeedbackType, reason *types.FeedbackReasonCode) (*types.MessageFeedbackState, error)
	ResetChunkFeedback(ctx context.Context, kbID, chunkID string) error
	GetChunkFeedbackDetails(ctx context.Context, chunkID string) (*types.ChunkFeedbackDetails, error)
}
