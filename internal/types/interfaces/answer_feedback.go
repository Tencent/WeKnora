package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// AnswerFeedbackService records which chunks an answer cited, attributes
// like/dislike ratings to those chunks, and serves the chunk-level admin view.
type AnswerFeedbackService interface {
	// RecordAnswerChunks persists answer-to-chunk links. Repeated calls for
	// the same message and chunk are ignored.
	RecordAnswerChunks(ctx context.Context, sessionID, messageID string, refs []types.AnswerChunkRef) error
	// Submit stores the caller's rating (like, dislike, or none to cancel)
	// and updates every linked chunk.
	Submit(ctx context.Context, sessionID, messageID, rating, reason string) (*types.MessageFeedbackState, error)
	// Get returns the caller's rating for one message. A message with no
	// rating returns a none state.
	Get(ctx context.Context, sessionID, messageID string) (*types.MessageFeedbackState, error)
	// ListForMessages returns the caller's ratings keyed by message ID.
	// Messages without a rating are absent.
	ListForMessages(ctx context.Context, messageIDs []string) (map[string]*types.MessageFeedbackState, error)
	// ListChunkFeedback lists chunk stats for one knowledge base.
	ListChunkFeedback(
		ctx context.Context, knowledgeBaseID string, filter types.ChunkFeedbackListFilter,
	) (*types.ChunkFeedbackPage, error)
	// GetChunkFeedback returns stats, dislike reasons, citing sessions, and
	// weight logs for one chunk in the knowledge base.
	GetChunkFeedback(ctx context.Context, knowledgeBaseID, chunkID string) (*types.ChunkFeedbackDetail, error)
	// ResetChunkFeedback clears one chunk's counts, approval rate, recall
	// weight, and optimization flag, and writes a reset log.
	ResetChunkFeedback(ctx context.Context, knowledgeBaseID, chunkID string) (*types.ChunkFeedbackDetail, error)
}
