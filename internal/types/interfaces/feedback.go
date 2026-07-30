package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// FeedbackRepository persists attribution, feedback events, projections, and
// governance history.
type FeedbackRepository interface {
	CompleteAssistantMessageWithReferences(
		ctx context.Context, messageTenantID uint64, message *types.Message,
		references types.References,
	) (bool, error)
	HydrateMessages(ctx context.Context, tenantID uint64, userID string, messages []*types.Message) error
	HydrateChunks(ctx context.Context, chunks []*types.Chunk, optimizationThreshold float64) error
	ListChunkFeedbackStats(
		ctx context.Context,
		scopes []types.ChunkFeedbackScope,
	) ([]types.ChunkFeedbackStat, error)
	ListChunkFeedbackGovernance(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		query *types.ChunkFeedbackListQuery,
	) ([]*types.ChunkFeedbackListItem, int64, error)
	ListChunkFeedbackHistory(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID, chunkID string,
		page *types.Pagination,
	) ([]*types.ChunkFeedbackAudit, int64, error)
	ApplyMessageFeedback(ctx context.Context, input types.ApplyMessageFeedbackInput) (*types.MessageFeedbackState, error)
	ResetChunkFeedback(ctx context.Context, input types.ResetChunkFeedbackInput) error
	GetChunkFeedbackDetails(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetails, error)
	DeleteMessageWithFeedback(ctx context.Context, tenantID uint64, sessionID, messageID, actorUserID string) error
	DeleteSessionMessagesWithFeedback(
		ctx context.Context, tenantID uint64, sessionIDs []string, actorUserID string, deleteSessions bool,
	) error
}

// FeedbackService exposes answer feedback and KB-scoped governance operations.
type FeedbackService interface {
	ApplyMessageFeedback(
		ctx context.Context,
		sessionID, messageID string,
		feedbackType types.FeedbackType,
		reason *types.FeedbackReasonCode,
	) (*types.MessageFeedbackState, error)
	ResetChunkFeedback(ctx context.Context, kbID, chunkID string) error
	GetChunkFeedbackDetails(ctx context.Context, chunkID string) (*types.ChunkFeedbackDetails, error)
	ListChunkFeedback(ctx context.Context, kbID string, query *types.ChunkFeedbackListQuery) (*types.PageResult, error)
	GetChunkFeedbackGovernanceDetails(ctx context.Context, kbID, chunkID string) (*types.ChunkFeedbackDetails, error)
	ListChunkFeedbackHistory(ctx context.Context, kbID, chunkID string, page *types.Pagination) (*types.PageResult, error)
}
