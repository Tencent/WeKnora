package interfaces

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

var (
	// ErrEvaluationTaskNotFound hides whether a task belongs to another tenant.
	ErrEvaluationTaskNotFound = errors.New("evaluation task not found")
	// ErrEvaluationTaskAlreadyExists reports a duplicate task identity.
	ErrEvaluationTaskAlreadyExists = errors.New("evaluation task already exists")
	// ErrEvaluationTaskTenantMismatch reports a disagreement between an explicit tenant boundary and an entity.
	ErrEvaluationTaskTenantMismatch = errors.New("evaluation task tenant mismatch")
	// ErrEvaluationTaskOwnerConflict reports that another service instance owns the task.
	ErrEvaluationTaskOwnerConflict = errors.New("evaluation task owner conflict")
	// ErrEvaluationTaskVersionConflict reports a stale compare-and-swap version.
	ErrEvaluationTaskVersionConflict = errors.New("evaluation task version conflict")
	// ErrEvaluationTaskStateConflict reports that the requested lifecycle transition is no longer valid.
	ErrEvaluationTaskStateConflict = errors.New("evaluation task state conflict")
)

// EvaluationTaskRepository persists tenant-scoped evaluation task snapshots.
type EvaluationTaskRepository interface {
	CreateTask(ctx context.Context, tenantID uint64, task *types.EvaluationTaskEntity) error
	GetTask(ctx context.Context, tenantID uint64, taskID string) (*types.EvaluationTaskEntity, error)
	GetTasksByIDs(
		ctx context.Context,
		tenantID uint64,
		taskIDs []string,
	) (map[string]*types.EvaluationTaskEntity, error)
	TryStartTask(ctx context.Context, command types.EvaluationTaskStartCommand) (*types.EvaluationTaskEntity, error)
	HeartbeatTask(
		ctx context.Context,
		command types.EvaluationTaskHeartbeatCommand,
	) (*types.EvaluationTaskEntity, error)
	ClaimExpiredTasks(
		ctx context.Context,
		command types.EvaluationTaskClaimExpiredCommand,
	) ([]*types.EvaluationTaskEntity, error)
	PublishProgress(
		ctx context.Context,
		command types.EvaluationTaskProgressCommand,
	) (*types.EvaluationTaskEntity, error)
	RecordTemporaryKnowledge(
		ctx context.Context,
		command types.EvaluationTaskKnowledgeCommand,
	) (*types.EvaluationTaskEntity, error)
	PublishTerminal(
		ctx context.Context,
		command types.EvaluationTaskTerminalCommand,
	) (*types.EvaluationTaskEntity, error)
	RequestCancel(
		ctx context.Context,
		command types.EvaluationTaskCancelCommand,
	) (*types.EvaluationTaskEntity, error)
	ListTasks(
		ctx context.Context,
		tenantID uint64,
		query types.EvaluationTaskListQuery,
	) ([]*types.EvaluationTaskEntity, error)
	ListTaskLabels(ctx context.Context, tenantID uint64, taskIDs []string) (map[string][]string, error)
	ReplaceTaskLabels(
		ctx context.Context,
		tenantID uint64,
		taskID string,
		labels []string,
		now time.Time,
	) error
	DeleteTask(
		ctx context.Context,
		tenantID uint64,
		taskID string,
		now time.Time,
	) error
	DeleteExpiredTerminalTasks(
		ctx context.Context,
		cutoff time.Time,
		limit int,
	) (int64, error)
}
