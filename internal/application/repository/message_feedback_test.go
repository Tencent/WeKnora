package repository

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// setupFeedbackWeightTestDB creates an in-memory SQLite database with the
// chunk and chunk_weight_logs tables plus the needs_optimization column
// (which lives only in the migration, not on the Chunk struct).
func setupFeedbackWeightTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Chunk{}, &types.ChunkWeightLog{}))
	// needs_optimization is a column-only field (not on the Chunk struct)
	// added by the answer_feedback migration. Add it manually so the
	// repository's map-based Updates can write to it.
	require.NoError(t, db.Exec("ALTER TABLE chunks ADD COLUMN needs_optimization INTEGER NOT NULL DEFAULT 0").Error)
	return db
}

func makeFeedbackChunk(kbID string, like, dislike int, weight float64) *types.Chunk {
	return &types.Chunk{
		ID:              uuid.New().String(),
		TenantID:        1,
		KnowledgeBaseID: kbID,
		KnowledgeID:     "doc-1",
		Content:         "test content",
		ChunkType:       "text",
		IsEnabled:       true,
		LikeCount:       like,
		DislikeCount:    dislike,
		RecallWeight:    weight,
	}
}

// needsOptimizationOf reads the raw needs_optimization column for a chunk,
// returning 0/1 since SQLite stores it as INTEGER. The Chunk struct does not
// expose this field, so a raw query is the only way to verify it.
func needsOptimizationOf(t *testing.T, db *gorm.DB, chunkID string) int {
	t.Helper()
	var v int
	require.NoError(t, db.Raw("SELECT needs_optimization FROM chunks WHERE id = ?", chunkID).Scan(&v).Error)
	return v
}

// countWeightLogs returns the number of chunk_weight_logs rows for a chunk.
func countWeightLogs(t *testing.T, db *gorm.DB, chunkID string) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Model(&types.ChunkWeightLog{}).Where("chunk_id = ?", chunkID).Count(&n).Error)
	return n
}

// TestRefreshChunkWeights_EmptyChunkIDsIsNoop ensures an empty chunk ID list
// does not issue queries or write logs.
func TestRefreshChunkWeights_EmptyChunkIDsIsNoop(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, nil, nil, types.FeedbackWeightTriggerFeedback, "fb-1")
	})
	require.NoError(t, err)

	var logCount int64
	require.NoError(t, db.Model(&types.ChunkWeightLog{}).Count(&logCount).Error)
	assert.Equal(t, int64(0), logCount)
}

// TestRefreshChunkWeights_BelowMinSamplesKeepsNeutralWeight verifies that a
// chunk with fewer ratings than the configured minimum keeps the neutral 1.0
// weight and emits no log row. positive_rate is still refreshed.
func TestRefreshChunkWeights_BelowMinSamplesKeepsNeutralWeight(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	chunk := makeFeedbackChunk("kb-1", 1, 0, 1.0) // 1 sample < default min 3
	require.NoError(t, db.Create(chunk).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, []string{chunk.ID}, nil, types.FeedbackWeightTriggerFeedback, "fb-1")
	})
	require.NoError(t, err)

	var updated types.Chunk
	require.NoError(t, db.First(&updated, "id = ?", chunk.ID).Error)
	assert.Equal(t, 1.0, updated.RecallWeight, "weight stays neutral below min samples")
	assert.Equal(t, 1.0, updated.PositiveRate, "positive_rate is refreshed even when weight is unchanged")
	assert.Equal(t, 0, needsOptimizationOf(t, db, chunk.ID), "not flagged below min samples")
	assert.Equal(t, int64(0), countWeightLogs(t, db, chunk.ID), "no log when weight unchanged")
}

// TestRefreshChunkWeights_BoostOnHighLikeRate verifies that a chunk whose
// positive rate crosses the boost threshold gets its recall weight raised to
// the boost factor and a weight log row is written.
func TestRefreshChunkWeights_BoostOnHighLikeRate(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	chunk := makeFeedbackChunk("kb-1", 5, 0, 1.0) // rate=1.0 >= default boost 0.8
	require.NoError(t, db.Create(chunk).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, []string{chunk.ID}, nil, types.FeedbackWeightTriggerFeedback, "fb-boost")
	})
	require.NoError(t, err)

	var updated types.Chunk
	require.NoError(t, db.First(&updated, "id = ?", chunk.ID).Error)
	assert.Equal(t, 1.2, updated.RecallWeight, "weight boosted to default boost factor")
	assert.Equal(t, 1.0, updated.PositiveRate)
	assert.Equal(t, 0, needsOptimizationOf(t, db, chunk.ID), "high-quality chunk is not flagged")

	var logs []types.ChunkWeightLog
	require.NoError(t, db.Where("chunk_id = ?", chunk.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, 1.0, logs[0].OldWeight)
	assert.Equal(t, 1.2, logs[0].NewWeight)
	assert.Equal(t, 1.0, logs[0].PositiveRate)
	assert.Equal(t, types.FeedbackWeightTriggerFeedback, logs[0].TriggerSource)
	assert.Equal(t, "fb-boost", logs[0].FeedbackID)
	assert.Equal(t, "kb-1", logs[0].KnowledgeBaseID)
}

// TestRefreshChunkWeights_PenaltyOnHighDislikeRate verifies that a chunk
// whose positive rate falls below the penalty threshold gets its recall
// weight lowered, is flagged as needing optimization, and a weight log row
// is written.
func TestRefreshChunkWeights_PenaltyOnHighDislikeRate(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	chunk := makeFeedbackChunk("kb-1", 0, 5, 1.0) // rate=0.0 < default penalty 0.5
	require.NoError(t, db.Create(chunk).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, []string{chunk.ID}, nil, types.FeedbackWeightTriggerFeedback, "fb-penalty")
	})
	require.NoError(t, err)

	var updated types.Chunk
	require.NoError(t, db.First(&updated, "id = ?", chunk.ID).Error)
	assert.Equal(t, 0.8, updated.RecallWeight, "weight lowered to default penalty factor")
	assert.Equal(t, 0.0, updated.PositiveRate)
	assert.Equal(t, 1, needsOptimizationOf(t, db, chunk.ID), "low-quality chunk is flagged")

	var logs []types.ChunkWeightLog
	require.NoError(t, db.Where("chunk_id = ?", chunk.ID).Find(&logs).Error)
	require.Len(t, logs, 1)
	assert.Equal(t, 1.0, logs[0].OldWeight)
	assert.Equal(t, 0.8, logs[0].NewWeight)
	assert.Equal(t, 0.0, logs[0].PositiveRate)
	assert.Equal(t, types.FeedbackWeightTriggerFeedback, logs[0].TriggerSource)
}

// TestRefreshChunkWeights_NoLogWhenWeightUnchanged verifies that when the
// recomputed weight equals the existing weight (within epsilon), the chunk
// row is still refreshed but no weight log row is emitted.
func TestRefreshChunkWeights_NoLogWhenWeightUnchanged(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	// Chunk already at the boost factor with enough likes to recompute to
	// the same value — weight is unchanged so no log should be written.
	chunk := makeFeedbackChunk("kb-1", 5, 0, 1.2)
	require.NoError(t, db.Create(chunk).Error)

	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, []string{chunk.ID}, nil, types.FeedbackWeightTriggerConfig, "")
	})
	require.NoError(t, err)

	var updated types.Chunk
	require.NoError(t, db.First(&updated, "id = ?", chunk.ID).Error)
	assert.Equal(t, 1.2, updated.RecallWeight, "weight stays at boost factor")
	assert.Equal(t, 1.0, updated.PositiveRate, "positive_rate still refreshed")
	assert.Equal(t, int64(0), countWeightLogs(t, db, chunk.ID), "no log when weight unchanged")
}

// TestRefreshChunkWeights_MultipleChunksMixed exercises the batch path with
// chunks in different weight bands to ensure each is recomputed and logged
// independently within one call.
func TestRefreshChunkWeights_MultipleChunksMixed(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	neutral := makeFeedbackChunk("kb-1", 1, 0, 1.0)   // below min → stays 1.0, no log
	boosted := makeFeedbackChunk("kb-1", 5, 0, 1.0)   // high like → 1.2, log
	penalized := makeFeedbackChunk("kb-1", 0, 5, 1.0) // high dislike → 0.8, log
	require.NoError(t, db.Create(neutral).Error)
	require.NoError(t, db.Create(boosted).Error)
	require.NoError(t, db.Create(penalized).Error)

	chunkIDs := []string{neutral.ID, boosted.ID, penalized.ID}
	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, chunkIDs, nil, types.FeedbackWeightTriggerFeedback, "fb-multi")
	})
	require.NoError(t, err)

	var n types.Chunk
	require.NoError(t, db.First(&n, "id = ?", neutral.ID).Error)
	assert.Equal(t, 1.0, n.RecallWeight)
	assert.Equal(t, int64(0), countWeightLogs(t, db, neutral.ID))

	var b types.Chunk
	require.NoError(t, db.First(&b, "id = ?", boosted.ID).Error)
	assert.Equal(t, 1.2, b.RecallWeight)
	assert.Equal(t, int64(1), countWeightLogs(t, db, boosted.ID))

	var p types.Chunk
	require.NoError(t, db.First(&p, "id = ?", penalized.ID).Error)
	assert.Equal(t, 0.8, p.RecallWeight)
	assert.Equal(t, 1, needsOptimizationOf(t, db, penalized.ID))
	assert.Equal(t, int64(1), countWeightLogs(t, db, penalized.ID))

	// Total: 2 logs (boosted + penalized), neutral emitted none.
	var totalLogs int64
	require.NoError(t, db.Model(&types.ChunkWeightLog{}).Count(&totalLogs).Error)
	assert.Equal(t, int64(2), totalLogs)
}

// TestRefreshChunkWeights_CustomConfig verifies that a non-default
// RetrievalConfig is honoured: with a lowered boost threshold and a higher
// boost factor, a mixed-ratio chunk that would be neutral under defaults
// gets boosted.
func TestRefreshChunkWeights_CustomConfig(t *testing.T) {
	db := setupFeedbackWeightTestDB(t)
	// 3 likes / 1 dislike → rate = 0.75. Under defaults (boost threshold 0.8)
	// this is neutral; with boost threshold 0.7 it gets boosted.
	chunk := makeFeedbackChunk("kb-1", 3, 1, 1.0)
	require.NoError(t, db.Create(chunk).Error)

	cfg := &types.RetrievalConfig{
		FeedbackMinSamples:                 3,
		FeedbackBoostThreshold:             0.7,
		FeedbackBoostFactor:                1.5,
		FeedbackPenaltyThreshold:           0.4,
		FeedbackPenaltyFactor:              0.7,
		FeedbackNeedsOptimizationThreshold: 0.3,
	}
	err := db.Transaction(func(tx *gorm.DB) error {
		return refreshChunkWeights(context.Background(), tx, []string{chunk.ID}, cfg, types.FeedbackWeightTriggerConfig, "")
	})
	require.NoError(t, err)

	var updated types.Chunk
	require.NoError(t, db.First(&updated, "id = ?", chunk.ID).Error)
	assert.Equal(t, 1.5, updated.RecallWeight, "custom boost factor applied")
	assert.Equal(t, 0.75, updated.PositiveRate)
	assert.Equal(t, int64(1), countWeightLogs(t, db, chunk.ID))
}
