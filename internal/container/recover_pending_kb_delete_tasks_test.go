package container

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const kbDeleteRecoveryTestDDL = `
CREATE TABLE task_pending_ops (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    tenant_id INTEGER NOT NULL,
    task_type VARCHAR(64) NOT NULL,
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    op VARCHAR(32) NOT NULL,
    dedup_key VARCHAR(128) NOT NULL DEFAULT '',
    payload TEXT NOT NULL DEFAULT '{}',
    fail_count INTEGER NOT NULL DEFAULT 0,
    enqueued_at DATETIME DEFAULT CURRENT_TIMESTAMP,
    claimed_at DATETIME
);`

type kbDeleteRecoveryEnqueuer struct {
	tasks     []*asynq.Task
	err       error
	conflicts int
}

func (e *kbDeleteRecoveryEnqueuer) Enqueue(
	task *asynq.Task, _ ...asynq.Option,
) (*asynq.TaskInfo, error) {
	e.tasks = append(e.tasks, task)
	if e.conflicts > 0 {
		e.conflicts--
		return nil, asynq.ErrTaskIDConflict
	}
	if e.err != nil {
		return nil, e.err
	}
	return &asynq.TaskInfo{ID: "recovered-kb-delete"}, nil
}

type kbDeleteRecoveryController struct {
	info       *asynq.TaskInfo
	deleteIDs  []string
	inspectErr error
	deleteErr  error
}

func (c *kbDeleteRecoveryController) GetTaskInfo(_, _ string) (*asynq.TaskInfo, error) {
	if c.inspectErr != nil {
		return nil, c.inspectErr
	}
	if c.info == nil {
		return nil, asynq.ErrTaskNotFound
	}
	copy := *c.info
	return &copy, nil
}

func (c *kbDeleteRecoveryController) DeleteTask(_, id string) error {
	c.deleteIDs = append(c.deleteIDs, id)
	if c.deleteErr != nil {
		return c.deleteErr
	}
	c.info = nil
	return nil
}

func setupKBDeleteRecoveryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec(kbDeleteRecoveryTestDDL).Error)
	return db
}

func TestRecoverPendingKBDeleteTasksRearmsDurableIntent(t *testing.T) {
	db := setupKBDeleteRecoveryDB(t)
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        7,
		KnowledgeBaseID: "kb-pending-delete",
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 7,
		TaskType: types.TypeKBDelete,
		Scope:    types.TaskScopeKnowledgeBaseDelete,
		ScopeID:  "kb-pending-delete",
		Op:       types.TaskOpKnowledgeBaseDelete,
		DedupKey: "kb-pending-delete",
		Payload:  payload,
	}).Error)
	enqueuer := &kbDeleteRecoveryEnqueuer{}

	recoverPendingKBDeleteTasks(db, enqueuer, &kbDeleteRecoveryController{})

	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, types.TypeKBDelete, enqueuer.tasks[0].Type())
	assert.JSONEq(t, string(payload), string(enqueuer.tasks[0].Payload()))
	var pendingCount int64
	require.NoError(t, db.Model(&types.TaskPendingOp{}).Count(&pendingCount).Error)
	assert.EqualValues(t, 1, pendingCount, "the worker consumes the intent only after cleanup succeeds")
}

func TestRecoverPendingKBDeleteTasksRejectsMismatchedPayload(t *testing.T) {
	db := setupKBDeleteRecoveryDB(t)
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 7,
		TaskType: types.TypeKBDelete,
		Scope:    types.TaskScopeKnowledgeBaseDelete,
		ScopeID:  "kb-delete",
		Op:       types.TaskOpKnowledgeBaseDelete,
		DedupKey: "kb-delete",
		Payload:  []byte(`{"tenant_id":8,"knowledge_base_id":"kb-delete"}`),
	}).Error)
	enqueuer := &kbDeleteRecoveryEnqueuer{err: errors.New("must not be called")}

	recoverPendingKBDeleteTasks(db, enqueuer, &kbDeleteRecoveryController{})

	assert.Empty(t, enqueuer.tasks)
}

func TestRecoverPendingKBDeleteTasksReplacesArchivedTrigger(t *testing.T) {
	db := setupKBDeleteRecoveryDB(t)
	payload, err := json.Marshal(types.KBDeletePayload{
		TenantID:        7,
		KnowledgeBaseID: "kb-archived-delete",
	})
	require.NoError(t, err)
	require.NoError(t, db.Create(&types.TaskPendingOp{
		TenantID: 7,
		TaskType: types.TypeKBDelete,
		Scope:    types.TaskScopeKnowledgeBaseDelete,
		ScopeID:  "kb-archived-delete",
		Op:       types.TaskOpKnowledgeBaseDelete,
		DedupKey: "kb-archived-delete",
		Payload:  payload,
	}).Error)
	enqueuer := &kbDeleteRecoveryEnqueuer{conflicts: 1}
	controller := &kbDeleteRecoveryController{info: &asynq.TaskInfo{
		ID:    service.KBDeleteTaskID("kb-archived-delete"),
		Queue: types.QueueMaintenance,
		State: asynq.TaskStateArchived,
	}}

	recoverPendingKBDeleteTasks(db, enqueuer, controller)

	require.Len(t, enqueuer.tasks, 2, "the second enqueue replaces the archived trigger")
	assert.Equal(t, []string{service.KBDeleteTaskID("kb-archived-delete")}, controller.deleteIDs)
}
