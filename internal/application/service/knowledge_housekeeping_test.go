package service

import (
	"context"
	"errors"
	"fmt"
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
    profile TEXT,
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
    name            VARCHAR(255) NOT NULL,
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

func setupHousekeepingDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(knowledgeTestDDL).Error)
	require.NoError(t, db.Exec(housekeepingSpansDDL).Error)
	require.NoError(t, db.Exec(housekeepingPendingOpsDDL).Error)
	return db
}

// insertWikiPendingOp mirrors newWikiIngestPendingOp: the durable row is
// scoped to the KB but deduplicated on the knowledge ID, which is exactly
// why the per-knowledge asynq probe cannot see it.
func insertWikiPendingOp(t *testing.T, db *gorm.DB, kbID, knowledgeID string) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO task_pending_ops (task_type, scope, scope_id, op, dedup_key, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		wikiTaskType, wikiTaskScope, kbID, WikiOpIngest, knowledgeID,
		`{"op":"ingest","knowledge_id":"`+knowledgeID+`"}`,
	).Error)
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

func insertSpan(t *testing.T, db *gorm.DB, kid string, attempt int, spanID, status string, updatedAt time.Time) {
	t.Helper()
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_processing_spans (knowledge_id, attempt, span_id, name, kind, status, updated_at)
		 VALUES (?, ?, ?, 'docreader', 'stage', ?, ?)`,
		kid, attempt, spanID, status, updatedAt,
	).Error)
}

// fakeTaskInspector is a controllable TaskInspector for the housekeeping
// suite. queued maps knowledge_id → "still has a queued task"; err forces
// the probe to fail so the fail-safe branch can be exercised.
type fakeTaskInspector struct {
	queued map[string]bool
	// deleteQueued independently controls the delete-task liveness probe;
	// nil means "no delete task alive" for every ID.
	deleteQueued map[string]bool
	err          error
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

func (f fakeTaskInspector) QueuedKnowledgeIDs(context.Context) (map[string]struct{}, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]struct{})
	for id, queued := range f.queued {
		if queued {
			out[id] = struct{}{}
		}
	}
	return out, nil
}

func (f fakeTaskInspector) HasQueuedDeleteTasksForKnowledge(
	_ context.Context, knowledgeID string,
) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.deleteQueued[knowledgeID], nil
}

func (f fakeTaskInspector) QueueStats(
	_ context.Context,
) ([]types.QueueStat, bool, error) {
	return nil, false, nil
}

func (f fakeTaskInspector) WorkerServerStats(
	_ context.Context,
) ([]types.WorkerServerStat, bool, error) {
	return nil, false, nil
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
	return NewHousekeepingService(db, cfg, inspector, nil)
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

func TestHousekeeping_RecoversPendingTaskMissingFromQueue(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-pending-orphan", types.ParseStatusPending, stale)

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-pending-orphan",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFailed, status,
		"a stale pending row with no queue task must not remain pending forever")
}

func TestHousekeeping_PreservesPendingTaskStillQueued(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		queued: map[string]bool{"kid-pending-queued": true},
	})
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-pending-queued", types.ParseStatusPending, stale)

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-pending-queued",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusPending, status,
		"backlogged pending work remains owned by the durable queue")
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
	insertKnowledge(t, db, "kid-backlogged", types.ParseStatusFinalizing, stale)
	insertSpan(t, db, "kid-backlogged", 1, "post-1", types.SpanStatusRunning, stale)

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-backlogged",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFinalizing, status,
		"finalizing row with tasks still queued must NOT be flipped to failed")
}

// A document whose only outstanding work is a queued Wiki ingest is
// invisible to the asynq probe: the durable op lives in task_pending_ops
// keyed by knowledge ID, while asynq holds only a per-KB trigger, and
// TypeWikiIngest is not in taskTypesForKnowledgeCancel either. The
// inspector below reports "nothing queued" — exactly what production does
// — so without the durable gate the sweep force-fails a healthy row.
func TestHousekeeping_NoFalseKill_DurableWikiIngestPending(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{})
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-durable-wiki", types.ParseStatusFinalizing, stale)
	insertSpan(t, db, "kid-durable-wiki", 1, "wiki-1", types.SpanStatusRunning, stale)
	insertWikiPendingOp(t, db, "kb-1", "kid-durable-wiki")

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-durable-wiki",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFinalizing, status,
		"row with a durable wiki ingest op pending must NOT be flipped to failed")
}

// The durable gate must not become a blanket amnesty: a stale row with no
// pending op and nothing in the queue is still genuinely orphaned, and the
// sweep must keep recovering it. This is the regression guard for the gate
// itself.
func TestHousekeeping_StillRecoversWhenNoDurableOp(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{})
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-orphan", types.ParseStatusFinalizing, stale)
	insertSpan(t, db, "kid-orphan", 1, "wiki-1", types.SpanStatusRunning, stale)
	// A pending op for a DIFFERENT document must not shield this one.
	insertWikiPendingOp(t, db, "kb-1", "kid-someone-else")

	svc.runSweep(context.Background())

	var status string
	require.NoError(t, db.Raw(
		`SELECT parse_status FROM knowledges WHERE id = ?`, "kid-orphan",
	).Row().Scan(&status))
	assert.Equal(t, types.ParseStatusFailed, status,
		"row with no durable op and nothing queued must still be recovered")
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

// --- Sweep C: stranded "deleting" rows (issues #3338/#3345) --—

func readKnowledgeStatus(t *testing.T, db *gorm.DB, id string) (string, string) {
	t.Helper()
	var status, errMsg string
	require.NoError(t, db.Raw(
		`SELECT parse_status, COALESCE(error_message, '') FROM knowledges WHERE id = ?`, id,
	).Row().Scan(&status, &errMsg))
	return status, errMsg
}

func TestHousekeeping_RecoversStrandedDeletingRow(t *testing.T) {
	db := setupHousekeepingDB(t)
	// No deleteQueued entry: the delete task is gone (worker death / lost
	// queue), the dead-letter path never fired, the row is stranded.
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-stranded-delete", types.ParseStatusDeleting, stale)

	svc.runSweep(context.Background())

	status, errMsg := readKnowledgeStatus(t, db, "kid-stranded-delete")
	assert.Equal(t, types.ParseStatusFailed, status)
	assert.Contains(t, errMsg, "stranded")
}

func TestHousekeeping_KeepsBackloggedDeletingRow(t *testing.T) {
	db := setupHousekeepingDB(t)
	// A live (queued/active/retry) knowledge:list_delete still covers the
	// row — backpressure, not stranded; must stay untouched.
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		deleteQueued: map[string]bool{"kid-backlogged-delete": true},
	})
	insertKnowledge(t, db, "kid-backlogged-delete", types.ParseStatusDeleting,
		time.Now().Add(-3*time.Hour))

	svc.runSweep(context.Background())

	status, _ := readKnowledgeStatus(t, db, "kid-backlogged-delete")
	assert.Equal(t, types.ParseStatusDeleting, status)
}

func TestHousekeeping_FreshDeletingRowUntouched(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	// Marked deleting moments ago: below the staleness cutoff, so the sweep
	// must not even consider it (a delete just handed to the queue).
	insertKnowledge(t, db, "kid-fresh-delete", types.ParseStatusDeleting, time.Now())

	svc.runSweep(context.Background())

	status, _ := readKnowledgeStatus(t, db, "kid-fresh-delete")
	assert.Equal(t, types.ParseStatusDeleting, status)
}

func TestHousekeeping_DeletingProbeErrorDefers(t *testing.T) {
	db := setupHousekeepingDB(t)
	// Backend probe error must defer the row to the next sweep rather than
	// guess: failing a live delete would surface a wrong "failed" card.
	svc := newHousekeepingSvcWithInspector(db, fakeTaskInspector{
		err: assert.AnError,
	})
	insertKnowledge(t, db, "kid-probe-err-delete", types.ParseStatusDeleting,
		time.Now().Add(-3*time.Hour))

	svc.runSweep(context.Background())

	status, _ := readKnowledgeStatus(t, db, "kid-probe-err-delete")
	assert.Equal(t, types.ParseStatusDeleting, status)
}

func TestHousekeeping_NilInspectorDefersDeletingSweep(t *testing.T) {
	db := setupHousekeepingDB(t)
	// Without any inspector wired there is no way to tell backlog from
	// orphan, so the whole Sweep C defers — never guesses.
	svc := newHousekeepingSvcWithInspector(db, nil)
	insertKnowledge(t, db, "kid-nil-inspector", types.ParseStatusDeleting,
		time.Now().Add(-3*time.Hour))

	svc.runSweep(context.Background())

	status, _ := readKnowledgeStatus(t, db, "kid-nil-inspector")
	assert.Equal(t, types.ParseStatusDeleting, status)
}

// A recovered row names the stage it stalled in and when it last moved, and
// its open spans are closed so the timeline stops showing them as running.
func TestHousekeeping_RecoveredRowNamesStalledStageAndClosesSpans(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	lastBeat := stale.Add(30 * time.Minute)
	insertKnowledge(t, db, "kid-stalled", types.ParseStatusProcessing, stale)
	insertSpan(t, db, "kid-stalled", 1, "doc-1", types.SpanStatusRunning, lastBeat)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_processing_spans
		   (knowledge_id, attempt, span_id, parent_span_id, name, kind, status, updated_at)
		 VALUES ('kid-stalled', 1, 'sub-1', 'doc-1', 'docreader.call', 'subspan', 'running', ?)`, stale,
	).Error)

	svc.runSweep(context.Background())

	var status, errMsg string
	require.NoError(t, db.Raw(
		`SELECT parse_status, error_message FROM knowledges WHERE id = ?`, "kid-stalled",
	).Row().Scan(&status, &errMsg))
	assert.Equal(t, types.ParseStatusFailed, status)
	assert.Contains(t, errMsg, "stuck in processing at docreader stage")
	assert.Contains(t, errMsg, lastBeat.UTC().Format(time.RFC3339))

	type spanState struct {
		SpanID    string
		Status    string
		ErrorCode string
	}
	var spans []spanState
	require.NoError(t, db.Raw(
		`SELECT span_id, status, error_code FROM knowledge_processing_spans WHERE knowledge_id = ? ORDER BY span_id`,
		"kid-stalled",
	).Scan(&spans).Error)
	assert.Equal(t, []spanState{
		{SpanID: "doc-1", Status: types.SpanStatusFailed, ErrorCode: "TASK_STALLED"},
		{SpanID: "sub-1", Status: types.SpanStatusCancelled, ErrorCode: "TASK_STALLED"},
	}, spans)
}

func insertTreeSpan(
	t *testing.T, db *gorm.DB, kid, spanID, parent, name, kind, status string, updatedAt time.Time,
) {
	t.Helper()
	started := updatedAt.Add(-5 * time.Minute)
	require.NoError(t, db.Exec(
		`INSERT INTO knowledge_processing_spans
		   (knowledge_id, attempt, span_id, parent_span_id, name, kind, status, started_at, updated_at)
		 VALUES (?, 1, ?, ?, ?, ?, ?, ?, ?)`,
		kid, spanID, parent, name, kind, status, started, updatedAt,
	).Error)
}

type stalledSpanRow struct {
	SpanID     string
	Status     string
	ErrorCode  string
	DurationMs int64
}

func stalledSpanRows(t *testing.T, db *gorm.DB, kid string) map[string]stalledSpanRow {
	t.Helper()
	var rows []stalledSpanRow
	require.NoError(t, db.Raw(
		`SELECT span_id, status, COALESCE(error_code, '') AS error_code, COALESCE(duration_ms, 0) AS duration_ms
		 FROM knowledge_processing_spans WHERE knowledge_id = ?`, kid,
	).Scan(&rows).Error)
	out := make(map[string]stalledSpanRow, len(rows))
	for _, r := range rows {
		out[r.SpanID] = r
	}
	return out
}

// A row stuck in finalizing has its post-process stage already closed; the
// stalled work is the enrichment subspans, which are what fail.
func TestHousekeeping_FinalizingStallFailsTheRunningSubspan(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-fin", types.ParseStatusFinalizing, stale)
	insertTreeSpan(t, db, "kid-fin", "root", "", "knowledge_processing", types.SpanKindRoot,
		types.SpanStatusDone, stale)
	insertTreeSpan(t, db, "kid-fin", "post", "root", types.StagePostProcess, types.SpanKindStage,
		types.SpanStatusDone, stale)
	insertTreeSpan(t, db, "kid-fin", "summary", "post", "postprocess.summary", types.SpanKindSubSpan,
		types.SpanStatusRunning, stale)
	insertTreeSpan(t, db, "kid-fin", "wiki", "post", "postprocess.wiki", types.SpanKindSubSpan,
		types.SpanStatusPending, stale)

	svc.runSweep(context.Background())

	var errMsg string
	require.NoError(t, db.Raw(`SELECT error_message FROM knowledges WHERE id = 'kid-fin'`).Scan(&errMsg).Error)
	assert.Contains(t, errMsg, "stuck in finalizing at postprocess stage (postprocess.summary)")
	spans := stalledSpanRows(t, db, "kid-fin")
	assert.Equal(t, types.SpanStatusFailed, spans["summary"].Status)
	assert.Equal(t, "TASK_STALLED", spans["summary"].ErrorCode)
	assert.Positive(t, spans["summary"].DurationMs, "a closed span carries its duration")
	assert.Equal(t, types.SpanStatusCancelled, spans["wiki"].Status)
	assert.Equal(t, types.SpanStatusDone, spans["post"].Status, "finished spans are left alone")
	assert.Equal(t, types.SpanStatusDone, spans["root"].Status)
}

// Two stages stalled together are both named, in pipeline order.
func TestHousekeeping_NamesEveryStalledStageInOrder(t *testing.T) {
	db := setupHousekeepingDB(t)
	svc := newHousekeepingSvcForTest(db)
	stale := time.Now().Add(-3 * time.Hour)
	insertKnowledge(t, db, "kid-two", types.ParseStatusProcessing, stale)
	running := types.SpanStatusRunning
	insertTreeSpan(t, db, "kid-two", "mm", "", types.StageMultimodal, types.SpanKindStage, running, stale)
	insertTreeSpan(t, db, "kid-two", "emb", "", types.StageEmbedding, types.SpanKindStage, running, stale)

	svc.runSweep(context.Background())

	var errMsg string
	require.NoError(t, db.Raw(`SELECT error_message FROM knowledges WHERE id = 'kid-two'`).Scan(&errMsg).Error)
	assert.Contains(t, errMsg, "at embedding/multimodal stage")
	spans := stalledSpanRows(t, db, "kid-two")
	assert.Equal(t, types.SpanStatusFailed, spans["mm"].Status)
	assert.Equal(t, types.SpanStatusFailed, spans["emb"].Status)
}

// Without a heartbeat the message gives no time: updated_at can predate the
// last span write.
func TestStallMessageOmitsTimeWithoutHeartbeat(t *testing.T) {
	k := types.Knowledge{ID: "k", ParseStatus: types.ParseStatusProcessing, UpdatedAt: time.Now().Add(-5 * time.Hour)}
	msg := stallMessage(k, &stallSite{stages: []string{types.StageDocReader}}, nil, 70*time.Minute)
	assert.Equal(t,
		"task stuck in processing at docreader stage: no progress for > 1h10m0s, recovered by housekeeping", msg)
}

// QueuedWork answers a whole batch from the durable Wiki table plus one shared
// queue scan, reuses that scan across calls, and reports a failed scan as an
// error without caching it.
func TestHousekeeping_QueuedWorkSharesOneQueueScan(t *testing.T) {
	db := setupHousekeepingDB(t)
	inspector := &countingTaskInspector{queued: map[string]bool{"k-queued": true}}
	svc := newHousekeepingSvcWithInspector(db, inspector)
	insertWikiPendingOp(t, db, "kb-1", "k-wiki")
	ids := []string{"k-wiki", "k-queued"}
	for i := 0; i < 40; i++ {
		ids = append(ids, fmt.Sprintf("k-idle-%d", i))
	}

	got, err := svc.QueuedWork(context.Background(), ids)
	require.NoError(t, err)
	assert.Len(t, got, len(ids))
	assert.True(t, got["k-wiki"])
	assert.True(t, got["k-queued"])
	assert.False(t, got["k-idle-39"])
	assert.Equal(t, 1, inspector.scans, "one scan answers the whole batch")

	_, err = svc.QueuedWork(context.Background(), []string{"k-queued"})
	require.NoError(t, err)
	assert.Equal(t, 1, inspector.scans, "the scan is reused within its TTL")

	svc.queuedAt = time.Now().Add(-2 * queuedProbeTTL)
	inspector.err = errors.New("redis down")
	_, err = svc.QueuedWork(context.Background(), []string{"k-queued"})
	require.Error(t, err)
	inspector.err = nil
	_, err = svc.QueuedWork(context.Background(), []string{"k-queued"})
	require.NoError(t, err)
	assert.Equal(t, 3, inspector.scans, "a failed scan is not cached")
}

type countingTaskInspector struct {
	fakeTaskInspector
	queued map[string]bool
	err    error
	scans  int
}

func (f *countingTaskInspector) QueuedKnowledgeIDs(context.Context) (map[string]struct{}, error) {
	f.scans++
	if f.err != nil {
		return nil, f.err
	}
	out := make(map[string]struct{})
	for id, queued := range f.queued {
		if queued {
			out[id] = struct{}{}
		}
	}
	return out, nil
}
