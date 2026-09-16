package service

import (
	"context"
	"errors"
	"sort"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

const (
	evaluationMemoryStorageTenantID                 uint64 = 1
	evaluationMemoryStorageOwnerID                         = "evaluation-test-owner"
	evaluationMemoryStorageTemporaryKnowledgeBaseID        = "evaluation-kb"
)

// evaluationMemoryStorage keeps the M1 lifecycle tests focused on service
// semantics while exercising the same entity/detail boundary as persistence.
// Production code never constructs this test-only adapter.
type evaluationMemoryStorage struct {
	*fakeEvaluationTaskRepository
	tenantID uint64
	ownerID  string
}

func newEvaluationMemoryStorage() *evaluationMemoryStorage {
	return &evaluationMemoryStorage{
		fakeEvaluationTaskRepository: newFakeEvaluationTaskRepository(),
		tenantID:                     evaluationMemoryStorageTenantID,
		ownerID:                      evaluationMemoryStorageOwnerID,
	}
}

func (s *evaluationMemoryStorage) register(detail *types.EvaluationDetail) {
	if detail == nil || detail.Task == nil {
		return
	}
	if detail.Task.TenantID == 0 {
		detail.Task.TenantID = s.tenantID
	}
	if detail.Task.StartTime.IsZero() {
		detail.Task.StartTime = time.Now().UTC()
	}
	entity, err := evaluationDetailToEntity(
		detail,
		evaluationMemoryStorageTemporaryKnowledgeBaseID,
		s.ownerID,
		time.Now().UTC().Add(time.Hour),
	)
	if err != nil {
		panic(err)
	}
	entity.Version = 1
	entity.HeartbeatAt = entity.StartTime
	entity.CreatedAt = entity.StartTime
	entity.UpdatedAt = entity.StartTime
	s.fakeEvaluationTaskRepository.register(entity)
}

func (s *evaluationMemoryStorage) get(taskID string) (*types.EvaluationDetail, error) {
	entity, err := s.fakeEvaluationTaskRepository.get(s.tenantID, taskID)
	if err != nil {
		return nil, err
	}
	return evaluationEntityToDetail(entity)
}

func (s *evaluationMemoryStorage) update(
	taskID string,
	update func(*types.EvaluationDetail),
) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	key := evaluationTaskRepositoryKey{tenantID: s.tenantID, taskID: taskID}
	current, ok := s.tasks[key]
	if !ok {
		return interfaces.ErrEvaluationTaskNotFound
	}
	detail, err := evaluationEntityToDetail(current)
	if err != nil {
		return err
	}
	update(detail)
	leaseExpiresAt := time.Now().UTC().Add(time.Hour)
	updated, err := evaluationDetailToEntity(
		detail,
		current.TemporaryKnowledgeBaseID,
		current.OwnerID,
		leaseExpiresAt,
	)
	if err != nil {
		return err
	}
	updated.TemporaryKnowledgeID = current.TemporaryKnowledgeID
	updated.Version = current.Version
	updated.HeartbeatAt = current.HeartbeatAt
	updated.CreatedAt = current.CreatedAt
	updated.UpdatedAt = current.UpdatedAt
	if current.LeaseExpiresAt == nil {
		updated.LeaseExpiresAt = nil
	}
	s.tasks[key] = cloneEvaluationTaskEntity(updated)
	return nil
}

type evaluationTaskRepositoryCall struct {
	Method   string
	TenantID uint64
	TaskID   string
}

type fakeEvaluationTaskRepository struct {
	mu     sync.Mutex
	tasks  map[evaluationTaskRepositoryKey]*types.EvaluationTaskEntity
	labels map[evaluationTaskRepositoryKey][]string
	calls  []evaluationTaskRepositoryCall

	createErr           error
	getErr              error
	startErr            error
	progressErr         error
	knowledgeErr        error
	terminalErr         error
	heartbeatErr        error
	heartbeatErrs       []error
	heartbeatBlockCount int
	claimErr            error
	cancelErr           error
	retentionErr        error
	lastListLimit       int

	startEntered chan<- struct{}
	startRelease <-chan struct{}
	claimEntered chan struct{}
}

type evaluationTaskRepositoryKey struct {
	tenantID uint64
	taskID   string
}

func newFakeEvaluationTaskRepository() *fakeEvaluationTaskRepository {
	return &fakeEvaluationTaskRepository{
		tasks:  make(map[evaluationTaskRepositoryKey]*types.EvaluationTaskEntity),
		labels: make(map[evaluationTaskRepositoryKey][]string),
	}
}

func (r *fakeEvaluationTaskRepository) CreateTask(
	_ context.Context,
	tenantID uint64,
	task *types.EvaluationTaskEntity,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	taskID := ""
	if task != nil {
		taskID = task.ID
	}
	r.recordLocked("CreateTask", tenantID, taskID)
	if r.createErr != nil {
		return r.createErr
	}
	if task == nil {
		return errors.New("create evaluation task: task is required")
	}
	if task.TenantID != tenantID {
		return interfaces.ErrEvaluationTaskTenantMismatch
	}
	key := evaluationTaskRepositoryKey{tenantID: tenantID, taskID: task.ID}
	if _, exists := r.tasks[key]; exists {
		return interfaces.ErrEvaluationTaskAlreadyExists
	}

	if task.Version == 0 {
		task.Version = 1
	}
	task.StartTime = task.StartTime.UTC()
	if task.LeaseExpiresAt != nil {
		leaseExpiresAt := task.LeaseExpiresAt.UTC()
		task.LeaseExpiresAt = &leaseExpiresAt
	}
	if !task.HeartbeatAt.IsZero() {
		task.HeartbeatAt = task.HeartbeatAt.UTC()
	}
	if task.CreatedAt.IsZero() {
		task.CreatedAt = task.StartTime
	}
	if task.UpdatedAt.IsZero() {
		task.UpdatedAt = task.StartTime
	}
	r.tasks[key] = cloneEvaluationTaskEntity(task)
	return nil
}

func (r *fakeEvaluationTaskRepository) GetTask(
	_ context.Context,
	tenantID uint64,
	taskID string,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("GetTask", tenantID, taskID)
	if r.getErr != nil {
		return nil, r.getErr
	}
	task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]
	if !ok || task.DeletedAt.Valid {
		return nil, interfaces.ErrEvaluationTaskNotFound
	}
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) GetTasksByIDs(
	_ context.Context,
	tenantID uint64,
	taskIDs []string,
) (map[string]*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.recordLocked("GetTasksByIDs", tenantID, "")
	result := make(map[string]*types.EvaluationTaskEntity, len(taskIDs))
	for _, taskID := range taskIDs {
		if task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]; ok {
			result[taskID] = cloneEvaluationTaskEntity(task)
		}
	}
	return result, nil
}

func (r *fakeEvaluationTaskRepository) TryStartTask(
	_ context.Context,
	command types.EvaluationTaskStartCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	r.recordLocked("TryStartTask", command.TenantID, command.TaskID)
	startErr := r.startErr
	startEntered := r.startEntered
	startRelease := r.startRelease
	r.mu.Unlock()

	if startEntered != nil {
		select {
		case startEntered <- struct{}{}:
		default:
		}
	}
	if startRelease != nil {
		<-startRelease
	}
	if startErr != nil {
		return nil, startErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	task, err := r.mutableTaskLocked(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		types.EvaluationStatuePending,
	)
	if err != nil {
		return nil, err
	}
	now := command.Now.UTC()
	leaseExpiresAt := command.LeaseExpiresAt.UTC()
	task.Status = types.EvaluationStatueRunning
	task.ErrMsg = ""
	task.EndTime = nil
	task.CleanupErrors = types.JSON(`[]`)
	task.HeartbeatAt = now
	task.LeaseExpiresAt = &leaseExpiresAt
	task.UpdatedAt = now
	task.Version++
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) PublishProgress(
	_ context.Context,
	command types.EvaluationTaskProgressCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("PublishProgress", command.TenantID, command.TaskID)
	if r.progressErr != nil {
		return nil, r.progressErr
	}
	task, err := r.mutableTaskLocked(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		types.EvaluationStatueRunning,
	)
	if err != nil {
		return nil, err
	}
	if (task.Total != 0 && task.Total != command.Total) || task.Finished > command.Finished {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	now := command.Now.UTC()
	leaseExpiresAt := command.LeaseExpiresAt.UTC()
	task.Total = command.Total
	task.Finished = command.Finished
	task.Metric = append(types.JSON(nil), command.Metric...)
	task.RuntimeMetrics = append(types.JSON(nil), command.RuntimeMetrics...)
	if task.HeartbeatAt.Before(now) {
		task.HeartbeatAt = now
	}
	if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.Before(leaseExpiresAt) {
		task.LeaseExpiresAt = &leaseExpiresAt
	}
	if task.UpdatedAt.Before(now) {
		task.UpdatedAt = now
	}
	task.Version++
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) RecordTemporaryKnowledge(
	_ context.Context,
	command types.EvaluationTaskKnowledgeCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("RecordTemporaryKnowledge", command.TenantID, command.TaskID)
	if r.knowledgeErr != nil {
		return nil, r.knowledgeErr
	}
	task, err := r.mutableTaskLocked(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		types.EvaluationStatueRunning,
	)
	if err != nil {
		return nil, err
	}
	if task.TemporaryKnowledgeID != "" {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	task.TemporaryKnowledgeID = command.TemporaryKnowledgeID
	if updatedAt := command.UpdatedAt.UTC(); task.UpdatedAt.Before(updatedAt) {
		task.UpdatedAt = updatedAt
	}
	task.Version++
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) PublishTerminal(
	_ context.Context,
	command types.EvaluationTaskTerminalCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("PublishTerminal", command.TenantID, command.TaskID)
	if r.terminalErr != nil {
		return nil, r.terminalErr
	}
	task, err := r.mutableTaskInStatesLocked(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		command.ExpectedVersion,
		types.EvaluationStatuePending,
		types.EvaluationStatueRunning,
	)
	if err != nil {
		return nil, err
	}
	if command.Status != types.EvaluationStatueSuccess &&
		command.Status != types.EvaluationStatueFailed &&
		command.Status != types.EvaluationStatueTimedOut &&
		command.Status != types.EvaluationStatueInterrupted &&
		command.Status != types.EvaluationStatueCanceled {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	if command.Status == types.EvaluationStatueSuccess && task.Finished != task.Total {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	if command.Status == types.EvaluationStatueCanceled {
		if task.CancelRequestedAt == nil {
			return nil, interfaces.ErrEvaluationTaskStateConflict
		}
	} else if task.CancelRequestedAt != nil {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	endTime := command.EndTime.UTC()
	task.Status = command.Status
	task.EndTime = &endTime
	task.ErrMsg = command.ErrMsg
	task.CleanupErrors = append(types.JSON(nil), command.CleanupErrors...)
	task.Metric = append(types.JSON(nil), command.Metric...)
	task.RuntimeMetrics = append(types.JSON(nil), command.RuntimeMetrics...)
	task.LeaseExpiresAt = nil
	if task.UpdatedAt.Before(endTime) {
		task.UpdatedAt = endTime
	}
	task.Version++
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) RequestCancel(
	_ context.Context,
	command types.EvaluationTaskCancelCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("RequestCancel", command.TenantID, command.TaskID)
	if r.cancelErr != nil {
		return nil, r.cancelErr
	}
	task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: command.TenantID, taskID: command.TaskID}]
	if !ok {
		return nil, interfaces.ErrEvaluationTaskNotFound
	}
	if task.Status != types.EvaluationStatuePending && task.Status != types.EvaluationStatueRunning {
		return cloneEvaluationTaskEntity(task), nil
	}
	if task.CancelRequestedAt == nil {
		cancelRequestedAt := command.Now.UTC()
		task.CancelRequestedAt = &cancelRequestedAt
		if task.UpdatedAt.Before(cancelRequestedAt) {
			task.UpdatedAt = cancelRequestedAt
		}
	}
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) HeartbeatTask(
	ctx context.Context,
	command types.EvaluationTaskHeartbeatCommand,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	r.recordLocked("HeartbeatTask", command.TenantID, command.TaskID)
	blockUntilContextDone := r.heartbeatBlockCount > 0
	if blockUntilContextDone {
		r.heartbeatBlockCount--
	}
	heartbeatErr := r.heartbeatErr
	if len(r.heartbeatErrs) > 0 {
		heartbeatErr = r.heartbeatErrs[0]
		r.heartbeatErrs = r.heartbeatErrs[1:]
	}
	r.mu.Unlock()

	if blockUntilContextDone {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	if heartbeatErr != nil {
		return nil, heartbeatErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	task, err := r.mutableTaskInStatesLocked(
		command.TenantID,
		command.TaskID,
		command.OwnerID,
		taskVersionIgnored,
		types.EvaluationStatueRunning,
	)
	if err != nil {
		return nil, err
	}
	now := command.Now.UTC()
	leaseExpiresAt := command.LeaseExpiresAt.UTC()
	if task.HeartbeatAt.Before(now) {
		task.HeartbeatAt = now
	}
	if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.Before(leaseExpiresAt) {
		task.LeaseExpiresAt = &leaseExpiresAt
	}
	if task.UpdatedAt.Before(now) {
		task.UpdatedAt = now
	}
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) setHeartbeatErr(err error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.heartbeatErr = err
}

func (r *fakeEvaluationTaskRepository) ClaimExpiredTasks(
	_ context.Context,
	command types.EvaluationTaskClaimExpiredCommand,
) ([]*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("ClaimExpiredTasks", 0, "")
	if r.claimEntered != nil {
		select {
		case r.claimEntered <- struct{}{}:
		default:
		}
	}
	if r.claimErr != nil {
		return nil, r.claimErr
	}
	claimed := make([]*types.EvaluationTaskEntity, 0, command.Limit)
	now := command.Now.UTC()
	leaseExpiresAt := command.LeaseExpiresAt.UTC()
	for _, task := range r.tasks {
		if len(claimed) >= command.Limit {
			break
		}
		if task.Status != types.EvaluationStatuePending && task.Status != types.EvaluationStatueRunning {
			continue
		}
		if task.LeaseExpiresAt == nil || task.LeaseExpiresAt.After(now) {
			continue
		}
		task.OwnerID = command.OwnerID
		task.HeartbeatAt = now
		task.LeaseExpiresAt = &leaseExpiresAt
		if task.UpdatedAt.Before(now) {
			task.UpdatedAt = now
		}
		task.Version++
		claimed = append(claimed, cloneEvaluationTaskEntity(task))
	}
	return claimed, nil
}

func (r *fakeEvaluationTaskRepository) ListTasks(
	_ context.Context,
	tenantID uint64,
	query types.EvaluationTaskListQuery,
) ([]*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("ListTasks", tenantID, "")
	r.lastListLimit = query.Limit
	if query.Limit <= 0 {
		return nil, errors.New("list evaluation tasks: limit must be positive")
	}
	tasks := make([]*types.EvaluationTaskEntity, 0)
	for _, task := range r.tasks {
		if task.TenantID != tenantID || task.DeletedAt.Valid {
			continue
		}
		if query.Status != nil && task.Status != *query.Status {
			continue
		}
		if query.DatasetID != "" && task.DatasetID != query.DatasetID {
			continue
		}
		if query.DatasetVersionID != "" && (task.DatasetVersionID == nil ||
			*task.DatasetVersionID != query.DatasetVersionID) {
			continue
		}
		if query.StartedFrom != nil && task.StartTime.Before(query.StartedFrom.UTC()) {
			continue
		}
		if query.StartedTo != nil && task.StartTime.After(query.StartedTo.UTC()) {
			continue
		}
		matchedLabels := true
		storedLabels := r.labels[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: task.ID}]
		for _, requiredLabel := range query.Labels {
			found := false
			for _, storedLabel := range storedLabels {
				if storedLabel == requiredLabel {
					found = true
					break
				}
			}
			if !found {
				matchedLabels = false
				break
			}
		}
		if !matchedLabels {
			continue
		}
		if query.StartBefore != nil {
			startBefore := query.StartBefore.UTC()
			startTime := task.StartTime.UTC()
			if startTime.After(startBefore) {
				continue
			}
			if startTime.Equal(startBefore) && task.ID >= query.IDBefore {
				continue
			}
		}
		tasks = append(tasks, cloneEvaluationTaskEntity(task))
	}
	sort.Slice(tasks, func(i, j int) bool {
		left, right := tasks[i].StartTime.UTC(), tasks[j].StartTime.UTC()
		if !left.Equal(right) {
			return left.After(right)
		}
		return tasks[i].ID > tasks[j].ID
	})
	if len(tasks) > query.Limit {
		tasks = tasks[:query.Limit]
	}
	return tasks, nil
}

func (r *fakeEvaluationTaskRepository) ListTaskLabels(
	_ context.Context,
	tenantID uint64,
	taskIDs []string,
) (map[string][]string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	result := make(map[string][]string, len(taskIDs))
	for _, taskID := range taskIDs {
		labels := r.labels[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]
		if labels != nil {
			result[taskID] = append([]string(nil), labels...)
		}
	}
	return result, nil
}

func (r *fakeEvaluationTaskRepository) ReplaceTaskLabels(
	_ context.Context,
	tenantID uint64,
	taskID string,
	labels []string,
	_ time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	key := evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}
	if _, ok := r.tasks[key]; !ok {
		return interfaces.ErrEvaluationTaskNotFound
	}
	r.labels[key] = append([]string(nil), labels...)
	return nil
}

func (r *fakeEvaluationTaskRepository) DeleteTask(
	_ context.Context,
	tenantID uint64,
	taskID string,
	now time.Time,
) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("DeleteTask", tenantID, taskID)
	task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]
	if !ok {
		return nil
	}
	if task.DeletedAt.Valid {
		return nil
	}
	switch task.Status {
	case types.EvaluationStatueSuccess,
		types.EvaluationStatueFailed,
		types.EvaluationStatueTimedOut,
		types.EvaluationStatueInterrupted,
		types.EvaluationStatueCanceled:
		task.DeletedAt = gorm.DeletedAt{Time: now.UTC(), Valid: true}
		return nil
	default:
		return interfaces.ErrEvaluationTaskStateConflict
	}
}

func (r *fakeEvaluationTaskRepository) DeleteExpiredTerminalTasks(
	_ context.Context,
	cutoff time.Time,
	limit int,
) (int64, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.recordLocked("DeleteExpiredTerminalTasks", 0, "")
	if r.retentionErr != nil {
		return 0, r.retentionErr
	}
	if limit <= 0 {
		return 0, errors.New("delete expired evaluation tasks: limit must be positive")
	}
	cutoff = cutoff.UTC()
	expired := make([]*types.EvaluationTaskEntity, 0)
	for _, task := range r.tasks {
		switch task.Status {
		case types.EvaluationStatueSuccess,
			types.EvaluationStatueFailed,
			types.EvaluationStatueTimedOut,
			types.EvaluationStatueInterrupted,
			types.EvaluationStatueCanceled:
		default:
			continue
		}
		if task.EndTime == nil || !task.EndTime.UTC().Before(cutoff) {
			continue
		}
		expired = append(expired, task)
	}
	sort.Slice(expired, func(i, j int) bool {
		left, right := expired[i].EndTime.UTC(), expired[j].EndTime.UTC()
		if !left.Equal(right) {
			return left.Before(right)
		}
		return expired[i].ID < expired[j].ID
	})
	if len(expired) > limit {
		expired = expired[:limit]
	}
	for _, task := range expired {
		delete(r.tasks, evaluationTaskRepositoryKey{tenantID: task.TenantID, taskID: task.ID})
	}
	return int64(len(expired)), nil
}

func (r *fakeEvaluationTaskRepository) register(task *types.EvaluationTaskEntity) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if task == nil {
		return
	}
	r.tasks[evaluationTaskRepositoryKey{tenantID: task.TenantID, taskID: task.ID}] = cloneEvaluationTaskEntity(task)
}

func (r *fakeEvaluationTaskRepository) get(
	tenantID uint64,
	taskID string,
) (*types.EvaluationTaskEntity, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]
	if !ok {
		return nil, interfaces.ErrEvaluationTaskNotFound
	}
	return cloneEvaluationTaskEntity(task), nil
}

func (r *fakeEvaluationTaskRepository) callsSnapshot() []evaluationTaskRepositoryCall {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]evaluationTaskRepositoryCall(nil), r.calls...)
}

func (r *fakeEvaluationTaskRepository) countCalls(method string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	count := 0
	for _, call := range r.calls {
		if call.Method == method {
			count++
		}
	}
	return count
}

func (r *fakeEvaluationTaskRepository) recordLocked(method string, tenantID uint64, taskID string) {
	r.calls = append(r.calls, evaluationTaskRepositoryCall{
		Method:   method,
		TenantID: tenantID,
		TaskID:   taskID,
	})
}

func (r *fakeEvaluationTaskRepository) mutableTaskLocked(
	tenantID uint64,
	taskID string,
	ownerID string,
	expectedVersion uint64,
	expectedStatus types.EvaluationStatue,
) (*types.EvaluationTaskEntity, error) {
	return r.mutableTaskInStatesLocked(tenantID, taskID, ownerID, expectedVersion, expectedStatus)
}

func (r *fakeEvaluationTaskRepository) mutableTaskInStatesLocked(
	tenantID uint64,
	taskID string,
	ownerID string,
	expectedVersion uint64,
	expectedStatuses ...types.EvaluationStatue,
) (*types.EvaluationTaskEntity, error) {
	task, ok := r.tasks[evaluationTaskRepositoryKey{tenantID: tenantID, taskID: taskID}]
	if !ok {
		return nil, interfaces.ErrEvaluationTaskNotFound
	}
	if task.OwnerID != ownerID {
		return nil, interfaces.ErrEvaluationTaskOwnerConflict
	}
	statusMatches := false
	for _, expectedStatus := range expectedStatuses {
		if task.Status == expectedStatus {
			statusMatches = true
			break
		}
	}
	if !statusMatches {
		return nil, interfaces.ErrEvaluationTaskStateConflict
	}
	if expectedVersion != taskVersionIgnored && task.Version != expectedVersion {
		return nil, interfaces.ErrEvaluationTaskVersionConflict
	}
	return task, nil
}

const taskVersionIgnored = ^uint64(0)

func cloneEvaluationTaskEntity(task *types.EvaluationTaskEntity) *types.EvaluationTaskEntity {
	if task == nil {
		return nil
	}
	cloned := *task
	cloned.Params = append(types.JSON(nil), task.Params...)
	cloned.Metric = append(types.JSON(nil), task.Metric...)
	cloned.RuntimeMetrics = append(types.JSON(nil), task.RuntimeMetrics...)
	cloned.ExperimentSnapshot = append(types.JSON(nil), task.ExperimentSnapshot...)
	cloned.CleanupErrors = append(types.JSON(nil), task.CleanupErrors...)
	cloned.Labels = append([]string(nil), task.Labels...)
	if task.DatasetVersionID != nil {
		value := *task.DatasetVersionID
		cloned.DatasetVersionID = &value
	}
	if task.DatasetContentSHA256 != nil {
		value := *task.DatasetContentSHA256
		cloned.DatasetContentSHA256 = &value
	}
	if task.ExperimentSHA256 != nil {
		value := *task.ExperimentSHA256
		cloned.ExperimentSHA256 = &value
	}
	if task.EndTime != nil {
		endTime := *task.EndTime
		cloned.EndTime = &endTime
	}
	if task.LeaseExpiresAt != nil {
		leaseExpiresAt := *task.LeaseExpiresAt
		cloned.LeaseExpiresAt = &leaseExpiresAt
	}
	if task.CancelRequestedAt != nil {
		cancelRequestedAt := *task.CancelRequestedAt
		cloned.CancelRequestedAt = &cancelRequestedAt
	}
	return &cloned
}

var _ interfaces.EvaluationTaskRepository = (*fakeEvaluationTaskRepository)(nil)
