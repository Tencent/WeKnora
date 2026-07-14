package container

import (
	"encoding/json"
	"os"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const resetPendingKnowledgeDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id              VARCHAR(64) PRIMARY KEY,
    parse_status    VARCHAR(32) NOT NULL DEFAULT 'pending',
    summary_status  VARCHAR(32) NOT NULL DEFAULT 'none',
    pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at      DATETIME
);
`

const resetPendingSyncLogDDL = `
CREATE TABLE IF NOT EXISTS sync_logs (
    id              VARCHAR(64) PRIMARY KEY,
    data_source_id  VARCHAR(64) NOT NULL DEFAULT '',
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    status          VARCHAR(32) NOT NULL,
    started_at      DATETIME,
    finished_at     DATETIME,
    error_message   TEXT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP
);
`

const resetPendingSpansDDL = `
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         INTEGER NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(255) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    error_code      VARCHAR(64),
    error_message   TEXT,
    started_at      DATETIME,
    finished_at     DATETIME,
    duration_ms     INTEGER,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (knowledge_id, attempt, span_id)
);
`

const resetPendingOpsDDL = `
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id   INTEGER NOT NULL DEFAULT 0,
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

func setupResetPendingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(resetPendingKnowledgeDDL).Error)
	require.NoError(t, db.Exec(resetPendingSyncLogDDL).Error)
	require.NoError(t, db.Exec(resetPendingSpansDDL).Error)
	require.NoError(t, db.Exec(resetPendingOpsDDL).Error)
	return db
}

func TestResetPendingTasks_KnowledgeFindThenUpdate(t *testing.T) {
	db := setupResetPendingDB(t)
	stale := time.Now().Add(-2 * time.Hour)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, updated_at) VALUES (?, ?, ?)`,
		"k-stuck", types.ParseStatusProcessing, stale,
	).Error)

	os.Unsetenv("REDIS_ADDR")
	resetPendingTasks(db)

	var status, errMsg string
	require.NoError(t, db.Raw(
		`SELECT parse_status, error_message FROM knowledges WHERE id = ?`, "k-stuck",
	).Row().Scan(&status, &errMsg))
	assert.Equal(t, types.ParseStatusFailed, status)
	assert.Contains(t, errMsg, "application restart")
}

func TestResetPendingTasks_KnowledgeFreshInDistributedMode(t *testing.T) {
	db := setupResetPendingDB(t)
	fresh := time.Now().Add(-5 * time.Minute)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, updated_at) VALUES (?, ?, ?)`,
		"k-fresh", types.ParseStatusProcessing, fresh,
	).Error)

	t.Setenv("REDIS_ADDR", "redis:6379")
	resetPendingTasks(db)

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "k-fresh",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusProcessing, status)
}

func TestResetPendingTasks_DistributedModePreservesEveryStage(t *testing.T) {
	cases := []struct {
		name        string
		parseStatus string
	}{
		{"pending", types.ParseStatusPending},
		{"docreader_chunking_embedding", types.ParseStatusProcessing},
		{"multimodal", types.ParseStatusProcessing},
		{"postprocess", types.ParseStatusFinalizing},
		{"wiki", types.ParseStatusFinalizing},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			db := setupResetPendingDB(t)
			stale := time.Now().Add(-2 * time.Hour)
			require.NoError(t, db.Exec(
				`INSERT INTO knowledges (id, parse_status, updated_at) VALUES (?, ?, ?)`,
				"k-active-span", tc.parseStatus, stale,
			).Error)

			t.Setenv("REDIS_ADDR", "redis:6379")
			resetPendingTasks(db)

			var status string
			require.NoError(t, db.Raw(
				`SELECT parse_status FROM knowledges WHERE id = ?`, "k-active-span",
			).Row().Scan(&status))
			assert.Equal(t, tc.parseStatus, status,
				"distributed tasks belong to Asynq/housekeeping, not startup reset")
		})
	}
}

func TestResetPendingTasks_DistributedSummaryTaskSurvivesRestart(t *testing.T) {
	db := setupResetPendingDB(t)
	stale := time.Now().Add(-2 * time.Hour)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, summary_status, updated_at)
		 VALUES (?, ?, ?, ?)`,
		"k-summary", types.ParseStatusCompleted, types.SummaryStatusProcessing, stale,
	).Error)

	t.Setenv("REDIS_ADDR", "redis:6379")
	resetPendingTasks(db)

	var status string
	require.NoError(t, db.Raw(
		`SELECT summary_status FROM knowledges WHERE id = ?`, "k-summary",
	).Row().Scan(&status))
	assert.Equal(t, types.SummaryStatusProcessing, status)
}

func TestResetPendingTasks_DurableWikiOpSurvivesLiteRestart(t *testing.T) {
	db := setupResetPendingDB(t)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, pending_subtasks_count)
		 VALUES (?, ?, 1)`, "k-wiki", types.ParseStatusFinalizing,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO task_pending_ops
		 (tenant_id, task_type, scope, scope_id, op, dedup_key, payload)
		 VALUES (7, ?, ?, 'kb-wiki', 'ingest', 'k-wiki', '{}')`,
		types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
	).Error)

	os.Unsetenv("REDIS_ADDR")
	resetPendingTasks(db)

	var status string
	var pending int
	require.NoError(t, db.Raw(
		`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, "k-wiki",
	).Row().Scan(&status, &pending))
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 1, pending, "resumed wiki worker still owns its finalizing slot")
}

func TestResetPendingTasks_LiteWikiDoesNotHideOtherLostSubtasks(t *testing.T) {
	db := setupResetPendingDB(t)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, pending_subtasks_count)
		 VALUES (?, ?, 2)`, "k-wiki-plus-summary", types.ParseStatusFinalizing,
	).Error)
	require.NoError(t, db.Exec(
		`INSERT INTO task_pending_ops
		 (tenant_id, task_type, scope, scope_id, op, dedup_key, payload)
		 VALUES (7, ?, ?, 'kb-wiki', 'ingest', 'k-wiki-plus-summary', '{}')`,
		types.TypeWikiIngest, types.TaskScopeKnowledgeBase,
	).Error)

	os.Unsetenv("REDIS_ADDR")
	resetPendingTasks(db)

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "k-wiki-plus-summary",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFailed, status,
		"a durable wiki op cannot recover another lost in-memory subtask")
}

func TestResetPendingTasks_SyncLogStaleRunning(t *testing.T) {
	db := setupResetPendingDB(t)
	stale := time.Now().Add(-2 * time.Hour)
	require.NoError(t, db.Exec(
		`INSERT INTO sync_logs (id, status, started_at) VALUES (?, ?, ?)`,
		"sync-1", types.SyncLogStatusRunning, stale,
	).Error)

	t.Setenv("REDIS_ADDR", "redis:6379")
	resetPendingTasks(db)

	var status string
	var finishedAt *time.Time
	require.NoError(t, db.Raw(
		`SELECT status, finished_at FROM sync_logs WHERE id = ?`, "sync-1",
	).Row().Scan(&status, &finishedAt))
	assert.Equal(t, types.SyncLogStatusFailed, status)
	require.NotNil(t, finishedAt)
}

func TestResetPendingTasks_SyncLogLiteMode(t *testing.T) {
	db := setupResetPendingDB(t)
	os.Unsetenv("REDIS_ADDR")
	require.NoError(t, db.Exec(
		`INSERT INTO sync_logs (id, status, started_at) VALUES (?, ?, ?)`,
		"sync-lite", types.SyncLogStatusRunning, time.Now(),
	).Error)

	resetPendingTasks(db)

	var status string
	require.NoError(t, db.Raw(
		`SELECT status FROM sync_logs WHERE id = ?`, "sync-lite",
	).Row().Scan(&status))
	assert.Equal(t, types.SyncLogStatusFailed, status)
}

func TestStuckKnowledgeParseQuery_ReuseAfterFindDoesNotBreakUpdate(t *testing.T) {
	db := setupResetPendingDB(t)
	stale := time.Now().Add(-2 * time.Hour)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, updated_at) VALUES (?, ?, ?)`,
		"k-reuse", types.ParseStatusProcessing, stale,
	).Error)

	var rows []types.Knowledge
	q := stuckKnowledgeParseQuery(db)
	require.NoError(t, q.Select("id").Find(&rows).Error)
	require.Len(t, rows, 1)

	result := stuckKnowledgeParseQuery(db).Updates(map[string]interface{}{
		"parse_status": types.ParseStatusFailed,
	})
	require.NoError(t, result.Error)
	assert.Equal(t, int64(1), result.RowsAffected)
}

type recordingTaskEnqueuer struct {
	tasks []*asynq.Task
}

func (r *recordingTaskEnqueuer) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	r.tasks = append(r.tasks, task)
	return &asynq.TaskInfo{ID: "test", Type: task.Type()}, nil
}

func TestRecoverPendingWikiTasks_RecreatesOneTriggerPerLaneAndKB(t *testing.T) {
	db := setupResetPendingDB(t)
	rows := []struct {
		tenantID uint64
		taskType string
		kbID     string
		dedup    string
	}{
		{7, types.TypeWikiIngest, "kb-a", "k-1"},
		{7, types.TypeWikiIngest, "kb-a", "k-2"}, // same lane: one trigger
		{7, types.TypeWikiFinalize, "kb-a", "slug-a"},
		{8, types.TypeWikiIngest, "kb-b", "k-3"},
	}
	for _, row := range rows {
		require.NoError(t, db.Exec(
			`INSERT INTO task_pending_ops
			 (tenant_id, task_type, scope, scope_id, op, dedup_key, payload)
			 VALUES (?, ?, ?, ?, 'ingest', ?, '{}')`,
			row.tenantID, row.taskType, types.TaskScopeKnowledgeBase, row.kbID, row.dedup,
		).Error)
	}

	recorder := &recordingTaskEnqueuer{}
	recoverPendingWikiTasks(db, recorder)
	require.Len(t, recorder.tasks, 3)

	seen := map[string]service.WikiIngestPayload{}
	for _, task := range recorder.tasks {
		var payload service.WikiIngestPayload
		require.NoError(t, json.Unmarshal(task.Payload(), &payload))
		seen[task.Type()+":"+payload.KnowledgeBaseID] = payload
	}
	assert.Equal(t, uint64(7), seen[types.TypeWikiIngest+":kb-a"].TenantID)
	assert.Equal(t, uint64(7), seen[types.TypeWikiFinalize+":kb-a"].TenantID)
	assert.Equal(t, uint64(8), seen[types.TypeWikiIngest+":kb-b"].TenantID)
}
