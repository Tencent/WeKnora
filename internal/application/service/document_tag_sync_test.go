package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

type documentTagProjectionSpy struct {
	supported bool
	calls     [][]string
	failAt    int
	err       error
}

func (p *documentTagProjectionSpy) SupportsDocumentTags() bool { return p.supported }
func (p *documentTagProjectionSpy) SyncDocumentTags(_ context.Context, _ string, ids []string) error {
	p.calls = append(p.calls, append([]string{}, ids...))
	if p.err != nil {
		return p.err
	}
	if p.failAt == len(p.calls) {
		return errors.New("partial update")
	}
	return nil
}

type documentTagQueueSpy struct {
	tasks []*asynq.Task
	err   error
}

func (q *documentTagQueueSpy) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	q.tasks = append(q.tasks, task)
	return &asynq.TaskInfo{}, q.err
}

func documentTagWorkerFixture(t *testing.T, ready bool, count int) (*DocumentTagSyncService, *documentTagProjectionSpy, *documentTagQueueSpy) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })
	require.NoError(t, db.Exec(`CREATE TABLE knowledge_bases(id TEXT PRIMARY KEY, tenant_id INTEGER, type TEXT, document_tag_ready BOOLEAN, vector_store_id TEXT, deleted_at DATETIME);
CREATE TABLE knowledges(id TEXT PRIMARY KEY, tenant_id INTEGER, knowledge_base_id TEXT, deleted_at DATETIME);`).Error)
	require.NoError(t, db.Exec("INSERT INTO knowledge_bases(id,tenant_id,type,document_tag_ready) VALUES ('kb',7,'document',?)", ready).Error)
	for i := 0; i < count; i++ {
		require.NoError(t, db.Exec("INSERT INTO knowledges(id,tenant_id,knowledge_base_id) VALUES (?,7,'kb')", fmt.Sprintf("doc-%03d", i)).Error)
	}
	p, q := &documentTagProjectionSpy{supported: true}, &documentTagQueueSpy{}
	s := &DocumentTagSyncService{db: db, task: q, resolve: func(context.Context, *types.KnowledgeBase) (interfaces.DocumentTagProjection, error) { return p, nil }}
	return s, p, q
}
func documentTagTask(t *testing.T, p types.DocumentTagSyncPayload) *asynq.Task {
	t.Helper()
	data, err := json.Marshal(p)
	require.NoError(t, err)
	return asynq.NewTask(types.TypeDocumentTagSync, data)
}
func documentTagReady(t *testing.T, s *DocumentTagSyncService) bool {
	t.Helper()
	var kb types.KnowledgeBase
	require.NoError(t, s.db.First(&kb, "id = ?", "kb").Error)
	return kb.DocumentTagReady
}

func TestDocumentTagBootstrapRetriesWholeScanAndPersistsReady(t *testing.T) {
	s, p, _ := documentTagWorkerFixture(t, false, 101)
	p.failAt = 3 // schema succeeds, first page succeeds, second page fails.
	task := documentTagTask(t, types.DocumentTagSyncPayload{TenantID: 7, KnowledgeBaseID: "kb"})
	require.Error(t, s.Handle(context.Background(), task))
	assert.False(t, documentTagReady(t, s))
	require.Len(t, p.calls, 3)
	assert.Len(t, p.calls[1], 100)
	assert.Len(t, p.calls[2], 1)
	p.failAt = 0
	p.calls = nil
	require.NoError(t, s.Handle(context.Background(), task))
	assert.True(t, documentTagReady(t, s))
	require.Len(t, p.calls, 3)
	// Restarted service instance trusts the persisted marker.
	restarted := &DocumentTagSyncService{db: s.db, task: s.task, resolve: s.resolve}
	require.NoError(t, restarted.Handle(context.Background(), task))
	assert.Len(t, p.calls, 3)
}

func TestDocumentTagIncrementalFailureDoesNotInvalidateAndEnqueuesLatestStateRepair(t *testing.T) {
	s, p, q := documentTagWorkerFixture(t, true, 1)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	p.err = errors.New("offline")
	require.ErrorContains(t, s.Sync(ctx, []string{"doc-000"}), "tags saved")
	assert.True(t, documentTagReady(t, s))
	require.Len(t, q.tasks, 1)
	var payload types.DocumentTagSyncPayload
	require.NoError(t, json.Unmarshal(q.tasks[0].Payload(), &payload))
	assert.Equal(t, []string{"doc-000"}, payload.KnowledgeIDs)
	assert.Equal(t, uint64(7), payload.TenantID)
	p.err = nil
	require.NoError(t, s.Handle(context.Background(), q.tasks[0]))
	assert.True(t, documentTagReady(t, s))
	assert.Len(t, p.calls, 2)
}

func TestDocumentTagLegacyIncrementalCannotCertifyWholeKB(t *testing.T) {
	s, p, q := documentTagWorkerFixture(t, false, 2)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	require.NoError(t, s.Sync(ctx, []string{"doc-000"}))
	assert.False(t, documentTagReady(t, s))
	assert.Len(t, p.calls, 1)
	require.Len(t, q.tasks, 1)
	var payload types.DocumentTagSyncPayload
	require.NoError(t, json.Unmarshal(q.tasks[0].Payload(), &payload))
	assert.Empty(t, payload.KnowledgeIDs)
}

func TestDocumentTagTasksRespectTenantFAQAndUnsupportedStores(t *testing.T) {
	s, p, _ := documentTagWorkerFixture(t, false, 1)
	require.NoError(t, s.Handle(context.Background(), documentTagTask(t, types.DocumentTagSyncPayload{TenantID: 8, KnowledgeBaseID: "kb"})))
	assert.Empty(t, p.calls)
	p.supported = false
	require.NoError(t, s.Handle(context.Background(), documentTagTask(t, types.DocumentTagSyncPayload{TenantID: 7, KnowledgeBaseID: "kb"})))
	assert.False(t, documentTagReady(t, s))
	assert.Empty(t, p.calls)
	p.supported = true
	require.NoError(t, s.db.Exec("UPDATE knowledge_bases SET type='faq'").Error)
	require.NoError(t, s.Handle(context.Background(), documentTagTask(t, types.DocumentTagSyncPayload{TenantID: 7, KnowledgeBaseID: "kb"})))
	assert.Empty(t, p.calls)
}

func TestDocumentTagForceRepairAndPendingRecovery(t *testing.T) {
	s, p, q := documentTagWorkerFixture(t, false, 0)
	require.NoError(t, s.RecoverPending(context.Background()))
	require.Len(t, q.tasks, 1)
	require.NoError(t, s.Handle(context.Background(), q.tasks[0]))
	assert.True(t, documentTagReady(t, s))
	assert.Len(t, p.calls, 1, "empty KB still prepares schema")
	require.NoError(t, s.RecoverPending(context.Background()))
	assert.Len(t, q.tasks, 1)
	p.err = errors.New("unavailable")
	force := documentTagTask(t, types.DocumentTagSyncPayload{TenantID: 7, KnowledgeBaseID: "kb", Force: true})
	require.Error(t, s.Handle(context.Background(), force))
	assert.True(t, documentTagReady(t, s), "manual repair failures do not invalidate initialized KBs")
}
