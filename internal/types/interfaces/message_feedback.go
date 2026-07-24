package interfaces

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ChunkFeedbackStatsQuery bundles the filter/sort/pagination parameters of
// the per-chunk feedback statistics listing.
type ChunkFeedbackStatsQuery struct {
	Pagination            *types.Pagination
	SortBy                string // like_count | dislike_count | positive_rate | recall_weight | total
	Order                 string // asc | desc
	MinTotal              int
	MinRate               *float64
	MaxRate               *float64
	NeedsOptimizationOnly bool
}

// MessageFeedbackRepository persists answer ratings, answer->chunk reference
// facts and the chunk counters/weights derived from them.
type MessageFeedbackRepository interface {
	// SyncMessageChunkRefs idempotently inserts reference rows (conflicts on
	// (message_id, chunk_id) are ignored).
	SyncMessageChunkRefs(ctx context.Context, refs []types.MessageChunkReference) error
	ListChunkRefsByMessage(ctx context.Context, messageID string) ([]types.MessageChunkReference, error)
	// UpsertFeedback applies a rating mutation in one transaction: it locks
	// the message row (postgres), re-reads each involved KB's feedback epoch
	// and owner tenant under its row lock, reads the owner tenants' retrieval
	// configs through the transaction, writes/deletes the feedback row and
	// adjusts chunk counters, stored positive rates, recall weights and weight
	// logs. rating == "none" deletes the row. Returns the previous rating ("" when
	// none existed).
	UpsertFeedback(
		ctx context.Context,
		feedback *types.MessageFeedback,
		refs []types.MessageChunkReference,
	) (oldRating string, err error)
	GetByMessageAndUser(ctx context.Context, messageID, userID string) (*types.MessageFeedback, error)
	// ListRatingsByMessageIDs returns messageID -> rating for one user.
	ListRatingsByMessageIDs(ctx context.Context, userID string, messageIDs []string) (map[string]string, error)
	ListChunkStats(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		resetAt *time.Time,
		query *ChunkFeedbackStatsQuery,
	) ([]*types.ChunkFeedbackStat, int64, error)
	ListWeightLogs(
		ctx context.Context,
		tenantID uint64,
		kbID string,
		chunkID string,
		p *types.Pagination,
	) ([]*types.ChunkWeightLog, int64, error)
	// ResetKnowledgeBaseFeedback advances the KB's feedback epoch, zeroes the
	// chunk counters/rates, restores neutral weights (logging trigger=reset)
	// and clears needs-optimization flags. Feedback and reference rows are
	// kept. Returns the number of chunks touched.
	ResetKnowledgeBaseFeedback(ctx context.Context, tenantID uint64, kbID string) (int64, error)
	// ListChunkWeights returns chunkID -> recall_weight for weights != 1.
	ListChunkWeights(ctx context.Context, chunkIDs []string) (map[string]float64, error)
	// RecomputeFeedbackWeights re-derives weights/flags of all rated chunks
	// of a tenant from their stored counters using the given per-KB configs,
	// logging changes with trigger=config. expectedFingerprint is compared
	// against the tenant's current config (re-read through the transaction)
	// right before committing; a mismatch aborts with ErrFeedbackRecomputeStale
	// so a slow recomputation cannot overwrite the effects of a newer config save.
	RecomputeFeedbackWeights(
		ctx context.Context,
		tenantID uint64,
		cfgByKB map[string]*types.RetrievalConfig,
		expectedFingerprint string,
	) (int64, error)
}

// MessageFeedbackService orchestrates rating writes (session ownership
// checks, chunk attribution, per-KB policies) and the statistics surfaces.
type MessageFeedbackService interface {
	UpsertFeedback(
		ctx context.Context,
		sessionID string,
		messageID string,
		rating string,
		reasons []string,
		comment string,
	) (*types.MessageFeedback, error)
	// AttachUserFeedback stamps Message.UserFeedback for the current caller.
	// Failures are logged, never fatal for message loading.
	AttachUserFeedback(ctx context.Context, messages []*types.Message)
	// RecordMessageReferences persists the answer->chunk reference facts of a
	// completed assistant message. Idempotent.
	RecordMessageReferences(ctx context.Context, msg *types.Message) error
	ListChunkStats(ctx context.Context, kbID string, query *ChunkFeedbackStatsQuery) (*types.PageResult, error)
	ListWeightLogs(ctx context.Context, kbID string, chunkID string, p *types.Pagination) (*types.PageResult, error)
	ResetKnowledgeBaseFeedback(ctx context.Context, kbID string) (int64, error)
	// RecomputeTenantFeedbackWeights refreshes stored weights after a
	// retrieval config change for the tenant.
	RecomputeTenantFeedbackWeights(ctx context.Context, tenantID uint64) (int64, error)
}
