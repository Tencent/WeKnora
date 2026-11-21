package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// MessageFeedbackService records answer-to-chunk links and applies
// like/dislike votes to those chunks.
type MessageFeedbackService interface {
	// RecordAnswerChunks persists the knowledge chunks an assistant reply cited.
	// Calling it again for the same reply is safe.
	RecordAnswerChunks(ctx context.Context, message *types.Message) error
	// Submit stores the caller's vote and attributes it to every linked chunk.
	Submit(
		ctx context.Context, sessionID, messageID string, req *types.MessageFeedbackRequest,
	) (*types.MessageFeedbackView, error)
	// Attach loads the caller's current vote onto each assistant message.
	Attach(ctx context.Context, messages []*types.Message)
	// ChunkStats returns the admin view of one chunk.
	ChunkStats(ctx context.Context, chunkID string) (*types.ChunkFeedbackStats, error)
	// ListWeightLogs returns the newest weight-change rows for a chunk.
	ListWeightLogs(ctx context.Context, chunkID string, limit int) ([]*types.ChunkWeightLog, error)
	// ResetChunk clears vote tallies and restores a neutral recall weight.
	ResetChunk(ctx context.Context, chunkID string) (*types.Chunk, error)
	// ListKnowledgeChunks lists a document's chunks, optionally filtered by
	// approval rate or the needs-optimization flag.
	ListKnowledgeChunks(
		ctx context.Context,
		knowledgeID string,
		page *types.Pagination,
		chunkTypes []types.ChunkType,
		filter types.ChunkFeedbackFilter,
	) (*types.PageResult, error)
}
