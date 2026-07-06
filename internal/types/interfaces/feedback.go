package interfaces

import (
	"context"

	"github.com/Tencent/WeKnora/internal/types"
)

// FeedbackRepository defines the data-access interface for message feedback
// and message-chunk reference records.
type FeedbackRepository interface {
	// GetFeedback retrieves the current feedback row for a (user, message) pair.
	// Returns nil, nil when no feedback exists.
	GetFeedback(ctx context.Context, userID, messageID string) (*types.MessageFeedback, error)

	// UpsertFeedback inserts or replaces the feedback row for a (user, message) pair.
	UpsertFeedback(ctx context.Context, fb *types.MessageFeedback) error

	// DeleteFeedback removes the feedback row for a (user, message) pair.
	DeleteFeedback(ctx context.Context, userID, messageID string) error

	// EnsureChunkRefs populates message_chunk_refs for the given message if not
	// already present. It extracts chunk IDs from the message's KnowledgeReferences
	// and inserts normalized rows. Idempotent.
	EnsureChunkRefs(ctx context.Context, msg *types.Message) error

	// ListChunkIDsByMessage returns all chunk IDs cited by a message.
	ListChunkIDsByMessage(ctx context.Context, messageID string) ([]string, error)

	// CountDistinctSessionsByChunk returns the number of distinct sessions that
	// cited the given chunk.
	CountDistinctSessionsByChunk(ctx context.Context, tenantID uint64, chunkID string) (int64, error)

	// AggregateDislikeReasonsByChunk returns dislike reason code → count for a chunk.
	AggregateDislikeReasonsByChunk(ctx context.Context, tenantID uint64, chunkID string) ([]types.DislikeReasonCount, error)

	// CountFeedbackByChunk returns the total feedback count (likes + dislikes)
	// attributed to a chunk via its message refs.
	CountFeedbackByChunk(ctx context.Context, tenantID uint64, chunkID string) (int64, error)
}

// ChunkWeightLogRepository defines the data-access interface for chunk weight
// change audit logs.
type ChunkWeightLogRepository interface {
	// CreateLog inserts a new weight change log entry.
	CreateLog(ctx context.Context, log *types.ChunkWeightLog) error

	// ListLogsByChunk returns weight change logs for a specific chunk, newest first.
	ListLogsByChunk(ctx context.Context, tenantID uint64, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)

	// ListLogsByTenant returns weight change logs across all chunks in a tenant.
	ListLogsByTenant(ctx context.Context, tenantID uint64, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)
}

// FeedbackService defines the business-logic interface for the like/dislike
// feedback feature.
type FeedbackService interface {
	// SubmitFeedback records a user's like/dislike/cancel on a message,
	// attributes the feedback to all cited chunks, updates chunk counters,
	// recalculates approval rate and recall weight, and logs weight changes.
	SubmitFeedback(ctx context.Context, req *types.FeedbackRequest) (*types.MessageFeedback, error)

	// GetFeedback retrieves the current feedback for a (user, message) pair.
	GetFeedback(ctx context.Context, messageID string) (*types.MessageFeedback, error)

	// GetChunkFeedbackStats returns aggregate feedback statistics for a chunk.
	GetChunkFeedbackStats(ctx context.Context, chunkID string) (*types.ChunkFeedbackStats, error)

	// ListChunkFeedbackStats returns paginated chunk feedback statistics with
	// optional filtering by approval rate range and needs_optimization flag.
	ListChunkFeedbackStats(ctx context.Context, kbID string, page, pageSize int, minApproval, maxApproval float64, needsOptimizationOnly bool) ([]*types.ChunkFeedbackStats, int64, error)

	// ListWeightLogs returns paginated weight change logs for a chunk.
	ListWeightLogs(ctx context.Context, chunkID string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)

	// ListAllWeightLogs returns paginated weight change logs across all chunks.
	ListAllWeightLogs(ctx context.Context, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)

	// AdminResetChunkFeedback resets a chunk's like/dislike counts, approval
	// rate, recall weight, and needs_optimization flag to defaults. Logs the reset.
	AdminResetChunkFeedback(ctx context.Context, chunkID string, adminUserID string) error

	// AdminSetChunkWeight manually sets a chunk's recall weight. Logs the change.
	AdminSetChunkWeight(ctx context.Context, chunkID string, weight float64, adminUserID string) error

	// GetThresholds returns the current feedback weight-adjustment thresholds.
	GetThresholds(ctx context.Context) *types.FeedbackThresholds
}

// ChunkFeedbackStatsRepository defines the data-access interface for chunk
// feedback statistics queries.
type ChunkFeedbackStatsRepository interface {
	// ListChunkFeedbackStats returns paginated chunks with feedback stats.
	ListChunkFeedbackStats(ctx context.Context, tenantID uint64, kbID string, page, pageSize int, minApproval, maxApproval float64, needsOptimizationOnly bool) ([]*types.ChunkFeedbackStats, int64, error)

	// GetChunkFeedbackStats returns feedback stats for a single chunk.
	GetChunkFeedbackStats(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackStats, error)
}
