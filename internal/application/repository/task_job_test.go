package repository

import (
	"context"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

const taskLedgerTestDDL = `
CREATE TABLE task_jobs (
    job_id VARCHAR(64) PRIMARY KEY,
    tenant_id INTEGER NOT NULL,
    created_by VARCHAR(64) NOT NULL DEFAULT '',
    kind VARCHAR(32) NOT NULL,
    origin VARCHAR(8) NOT NULL DEFAULT 'user',
    display_name VARCHAR(255) NOT NULL DEFAULT '',
    scope VARCHAR(32) NOT NULL,
    scope_id VARCHAR(64) NOT NULL,
    related_id VARCHAR(64) NOT NULL DEFAULT '',
    process_attempt INTEGER NOT NULL DEFAULT 0,
    state VARCHAR(16) NOT NULL DEFAULT 'queued',
    metadata TEXT NOT NULL DEFAULT '{}',
    replay_spec TEXT NOT NULL DEFAULT '{}',
    last_error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    failed_task_type VARCHAR(64) NOT NULL DEFAULT '',
    failed_task_id VARCHAR(64) NOT NULL DEFAULT '',
    created_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    updated_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    finished_at DATETIME
);
CREATE INDEX idx_task_jobs_scope_attempt
    ON task_jobs(tenant_id, scope, scope_id, process_attempt);

CREATE TABLE task_executions (
    execution_id VARCHAR(64) PRIMARY KEY,
    job_id VARCHAR(64) NOT NULL REFERENCES task_jobs(job_id) ON DELETE CASCADE,
    process_attempt INTEGER NOT NULL DEFAULT 0,
    task_type VARCHAR(64) NOT NULL,
    queue VARCHAR(32) NOT NULL DEFAULT '',
    state VARCHAR(16) NOT NULL DEFAULT 'queued',
    retry_count INTEGER NOT NULL DEFAULT 0,
    error_class VARCHAR(24) NOT NULL DEFAULT '',
    last_error TEXT NOT NULL DEFAULT '',
    retry_of VARCHAR(64) NOT NULL DEFAULT '',
    rescheduled_to_execution_id VARCHAR(64) NOT NULL DEFAULT '',
    enqueued_at DATETIME NOT NULL DEFAULT CURRENT_TIMESTAMP,
    dispatched_at DATETIME,
    started_at DATETIME,
    finished_at DATETIME
);
CREATE INDEX idx_task_executions_state_enqueued
    ON task_executions(state, enqueued_at);
`

func setupTaskLedgerTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	require.NoError(t, db.Exec("PRAGMA foreign_keys = ON").Error)
	require.NoError(t, db.Exec(taskLedgerTestDDL).Error)
	return db
}

func makeTaskJob(jobID string, attempt int) *types.TaskJob {
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
	}
}

func makeTaskExecution(jobID, executionID string, attempt int) *types.TaskExecution {
	return &types.TaskExecution{
		ExecutionID:    executionID,
		JobID:          jobID,
		ProcessAttempt: attempt,
		TaskType:       "document:process",
		Queue:          "critical",
		State:          types.TaskExecutionStateQueued,
		EnqueuedAt:     time.Now(),
	}
}

func taskAttempt(attempt int) interfaces.TaskJobAttemptSelector {
	return interfaces.TaskJobAttemptSelector{
		TenantID:       7,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		ProcessAttempt: attempt,
	}
}

func TestTaskJobRepository_CreateDefaultsAndCascadeDelete(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	job := makeTaskJob("job-1", 0)
	exec := makeTaskExecution("", "exec-1", 0)
	require.NoError(t, repo.CreateJobAndExecution(ctx, job, exec))

	var got types.TaskJob
	require.NoError(t, db.First(&got, "job_id = ?", "job-1").Error)
	assert.Equal(t, types.JSON(`{}`), got.Metadata)
	assert.Equal(t, types.JSON(`{}`), got.ReplaySpec)

	require.NoError(t, db.Delete(&types.TaskJob{}, "job_id = ?", "job-1").Error)
	var n int64
	require.NoError(t, db.Model(&types.TaskExecution{}).Count(&n).Error)
	assert.Equal(t, int64(0), n, "execution rows should cascade with the job")
}

func TestTaskJobRepository_GetJobByExecutionID(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	job := makeTaskJob("job-lookup", 2)
	job.Kind = types.TaskJobKindDelete
	require.NoError(t, repo.CreateJobAndExecution(ctx, job, makeTaskExecution("job-lookup", "exec-lookup", 2)))

	got, err := repo.GetJobByExecutionID(ctx, "exec-lookup")
	require.NoError(t, err)
	require.NotNil(t, got)
	assert.Equal(t, "job-lookup", got.JobID)
	assert.Equal(t, types.TaskJobKindDelete, got.Kind)
	assert.Equal(t, 2, got.ProcessAttempt)

	missing, err := repo.GetJobByExecutionID(ctx, "missing")
	require.NoError(t, err)
	assert.Nil(t, missing)
}

func TestTaskJobRepository_AttemptIsolationAndTerminalGuard(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-old", 0), makeTaskExecution("job-old", "exec-old", 0)))
	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-new", 1), makeTaskExecution("job-new", "exec-new", 1)))

	changed, err := repo.MarkJobFailedIfCurrentAttempt(ctx, taskAttempt(0), interfaces.TaskLedgerFailure{
		ErrorClass:     types.TaskErrorClassTerminal,
		LastError:      "bad page",
		FailedTaskType: "chunk:extract",
		FailedTaskID:   "hidden-1",
	}, now)
	require.NoError(t, err)
	assert.True(t, changed)

	var oldJob, newJob types.TaskJob
	require.NoError(t, db.First(&oldJob, "job_id = ?", "job-old").Error)
	require.NoError(t, db.First(&newJob, "job_id = ?", "job-new").Error)
	assert.Equal(t, types.TaskJobStateFailed, oldJob.State)
	assert.Equal(t, "chunk:extract", oldJob.FailedTaskType)
	assert.Equal(t, types.TaskJobStateQueued, newJob.State, "new attempt must not be polluted by old attempt failure")

	changed, err = repo.MarkJobSucceededIfCurrentAttempt(ctx, taskAttempt(0), now)
	require.NoError(t, err)
	assert.False(t, changed, "terminal jobs must not move back to succeeded")

	changed, err = repo.MarkJobFinalizingIfCurrentAttempt(ctx, taskAttempt(0))
	require.NoError(t, err)
	assert.False(t, changed, "failed old attempt should ignore late finalizing events")
}

func TestTaskJobRepository_JobIDIsolationWithinSameAttempt(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-a", 1), makeTaskExecution("job-a", "exec-a", 1)))
	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-b", 1), makeTaskExecution("job-b", "exec-b", 1)))

	changed, err := repo.MarkJobFailedIfCurrentAttempt(ctx, interfaces.TaskJobAttemptSelector{
		JobID:          "job-a",
		TenantID:       7,
		Scope:          types.TaskScopeKnowledge,
		ScopeID:        "knowledge-1",
		ProcessAttempt: 1,
	}, interfaces.TaskLedgerFailure{ErrorClass: types.TaskErrorClassRetryable, LastError: "boom"}, time.Now())
	require.NoError(t, err)
	require.True(t, changed)

	var jobA, jobB types.TaskJob
	require.NoError(t, db.First(&jobA, "job_id = ?", "job-a").Error)
	require.NoError(t, db.First(&jobB, "job_id = ?", "job-b").Error)
	assert.Equal(t, types.TaskJobStateFailed, jobA.State)
	assert.Equal(t, types.TaskJobStateQueued, jobB.State, "job_id selector must not update a sibling job with same scope/attempt")
}

func TestTaskJobRepository_ExecutionUpdateIfExistsAndTerminalGuard(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()
	now := time.Now()

	changed, err := repo.MarkExecActiveIfExists(ctx, "missing", 0, now)
	require.NoError(t, err)
	assert.False(t, changed, "missing execution means internal fan-out task and must not be inserted")

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-1", 0), makeTaskExecution("job-1", "exec-1", 0)))

	changed, err = repo.MarkExecActiveIfExists(ctx, "exec-1", 2, now)
	require.NoError(t, err)
	assert.True(t, changed)

	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-1").Error)
	assert.Equal(t, types.TaskExecutionStateActive, exec.State)
	assert.Equal(t, 2, exec.RetryCount)
	require.NotNil(t, exec.StartedAt)
	require.NotNil(t, exec.DispatchedAt, "worker active should backfill dispatched_at")

	changed, err = repo.MarkExecSucceededIfNonTerminal(ctx, "exec-1", now)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = repo.MarkExecFailedIfNonTerminal(ctx, "exec-1", interfaces.TaskLedgerFailure{LastError: "late"}, now)
	require.NoError(t, err)
	assert.False(t, changed, "terminal execution must not be overwritten by late failure")
}

func TestTaskJobRepository_RescheduledExecutionIsTerminal(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()
	now := time.Now()

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-1", 1), makeTaskExecution("job-1", "exec-1", 1)))
	require.NoError(t, repo.CreateExecutionForJob(ctx, &types.TaskExecution{
		ExecutionID: "exec-2",
		JobID:       "job-1",
		TaskType:    types.TypeDocumentProcess,
		Queue:       types.QueueCritical,
		State:       types.TaskExecutionStateQueued,
		EnqueuedAt:  now,
		RetryOf:     "exec-1",
	}))

	changed, err := repo.MarkExecRescheduled(ctx, "exec-1", "exec-2", now)
	require.NoError(t, err)
	assert.True(t, changed)

	changed, err = repo.MarkExecSucceededIfNonTerminal(ctx, "exec-1", now.Add(time.Second))
	require.NoError(t, err)
	assert.False(t, changed)
	changed, err = repo.MarkExecFailedIfNonTerminal(ctx, "exec-1", interfaces.TaskLedgerFailure{LastError: "late"}, now.Add(time.Second))
	require.NoError(t, err)
	assert.False(t, changed)

	var exec types.TaskExecution
	require.NoError(t, db.First(&exec, "execution_id = ?", "exec-1").Error)
	assert.Equal(t, types.TaskExecutionStateRescheduled, exec.State)
	assert.Equal(t, "exec-2", exec.RescheduledToExecutionID)
}

func TestTaskJobRepository_ListExecutionsForJobs(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-1", 1), makeTaskExecution("job-1", "exec-1a", 1)))
	require.NoError(t, db.Create(makeTaskExecution("job-1", "exec-1b", 1)).Error)
	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-2", 2), makeTaskExecution("job-2", "exec-2a", 2)))
	other := makeTaskJob("job-other-tenant", 1)
	other.TenantID = 8
	require.NoError(t, repo.CreateJobAndExecution(ctx, other, makeTaskExecution("job-other-tenant", "exec-other", 1)))

	rows, err := repo.ListExecutionsForJobs(ctx, 7, []string{"job-1", "job-2", "job-other-tenant"})
	require.NoError(t, err)
	require.Len(t, rows["job-1"], 2)
	require.Len(t, rows["job-2"], 1)
	assert.Empty(t, rows["job-other-tenant"], "batch lookup must remain tenant-scoped")
}

func TestTaskJobRepository_CancelAllNonTerminalExecutionsForJob(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-cancel", 1), makeTaskExecution("job-cancel", "exec-1", 1)))
	require.NoError(t, db.Create(makeTaskExecution("job-cancel", "exec-2", 1)).Error)
	done := makeTaskExecution("job-cancel", "exec-done", 1)
	done.State = types.TaskExecutionStateSucceeded
	require.NoError(t, db.Create(done).Error)

	changed, err := repo.MarkExecutionsCanceledForJob(ctx, 7, "job-cancel", interfaces.TaskLedgerFailure{
		ErrorClass: types.TaskErrorClassCanceled,
		LastError:  "canceled by user",
	}, time.Now())
	require.NoError(t, err)
	assert.Equal(t, int64(2), changed)

	var canceled int64
	require.NoError(t, db.Model(&types.TaskExecution{}).
		Where("job_id = ? AND state = ?", "job-cancel", types.TaskExecutionStateCanceled).
		Count(&canceled).Error)
	assert.Equal(t, int64(2), canceled)

	var succeeded types.TaskExecution
	require.NoError(t, db.First(&succeeded, "execution_id = ?", "exec-done").Error)
	assert.Equal(t, types.TaskExecutionStateSucceeded, succeeded.State)
}

func TestTaskJobRepository_ListJobsScopesSearchToTenantAndCreator(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()

	own := makeTaskJob("job-own", 0)
	own.DisplayName = "needle report"
	own.State = types.TaskJobStateFinalizing
	require.NoError(t, repo.CreateJobAndExecution(ctx, own, makeTaskExecution("job-own", "exec-own", 0)))

	otherUser := makeTaskJob("job-other-user", 0)
	otherUser.CreatedBy = "user-2"
	otherUser.ScopeID = "needle-other-user"
	require.NoError(t, repo.CreateJobAndExecution(ctx, otherUser, makeTaskExecution("job-other-user", "exec-other-user", 0)))

	otherTenant := makeTaskJob("job-other-tenant", 0)
	otherTenant.TenantID = 8
	otherTenant.ScopeID = "needle-other-tenant"
	require.NoError(t, repo.CreateJobAndExecution(ctx, otherTenant, makeTaskExecution("job-other-tenant", "exec-other-tenant", 0)))

	internalJob := makeTaskJob("job-internal", 0)
	internalJob.Origin = types.TaskJobOriginInternal
	internalJob.DisplayName = "needle internal"
	require.NoError(t, repo.CreateJobAndExecution(ctx, internalJob, makeTaskExecution("job-internal", "exec-internal", 0)))

	rows, total, err := repo.ListJobs(ctx, interfaces.TaskJobQuery{
		TenantID: 7,
		UserID:   "user-1",
		Q:        "needle",
		Page:     1,
		PageSize: 20,
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, rows, 1)
	assert.Equal(t, "job-own", rows[0].JobID)

	summary, err := repo.Summary(ctx, interfaces.TaskJobQuery{
		TenantID: 7,
		UserID:   "user-1",
		Q:        "needle",
	})
	require.NoError(t, err)
	assert.Equal(t, int64(1), summary.Processing, "finalizing should roll up into the processing bucket")
	assert.Zero(t, summary.Queued)
}

func TestTaskJobRepository_StaleDispatchAndTerminalRetention(t *testing.T) {
	db := setupTaskLedgerTestDB(t)
	repo := NewTaskJobRepository(db)
	ctx := context.Background()
	now := time.Now()

	oldExec := makeTaskExecution("job-old", "exec-old", 0)
	oldExec.EnqueuedAt = now.Add(-10 * time.Minute)
	newExec := makeTaskExecution("job-new", "exec-new", 0)
	newExec.EnqueuedAt = now
	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-old", 0), oldExec))
	require.NoError(t, repo.CreateJobAndExecution(ctx, makeTaskJob("job-new", 0), newExec))

	stale, err := repo.FindStaleDispatches(ctx, now.Add(-5*time.Minute), 10)
	require.NoError(t, err)
	require.Len(t, stale, 1)
	assert.Equal(t, "exec-old", stale[0].ExecutionID)

	changed, err := repo.MarkStaleDispatchFailed(ctx, "exec-old", now)
	require.NoError(t, err)
	require.True(t, changed)

	var oldJob types.TaskJob
	require.NoError(t, db.First(&oldJob, "job_id = ?", "job-old").Error)
	assert.Equal(t, types.TaskJobStateFailed, oldJob.State)
	assert.Equal(t, types.TaskErrorClassEnqueueFailed, oldJob.LastErrorClass)

	deleted, err := repo.DeleteTerminalJobsFinishedBefore(ctx, now.Add(time.Second), 100)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	var execCount int64
	require.NoError(t, db.Model(&types.TaskExecution{}).Where("job_id = ?", "job-old").Count(&execCount).Error)
	assert.Equal(t, int64(0), execCount, "retention delete should cascade executions")
}
