package repository

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// setupFeedbackTestDB creates an in-memory SQLite database with the tables
// the feedback repository touches. knowledge_bases / knowledges are created
// with minimal hand-written schemas (only the columns the repository reads)
// to avoid AutoMigrate churn on their many JSON columns.
func setupFeedbackTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(
		&types.Chunk{},
		&types.MessageFeedback{},
		&types.MessageChunkReference{},
		&types.ChunkWeightLog{},
	))
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledge_bases (
			id VARCHAR(36) PRIMARY KEY,
			tenant_id INTEGER NOT NULL,
			feedback_reset_at DATETIME,
			deleted_at DATETIME
		)`).Error)
	require.NoError(t, db.Exec(
		`CREATE TABLE knowledges (
			id VARCHAR(36) PRIMARY KEY,
			title VARCHAR(255),
			deleted_at DATETIME
		)`).Error)
	require.NoError(t, db.Exec(
		`CREATE TABLE tenants (
			id INTEGER PRIMARY KEY,
			retrieval_config TEXT,
			deleted_at DATETIME
		)`).Error)
	return db
}

type feedbackFixture struct {
	repo      interfaces.MessageFeedbackRepository
	db        *gorm.DB
	kbID      string
	chunkIDs  []string
	messageID string
	sessionID string
}

func newFeedbackFixture(t *testing.T, chunkCount int) *feedbackFixture {
	t.Helper()
	db := setupFeedbackTestDB(t)
	repo := NewMessageFeedbackRepository(db)

	// Owner tenant 1 with no stored retrieval config → weight computation
	// uses all-defaults (penalty factor 0.9, min samples 3, etc.).
	require.NoError(t, db.Exec(
		"INSERT INTO tenants (id, retrieval_config) VALUES (1, NULL)").Error)

	kbID := uuid.New().String()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id) VALUES (?, 1)", kbID).Error)
	knowledgeID := uuid.New().String()
	require.NoError(t, db.Exec(
		"INSERT INTO knowledges (id, title) VALUES (?, 'doc.pdf')", knowledgeID).Error)

	chunkIDs := make([]string, 0, chunkCount)
	for i := 0; i < chunkCount; i++ {
		chunk := &types.Chunk{
			ID:              uuid.New().String(),
			TenantID:        1,
			KnowledgeBaseID: kbID,
			KnowledgeID:     knowledgeID,
			Content:         "chunk content",
			IsEnabled:       true,
			Flags:           types.ChunkFlagRecommended,
			RecallWeight:    1,
		}
		require.NoError(t, db.Create(chunk).Error)
		chunkIDs = append(chunkIDs, chunk.ID)
	}

	f := &feedbackFixture{
		repo:      repo,
		db:        db,
		kbID:      kbID,
		chunkIDs:  chunkIDs,
		messageID: uuid.New().String(),
		sessionID: uuid.New().String(),
	}
	require.NoError(t, repo.SyncMessageChunkRefs(context.Background(), f.refs(f.messageID)))
	return f
}

func (f *feedbackFixture) refs(messageID string) []types.MessageChunkReference {
	refs := make([]types.MessageChunkReference, 0, len(f.chunkIDs))
	for _, chunkID := range f.chunkIDs {
		refs = append(refs, types.MessageChunkReference{
			MessageID:       messageID,
			SessionID:       f.sessionID,
			ChunkID:         chunkID,
			KnowledgeBaseID: f.kbID,
		})
	}
	return refs
}

func (f *feedbackFixture) upsert(t *testing.T, userID, rating string, reasons []string) string {
	t.Helper()
	fb := &types.MessageFeedback{
		TenantID:  1,
		SessionID: f.sessionID,
		MessageID: f.messageID,
		UserID:    userID,
		Rating:    rating,
		Reasons:   reasons,
	}
	old, err := f.repo.UpsertFeedback(context.Background(), fb, f.refs(f.messageID))
	require.NoError(t, err)
	return old
}

func (f *feedbackFixture) chunk(t *testing.T, id string) *types.Chunk {
	t.Helper()
	var chunk types.Chunk
	require.NoError(t, f.db.First(&chunk, "id = ?", id).Error)
	return &chunk
}

func TestUpsertFeedbackFirstLikeIncrementsCounters(t *testing.T) {
	f := newFeedbackFixture(t, 2)
	old := f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	assert.Equal(t, "", old)
	for _, id := range f.chunkIDs {
		chunk := f.chunk(t, id)
		assert.Equal(t, 1, chunk.LikeCount)
		assert.Equal(t, 0, chunk.DislikeCount)
		assert.Equal(t, 1.0, chunk.PositiveRate)
		assert.Equal(t, 1.0, chunk.RecallWeight, "below min samples keeps neutral weight")
	}
}

func TestUpsertFeedbackRatingSwitch(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	old := f.upsert(t, "u1", types.FeedbackRatingDislike, []string{"inaccurate"})
	assert.Equal(t, types.FeedbackRatingLike, old)

	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 0, chunk.LikeCount)
	assert.Equal(t, 1, chunk.DislikeCount)
	assert.Equal(t, 0.0, chunk.PositiveRate)
}

func TestUpsertFeedbackCancelDeletesRowAndDecrements(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	old := f.upsert(t, "u1", types.FeedbackRatingNone, nil)
	assert.Equal(t, types.FeedbackRatingLike, old)

	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 0, chunk.LikeCount)

	fb, err := f.repo.GetByMessageAndUser(context.Background(), f.messageID, "u1")
	require.NoError(t, err)
	assert.Nil(t, fb, "feedback row must be deleted on cancel")
}

func TestUpsertFeedbackRepeatedRatingIsIdempotent(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 1, chunk.LikeCount, "repeated identical rating must not double count")
}

func TestUpsertFeedbackCountersNeverGoNegative(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	// Force an inconsistent zero counter, then cancel a stored like.
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	require.NoError(t, f.db.Model(&types.Chunk{}).
		Where("id = ?", f.chunkIDs[0]).
		UpdateColumn("like_count", 0).Error)
	f.upsert(t, "u1", types.FeedbackRatingNone, nil)
	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 0, chunk.LikeCount, "guarded decrement must not underflow")
}

func TestUpsertFeedbackWeightAndNeedsOptimization(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	for _, user := range []string{"u1", "u2", "u3"} {
		f.upsert(t, user, types.FeedbackRatingDislike, []string{"inaccurate"})
	}
	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 3, chunk.DislikeCount)
	assert.InDelta(t, 0.9, chunk.RecallWeight, 1e-9, "3 dislikes reach min samples and penalize")
	assert.True(t, chunk.Flags.HasFlag(types.ChunkFlagNeedsOptimization))

	var logs []types.ChunkWeightLog
	require.NoError(t, f.db.Order("id ASC").Find(&logs).Error)
	require.Len(t, logs, 1, "weight changed once (1.0 -> 0.9)")
	assert.Equal(t, types.FeedbackWeightTriggerFeedback, logs[0].TriggerSource)
	assert.Equal(t, 1.0, logs[0].OldWeight)
	assert.InDelta(t, 0.9, logs[0].NewWeight, 1e-9)
}

func TestUpsertFeedbackWithDeletedChunkDoesNotFail(t *testing.T) {
	f := newFeedbackFixture(t, 2)
	require.NoError(t, f.db.Delete(&types.Chunk{}, "id = ?", f.chunkIDs[0]).Error)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	chunk := f.chunk(t, f.chunkIDs[1])
	assert.Equal(t, 1, chunk.LikeCount)
}

func TestResetAdvancesEpochAndOldFeedbackStopsCounting(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	for _, user := range []string{"u1", "u2", "u3"} {
		f.upsert(t, user, types.FeedbackRatingDislike, []string{"outdated"})
	}
	reset, err := f.repo.ResetKnowledgeBaseFeedback(context.Background(), 1, f.kbID)
	require.NoError(t, err)
	assert.Equal(t, int64(1), reset)

	chunk := f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 0, chunk.DislikeCount)
	assert.Equal(t, 1.0, chunk.RecallWeight)
	assert.False(t, chunk.Flags.HasFlag(types.ChunkFlagNeedsOptimization))

	var resetLogs []types.ChunkWeightLog
	require.NoError(t, f.db.Where("trigger_source = ?", types.FeedbackWeightTriggerReset).Find(&resetLogs).Error)
	require.Len(t, resetLogs, 1)
	assert.Equal(t, 1.0, resetLogs[0].NewWeight)

	// A pre-reset rating switching afterwards must be treated as old="":
	// dislike->like adds a like but must NOT decrement the (already zeroed)
	// dislike counter into producing drift.
	// SQLite's CURRENT_TIMESTAMP-second granularity: nudge the epoch back to
	// guarantee updated_at(before reset) < reset_at strictly.
	require.NoError(t, f.db.Exec(
		"UPDATE message_feedbacks SET updated_at = ?", time.Now().UTC().Add(-time.Hour)).Error)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	chunk = f.chunk(t, f.chunkIDs[0])
	assert.Equal(t, 1, chunk.LikeCount)
	assert.Equal(t, 0, chunk.DislikeCount)
}

func TestListChunkStatsSortingFilteringAndAggregation(t *testing.T) {
	f := newFeedbackFixture(t, 2)
	// chunk 0 and 1 both referenced by f.messageID. u1/u2 dislike, u3 likes.
	f.upsert(t, "u1", types.FeedbackRatingDislike, []string{"inaccurate", "outdated"})
	f.upsert(t, "u2", types.FeedbackRatingDislike, []string{"inaccurate"})
	f.upsert(t, "u3", types.FeedbackRatingLike, nil)

	// A second, unrated message in another session also cites chunk 0:
	// SessionCount is a usage stat and must include it.
	otherMsg := uuid.New().String()
	otherSession := uuid.New().String()
	require.NoError(t, f.repo.SyncMessageChunkRefs(context.Background(), []types.MessageChunkReference{{
		MessageID:       otherMsg,
		SessionID:       otherSession,
		ChunkID:         f.chunkIDs[0],
		KnowledgeBaseID: f.kbID,
	}}))

	stats, total, err := f.repo.ListChunkStats(context.Background(), 1, f.kbID, nil,
		&interfaces.ChunkFeedbackStatsQuery{
			Pagination: &types.Pagination{Page: 1, PageSize: 10},
			SortBy:     "dislike_count",
			Order:      "desc",
		})
	require.NoError(t, err)
	assert.Equal(t, int64(2), total)
	require.Len(t, stats, 2)

	byChunk := map[string]*types.ChunkFeedbackStat{}
	for _, stat := range stats {
		byChunk[stat.ChunkID] = stat
	}
	stat0 := byChunk[f.chunkIDs[0]]
	require.NotNil(t, stat0)
	assert.Equal(t, 1, stat0.LikeCount)
	assert.Equal(t, 2, stat0.DislikeCount)
	assert.Equal(t, 2, stat0.DislikeReasons["inaccurate"])
	assert.Equal(t, 1, stat0.DislikeReasons["outdated"])
	assert.Equal(t, 2, stat0.SessionCount, "unrated citing session must count")
	assert.Equal(t, "doc.pdf", stat0.KnowledgeTitle)

	stat1 := byChunk[f.chunkIDs[1]]
	require.NotNil(t, stat1)
	assert.Equal(t, 1, stat1.SessionCount)

	// min_total filter drops both chunks when raised above their totals.
	_, total, err = f.repo.ListChunkStats(context.Background(), 1, f.kbID, nil,
		&interfaces.ChunkFeedbackStatsQuery{
			Pagination: &types.Pagination{Page: 1, PageSize: 10},
			MinTotal:   4,
		})
	require.NoError(t, err)
	assert.Equal(t, int64(0), total)

	// The epoch filter hides pre-reset dislike reasons.
	past := time.Now().UTC().Add(time.Hour)
	stats, _, err = f.repo.ListChunkStats(context.Background(), 1, f.kbID, &past,
		&interfaces.ChunkFeedbackStatsQuery{
			Pagination: &types.Pagination{Page: 1, PageSize: 10},
		})
	require.NoError(t, err)
	for _, stat := range stats {
		assert.Empty(t, stat.DislikeReasons, "pre-epoch ratings must not aggregate")
	}
}

func TestListChunkWeightsReturnsOnlyNonNeutral(t *testing.T) {
	f := newFeedbackFixture(t, 2)
	for _, user := range []string{"u1", "u2", "u3"} {
		f.upsert(t, user, types.FeedbackRatingDislike, nil)
	}
	weights, err := f.repo.ListChunkWeights(context.Background(), f.chunkIDs)
	require.NoError(t, err)
	require.Len(t, weights, 2)
	for _, id := range f.chunkIDs {
		assert.InDelta(t, 0.9, weights[id], 1e-9)
	}

	// Neutral chunks are omitted.
	reset, err := f.repo.ResetKnowledgeBaseFeedback(context.Background(), 1, f.kbID)
	require.NoError(t, err)
	assert.Equal(t, int64(2), reset)
	weights, err = f.repo.ListChunkWeights(context.Background(), f.chunkIDs)
	require.NoError(t, err)
	assert.Empty(t, weights)
}

func TestRecomputeFeedbackWeightsAppliesNewConfigAndLogs(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	for _, user := range []string{"u1", "u2", "u3"} {
		f.upsert(t, user, types.FeedbackRatingDislike, nil)
	}
	newCfg := &types.RetrievalConfig{FeedbackPenaltyFactor: 0.5}
	// The in-transaction stale check re-reads the tenant's stored config, so
	// it must match the config being applied (and its fingerprint).
	require.NoError(t, f.db.Exec("UPDATE tenants SET retrieval_config = ? WHERE id = 1",
		types.RetrievalConfigFingerprint(newCfg)).Error)
	changed, err := f.repo.RecomputeFeedbackWeights(context.Background(), 1,
		map[string]*types.RetrievalConfig{"": newCfg}, types.RetrievalConfigFingerprint(newCfg))
	require.NoError(t, err)
	assert.Equal(t, int64(1), changed)

	chunk := f.chunk(t, f.chunkIDs[0])
	assert.InDelta(t, 0.5, chunk.RecallWeight, 1e-9)

	var configLogs []types.ChunkWeightLog
	require.NoError(t, f.db.Where("trigger_source = ?", types.FeedbackWeightTriggerConfig).Find(&configLogs).Error)
	require.Len(t, configLogs, 1)
	assert.InDelta(t, 0.9, configLogs[0].OldWeight, 1e-9)
	assert.InDelta(t, 0.5, configLogs[0].NewWeight, 1e-9)
}

func TestRecomputeFeedbackWeightsAbortsWhenStale(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	for _, user := range []string{"u1", "u2", "u3"} {
		f.upsert(t, user, types.FeedbackRatingDislike, nil)
	}
	newCfg := &types.RetrievalConfig{FeedbackPenaltyFactor: 0.5}
	// The tenant's stored config (empty here) differs from the fingerprint we
	// pass, simulating a newer save landing mid-recompute → must abort.
	_, err := f.repo.RecomputeFeedbackWeights(context.Background(), 1,
		map[string]*types.RetrievalConfig{"": newCfg},
		types.RetrievalConfigFingerprint(newCfg))
	require.ErrorIs(t, err, types.ErrFeedbackRecomputeStale)

	chunk := f.chunk(t, f.chunkIDs[0])
	assert.InDelta(t, 0.9, chunk.RecallWeight, 1e-9, "aborted recompute must roll back")
}

func TestSyncMessageChunkRefsIsIdempotent(t *testing.T) {
	f := newFeedbackFixture(t, 2)
	require.NoError(t, f.repo.SyncMessageChunkRefs(context.Background(), f.refs(f.messageID)))
	refs, err := f.repo.ListChunkRefsByMessage(context.Background(), f.messageID)
	require.NoError(t, err)
	assert.Len(t, refs, 2, "duplicate sync must not create extra rows")
}

func TestListRatingsByMessageIDs(t *testing.T) {
	f := newFeedbackFixture(t, 1)
	f.upsert(t, "u1", types.FeedbackRatingLike, nil)
	ratings, err := f.repo.ListRatingsByMessageIDs(context.Background(), "u1", []string{f.messageID, "missing"})
	require.NoError(t, err)
	assert.Equal(t, map[string]string{f.messageID: types.FeedbackRatingLike}, ratings)

	ratings, err = f.repo.ListRatingsByMessageIDs(context.Background(), "u2", []string{f.messageID})
	require.NoError(t, err)
	assert.Empty(t, ratings)
}
