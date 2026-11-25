package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/postgres"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

type progressTestQueue struct {
	tasks  []*asynq.Task
	ids    map[string]bool
	failAt int
}

func (q *progressTestQueue) Enqueue(task *asynq.Task, options ...asynq.Option) (*asynq.TaskInfo, error) {
	if q.failAt > 0 && len(q.tasks) >= q.failAt {
		return nil, errors.New("test queue unavailable")
	}
	id := ""
	for _, opt := range options {
		if opt.Type() == asynq.TaskIDOpt {
			id = fmt.Sprint(opt.Value())
		}
	}
	if q.ids == nil {
		q.ids = map[string]bool{}
	}
	if q.ids[id] {
		return nil, asynq.ErrTaskIDConflict
	}
	q.ids[id] = true
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{ID: id}, nil
}

func progressTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	if dsn := os.Getenv("WEKNORA_TEST_POSTGRES_DSN"); dsn != "" {
		require.Contains(t, dsn, "dbname=weknora_task_progress_test", "use a disposable task-progress database")
		config := &gorm.Config{Logger: logger.Default.LogMode(logger.Silent)}
		base, err := gorm.Open(postgres.Open(dsn), config)
		require.NoError(t, err)
		schema := fmt.Sprintf("document_progress_%d", time.Now().UnixNano())
		require.NoError(t, base.Exec("CREATE SCHEMA "+schema).Error)
		db, err := gorm.Open(postgres.Open(dsn+" search_path="+schema), config)
		require.NoError(t, err)
		require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.KnowledgeBase{}, &types.Knowledge{},
			&types.KnowledgeProcessingSpan{}, &types.TaskPendingOp{}, &types.TaskDeadLetter{}))
		t.Cleanup(func() {
			connection, err := db.DB()
			require.NoError(t, err)
			require.NoError(t, connection.Close())
			require.NoError(t, base.Exec("DROP SCHEMA "+schema+" CASCADE").Error)
			connection, err = base.DB()
			require.NoError(t, err)
			require.NoError(t, connection.Close())
		})
		return db
	}
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "progress.db")), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.Tenant{}, &types.KnowledgeBase{}, &types.Knowledge{},
		&types.KnowledgeProcessingSpan{}, &types.TaskPendingOp{}, &types.TaskDeadLetter{}))
	connection, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, connection.Close()) })
	return db
}

func seedProgressDocument(t *testing.T, db *gorm.DB, id string, wiki bool) {
	t.Helper()
	require.NoError(t, db.FirstOrCreate(&types.Tenant{ID: 1, Name: "test workspace"}).Error)
	kb := types.KnowledgeBase{ID: "kb", TenantID: 1, Name: "Test knowledge base"}
	kb.IndexingStrategy.WikiEnabled = wiki
	require.NoError(t, db.FirstOrCreate(&kb).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: id, TenantID: 1, KnowledgeBaseID: "kb", Type: "file", Title: "Document " + id, FileName: id + ".pdf",
		ParseStatus: types.ParseStatusCompleted,
	}).Error)
}

func addProgressSpan(t *testing.T, db *gorm.DB, id string, attempt int, name, status string) {
	t.Helper()
	finished := time.Now()
	kind := types.SpanKindStage
	if name == "postprocess.wiki" {
		kind = types.SpanKindSubSpan
	}
	require.NoError(t, db.Create(&types.KnowledgeProcessingSpan{
		KnowledgeID: id, Attempt: attempt, SpanID: fmt.Sprintf("%s-%d-%s", id, attempt, name),
		Name: name, Kind: kind, Status: status, FinishedAt: &finished,
		Input: types.JSONMap{"prompt": "PRIVATE_PROMPT"}, Output: types.JSONMap{"body": "PRIVATE_BODY"},
	}).Error)
}

func addProgressPending(t *testing.T, db *gorm.DB, id string, attempt int) *types.TaskPendingOp {
	t.Helper()
	payload, err := json.Marshal(WikiPendingOp{Op: WikiOpIngest, KnowledgeID: id, Attempt: attempt, Language: "zh-CN"})
	require.NoError(t, err)
	op := &types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb", Op: WikiOpIngest, DedupKey: id, Payload: payload, FailCount: 2, EnqueuedAt: time.Now(),
	}
	require.NoError(t, db.Create(op).Error)
	return op
}

func TestDocumentProgressSeparatesCurrentAttemptAndFailureHistory(t *testing.T) {
	db := progressTestDB(t)
	seedProgressDocument(t, db, "doc", true)
	addProgressSpan(t, db, "doc", 1, types.StageDocReader, types.SpanStatusFailed)
	addProgressSpan(t, db, "doc", 1, "postprocess.wiki", types.SpanStatusDone)
	dead := types.TaskDeadLetter{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: "kb", RelatedID: "doc", Payload: json.RawMessage(`{"attempt":1,"secret":"PRIVATE_PAYLOAD"}`),
		LastError: "original failure", FailCount: 3, FailedAt: time.Now().Add(-time.Hour),
	}
	require.NoError(t, db.Create(&dead).Error)
	addProgressSpan(t, db, "doc", 2, types.StageDocReader, types.SpanStatusDone)
	addProgressSpan(t, db, "doc", 2, "postprocess.wiki", types.SpanStatusRunning)
	row := addProgressPending(t, db, "doc", 2)
	fresh := time.Now()
	require.NoError(t, db.Model(row).Update("claimed_at", fresh).Error)
	s := NewDocumentProgressService(db, &progressTestQueue{})
	page, err := s.List(context.Background(), DocumentProgressFilter{Status: "unfinished", Page: 1, PageSize: 25})
	require.NoError(t, err)
	require.Len(t, page.Documents, 1)
	doc := page.Documents[0]
	require.Equal(t, 2, doc.Attempt)
	require.Equal(t, "processing", doc.WikiStatus)
	require.False(t, doc.CanStart)
	require.EqualValues(t, 1, doc.FailureCount)
	for _, stage := range doc.Stages {
		require.NotEqual(t, types.SpanStatusFailed, stage.Status)
	}
	encoded, err := json.Marshal(page)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE_")
	require.NoError(t, db.Delete(row).Error)
	addProgressSpan(t, db, "doc", 2, "postprocess.wiki", types.SpanStatusDone)
	detail, err := s.Detail(context.Background(), "doc")
	require.NoError(t, err)
	require.Equal(t, "completed", detail.Document.WikiStatus)
	require.Len(t, detail.Failures, 1)
	require.Equal(t, "original failure", detail.Failures[0].LastError)
	encoded, err = json.Marshal(detail)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "PRIVATE_")
	var stored types.TaskDeadLetter
	require.NoError(t, db.First(&stored, dead.ID).Error)
	require.Equal(t, dead.LastError, stored.LastError)
	require.JSONEq(t, string(dead.Payload), string(stored.Payload))
}

func TestDocumentProgressFiltersAndScopeBoundaries(t *testing.T) {
	db := progressTestDB(t)
	seedProgressDocument(t, db, "waiting", true)
	seedProgressDocument(t, db, "completed", true)
	seedProgressDocument(t, db, "failed", true)
	addProgressSpan(t, db, "completed", 1, "postprocess.wiki", types.SpanStatusDone)
	addProgressSpan(t, db, "waiting", 1, types.StageDocReader, types.SpanStatusDone)
	addProgressPending(t, db, "waiting", 1)
	addProgressSpan(t, db, "failed", 1, "postprocess.wiki", types.SpanStatusFailed)
	s := NewDocumentProgressService(db, &progressTestQueue{})
	for _, tc := range []struct {
		state string
		total int64
	}{
		{"all", 3}, {"unfinished", 2}, {"completed", 1}, {"failed", 1},
	} {
		t.Run(tc.state, func(t *testing.T) {
			page, err := s.List(context.Background(), DocumentProgressFilter{Status: tc.state, Page: 1, PageSize: 1})
			require.NoError(t, err)
			require.Equal(t, tc.total, page.Total)
			require.Len(t, page.Documents, 1)
		})
	}
	filtered, err := s.List(context.Background(), DocumentProgressFilter{
		Status: "all", Search: "Document waiting", Page: 1, PageSize: 25,
	})
	require.NoError(t, err)
	require.EqualValues(t, 1, filtered.Total)
	require.Equal(t, "waiting", filtered.Documents[0].ID)
	// A queue row that claims another workspace cannot alter the document view.
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key=?", "waiting").Update("tenant_id", 99).Error)
	detail, err := s.Detail(context.Background(), "waiting")
	require.NoError(t, err)
	require.Zero(t, detail.Document.WikiPending)
	require.False(t, detail.Document.CanStart)
	require.NoError(t, db.Delete(&types.Tenant{}, 1).Error)
	page, err := s.List(context.Background(), DocumentProgressFilter{Status: "all", Page: 1, PageSize: 25})
	require.NoError(t, err)
	require.Empty(t, page.Documents)
	require.Zero(t, page.Total)
	_, err = s.Detail(context.Background(), "waiting")
	require.ErrorIs(t, err, gorm.ErrRecordNotFound)
}

func TestDocumentProgressHistoryIsBoundedAndDoesNotCountRecoveryAsFailure(t *testing.T) {
	db := progressTestDB(t)
	seedProgressDocument(t, db, "doc", true)
	for i := 0; i < 55; i++ {
		require.NoError(t, db.Create(&types.TaskDeadLetter{
			TenantID: 1, TaskType: types.TypeWikiIngest,
			Scope: types.TaskScopeKnowledgeBase, ScopeID: "kb", RelatedID: "doc",
			Payload: json.RawMessage(`{"attempt":1}`), LastError: strings.Repeat("错", 3000),
			FailedAt: time.Now().Add(time.Hour),
		}).Error)
	}
	addProgressSpan(t, db, "doc", 2, "postprocess.wiki", types.SpanStatusDone)
	s := NewDocumentProgressService(db, nil)
	page, err := s.List(context.Background(), DocumentProgressFilter{Status: "failed", Page: 1, PageSize: 25})
	require.NoError(t, err)
	require.Empty(t, page.Documents)
	detail, err := s.Detail(context.Background(), "doc")
	require.NoError(t, err)
	require.Len(t, detail.Failures, 50)
	require.True(t, detail.HasMoreFailures)
	require.Len(t, []rune(detail.Failures[0].LastError), 2048)
	require.EqualValues(t, 55, detail.Document.FailureCount)
}

func TestDocumentWikiStartPartialHandoffAndEligibility(t *testing.T) {
	db := progressTestDB(t)
	for _, id := range []string{"first", "second", "held", "obsolete", "mismatched"} {
		seedProgressDocument(t, db, id, true)
		addProgressSpan(t, db, id, 2, types.StageDocReader, types.SpanStatusDone)
		addProgressPending(t, db, id, 2)
	}
	// A newly queued sibling cannot bypass an already held operation for the same key.
	fresh := time.Now()
	held := addProgressPending(t, db, "held", 2)
	require.NoError(t, db.Model(held).Update("claimed_at", fresh).Error)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key=?", "obsolete").
		Update("payload", json.RawMessage(`{"op":"ingest","knowledge_id":"obsolete","attempt":1}`)).Error)
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key=?", "mismatched").
		Update("payload", json.RawMessage(`{"op":"ingest","knowledge_id":"other","attempt":2}`)).Error)
	queue := &progressTestQueue{failAt: 1}
	s := NewDocumentProgressService(db, queue)
	result, err := s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 10})
	require.Error(t, err)
	require.Equal(t, 1, result.Started)
	queue.failAt = 0
	result, err = s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 10})
	require.NoError(t, err)
	require.Equal(t, 1, result.Started)
	require.Equal(t, 1, result.AlreadyQueued)
	detail, err := s.Detail(context.Background(), "held")
	require.NoError(t, err)
	require.Equal(t, "processing", detail.Document.WikiStatus)
	require.False(t, detail.Document.CanStart)
	require.True(t, (DocumentWikiStartRequest{Count: 1, Search: strings.Repeat("文", 200)}).Valid())
	require.False(t, (DocumentWikiStartRequest{Count: 1, Search: strings.Repeat("文", 201)}).Valid())
	require.NoError(t, db.Model(&types.KnowledgeBase{}).Where("id=?", "kb").
		Update("indexing_strategy", types.IndexingStrategy{}).Error)
	result, err = s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 10})
	require.NoError(t, err)
	require.Zero(t, result.Started)
	require.Zero(t, result.AlreadyQueued)
}

func TestDocumentWikiStartIsBoundedIdempotentAndRetainsDurableProgress(t *testing.T) {
	db := progressTestDB(t)
	for _, id := range []string{"oldest", "next", "held", "unready"} {
		seedProgressDocument(t, db, id, true)
		addProgressSpan(t, db, id, 1, types.StageDocReader, types.SpanStatusDone)
		addProgressPending(t, db, id, 1)
	}
	now := time.Now()
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Where("dedup_key=?", "held").Update("claimed_at", now).Error)
	require.NoError(t, db.Model(&types.Knowledge{}).Where("id=?", "unready").
		Update("parse_status", types.ParseStatusProcessing).Error)
	var before []types.TaskPendingOp
	require.NoError(t, db.Order("id").Find(&before).Error)
	queue := &progressTestQueue{}
	s := NewDocumentProgressService(db, queue)
	first, err := s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 1})
	require.NoError(t, err)
	require.Equal(t, 1, first.Started)
	var payload WikiIngestPayload
	require.NoError(t, json.Unmarshal(queue.tasks[0].Payload(), &payload))
	require.Equal(t, "oldest", payload.KnowledgeID)
	require.EqualValues(t, 1, payload.TenantID)
	second, err := s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 1})
	require.NoError(t, err)
	require.Equal(t, 1, second.Started)
	require.Equal(t, 1, second.AlreadyQueued)
	require.NoError(t, json.Unmarshal(queue.tasks[1].Payload(), &payload))
	require.Equal(t, "next", payload.KnowledgeID)
	again, err := s.StartWiki(context.Background(), DocumentWikiStartRequest{KnowledgeID: "oldest", Count: 1})
	require.NoError(t, err)
	require.Zero(t, again.Started)
	require.Equal(t, 1, again.AlreadyQueued)
	var after []types.TaskPendingOp
	require.NoError(t, db.Order("id").Find(&after).Error)
	require.Equal(t, before, after)
	for _, r := range []DocumentWikiStartRequest{{Count: 0}, {Count: 101}, {KnowledgeID: "oldest", Count: 2}} {
		_, err := s.StartWiki(context.Background(), r)
		require.Error(t, err)
	}
	queue.failAt = 2
	_, err = s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 2})
	require.Error(t, err)
	require.NoError(t, db.Order("id").Find(&after).Error)
	require.Equal(t, before, after)
}

func TestDocumentProgressHandlesAutoGenerateDisabledTypesCancelledAndLikeEscaping(t *testing.T) {
	db := progressTestDB(t)
	require.NoError(t, db.FirstOrCreate(&types.Tenant{ID: 1, Name: "test workspace"}).Error)

	autoOff := false
	manualKB := types.KnowledgeBase{
		ID:               "kb-manual-wiki",
		TenantID:         1,
		Name:             "Manual Wiki KB",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
		WikiConfig:       &types.WikiConfig{AutoGenerate: &autoOff},
	}
	require.NoError(t, db.Create(&manualKB).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "manual-wiki-doc", TenantID: 1, KnowledgeBaseID: manualKB.ID, Type: "file",
		Title: "Manual Wiki Doc", FileName: "manual.pdf", ParseStatus: types.ParseStatusCompleted,
	}).Error)
	// Even with a stale pending op, manual-wiki KB documents must not appear unfinished or startable.
	payload, err := json.Marshal(WikiPendingOp{Op: WikiOpIngest, KnowledgeID: "manual-wiki-doc", Attempt: 0})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: manualKB.ID, Op: WikiOpIngest, DedupKey: "manual-wiki-doc", Payload: payload, EnqueuedAt: time.Now(),
	}).Error)

	autoKB := types.KnowledgeBase{
		ID:               "kb-auto-wiki",
		TenantID:         1,
		Name:             "Auto Wiki KB",
		IndexingStrategy: types.IndexingStrategy{WikiEnabled: true},
	}
	require.NoError(t, db.Create(&autoKB).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "file-url-doc", TenantID: 1, KnowledgeBaseID: autoKB.ID, Type: "file_url",
		Title: "100% Real Report", FileName: "report.pdf", ParseStatus: types.ParseStatusCompleted,
	}).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "passage-doc", TenantID: 1, KnowledgeBaseID: autoKB.ID, Type: "passage",
		Title: "1000 Other Report", FileName: "other.txt", ParseStatus: types.ParseStatusCompleted,
	}).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "cancelled-doc", TenantID: 1, KnowledgeBaseID: autoKB.ID, Type: "file",
		Title: "Cancelled Doc", FileName: "cancel.pdf", ParseStatus: types.ParseStatusCancelled,
	}).Error)
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "deleting-doc", TenantID: 1, KnowledgeBaseID: autoKB.ID, Type: "file",
		Title: "Deleting Doc", FileName: "deleting.pdf", ParseStatus: types.ParseStatusDeleting,
	}).Error)
	// Add a pre-attempt-2 dead letter without an attempt field to verify it does not poison attempt 2.
	require.NoError(t, db.Create(&types.Knowledge{
		ID: "recovered-doc", TenantID: 1, KnowledgeBaseID: autoKB.ID, Type: "file",
		Title: "Recovered Doc", FileName: "rec.pdf", ParseStatus: types.ParseStatusCompleted,
	}).Error)
	oldFailure := time.Now().Add(-2 * time.Hour)
	attemptStart := time.Now().Add(-time.Hour)
	attemptEnd := time.Now().Add(-30 * time.Minute)
	require.NoError(t, db.Create(&types.TaskDeadLetter{
		TenantID: 1, TaskType: types.TypeWikiIngest, Scope: types.TaskScopeKnowledgeBase,
		ScopeID: autoKB.ID, RelatedID: "recovered-doc",
		Payload:   json.RawMessage(`{"knowledge_base_id":"kb-auto-wiki","knowledge_id":"recovered-doc"}`),
		LastError: "legacy asynqdl failure", FailCount: 3, FailedAt: oldFailure,
	}).Error)
	require.NoError(t, db.Create(&types.KnowledgeProcessingSpan{
		KnowledgeID: "recovered-doc", Attempt: 2, SpanID: "rec-2-stage",
		Name: types.StageDocReader, Kind: types.SpanKindStage, Status: types.SpanStatusDone,
		StartedAt: &attemptStart, FinishedAt: &attemptEnd,
	}).Error)

	queue := &progressTestQueue{}
	s := NewDocumentProgressService(db, queue)

	// Unfinished must include file_url, passage, and recovered-doc (wiki not started yet),
	// but exclude manual-wiki-doc (auto_generate=false), cancelled-doc, and deleting-doc.
	unfinished, err := s.List(context.Background(), DocumentProgressFilter{Status: "unfinished", Page: 1, PageSize: 25})
	require.NoError(t, err)
	unfinishedIDs := make([]string, 0, len(unfinished.Documents))
	for _, d := range unfinished.Documents {
		unfinishedIDs = append(unfinishedIDs, d.ID)
	}
	require.ElementsMatch(t, []string{"file-url-doc", "passage-doc", "recovered-doc"}, unfinishedIDs)

	// manual-wiki-doc should show up under completed with WikiStatus == "disabled" and CanStart == false.
	completed, err := s.List(context.Background(), DocumentProgressFilter{Status: "completed", Page: 1, PageSize: 25})
	require.NoError(t, err)
	require.Len(t, completed.Documents, 1)
	require.Equal(t, "manual-wiki-doc", completed.Documents[0].ID)
	require.Equal(t, "disabled", completed.Documents[0].WikiStatus)
	require.False(t, completed.Documents[0].CanStart)

	// StartWiki must ignore manual-wiki-doc even though it has a pending op row.
	startRes, err := s.StartWiki(context.Background(), DocumentWikiStartRequest{Count: 10})
	require.NoError(t, err)
	require.Zero(t, startRes.Started)

	// Legacy attempt-0 dead letter older than attempt 2 must not mark recovered-doc as failed.
	failed, err := s.List(context.Background(), DocumentProgressFilter{Status: "failed", Page: 1, PageSize: 25})
	require.NoError(t, err)
	require.Empty(t, failed.Documents)

	// LIKE wildcard escaping: searching "100%" must match only "100% Real Report", not "1000 Other Report".
	escaped, err := s.List(context.Background(), DocumentProgressFilter{
		Status: "all", Search: "100%", Page: 1, PageSize: 25,
	})
	require.NoError(t, err)
	require.Len(t, escaped.Documents, 1)
	require.Equal(t, "file-url-doc", escaped.Documents[0].ID)
}
