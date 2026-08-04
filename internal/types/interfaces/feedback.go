package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkFeedbackStatsParams is the filter/pagination input for the admin chunk
// feedback statistics listing.
type ChunkFeedbackStatsParams struct {
	TenantID        uint64
	KnowledgeBaseID string
	KnowledgeID     string
	// MinApprovalRate filters chunks whose approval rate is >= the value.
	MinApprovalRate *float64
	// MaxApprovalRate filters chunks whose approval rate is <= the value.
	MaxApprovalRate   *float64
	NeedsOptimization *bool
	Keyword           string
	Page              int
	PageSize          int
	// SortBy is one of: like_count | dislike_count | approval_rate |
	// recall_weight | updated_at. Defaults to updated_at.
	SortBy string
	// SortOrder is "asc" or "desc" (default desc).
	SortOrder string
}

// ChunkFeedbackRepository persists Q&A answer <-> chunk links, user rating
// records, per-chunk feedback counters, recall-weight audit logs and the
// tenant feedback config.
type ChunkFeedbackRepository interface {
	// RecordMessageChunkLinks upserts message->chunk links idempotently
	// (unique on message_id + chunk_id).
	RecordMessageChunkLinks(ctx context.Context, links []*types.MessageChunkLink) error
	// ListChunkLinksByMessageID returns active chunk links for a message.
	ListChunkLinksByMessageID(ctx context.Context, messageID string) ([]*types.MessageChunkLink, error)
	// ListChunkLinksByMessageIDs returns active chunk links for several messages.
	ListChunkLinksByMessageIDs(ctx context.Context, messageIDs []string) ([]*types.MessageChunkLink, error)

	// GetFeedbackRecord returns the active rating record of a user for a message.
	GetFeedbackRecord(ctx context.Context, userID, messageID string) (*types.ChunkFeedbackRecord, error)
	// UpsertFeedbackRecord creates or updates (restoring when soft-deleted) a
	// user's rating record for a message.
	UpsertFeedbackRecord(ctx context.Context, record *types.ChunkFeedbackRecord) error
	// DeleteFeedbackRecord soft-deletes a user's rating record for a message.
	DeleteFeedbackRecord(ctx context.Context, userID, messageID string) error
	// GetFeedbackRatingsByMessages returns active ratings of a user keyed by message id.
	GetFeedbackRatingsByMessages(ctx context.Context, userID string, messageIDs []string) (map[string]string, error)
	// CountFeedbackByChunk recomputes like/dislike/total counts and the last
	// feedback time for a chunk from its linked records.
	CountFeedbackByChunk(ctx context.Context, tenantID uint64, chunkID string) (like, dislike, total int64, lastAt *time.Time, err error)

	// UpdateChunkFeedbackCounters persists the recomputed like/dislike counts
	// and approval rate for a chunk.
	UpdateChunkFeedbackCounters(ctx context.Context, tenantID uint64, chunkID string, like, dislike int64) error
	// UpdateChunkRecallWeight persists a chunk's recall weight and the
	// needs-optimization flag.
	UpdateChunkRecallWeight(ctx context.Context, tenantID uint64, chunkID string, weight float64, needsOptimization bool) error

	// GetChunkFeedbackStats returns paged per-chunk feedback stats.
	GetChunkFeedbackStats(ctx context.Context, params *ChunkFeedbackStatsParams) ([]*types.ChunkFeedbackStat, int64, error)
	// GetChunkFeedbackDetail returns per-chunk stats plus dislike reason
	// aggregation and related chat sessions.
	GetChunkFeedbackDetail(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetail, error)

	// CreateWeightLog appends a recall-weight change audit row.
	CreateWeightLog(ctx context.Context, log *types.ChunkWeightLog) error
	// ListWeightLogs returns paged weight-change logs.
	ListWeightLogs(ctx context.Context, tenantID uint64, chunkID, source string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)

	// ResetChunkFeedback zeroes counters/approval rate/weight/flag for the
	// given chunks and removes their linked feedback records.
	ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkIDs []string) error
	// UpdateChunkWeightsForReset directly rewrites recall weight (used by the
	// manual weight override and the reset flow).
	UpdateChunkWeights(ctx context.Context, tenantID uint64, chunkIDs []string, weight float64) error

	// GetFeedbackConfig loads the tenant config, returning defaults when absent.
	GetFeedbackConfig(ctx context.Context, tenantID uint64) (*types.ChunkFeedbackConfig, error)
	// UpdateFeedbackConfig upserts the tenant config.
	UpdateFeedbackConfig(ctx context.Context, cfg *types.ChunkFeedbackConfig) error
}

// ChunkFeedbackService exposes the feedback business logic used by handlers
// and by the message/chat pipeline.
type ChunkFeedbackService interface {
	// RecordMessageChunkLinks snapshots the knowledge references of a message
	// into the message_chunk_links table. Safe to call repeatedly.
	RecordMessageChunkLinks(ctx context.Context, message *types.Message) error
	// SubmitFeedback records a user's like/dislike (or rating switch) for an
	// assistant message and attributes it to all linked chunks, updating their
	// counters, approval rate and recall weight.
	SubmitFeedback(ctx context.Context, userID, sessionID, messageID string, rating types.ChunkFeedbackRating, reason string) error
	// CancelFeedback removes a user's rating for a message and re-attributes
	// the remaining ratings to the linked chunks.
	CancelFeedback(ctx context.Context, userID, sessionID, messageID string) error

	// GetMyRating returns the active rating ("like"/"dislike"/"") of a user for a message.
	GetMyRating(ctx context.Context, userID, messageID string) (string, error)
	// GetMyRatingsForMessages returns the active ratings of a user keyed by
	// message id (only messages that have a rating appear in the map).
	GetMyRatingsForMessages(ctx context.Context, userID string, messageIDs []string) (map[string]string, error)

	// GetChunkFeedbackStats returns paged per-chunk feedback stats.
	GetChunkFeedbackStats(ctx context.Context, params *ChunkFeedbackStatsParams) ([]*types.ChunkFeedbackStat, int64, error)
	// GetChunkFeedbackDetail returns the detail view for one chunk.
	GetChunkFeedbackDetail(ctx context.Context, tenantID uint64, chunkID string) (*types.ChunkFeedbackDetail, error)
	// ListWeightLogs returns paged weight-change logs.
	ListWeightLogs(ctx context.Context, tenantID uint64, chunkID, source string, page, pageSize int) ([]*types.ChunkWeightLog, int64, error)
	// ResetChunkFeedback manually zeroes feedback data and restores default
	// weight for the given chunks.
	ResetChunkFeedback(ctx context.Context, tenantID uint64, chunkIDs []string, operatorID string) error
	// GetConfig returns the tenant feedback config.
	GetConfig(ctx context.Context, tenantID uint64) (*types.ChunkFeedbackConfig, error)
	// UpdateConfig upserts the tenant feedback config.
	UpdateConfig(ctx context.Context, cfg *types.ChunkFeedbackConfig) error
}
