package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// MessageFeedbackService is the service-layer surface for the like/dislike
// answer feedback path required by issue #1248. It coordinates the
// repository write path, message-level chunk attribution, the per-KB
// statistics surface and the recall-weight recompute entry points.
type MessageFeedbackService interface {
	// UpsertFeedback validates and applies one rating mutation for the
	// current caller. rating "none" cancels an existing rating. The
	// returned feedback is populated from the persisted state.
	UpsertFeedback(
		ctx context.Context,
		sessionID string,
		messageID string,
		rating string,
		reasons []string,
		comment string,
	) (*types.MessageFeedback, error)

	// AttachUserFeedback stamps Message.UserFeedback for the current caller
	// onto a batch of loaded messages. Never fatal; logs and continues.
	AttachUserFeedback(ctx context.Context, messages []*types.Message)

	// RecordMessageReferences persists the answer->chunk reference facts of
	// a completed assistant message. Idempotent via the (message_id,
	// chunk_id) unique index and safe to call from inside a transaction.
	RecordMessageReferences(ctx context.Context, msg *types.Message) error

	// ListChunkStats returns the paged per-chunk feedback statistics of one
	// knowledge base, gated on KB ownership. Filters supported via the
	// optional query struct (low-quality chunk listing, sort, pagination).
	ListChunkStats(
		ctx context.Context,
		kbID string,
		query *ChunkFeedbackStatsQuery,
	) (*types.PageResult, error)

	// ListWeightLogs returns the paged recall-weight change audit of one KB,
	// optionally filtered to a single chunk. KB-owner / admin gated.
	ListWeightLogs(
		ctx context.Context,
		kbID string,
		chunkID string,
		p *types.Pagination,
	) (*types.PageResult, error)

	// ResetKnowledgeBaseFeedback advances the KB's feedback epoch so all
	// pre-reset ratings are excluded from chunk stats, and restores
	// neutral chunk counters. Returns the number of chunks reset.
	ResetKnowledgeBaseFeedback(ctx context.Context, kbID string) (int64, error)

	// RecomputeTenantFeedbackWeights refreshes all stored feedback weights
	// of a tenant after its retrieval config changed. Returns the number of
	// chunks whose weight actually changed. Detects concurrent config saves
	// and aborts silently if the in-flight save has been superseded.
	RecomputeTenantFeedbackWeights(ctx context.Context, tenantID uint64) (int64, error)
}

// ChunkFeedbackStatsQuery is the optional filter + pagination surface for
// the per-KB feedback stats listing. A nil query means "all chunks".
type ChunkFeedbackStatsQuery struct {
	Pagination *types.Pagination
	// SortBy selects the ordering. Supported: "" (default = positive_rate asc),
	// "positive_rate_asc", "positive_rate_desc", "feedback_count_desc",
	// "last_feedback_desc".
	SortBy string
	// LowQualityOnly restricts results to chunks whose positive rate is at
	// or below NeedsOptimizationThreshold (or whose positive rate is below
	// the penalty threshold when the flag is set).
	LowQualityOnly bool
	// Keyword restricts the result set to chunks whose content preview
	// contains the substring (case-insensitive). Empty disables.
	Keyword string
	// KnowledgeID further restricts to a single knowledge document.
	KnowledgeID string
}

// MessageFeedbackRepository is the storage-layer surface for the same
// feedback path. Implementations must keep UpsertFeedback atomic: the
// feedback row mutation, the per-chunk counter adjustments and the derived
// weight updates happen in a single transaction so a process crash mid-write
// cannot leave dangling counters.
type MessageFeedbackRepository interface {
	// SyncMessageChunkRefs idempotently inserts answer->chunk reference rows.
	SyncMessageChunkRefs(ctx context.Context, refs []types.MessageChunkReference) error
	// ListChunkRefsByMessage returns the reference rows of one message.
	ListChunkRefsByMessage(ctx context.Context, messageID string) ([]types.MessageChunkReference, error)
	// ListChunkRefsByChunkIDs returns every reference row pointing at any of
	// the supplied chunk IDs. Used for cascade delete accounting.
	ListChunkRefsByChunkIDs(ctx context.Context, chunkIDs []string) ([]types.MessageChunkReference, error)
	// ListDistinctKnowledgeBaseIDsByChunkIDs returns the KB IDs touched by
	// any reference row of the supplied chunk IDs. Used when a chunk is
	// deleted so we can reaggregate dependent KBs.
	ListDistinctKnowledgeBaseIDsByChunkIDs(ctx context.Context, chunkIDs []string) ([]string, error)
	// ListRatingsByMessageIDs returns the caller's current rating for a batch
	// of messages, indexed by message ID. Used by AttachUserFeedback.
	ListRatingsByMessageIDs(
		ctx context.Context,
		userID string,
		messageIDs []string,
	) (map[string]*types.MessageFeedbackView, error)

	// UpsertFeedback applies one rating mutation atomically inside its
	// transaction. See the implementation for the locking protocol that
	// prevents lost updates under concurrent submits. Returns the previous
	// rating, or "" if no prior feedback existed.
	UpsertFeedback(
		ctx context.Context,
		feedback *types.MessageFeedback,
		refs []types.MessageChunkReference,
	) (string, error)

	// ListChunkStats returns the paged feedback stats of one KB. The reset
	// epoch is supplied so feedback rows older than the epoch are excluded
	// from the aggregates.
	ListChunkStats(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		resetAt *time.Time,
		query *ChunkFeedbackStatsQuery,
	) ([]types.ChunkFeedbackStat, int64, error)

	// ListWeightLogs returns the paged recall-weight change audit of one KB,
	// optionally filtered to a single chunk.
	ListWeightLogs(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		chunkID string,
		p *types.Pagination,
	) ([]types.ChunkWeightLog, int64, error)

	// ResetKnowledgeBaseFeedback advances the KB's feedback epoch and
	// restores neutral chunk feedback state.
	ResetKnowledgeBaseFeedback(ctx context.Context, tenantID uint64, kbID string) (int64, error)

	// RecomputeFeedbackWeights re-derives every chunk's recall weight under
	// the supplied retrieval configs (keyed by knowledge_base_id; an empty
	// key falls back to the chunk's tenant default). The fingerprint is
	// compared right before commit so a slow recomputation of an older
	// config save aborts with ErrFeedbackRecomputeStale instead of
	// overwriting the newer save. Returns the number of chunks whose
	// weight actually changed.
	RecomputeFeedbackWeights(
		ctx context.Context,
		tenantID uint64,
		configsByKB map[string]*types.RetrievalConfig,
		expectedFingerprint string,
	) (int64, error)
}