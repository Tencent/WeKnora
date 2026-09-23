package repository

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// taskPendingOpsTestDDL mirrors the production schema in
// migrations/versioned/000041_task_queue_and_wiki_indexes.up.sql but uses
// SQLite-compatible types. INTEGER PRIMARY KEY AUTOINCREMENT preserves
// the monotonically-increasing ID semantics PeekBatch/cursor pagination
// rely on. JSONB → TEXT is fine since GORM round-trips json.RawMessage
// as bytes either way.
const taskPendingOpsTestDDL = `
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    op          VARCHAR(32) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}',
    fail_count  INTEGER NOT NULL DEFAULT 0,
    enqueued_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    claimed_at  DATETIME
);
`

const taskDeadLettersTestDDL = `
CREATE TABLE IF NOT EXISTS task_dead_letters (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(32) NOT NULL,
    scope_id    VARCHAR(64) NOT NULL,
    related_id  VARCHAR(64) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL,
    last_error  TEXT NOT NULL DEFAULT '',
    fail_count  INTEGER NOT NULL,
    failed_at   DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

const taskQueueKnowledgeBaseTestDDL = `
CREATE TABLE IF NOT EXISTS knowledge_bases (
    profile_config TEXT,
    generated_profile TEXT,
    id         VARCHAR(64) PRIMARY KEY,
    tenant_id  INTEGER NOT NULL,
    deleted_at DATETIME
);
`

const taskQueueKnowledgeTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    profile TEXT,
    id                     VARCHAR(64) PRIMARY KEY,
    tenant_id              INTEGER NOT NULL,
    knowledge_base_id      VARCHAR(64) NOT NULL,
    parse_status           VARCHAR(32) NOT NULL,
    pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
    error_message          TEXT,
    processed_at           DATETIME,
    updated_at             DATETIME,
    deleted_at             DATETIME
);
`

func setupTaskQueueTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(taskPendingOpsTestDDL).Error)
	require.NoError(t, db.Exec(taskDeadLettersTestDDL).Error)
	return db
}

func makePendingOp(taskType, scope, scopeID, op, dedup string, payload []byte) *types.TaskPendingOp {
	return &types.TaskPendingOp{
		TenantID: 1,
		TaskType: taskType,
		Scope:    scope,
		ScopeID:  scopeID,
		Op:       op,
		DedupKey: dedup,
		Payload:  payload,
	}
}

func setupFinalizingPendingOpTest(t *testing.T) (*gorm.DB, interfaces.TaskPendingOpsFinalizingSeeder) {
	t.Helper()
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	seeder, ok := repo.(interfaces.TaskPendingOpsFinalizingSeeder)
	require.True(t, ok, "task pending repository must support atomic finalizing handoff")
	require.NoError(t, db.Exec(taskQueueKnowledgeBaseTestDDL).Error)
	require.NoError(t, db.Exec(taskQueueKnowledgeTestDDL).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_bases (id, tenant_id) VALUES ('kb-1', 1)`,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status) VALUES ('knowledge-1', 1, 'kb-1', ?)`,
		types.ParseStatusProcessing,
	).Error)
	return db, seeder
}

func TestTaskPendingOps_SeedKnowledgeFinalizingWithPendingOpCommitsTogether(t *testing.T) {
	db, seeder := setupFinalizingPendingOpTest(t)
	op := makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1", "ingest", "knowledge-1", []byte(`{}`))

	promoted, err := seeder.SeedKnowledgeFinalizingWithPendingOp(
		context.Background(), "knowledge-1", 3, op,
	)

	require.NoError(t, err)
	require.True(t, promoted)
	var knowledge types.Knowledge
	require.NoError(t, db.Select("parse_status", "pending_subtasks_count").First(&knowledge, "id = ?", "knowledge-1").Error)
	assert.Equal(t, types.ParseStatusFinalizing, knowledge.ParseStatus)
	assert.Equal(t, 3, knowledge.PendingSubtasksCount)
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key = ?", "knowledge-1").Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTaskPendingOps_SeedKnowledgeFinalizingRollsBackWhenPendingOpInsertFails(t *testing.T) {
	db, seeder := setupFinalizingPendingOpTest(t)
	require.NoError(t, db.Exec(`DROP TABLE task_pending_ops`).Error)
	op := makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1", "ingest", "knowledge-1", []byte(`{}`))

	promoted, err := seeder.SeedKnowledgeFinalizingWithPendingOp(
		context.Background(), "knowledge-1", 3, op,
	)

	require.Error(t, err)
	assert.False(t, promoted)
	var knowledge types.Knowledge
	require.NoError(t, db.Select("parse_status", "pending_subtasks_count").First(&knowledge, "id = ?", "knowledge-1").Error)
	assert.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
	assert.Zero(t, knowledge.PendingSubtasksCount)
}

func TestTaskPendingOps_SeedKnowledgeFinalizingSkipsNonProcessingKnowledge(t *testing.T) {
	db, seeder := setupFinalizingPendingOpTest(t)
	require.NoError(t, db.Model(&types.Knowledge{}).
		Where("id = ?", "knowledge-1").
		Update("parse_status", types.ParseStatusCancelled).Error)
	op := makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1", "ingest", "knowledge-1", []byte(`{}`))

	promoted, err := seeder.SeedKnowledgeFinalizingWithPendingOp(
		context.Background(), "knowledge-1", 3, op,
	)

	require.NoError(t, err)
	assert.False(t, promoted)
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestTaskPendingOps_SeedKnowledgeFinalizingSkipsDeletedKnowledgeBase(t *testing.T) {
	db, seeder := setupFinalizingPendingOpTest(t)
	require.NoError(t, db.Exec(
		`UPDATE knowledge_bases SET deleted_at = ? WHERE id = ?`, time.Now(), "kb-1",
	).Error)
	op := makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1", "ingest", "knowledge-1", []byte(`{}`))

	promoted, err := seeder.SeedKnowledgeFinalizingWithPendingOp(
		context.Background(), "knowledge-1", 3, op,
	)

	require.NoError(t, err)
	assert.False(t, promoted)
	var knowledge types.Knowledge
	require.NoError(t, db.Select("parse_status", "pending_subtasks_count").First(&knowledge, "id = ?", "knowledge-1").Error)
	assert.Equal(t, types.ParseStatusProcessing, knowledge.ParseStatus)
	assert.Zero(t, knowledge.PendingSubtasksCount)
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	assert.Zero(t, count)
}

// ---------------- TaskPendingOpsRepository ----------------

// TestTaskPendingOps_Enqueue_AssignsIDAndDefaults verifies a freshly
// inserted op gets a positive ID and the empty payload becomes "{}"
// rather than NULL/empty.
func TestTaskPendingOps_Enqueue_AssignsIDAndDefaults(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	op := makePendingOp("wiki:ingest", "knowledge_base", "kb-1", "ingest", "k-1", nil)
	require.NoError(t, repo.Enqueue(ctx, op))
	assert.NotZero(t, op.ID)
	assert.Equal(t, json.RawMessage("{}"), op.Payload, "nil payload should default to {}")
}

// TestTaskPendingOps_Enqueue_RejectsMissingFields covers the validation
// layer: every required field must be set, otherwise the call returns an
// error WITHOUT touching the DB.
func TestTaskPendingOps_Enqueue_RejectsMissingFields(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	cases := []struct {
		name string
		op   *types.TaskPendingOp
	}{
		{"nil op", nil},
		{"missing task_type", makePendingOp("", "knowledge_base", "kb", "ingest", "", nil)},
		{"missing scope", makePendingOp("t", "", "kb", "ingest", "", nil)},
		{"missing scope_id", makePendingOp("t", "s", "", "ingest", "", nil)},
		{"missing op", makePendingOp("t", "s", "id", "", "", nil)},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			err := repo.Enqueue(ctx, c.op)
			assert.Error(t, err)
		})
	}

	var n int64
	db.Table("task_pending_ops").Count(&n)
	assert.Equal(t, int64(0), n)
}

// TestTaskPendingOps_PeekBatch_ScopedAndOrdered verifies PeekBatch only
// returns rows for the matching tuple, least-failed then id ASC (which
// is insertion order when every row is still fail_count = 0), and
// respects the limit.
func TestTaskPendingOps_PeekBatch_ScopedAndOrdered(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	// Three ops in kb-A, two in kb-B, one in different task_type.
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-A", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-A", "retract", "k2", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-A", "ingest", "k3", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-B", "ingest", "k4", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-B", "ingest", "k5", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("summary:gen", "knowledge_base", "kb-A", "ingest", "k6", nil)))

	// Peek up to 10 from kb-A — should see exactly 3, in insertion order.
	got, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb-A", 10)
	require.NoError(t, err)
	require.Len(t, got, 3)
	assert.Equal(t, "k1", got[0].DedupKey)
	assert.Equal(t, "k2", got[1].DedupKey)
	assert.Equal(t, "k3", got[2].DedupKey)
	assert.True(t, got[0].ID < got[1].ID && got[1].ID < got[2].ID, "ids should be ascending")

	// Limit caps result size.
	got, err = repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb-A", 2)
	require.NoError(t, err)
	assert.Len(t, got, 2)

	// Different task_type isolated.
	got, err = repo.PeekBatch(ctx, "summary:gen", "knowledge_base", "kb-A", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "k6", got[0].DedupKey)
}

// TestTaskPendingOps_PeekBatch_PrefersLeastFailed is the Lite-mode twin
// of TestTaskPendingOps_ClaimBatch_PrefersLeastFailed: peekPendingList
// still uses PeekBatch, and a retried row keeps its original (lowest)
// id, so a pure id sort would starve never-attempted work the same way.
func TestTaskPendingOps_PeekBatch_PrefersLeastFailed(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	hot := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "hot", nil)
	require.NoError(t, repo.Enqueue(ctx, hot))
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "fresh", nil)))

	_, err := repo.IncrFailCount(ctx, hot.ID)
	require.NoError(t, err)

	next, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 1)
	require.NoError(t, err)
	require.Len(t, next, 1)
	assert.Equal(t, "fresh", next[0].DedupKey,
		"a retried document must not starve a never-attempted one")

	both, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 2)
	require.NoError(t, err)
	require.Len(t, both, 2)
	assert.Equal(t, "fresh", both[0].DedupKey)
	assert.Equal(t, "hot", both[1].DedupKey,
		"a retried document must still be returned after untried work")
}

// TestTaskPendingOps_DeleteByIDs_RemovesOnlyTargets verifies the
// delete-after-consume path. Empty input must be a no-op so the consumer
// can call it unconditionally.
func TestTaskPendingOps_DeleteByIDs_RemovesOnlyTargets(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	a := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "a", nil)
	b := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "b", nil)
	c := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "c", nil)
	require.NoError(t, repo.Enqueue(ctx, a))
	require.NoError(t, repo.Enqueue(ctx, b))
	require.NoError(t, repo.Enqueue(ctx, c))

	// No-op: empty slice.
	require.NoError(t, repo.DeleteByIDs(ctx, nil))
	require.NoError(t, repo.DeleteByIDs(ctx, []int64{}))

	// Delete a + c, keep b.
	require.NoError(t, repo.DeleteByIDs(ctx, []int64{a.ID, c.ID}))

	got, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "b", got[0].DedupKey)
}

func TestTaskPendingOps_DeleteByScope_RemovesAllTaskTypesAndIsolatesScopes(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	cleaner, ok := repo.(interfaces.TaskPendingOpsScopeCleaner)
	require.True(t, ok)
	ctx := context.Background()

	// The deleted KB can have durable work in more than one wiki queue.
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-delete", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp(types.TypeWikiFinalize, types.TaskScopeKnowledgeBase, "kb-delete", "finalize", "k2", nil)))

	// Rows in another KB or another scope must survive even when their IDs or
	// task types overlap with the deleted KB.
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-keep", "ingest", "k3", nil)))
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp(types.TypeWikiFinalize, types.TaskScopeKnowledge, "kb-delete", "finalize", "k4", nil)))

	require.NoError(t, cleaner.DeleteByScope(ctx, types.TaskScopeKnowledgeBase, "kb-delete"))

	var remaining []*types.TaskPendingOp
	require.NoError(t, db.Order("id ASC").Find(&remaining).Error)
	require.Len(t, remaining, 2)
	assert.Equal(t, "kb-keep", remaining[0].ScopeID)
	assert.Equal(t, types.TaskScopeKnowledge, remaining[1].Scope)
	assert.Equal(t, "kb-delete", remaining[1].ScopeID)
}

func TestTaskPendingOps_DeleteByScope_RejectsMissingScope(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	cleaner, ok := repo.(interfaces.TaskPendingOpsScopeCleaner)
	require.True(t, ok)
	ctx := context.Background()

	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb", "ingest", "k1", nil)))

	assert.Error(t, cleaner.DeleteByScope(ctx, "", "kb"))
	assert.Error(t, cleaner.DeleteByScope(ctx, types.TaskScopeKnowledgeBase, ""))

	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestTaskPendingOps_EnqueueIfKnowledgeBaseActive(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases (
    profile_config TEXT,
    generated_profile TEXT,
		id VARCHAR(64) PRIMARY KEY,
		tenant_id INTEGER NOT NULL,
		deleted_at DATETIME
	)`).Error)
	require.NoError(t, db.Exec(
		"INSERT INTO knowledge_bases (id, tenant_id, deleted_at) VALUES (?, ?, NULL), (?, ?, ?)",
		"kb-active", 1, "kb-deleted", 1, time.Now(),
	).Error)

	repo := NewTaskPendingOpsRepository(db)
	guard, ok := repo.(interfaces.TaskPendingOpsKnowledgeBaseGuard)
	require.True(t, ok)
	ctx := context.Background()

	accepted, err := guard.EnqueueIfKnowledgeBaseActive(ctx,
		makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-active", "ingest", "active", nil))
	require.NoError(t, err)
	assert.True(t, accepted)

	for _, tc := range []struct {
		name string
		op   *types.TaskPendingOp
	}{
		{
			name: "soft deleted",
			op: makePendingOp(
				types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-deleted", "ingest", "deleted", nil,
			),
		},
		{
			name: "missing",
			op: makePendingOp(
				types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-missing", "ingest", "missing", nil,
			),
		},
		{name: "tenant mismatch", op: &types.TaskPendingOp{
			TenantID: 2, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
			ScopeID: "kb-active", Op: "ingest", DedupKey: "wrong-tenant",
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			accepted, err := guard.EnqueueIfKnowledgeBaseActive(ctx, tc.op)
			require.NoError(t, err)
			assert.False(t, accepted)
		})
	}

	var rows []*types.TaskPendingOp
	require.NoError(t, db.Find(&rows).Error)
	require.Len(t, rows, 1)
	assert.Equal(t, "active", rows[0].DedupKey)
}

// TestTaskPendingOps_IncrFailCount_ReturnsNewValueAndPersists exercises
// the UPDATE...RETURNING flow. Successive bumps should observe monotonic
// counts.
func TestTaskPendingOps_IncrFailCount_ReturnsNewValueAndPersists(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	op := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k", nil)
	require.NoError(t, repo.Enqueue(ctx, op))

	n, err := repo.IncrFailCount(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, 1, n)

	n, err = repo.IncrFailCount(ctx, op.ID)
	require.NoError(t, err)
	assert.Equal(t, 2, n)

	// Persisted value matches what was returned.
	rows, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, 2, rows[0].FailCount)
}

// TestTaskPendingOps_PendingCount_ScopedTuple confirms the count covers
// only the (task_type, scope, scope_id) tuple.
func TestTaskPendingOps_PendingCount_ScopedTuple(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-A", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-A", "ingest", "k2", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb-B", "ingest", "k3", nil)))

	n, err := repo.PendingCount(ctx, "wiki:ingest", "knowledge_base", "kb-A")
	require.NoError(t, err)
	assert.Equal(t, int64(2), n)

	n, err = repo.PendingCount(ctx, "wiki:ingest", "knowledge_base", "missing")
	require.NoError(t, err)
	assert.Equal(t, int64(0), n)
}

// TestTaskPendingOps_DeleteByDedupKey_Filters tests the wiki delete-race
// helper: matching rows go away, others survive, optional op filter
// narrows the scope, and an empty dedup_key is rejected (so a buggy
// caller can't wipe the entire queue).
func TestTaskPendingOps_DeleteByDedupKey_Filters(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	// Two ingests + one retract, all keyed on knowledge "k1"; one ingest
	// for unrelated "k2".
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "retract", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k2", nil)))

	// Empty key is an error, queue unchanged.
	err := repo.DeleteByDedupKey(ctx, "wiki:ingest", "knowledge_base", "kb", "", "")
	assert.Error(t, err)
	n, _ := repo.PendingCount(ctx, "wiki:ingest", "knowledge_base", "kb")
	assert.Equal(t, int64(4), n)

	// Drop only "ingest" rows for k1; retract survives.
	require.NoError(t, repo.DeleteByDedupKey(ctx, "wiki:ingest", "knowledge_base", "kb", "k1", "ingest"))
	rows, err := repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	// The two survivors must be the retract for k1 and the ingest for k2.
	keys := map[string]string{}
	for _, r := range rows {
		keys[r.Op] = r.DedupKey
	}
	assert.Equal(t, "k1", keys["retract"])
	assert.Equal(t, "k2", keys["ingest"])

	// Drop everything keyed on k1 regardless of op (empty op = wildcard).
	require.NoError(t, repo.DeleteByDedupKey(ctx, "wiki:ingest", "knowledge_base", "kb", "k1", ""))
	rows, err = repo.PeekBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10)
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, "k2", rows[0].DedupKey)
}

// TestTaskPendingOps_ClaimBatch_MarksAndReturnsDisjoint verifies that
// ClaimBatch returns rows in FIFO order, stamps claimed_at, and that a
// second claim does NOT re-return the already-claimed rows (the disjoint
// property concurrent consumers rely on).
func TestTaskPendingOps_ClaimBatch_MarksAndReturnsDisjoint(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	for _, k := range []string{"k1", "k2", "k3"} {
		require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", k, nil)))
	}
	// Unrelated tuple must never be claimed.
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "other", "ingest", "z", nil)))

	stale := time.Now().Add(-time.Hour)

	first, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 2, stale)
	require.NoError(t, err)
	require.Len(t, first, 2)
	assert.Equal(t, "k1", first[0].DedupKey)
	assert.Equal(t, "k2", first[1].DedupKey)
	assert.NotNil(t, first[0].ClaimedAt, "claimed_at should be stamped")

	// Second claim skips the two already-claimed rows and returns the last.
	second, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "k3", second[0].DedupKey)

	// Nothing left to claim.
	third, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	assert.Len(t, third, 0)
}

// TestTaskPendingOps_ClaimBatch_PrefersLeastFailed guards the starvation fix.
// A retried document keeps its ORIGINAL id — requeueFailedOps releases the
// claim rather than moving the row, so the fail_count budget keeps counting
// down — which under a pure `id ASC` ordering let it park at the head of the
// queue while never-attempted documents sat behind it. Observed on a real
// 87-document KB: four re-run documents held the head and all forty
// never-started ones waited. Selection must drain untried work first, without
// stranding a document that keeps failing.
func TestTaskPendingOps_ClaimBatch_PrefersLeastFailed(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	// "hot" is enqueued FIRST, so it owns the lowest id.
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "hot", nil)))
	require.NoError(t, repo.Enqueue(ctx,
		makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "fresh", nil)))

	stale := time.Now().Add(-time.Hour)

	// Replay the retry path on "hot": claim it, bump fail_count, release it
	// back to the pool exactly as requeueFailedOps does.
	first, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 1, stale)
	require.NoError(t, err)
	require.Len(t, first, 1)
	require.Equal(t, "hot", first[0].DedupKey)
	_, err = repo.IncrFailCount(ctx, first[0].ID)
	require.NoError(t, err)
	require.NoError(t, repo.ReleaseByIDs(ctx, []int64{first[0].ID}))

	// "hot" holds the lower id but has a failure on record; the
	// never-attempted document must be selected ahead of it.
	next, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 1, stale)
	require.NoError(t, err)
	require.Len(t, next, 1)
	assert.Equal(t, "fresh", next[0].DedupKey,
		"a retried document must not starve a never-attempted one")

	// The retried document still gets its turn once nothing fresher remains.
	last, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 1, stale)
	require.NoError(t, err)
	require.Len(t, last, 1)
	assert.Equal(t, "hot", last[0].DedupKey,
		"a retried document must eventually be picked up, not stranded")
}

// TestTaskPendingOps_ClaimBatch_KeepsSameKeyTogether verifies the
// dedup_key affinity invariant: all rows sharing a knowledge_id are claimed
// in the SAME batch (never split), and `limit` counts distinct keys, not
// rows. This is what stops a concurrent batch from processing one op of a
// document while another batch processes a second op of the same document.
func TestTaskPendingOps_ClaimBatch_KeepsSameKeyTogether(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	// Document k1 has TWO queued ops (ingest then retract); k2 and k3 have
	// one each. Enqueue order interleaves them so a naive row-ordered claim
	// would split k1 across batches.
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k2", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "retract", "k1", nil)))
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k3", nil)))

	stale := time.Now().Add(-time.Hour)

	// limit=2 keys → k1 (both rows) + k2. k1 must NOT be split.
	first, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 2, stale)
	require.NoError(t, err)
	require.Len(t, first, 3, "k1's two rows + k2's one row")
	byKey := map[string]int{}
	for _, r := range first {
		byKey[r.DedupKey]++
	}
	assert.Equal(t, 2, byKey["k1"], "both k1 ops claimed together")
	assert.Equal(t, 1, byKey["k2"])
	assert.Zero(t, byKey["k3"], "k3 belongs to the next batch (limit was 2 keys)")

	// A concurrent-style second claim gets the remaining key only — it can
	// never see k1's rows again (disjoint).
	second, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, second, 1)
	assert.Equal(t, "k3", second[0].DedupKey)
}

// TestTaskPendingOps_ClaimBatch_LateSiblingBlockedByFreshClaim verifies that a
// row enqueued for an already-claimed (in-flight) document is NOT claimed on
// its own: a dedup_key with any fresh claim is skipped WHOLESALE so the late
// sibling waits for the holder to finish (rows deleted → key freed) or for the
// claim to go stale, keeping same-document ops serialized across concurrent
// batches.
func TestTaskPendingOps_ClaimBatch_LateSiblingBlockedByFreshClaim(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))
	stale := time.Now().Add(-time.Hour)

	first, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 5, stale)
	require.NoError(t, err)
	require.Len(t, first, 1)
	ingestID := first[0].ID

	// A retract for the same, still-in-flight document arrives.
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "retract", "k1", nil)))

	// The retract must NOT be claimed while the ingest claim is fresh —
	// the whole k1 key is blocked.
	second, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 5, stale)
	require.NoError(t, err)
	require.Len(t, second, 0, "late sibling blocked while holder's claim is fresh")

	// Once the holder finishes (its claimed rows are deleted), the key is
	// free and the retract becomes claimable.
	require.NoError(t, repo.DeleteByIDs(ctx, []int64{ingestID}))
	third, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 5, stale)
	require.NoError(t, err)
	require.Len(t, third, 1)
	assert.Equal(t, "retract", third[0].Op)
}

// TestTaskPendingOps_ClaimBatch_LateSiblingClaimableAfterStale verifies the
// other release path: if the holder CRASHES (claim never cleared), the whole
// key — original row + late sibling — becomes claimable together once the
// claim goes stale, so the pair is folded back into one batch.
func TestTaskPendingOps_ClaimBatch_LateSiblingClaimableAfterStale(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))
	first, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 5, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, first, 1)

	// Late retract arrives; holder then crashes (claim left stamped).
	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "retract", "k1", nil)))

	// A future stale threshold makes the crashed claim eligible again; both
	// k1 rows are reclaimed together (never split).
	got, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 5, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 2, "stale key reclaims both the ingest and the late retract together")
	for _, r := range got {
		assert.Equal(t, "k1", r.DedupKey)
	}
}

// TestTaskPendingOps_ClaimBatch_ReclaimsStale verifies a claim older than
// staleBefore is re-claimable (crash recovery), while a fresh claim is not.
func TestTaskPendingOps_ClaimBatch_ReclaimsStale(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Enqueue(ctx, makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)))

	// Claim it now.
	got, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1)

	// A stale threshold in the past — the fresh claim is NOT stale yet.
	got, err = repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	assert.Len(t, got, 0, "fresh claim must not be re-claimed")

	// A stale threshold in the future makes every existing claim stale.
	got, err = repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1, "stale claim must be recoverable")
	assert.Equal(t, "k1", got[0].DedupKey)
}

// TestTaskPendingOps_ReleaseByIDs_ReturnsToPool verifies a released row
// becomes immediately claimable again without waiting for staleness.
func TestTaskPendingOps_ReleaseByIDs_ReturnsToPool(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskPendingOpsRepository(db)
	ctx := context.Background()

	op := makePendingOp("wiki:ingest", "knowledge_base", "kb", "ingest", "k1", nil)
	require.NoError(t, repo.Enqueue(ctx, op))

	stale := time.Now().Add(-time.Hour)
	got, err := repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, got, 1)

	// Claimed → not re-claimable.
	got, err = repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, got, 0)

	// Release → immediately claimable again.
	require.NoError(t, repo.ReleaseByIDs(ctx, nil)) // no-op tolerated
	require.NoError(t, repo.ReleaseByIDs(ctx, []int64{op.ID}))
	got, err = repo.ClaimBatch(ctx, "wiki:ingest", "knowledge_base", "kb", 10, stale)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "k1", got[0].DedupKey)
}

// ---------------- TaskDeadLetterRepository ----------------

func makeDeadLetter(taskType, scope, scopeID, relatedID, lastErr string) *types.TaskDeadLetter {
	return &types.TaskDeadLetter{
		TenantID:  1,
		TaskType:  taskType,
		Scope:     scope,
		ScopeID:   scopeID,
		RelatedID: relatedID,
		Payload:   json.RawMessage(`{"x":1}`),
		LastError: lastErr,
		FailCount: 5,
	}
}

// TestTaskDeadLetter_Insert_DefaultsAndAssignsID covers the empty-payload
// fallback and ID assignment.
func TestTaskDeadLetter_Insert_DefaultsAndAssignsID(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	dl := &types.TaskDeadLetter{
		TenantID:  1,
		TaskType:  "wiki:ingest",
		ScopeID:   "kb",
		FailCount: 3,
		// Scope intentionally empty — should default to "unknown".
		// Payload intentionally nil — should default to "{}".
	}
	require.NoError(t, repo.Insert(ctx, dl))
	assert.NotZero(t, dl.ID)
	assert.Equal(t, types.TaskScopeUnknown, dl.Scope)
	assert.Equal(t, json.RawMessage("{}"), dl.Payload)
}

// TestTaskDeadLetter_Insert_RejectsMissingFields verifies the guard
// against rows that would leave the table without the columns ops queries
// rely on.
func TestTaskDeadLetter_Insert_RejectsMissingFields(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	assert.Error(t, repo.Insert(ctx, nil))
	assert.Error(t, repo.Insert(ctx, &types.TaskDeadLetter{ScopeID: "kb"}))

	var n int64
	db.Table("task_dead_letters").Count(&n)
	assert.Equal(t, int64(0), n)
}

// TestTaskDeadLetter_ListByScope_NewestFirstAndCursored exercises the
// cursor pagination path used by the ops console.
func TestTaskDeadLetter_ListByScope_NewestFirstAndCursored(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	// Insert 5 rows for kb-A and 2 for kb-B.
	for i := 0; i < 5; i++ {
		require.NoError(t, repo.Insert(ctx, makeDeadLetter("wiki:ingest", "knowledge_base", "kb-A", "k", "boom")))
	}
	require.NoError(t, repo.Insert(ctx, makeDeadLetter("wiki:ingest", "knowledge_base", "kb-B", "k", "boom")))
	require.NoError(t, repo.Insert(ctx, makeDeadLetter("wiki:ingest", "knowledge_base", "kb-B", "k", "boom")))

	// First page of 2 from kb-A, newest first.
	page1, cursor, err := repo.ListByScope(ctx, "knowledge_base", "kb-A", "", 2)
	require.NoError(t, err)
	require.Len(t, page1, 2)
	assert.True(t, page1[0].ID > page1[1].ID, "newest first")
	require.NotEmpty(t, cursor)

	// Second page of 2.
	page2, cursor, err := repo.ListByScope(ctx, "knowledge_base", "kb-A", cursor, 2)
	require.NoError(t, err)
	require.Len(t, page2, 2)
	assert.True(t, page1[1].ID > page2[0].ID, "page2 should continue past page1")
	require.NotEmpty(t, cursor)

	// Last page — only 1 row left, cursor goes empty since len < limit.
	page3, cursor, err := repo.ListByScope(ctx, "knowledge_base", "kb-A", cursor, 2)
	require.NoError(t, err)
	require.Len(t, page3, 1)
	assert.Empty(t, cursor)

	// kb-B is isolated.
	pageB, _, err := repo.ListByScope(ctx, "knowledge_base", "kb-B", "", 10)
	require.NoError(t, err)
	require.Len(t, pageB, 2)
}

// TestTaskDeadLetter_ListByScope_RejectsMissingScope guards the input
// validation in the public method.
func TestTaskDeadLetter_ListByScope_RejectsMissingScope(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	_, _, err := repo.ListByScope(ctx, "", "kb", "", 10)
	assert.Error(t, err)
	_, _, err = repo.ListByScope(ctx, "knowledge_base", "", "", 10)
	assert.Error(t, err)
}

// TestTaskDeadLetter_ListByTaskType_FiltersAndPaginates is the cross-KB
// view: "all summary:generation failures" regardless of which KB they
// belong to.
func TestTaskDeadLetter_ListByTaskType_FiltersAndPaginates(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.Insert(ctx, makeDeadLetter("wiki:ingest", "knowledge_base", "kb-A", "k1", "")))
	require.NoError(t, repo.Insert(ctx, makeDeadLetter("summary:gen", "knowledge_base", "kb-A", "k2", "")))
	require.NoError(t, repo.Insert(ctx, makeDeadLetter("summary:gen", "knowledge_base", "kb-B", "k3", "")))
	require.NoError(t, repo.Insert(ctx, makeDeadLetter("wiki:ingest", "knowledge_base", "kb-B", "k4", "")))

	rows, _, err := repo.ListByTaskType(ctx, "summary:gen", "", 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, r := range rows {
		assert.Equal(t, "summary:gen", r.TaskType)
	}

	_, _, err = repo.ListByTaskType(ctx, "", "", 10)
	assert.Error(t, err)
}

// TestTaskDeadLetter_DeleteByID_IsIdempotent confirms a missing row does
// not produce an error — operators triggering concurrent deletes should
// see clean success.
func TestTaskDeadLetter_DeleteByID_IsIdempotent(t *testing.T) {
	db := setupTaskQueueTestDB(t)
	repo := NewTaskDeadLetterRepository(db)
	ctx := context.Background()

	dl := makeDeadLetter("wiki:ingest", "knowledge_base", "kb", "k", "")
	require.NoError(t, repo.Insert(ctx, dl))

	require.NoError(t, repo.DeleteByID(ctx, dl.ID))
	// Second delete on the same id should silently succeed.
	require.NoError(t, repo.DeleteByID(ctx, dl.ID))
	// Delete of unknown id should silently succeed.
	require.NoError(t, repo.DeleteByID(ctx, 99999))

	rows, _, err := repo.ListByScope(ctx, "knowledge_base", "kb", "", 10)
	require.NoError(t, err)
	assert.Len(t, rows, 0)
}

func setupDrainTest(t *testing.T) (*gorm.DB, interfaces.TaskPendingOpsRepository, interfaces.TaskPendingOpsDrainer) {
	t.Helper()
	db := setupTaskQueueTestDB(t)
	require.NoError(t, db.Exec(taskQueueKnowledgeTestDDL).Error)
	repo := NewTaskPendingOpsRepository(db)
	drainer, ok := repo.(interfaces.TaskPendingOpsDrainer)
	require.True(t, ok)
	return db, repo, drainer
}

func insertFinalizingKnowledge(t *testing.T, db *gorm.DB, id string, pending int) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, tenant_id, knowledge_base_id, parse_status, pending_subtasks_count)
		 VALUES (?, 1, 'kb-1', ?, ?)`, id, types.ParseStatusFinalizing, pending,
	).Error)
}

func wikiLane(scopeID, op, dedup string) *types.TaskPendingOp {
	return makePendingOp(types.TypeWikiIngest, types.TaskScopeKnowledgeBase, scopeID, op, dedup, []byte(`{}`))
}

// Draining releases each drained document's slot once, and leaves alone any
// document a live batch holds, even through an unclaimed row of its own.
func TestTaskPendingOps_DrainUnclaimedAndReleaseSparesLiveDocuments(t *testing.T) {
	db, repo, drainer := setupDrainTest(t)
	ctx := context.Background()
	for _, op := range []*types.TaskPendingOp{
		wikiLane("kb-1", "ingest", "k-unclaimed"),
		wikiLane("kb-1", "ingest", "k-unclaimed"),
		wikiLane("kb-1", "ingest", "k-stale"),
		wikiLane("kb-1", "ingest", "k-live"),
		wikiLane("kb-1", "ingest", "k-live"), // enqueued after the live claim
		wikiLane("kb-1", "retract", "k-retract-live"),
		wikiLane("kb-1", "ingest", "k-retract-live"),
		wikiLane("kb-1", "retract", "k-retract"),
		wikiLane("kb-2", "ingest", "k-other-kb"),
	} {
		require.NoError(t, repo.Enqueue(ctx, op))
	}
	staleBefore := time.Now().Add(-time.Hour)
	claim := func(where string, at time.Time) {
		require.NoError(t, db.Exec(`UPDATE task_pending_ops SET claimed_at = ? WHERE `+where, at).Error)
	}
	claim(`dedup_key = 'k-stale'`, staleBefore.Add(-time.Minute))
	claim(`id = (SELECT MIN(id) FROM task_pending_ops WHERE dedup_key = 'k-live')`, time.Now())
	claim(`dedup_key = 'k-retract-live' AND op = 'retract'`, time.Now())
	insertFinalizingKnowledge(t, db, "k-unclaimed", 1)
	insertFinalizingKnowledge(t, db, "k-stale", 2)
	insertFinalizingKnowledge(t, db, "k-live", 1)
	insertFinalizingKnowledge(t, db, "k-retract-live", 1)

	keys, err := drainer.DrainUnclaimedAndRelease(ctx, types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1",
		"ingest", staleBefore)

	require.NoError(t, err)
	assert.ElementsMatch(t, []string{"k-unclaimed", "k-stale"}, keys)
	var left []string
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Order("dedup_key").Pluck("dedup_key", &left).Error)
	assert.Equal(t, []string{"k-live", "k-live", "k-other-kb", "k-retract", "k-retract-live", "k-retract-live"}, left)

	type row struct {
		ID                   string
		ParseStatus          string
		PendingSubtasksCount int
	}
	var rows []row
	require.NoError(t, db.Raw(
		`SELECT id, parse_status, pending_subtasks_count FROM knowledges ORDER BY id`,
	).Scan(&rows).Error)
	assert.Equal(t, []row{
		{ID: "k-live", ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 1},
		{ID: "k-retract-live", ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 1},
		{ID: "k-stale", ParseStatus: types.ParseStatusFinalizing, PendingSubtasksCount: 1},
		{ID: "k-unclaimed", ParseStatus: types.ParseStatusCompleted, PendingSubtasksCount: 0},
	}, rows)
}

// A failed release rolls the delete back, so the retry still finds the ops.
func TestTaskPendingOps_DrainUnclaimedAndReleaseRollsBackOnReleaseFailure(t *testing.T) {
	db, repo, drainer := setupDrainTest(t)
	ctx := context.Background()
	require.NoError(t, repo.Enqueue(ctx, wikiLane("kb-1", "ingest", "k-1")))
	require.NoError(t, db.Exec(`DROP TABLE knowledges`).Error)

	keys, err := drainer.DrainUnclaimedAndRelease(ctx, types.TypeWikiIngest, types.TaskScopeKnowledgeBase, "kb-1",
		"ingest", time.Now().Add(-time.Hour))

	require.Error(t, err)
	assert.Empty(t, keys)
	var count int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}
