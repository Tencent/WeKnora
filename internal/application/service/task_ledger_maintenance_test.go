package service

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func setupTaskLedgerServiceTestDB(t *testing.T) (*gorm.DB, interfaces.TaskJobRepository) {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.AutoMigrate(&types.TaskJob{}, &types.TaskExecution{}))
	return db, repository.NewTaskJobRepository(db)
}

func serviceTaskJob(jobID string, attempt int) *types.TaskJob {
	return &types.TaskJob{
		JobID:          jobID,
		TenantID:       7,
		CreatedBy:      "user-1",
		Kind:           types.TaskJobKindUpload,
		Origin:         types.TaskJobOriginUser,
		DisplayName:    "paper.pdf",
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		RelatedID:      "kb-1",
		ProcessAttempt: attempt,
		State:          types.TaskJobStateQueued,
		Metadata:       types.JSON(`{}`),
		ReplaySpec:     types.JSON(`{}`),
	}
}

func serviceReplayableTaskJob(jobID string, attempt int) *types.TaskJob {
	job := serviceTaskJob(jobID, attempt)
	job.ReplaySpec = types.JSON(`{
		"version": 1,
		"source_ref": {"type": "object_storage", "id": "/tmp/paper.pdf"},
		"scope": {"knowledge_id": "knowledge-1", "knowledge_base_id": "kb-1"},
		"process_config": {
			"file_name": "paper.pdf",
			"file_type": "pdf",
			"enable_multimodel": true,
			"enable_question_generation": true,
			"question_count": 3,
			"language": "zh"
		}
	}`)
	return job
}

func serviceTaskExecution(jobID, executionID string, attempt int, enqueuedAt time.Time) *types.TaskExecution {
	return &types.TaskExecution{
		ExecutionID:    executionID,
		JobID:          jobID,
		ProcessAttempt: attempt,
		TaskType:       types.TypeDocumentProcess,
		Queue:          types.QueueCritical,
		State:          types.TaskExecutionStateQueued,
		EnqueuedAt:     enqueuedAt,
	}
}

func TestTaskJobDispatcher_LedgerCreateFailureDoesNotEnqueue(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceTaskJob("job-dup", 1),
		serviceTaskExecution("job-dup", "exec-existing", 1, time.Now()),
	))

	enqueuer := &fakeTaskEnqueuer{}
	dispatcher := NewTaskJobDispatcher(repo, enqueuer)
	_, err := dispatcher.DispatchUserRoot(ctx, UserRootDispatchRequest{
		JobID:          "job-dup",
		ExecutionID:    "exec-new",
		TenantID:       7,
		Kind:           types.TaskJobKindUpload,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		ProcessAttempt: 1,
		Task:           asynq.NewTask(types.TypeDocumentProcess, []byte(`{}`)),
		Options:        []asynq.Option{asynq.Queue(types.QueueCritical)},
	})
	require.Error(t, err)
	assert.Empty(t, enqueuer.tasks, "dispatcher must not enqueue when ledger insert fails")

	var execCount int64
	require.NoError(t, db.Model(&types.TaskExecution{}).Where("execution_id = ?", "exec-new").Count(&execCount).Error)
	assert.Zero(t, execCount)
}

func TestTaskLedgerMaintenance_RepairsDispatchedAtWhenQueueHasTask(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceTaskJob("job-stale", 1),
		serviceTaskExecution("job-stale", "exec-stale", 1, stale),
	))

	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{
		taskQueued: map[string]bool{"exec-stale": true},
	}, nil)
	runner.sweepStaleDispatch(ctx)

	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-stale").Error)
	require.NotNil(t, exec.DispatchedAt)
	assert.Equal(t, types.TaskExecutionStateQueued, exec.State)
	var job types.TaskJob
	require.NoError(t, db.First(&job, "job_id = ?", "job-stale").Error)
	assert.Equal(t, types.TaskJobStateQueued, job.State)
}

func TestTaskLedgerMaintenance_ProbeErrorDoesNotMarkFailed(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceTaskJob("job-stale", 1),
		serviceTaskExecution("job-stale", "exec-stale", 1, stale),
	))

	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{taskErr: assert.AnError}, nil)
	runner.sweepStaleDispatch(ctx)

	var job types.TaskJob
	require.NoError(t, db.First(&job, "job_id = ?", "job-stale").Error)
	assert.Equal(t, types.TaskJobStateQueued, job.State)
}

func TestTaskLedgerMaintenance_ReenqueuesReplayableMissingDispatch(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceReplayableTaskJob("job-replay", 1),
		serviceTaskExecution("job-replay", "exec-replay", 1, stale),
	))

	enqueuer := &fakeTaskEnqueuer{}
	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{}, enqueuer)
	runner.sweepStaleDispatch(ctx)

	require.Len(t, enqueuer.tasks, 1)
	assert.Equal(t, types.TypeDocumentProcess, enqueuer.tasks[0].Type())
	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-replay").Error)
	require.NotNil(t, exec.DispatchedAt)
	assert.Equal(t, types.TaskExecutionStateQueued, exec.State)
}

func TestTaskLedgerMaintenance_ArchivedDispatchFailsLedger(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceTaskJob("job-archived", 1),
		serviceTaskExecution("job-archived", "exec-archived", 1, stale),
	))

	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{
		taskState: map[string]interfaces.TaskQueueState{"exec-archived": interfaces.TaskQueueArchived},
	}, nil)
	runner.sweepStaleDispatch(ctx)

	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-archived").Error)
	assert.Equal(t, types.TaskExecutionStateFailed, exec.State)
	assert.Contains(t, exec.LastError, "archived")

	var job types.TaskJob
	require.NoError(t, db.First(&job, "job_id = ?", "job-archived").Error)
	assert.Equal(t, types.TaskJobStateFailed, job.State)
	assert.Contains(t, job.LastError, "archived")
}

func TestTaskLedgerMaintenance_ReplaySpecV2RestoresPolicyAndJobID(t *testing.T) {
	_, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	payload := types.DocumentProcessPayload{
		TenantID:        7,
		KnowledgeID:     "knowledge-1",
		KnowledgeBaseID: "kb-1",
		FilePath:        "/tmp/paper.pdf",
		Attempt:         1,
		JobID:           "stale-job",
	}
	payloadBytes, err := json.Marshal(payload)
	require.NoError(t, err)
	maxRetry := 7
	job := serviceTaskJob("job-replay-v2", 1)
	job.ReplaySpec = taskJobJSON(taskReplaySpecV2{
		Version:  2,
		TaskType: types.TypeDocumentProcess,
		JobID:    job.JobID,
		Attempt:  1,
		Payload:  payloadBytes,
		Policy: taskReplayPolicy{
			Queue:         types.QueueGraph,
			MaxRetry:      &maxRetry,
			TimeoutMillis: int64((45 * time.Second) / time.Millisecond),
		},
	})
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		job,
		serviceTaskExecution("job-replay-v2", "exec-replay-v2", 1, stale),
	))

	enqueuer := &fakeTaskEnqueuer{}
	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{}, enqueuer)
	runner.sweepStaleDispatch(ctx)

	require.Len(t, enqueuer.tasks, 1)
	var got types.DocumentProcessPayload
	require.NoError(t, json.Unmarshal(enqueuer.tasks[0].Payload(), &got))
	assert.Equal(t, "job-replay-v2", got.JobID)
	assert.Equal(t, 1, got.Attempt)
	require.Len(t, enqueuer.opts, 1)
	assertOptionValue(t, enqueuer.opts[0], asynq.QueueOpt, types.QueueGraph)
	assertOptionValue(t, enqueuer.opts[0], asynq.MaxRetryOpt, 7)
	assertOptionValue(t, enqueuer.opts[0], asynq.TimeoutOpt, 45*time.Second)
	assertOptionValue(t, enqueuer.opts[0], asynq.TaskIDOpt, "exec-replay-v2")
}

func TestTaskLedgerMaintenance_ReenqueueErrorDoesNotMarkFailed(t *testing.T) {
	db, repo := setupTaskLedgerServiceTestDB(t)
	ctx := context.Background()
	stale := time.Now().Add(-10 * time.Minute)
	require.NoError(t, repo.CreateJobAndExecution(ctx,
		serviceReplayableTaskJob("job-replay", 1),
		serviceTaskExecution("job-replay", "exec-replay", 1, stale),
	))

	runner := NewTaskLedgerMaintenanceRunner(repo, fakeTaskInspector{}, &fakeTaskEnqueuer{err: assert.AnError})
	runner.sweepStaleDispatch(ctx)

	var job types.TaskJob
	require.NoError(t, db.First(&job, "job_id = ?", "job-replay").Error)
	assert.Equal(t, types.TaskJobStateQueued, job.State)
	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-replay").Error)
	assert.Nil(t, exec.DispatchedAt)
}

func assertOptionValue(t *testing.T, opts []asynq.Option, typ asynq.OptionType, want any) {
	t.Helper()
	for _, opt := range opts {
		if opt.Type() == typ {
			assert.Equal(t, want, opt.Value())
			return
		}
	}
	t.Fatalf("missing option %v", typ)
}
