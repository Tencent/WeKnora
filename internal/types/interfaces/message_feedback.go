package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

type MessageFeedbackMutation struct {
	TenantID     uint64
	ActorID      string
	Message      *types.Message
	Rating       int
	ReasonCode   string
	ReasonDetail string
}

type MessageFeedbackRepository interface {
	SyncMessageChunkReferences(ctx context.Context, tenantID uint64, message *types.Message) error
	GetByMessageIDs(
		ctx context.Context,
		tenantID uint64,
		actorID string,
		messageIDs []string,
	) (map[string]*types.MessageFeedbackView, error)
	ApplyFeedback(
		ctx context.Context,
		mutation MessageFeedbackMutation,
	) (*types.MessageFeedbackView, error)
	CancelFeedback(
		ctx context.Context,
		tenantID uint64,
		actorID string,
		message *types.Message,
	) error
	ListChunkStats(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		filter types.ChunkFeedbackStatsFilter,
	) (*types.ChunkFeedbackStatsPage, error)
	ListWeightLogs(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		chunkID string,
		limit int,
	) ([]*types.ChunkFeedbackWeightLog, error)
	ResetChunk(
		ctx context.Context,
		tenantID uint64,
		knowledgeBaseID string,
		chunkID string,
		actorID string,
	) error
}

type MessageFeedbackService interface {
	Submit(
		ctx context.Context,
		sessionID string,
		messageID string,
		rating int,
		reasonCode string,
		reasonDetail string,
	) (*types.MessageFeedbackView, error)
	Cancel(ctx context.Context, sessionID string, messageID string) error
	ListChunkStats(
		ctx context.Context,
		knowledgeBaseID string,
		filter types.ChunkFeedbackStatsFilter,
	) (*types.ChunkFeedbackStatsPage, error)
	ListWeightLogs(
		ctx context.Context,
		knowledgeBaseID string,
		chunkID string,
		limit int,
	) ([]*types.ChunkFeedbackWeightLog, error)
	ResetChunk(ctx context.Context, knowledgeBaseID string, chunkID string) error
}
