package repository

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"
)

func setupBatchGuardTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupTaskQueueTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
		id VARCHAR(64) PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, deleted_at) VALUES (?, ?, NULL), (?, ?, ?)",
		"kb-active", 1, "kb-deleted", 1, time.Now(),
	).Error)
	require.NoError(t, db.Exec(`CREATE TABLE tenants (
		id INTEGER PRIMARY KEY,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO tenants (id, deleted_at) VALUES (?, NULL)",
		1,
	).Error)
	return db
}

func batchOps(n int) []*types.TaskPendingOp {
	ops := make([]*types.TaskPendingOp, 0, n)
	for i := 0; i < n; i++ {
		op := makePendingOp("wiki:finalize", "knowledge_base", "kb-active", "slug",
			"slug-"+string(rune('a'+i)), []byte(`{"slug":"s"}`))
		ops = append(ops, op)
	}
	return ops
}

func countPendingRows(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table("task_pending_ops").Where("task_type = ?", "wiki:finalize").Count(&n).Error)
	return n
}

// The batch guard must expose the same accept/reject contract as the
// single-row guard, but write every row of a live-KB batch in one
// transaction — the round-trip collapse that keeps a thousand-page batch's
// finalize rows inside a bounded tail window (#5716).
func TestTaskPendingOps_EnqueueBatchIfKnowledgeBaseActive(t *testing.T) {
	db := setupBatchGuardTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	batch, ok := repo.(interfaces.TaskPendingOpsBatchGuard)
	require.True(t, ok, "task pending repository must expose the batch guard")
	ctx := context.Background()

	accepted, err := batch.EnqueueBatchIfKnowledgeBaseActive(ctx, batchOps(3))
	require.NoError(t, err)
	assert.Equal(t, 3, accepted, "a live KB accepts the whole batch")
	assert.EqualValues(t, 3, countPendingRows(t, db))

	// A deleted KB rejects the batch as a whole and writes nothing.
	deleted := batchOps(2)
	for _, op := range deleted {
		op.ScopeID = "kb-deleted"
	}
	accepted, err = batch.EnqueueBatchIfKnowledgeBaseActive(ctx, deleted)
	require.NoError(t, err)
	assert.Zero(t, accepted)
	assert.EqualValues(t, 3, countPendingRows(t, db), "a rejected batch writes no rows")

	// A batch that mixes knowledge bases is refused up front: one guard
	// decision cannot cover two scopes.
	mixed := batchOps(2)
	mixed[1].ScopeID = "kb-other"
	_, err = batch.EnqueueBatchIfKnowledgeBaseActive(ctx, mixed)
	require.Error(t, err)
	assert.EqualValues(t, 3, countPendingRows(t, db), "an invalid batch writes no rows")

	// An empty batch is a no-op.
	accepted, err = batch.EnqueueBatchIfKnowledgeBaseActive(ctx, nil)
	require.NoError(t, err)
	assert.Zero(t, accepted)
}

// A thousand-page batch crosses the CreateInBatches chunk boundary: the
// insert must still land every row and count them all (#5716 is precisely
// about batches of this size).
func TestTaskPendingOps_EnqueueBatchCrossesInsertChunks(t *testing.T) {
	db := setupBatchGuardTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	batch, ok := repo.(interfaces.TaskPendingOpsBatchGuard)
	require.True(t, ok)

	ops := make([]*types.TaskPendingOp, 0, 1200)
	for i := 0; i < 1200; i++ {
		op := makePendingOp("wiki:finalize", "knowledge_base", "kb-active", "slug",
			fmt.Sprintf("slug-%04d", i), []byte(`{"slug":"s"}`))
		ops = append(ops, op)
	}

	accepted, err := batch.EnqueueBatchIfKnowledgeBaseActive(context.Background(), ops)

	require.NoError(t, err)
	assert.Equal(t, 1200, accepted)
	assert.EqualValues(t, 1200, countPendingRows(t, db))
}
