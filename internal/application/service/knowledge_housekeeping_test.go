package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/config"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// knowledgeTestDDL is the minimal subset of the knowledge schema this
// suite needs. We avoid AutoMigrate because Knowledge carries multiple
// JSONB-tagged fields whose SQLite mapping is fragile.
//
// Table name is `knowledges` (plural) — that's what migration 000000
// creates and what GORM's default pluralization expects when the
// service code uses Model(&types.Knowledge{}).
const knowledgeTestDDL = `
CREATE TABLE IF NOT EXISTS knowledges (
    id              VARCHAR(64) PRIMARY KEY,
    tenant_id       INTEGER NOT NULL DEFAULT 0,
    knowledge_base_id VARCHAR(64),
    parse_status    VARCHAR(32) NOT NULL DEFAULT 'pending',
    summary_status  VARCHAR(32) NOT NULL DEFAULT 'none',
    pending_subtasks_count INTEGER NOT NULL DEFAULT 0,
    error_message   TEXT,
    title           TEXT,
    file_type       TEXT,
    enable_status   TEXT NOT NULL DEFAULT 'enabled',
    type            TEXT NOT NULL DEFAULT 'document',
    embedding_model_id TEXT NOT NULL DEFAULT '',
    storage_size    BIGINT NOT NULL DEFAULT 0,
    processed_at    DATETIME,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    deleted_at      DATETIME
);
`

const housekeepingSpansDDL = `
CREATE TABLE IF NOT EXISTS knowledge_processing_spans (
    id              INTEGER PRIMARY KEY AUTOINCREMENT,
    knowledge_id    VARCHAR(64) NOT NULL,
    attempt         INTEGER     NOT NULL DEFAULT 1,
    span_id         VARCHAR(64) NOT NULL,
    parent_span_id  VARCHAR(64),
    name            VARCHAR(64) NOT NULL,
    kind            VARCHAR(16) NOT NULL,
    status          VARCHAR(16) NOT NULL,
    input           TEXT,
    output          TEXT,
    metadata        TEXT,
    error_code      VARCHAR(64),
    error_message   TEXT,
    error_detail    TEXT,
    started_at      DATETIME,
    finished_at     DATETIME,
    duration_ms     BIGINT,
    created_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    updated_at      DATETIME DEFAULT CURRENT_TIMESTAMP,
    UNIQUE (knowledge_id, attempt, span_id)
);
`

const housekeepingPendingOpsDDL = `
CREATE TABLE IF NOT EXISTS task_pending_ops (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    task_type   VARCHAR(64) NOT NULL,
    scope       VARCHAR(64) NOT NULL,
    scope_id    VARCHAR(128) NOT NULL,
    op          VARCHAR(64) NOT NULL,
    dedup_key   VARCHAR(128) NOT NULL DEFAULT '',
    payload     TEXT NOT NULL DEFAULT '{}'
);
`

func setupHousekeepingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeTestDDL).Error)
	require.NoError(t, db.Exec(housekeepingSpansDDL).Error)
	require.NoError(t, db.Exec(housekeepingPendingOpsDDL).Error)
	return db
}

// insertKnowledge writes a knowledge row at the given updated_at. We
// can't pass updated_at through GORM defaults since CURRENT_TIMESTAMP
// would override our test fixture; raw SQL keeps the timestamp.
func insertKnowledge(t *testing.T, db *gorm.DB, id, status string, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, updated_at) VALUES (?, ?, ?)`,
		id, status, updatedAt,
	).Error)
}

func insertKnowledgeWithPending(t *testing.T, db *gorm.DB, id, status string, pending int, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledges (id, parse_status, pending_subtasks_count, updated_at) VALUES (?, ?, ?, ?)`,
		id, status, pending, updatedAt,
	).Error)
}

func insertSpan(t *testing.T, db *gorm.DB, kid string, attempt int, spanID, status string, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status, updated_at)
		 VALUES (?, ?, ?, 'docreader', 'stage', ?, ?)`,
		kid, attempt, spanID, status, updatedAt,
	).Error)
}

func insertNamedSpan(t *testing.T, db *gorm.DB, kid string, attempt int, spanID, name, kind, status string, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?)`,
		kid, attempt, spanID, name, kind, status, updatedAt,
	).Error)
}

func insertPendingOp(t *testing.T, db *gorm.DB, knowledgeID string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO task_pending_ops (task_type, scope, scope_id, op, dedup_key, payload)
		 VALUES ('wiki:ingest', 'knowledge_base', 'kb', 'ingest', ?, '{}')`,
		knowledgeID,
	).Error)
}

// fakeTaskInspector is a controllable TaskInspector for the housekeeping
// suite. queued maps knowledge_id → "still has a queued task"; err forces
// the probe to fail so the fail-safe branch can be exercised.
type fakeTaskInspector struct {
	queued map[string]bool
	err    error
}

func (f fakeTaskInspector) CancelTasksForKnowledge(
	_ context.Context, _ string,
) (int, int, error) {
	return 0, 0, nil
}

func (f fakeTaskInspector) HasQueuedTasksForKnowledge(
	_ context.Context, knowledgeID string,
) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.queued[knowledgeID], nil
}

func newHousekeepingSvcForTest(db *gorm.DB) *HousekeepingService {
	return newHousekeepingSvcWithInspector(db, fakeTaskInspector{})
}

func newHousekeepingSvcWithInspector(db *gorm.DB, inspector interfaces.TaskInspector) *HousekeepingService {
	cfg := &config.Config{KnowledgeBase: &config.KnowledgeBaseConfig{
		// 1h floor + 10min buffer = 70min cutoff. Tight enough to keep
		// the test's relative timestamps in seconds; the production
		// default of 2h+10min is just a constant scale factor.
		DocumentProcessTimeout: 1 * time.Hour,
	}}
	return NewHousekeepingService(db, cfg, inspector)
}

// TestHousekeeping_RecoversAbandoned exercises the happy path: a
// knowledge stuck at "processing" with no recent heartbeat (no spans,
// stale knowledge.updated_at) MUST be flipped to failed.
func TestHousekeeping_RecoversAbandoned(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour) // well past 70min cutoff
	insertKnowledge(t, db, "kid-abandoned", types.ParseStatusProcessing, stale)

	svc.runSweep(context.Background())

	var status, errMsg string
	require.NoError(t, db.Raw(
		`SELECT parse_status, error_message FROM knowledges WHERE id = ?`, "kid-abandoned",
	).Row().Scan(&status, &errMsg))
	assert.Equal(t, types.ParseStatusFailed, status)
	assert.Contains(t, errMsg, "stuck in processing")
}

// TestHousekeeping_NoFalseKill_ActiveSpan is the regression test for
// the "long DocReader silently runs longer than DocumentProcessTimeout"
// scenario the user flagged. A knowledge whose knowledge.updated_at
// looks stale BUT whose span tree shows recent activity must NOT be
// killed.
func TestHousekeeping_NoFalseKill_ActiveSpan(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-active", types.ParseStatusProcessing, stale)
	// Span heartbeat well within the 70min cutoff — it represents
	// "we're STILL working, the worker just hasn't transitioned the
	// parse_status column yet".
	insertSpan(t, db, "kid-active", 1, "docreader-1", types.SpanStatusRunning, time.Now().Add(-2*time.Minute))

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-active",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusProcessing, status,
		"knowledge with recent span heartbeat must NOT be flipped to failed")
}

// TestHousekeeping_NoFalseKill_StaleSpanRecovers confirms the inverse:
// a knowledge whose span tree has ALSO gone silent past the threshold
// is genuinely stuck and must be recovered.
func TestHousekeeping_NoFalseKill_StaleSpanRecovers(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-stuck", types.ParseStatusProcessing, stale)
	// Span row stale by the same amount — no recent activity anywhere.
	insertSpan(t, db, "kid-stuck", 1, "docreader-1", types.SpanStatusRunning, stale)

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-stuck",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFailed, status,
		"genuinely stuck knowledge (knowledge AND spans both stale) must still be recovered")
}

// TestHousekeeping_NoFalseKill_TasksStillQueued is the regression test
// for the backpressure case: a finalizing row whose span heartbeat has
// gone stale (enrichment subtasks fanned out but no worker has picked
// them up yet) must NOT be killed while its tasks are still queued.
func TestHousekeeping_NoFalseKill_TasksStillQueued(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		queued: map[string]bool{"kid-backlogged": true},
	})
	stale := time.Now().Add(-3 * time.Hour)
	// finalizing + stale knowledge + stale span: span-only heuristics
	// would flag this as stuck, but the queue still holds its subtasks.
	insertKnowledgeWithPending(t, db, "kid-backlogged", types.ParseStatusFinalizing, 1, stale)
	insertSpan(t, db, "kid-backlogged", 1, "post-1", types.SpanStatusRunning, stale)

	svc.runSweep(context.Background())

	var status string
	var pending int
	require.NoError(t, db.Raw(
		`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, "kid-backlogged",
	).Row().Scan(&status, &pending))
	assert.Equal(t, types.ParseStatusFinalizing, status,
		"finalizing row with tasks still queued must NOT be flipped to failed")
	assert.Equal(t, 1, pending)
}

// TestHousekeeping_ReconcilesOrphanedFinalizingToCompleted covers the
// user-visible #1794 case: the latest attempt is already terminal and no
// queue task references the knowledge, but a stale finalizing counter and
// historical running span keep the UI stuck in "optimizing".
func TestHousekeeping_ReconcilesOrphanedFinalizingToCompleted(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledgeWithPending(t, db, "kid-orphan", types.ParseStatusFinalizing, 5, stale)
	insertNamedSpan(t, db, "kid-orphan", 1, "wiki-old", "postprocess.wiki", types.SpanKindSubSpan, types.SpanStatusRunning, stale)
	insertNamedSpan(t, db, "kid-orphan", 2, "root", "knowledge_processing", types.SpanKindRoot, types.SpanStatusDone, stale)
	insertNamedSpan(t, db, "kid-orphan", 2, "post", types.StagePostProcess, types.SpanKindStage, types.SpanStatusDone, stale)

	svc.runSweep(context.Background())

	var status string
	var pending int
	require.NoError(t, db.Raw(
		`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, "kid-orphan",
	).Row().Scan(&status, &pending))
	assert.Equal(t, types.ParseStatusCompleted, status)
	assert.Equal(t, 0, pending)

	var oldStatus, oldCode string
	require.NoError(t, db.Raw(
		`SELECT status, error_code FROM knowledge_processing_spans WHERE knowledge_id = ? AND span_id = ?`,
		"kid-orphan", "wiki-old",
	).Row().Scan(&oldStatus, &oldCode))
	assert.Equal(t, types.SpanStatusCancelled, oldStatus)
	assert.Equal(t, "TASK_ORPHANED", oldCode)
}

func TestHousekeeping_PreservesFinalizingWithPendingOp(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledgeWithPending(t, db, "kid-pending-op", types.ParseStatusFinalizing, 1, stale)
	insertPendingOp(t, db, "kid-pending-op")
	insertNamedSpan(t, db, "kid-pending-op", 2, "root", "knowledge_processing", types.SpanKindRoot, types.SpanStatusDone, stale)

	svc.runSweep(context.Background())

	var status string
	var pending int
	require.NoError(t, db.Raw(
		`SELECT parse_status, pending_subtasks_count FROM knowledges WHERE id = ?`, "kid-pending-op",
	).Row().Scan(&status, &pending))
	assert.Equal(t, types.ParseStatusFinalizing, status)
	assert.Equal(t, 1, pending)
}

// TestHousekeeping_QueueProbeError_FailsSafe confirms the fail-safe
// direction: when the queue probe errors we still recover the row rather
// than leaving it stranded forever.
func TestHousekeeping_QueueProbeError_FailsSafe(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		err: errors.New("redis unavailable"),
	})
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-probeerr", types.ParseStatusProcessing, stale)

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-probeerr",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFailed, status,
		"queue probe error must fail safe and still recover the stuck row")
}

// TestHousekeeping_PreservesRecentlyTouched: any knowledge whose
// updated_at is within the cutoff is left alone — that's the cheap
// fast path that doesn't even consult the spans table.
func TestHousekeeping_PreservesRecentlyTouched(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	insertKnowledge(t, db, "kid-fresh", types.ParseStatusProcessing, time.Now().Add(-30*time.Second))

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-fresh",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusProcessing, status,
		"knowledge updated within the cutoff must be left alone")
}
