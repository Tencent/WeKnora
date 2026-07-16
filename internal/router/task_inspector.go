package router

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
)

// NewAsynqInspector constructs an *asynq.Inspector pointed at the same
// Redis used by the asynq client. Only registered in asynq mode.
func NewAsynqInspector(redisClient *redis.Client) *asynq.Inspector {
	return asynq.NewInspectorFromRedisClient(redisClient)
}

// asynqTaskInspector implements interfaces.TaskInspector backed by an
// *asynq.Inspector. Scans the queues we actually use and matches tasks
// whose payload carries the given
// knowledge_id. Best-effort: any scan/delete error is logged and
// swallowed so the cancel API still returns success even when Redis is
// flaky.
type asynqTaskInspector struct {
	inspector *asynq.Inspector
	redis     redis.UniversalClient
}

// NewAsynqTaskInspector returns a TaskInspector wrapping the given
// *asynq.Inspector. nil-safe: a nil inspector degrades to a no-op so
// the cancel path remains usable when the inspector failed to init.
func NewAsynqTaskInspector(inspector *asynq.Inspector, redisClient *redis.Client) interfaces.TaskInspector {
	if inspector == nil || redisClient == nil {
		return noopTaskInspector{}
	}
	return &asynqTaskInspector{inspector: inspector, redis: redisClient}
}

// knowledgeIDProbe is the minimal payload shape we need to filter
// tasks. All pipeline payload types embed a json:"knowledge_id" field,
// so a single struct covers Document / ImageMultimodal / PostProcess /
// Question / Summary / Extract / Manual.
type knowledgeIDProbe struct {
	KnowledgeID string `json:"knowledge_id,omitempty"`
}

type runtimeTaskPayloadProbe struct {
	TenantID        uint64   `json:"tenant_id,omitempty"`
	KnowledgeBaseID string   `json:"knowledge_base_id,omitempty"`
	KBID            string   `json:"kb_id,omitempty"`
	KnowledgeID     string   `json:"knowledge_id,omitempty"`
	TaskID          string   `json:"task_id,omitempty"`
	SourceID        string   `json:"source_id,omitempty"`
	TargetID        string   `json:"target_id,omitempty"`
	SourceKBID      string   `json:"source_kb_id,omitempty"`
	TargetKBID      string   `json:"target_kb_id,omitempty"`
	DataSourceID    string   `json:"data_source_id,omitempty"`
	SyncLogID       string   `json:"sync_log_id,omitempty"`
	KnowledgeIDs    []string `json:"knowledge_ids,omitempty"`
	EnqueuedAt      int64    `json:"enqueued_at,omitempty"`
	CreatedAt       int64    `json:"created_at,omitempty"`
}

// queuesScanned is the fixed set of queue names this codebase enqueues
// into. Kept tight on purpose — we never scan user-defined queues.
// MUST include every queue any cancelable task type can land in; the
// multimodal queue is required here so cancelling a knowledge also purges
// its (potentially hundreds of) pending image:multimodal tasks.
var queuesScanned = func() []string {
	queues := make([]string, 0, len(types.QueueDefinitions()))
	for _, definition := range types.QueueDefinitions() {
		queues = append(queues, definition.Name)
	}
	return queues
}()

// taskTypesForKnowledgeCancel lists every asynq task type that carries
// a knowledge_id in its payload and should be cancelable. The set is
// deliberately narrow: we don't touch FAQ import / KB-level tasks
// because the cancel API is per-knowledge.
var taskTypesForKnowledgeCancel = map[string]struct{}{
	types.TypeDocumentProcess:      {},
	types.TypeManualProcess:        {},
	types.TypeImageMultimodal:      {},
	types.TypeKnowledgePostProcess: {},
	types.TypeQuestionGeneration:   {},
	types.TypeSummaryGeneration:    {},
	types.TypeChunkExtract:         {},
}

// listPageSize caps each Redis LIST call. Asynq pages tasks, so we
// loop until a short page comes back. 100 matches asynq's default.
const listPageSize = 100

// CancelTasksForKnowledge removes queued tasks whose payload references
// the given knowledge_id and signals active workers running such tasks
// to stop.
func (a *asynqTaskInspector) CancelTasksForKnowledge(
	ctx context.Context, knowledgeID string,
) (int, int, error) {
	if a == nil || a.inspector == nil || knowledgeID == "" {
		return 0, 0, nil
	}
	deleted := 0
	cancelled := 0

	for _, queue := range queuesScanned {
		// Pending / Scheduled / Retry can all be deleted by task ID.
		// Archived tasks are NOT touched: dead-letter rows are
		// already final and should remain visible to operators.
		deleted += a.deletePendingMatches(ctx, queue, knowledgeID)
		deleted += a.deleteScheduledMatches(ctx, queue, knowledgeID)
		deleted += a.deleteRetryMatches(ctx, queue, knowledgeID)
		cancelled += a.cancelActiveMatches(ctx, queue, knowledgeID)
	}

	logger.Infof(ctx,
		"[TaskInspector] knowledge=%s cancel summary: deleted_from_queue=%d active_cancel_signaled=%d",
		knowledgeID, deleted, cancelled,
	)
	return deleted, cancelled, nil
}

// HasQueuedTasksForKnowledge reports whether any pending / scheduled /
// retry / active task referencing knowledgeID still lives in the queue.
// Read-only counterpart of CancelTasksForKnowledge — the housekeeping
// sweep uses it to avoid flagging a backlogged-but-not-orphaned row as
// failed. Short-circuits on the first match and never deletes anything.
func (a *asynqTaskInspector) HasQueuedTasksForKnowledge(
	ctx context.Context, knowledgeID string,
) (bool, error) {
	if a == nil || a.inspector == nil || knowledgeID == "" {
		return false, nil
	}
	listers := []struct {
		state string
		list  func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error)
	}{
		{"pending", a.inspector.ListPendingTasks},
		{"scheduled", a.inspector.ListScheduledTasks},
		{"retry", a.inspector.ListRetryTasks},
		{"active", a.inspector.ListActiveTasks},
	}
	for _, queue := range queuesScanned {
		for _, l := range listers {
			if a.queueStateHasMatch(ctx, queue, knowledgeID, l.state, l.list) {
				return true, nil
			}
		}
	}
	return false, nil
}

// QueueStats returns a depth snapshot for every queue this app enqueues
// into. Read-only: it calls Inspector.GetQueueInfo per queue and maps
// the result onto types.QueueStat, attaching static pool/weight metadata
// from the central queue registry. A queue that has never received a task yields
// ErrQueueNotFound from asynq; we still surface it as a zeroed row so the
// dashboard shows the complete lane set even before a queue receives its
// first task.
func (a *asynqTaskInspector) QueueStats(
	ctx context.Context,
) ([]types.QueueStat, bool, error) {
	if a == nil || a.inspector == nil {
		return nil, false, nil
	}
	definitions := types.QueueDefinitions()
	stats := make([]types.QueueStat, 0, len(definitions))
	for _, definition := range definitions {
		queue := definition.Name
		stat := types.QueueStat{
			Name:   queue,
			Pool:   definition.Pool,
			Weight: definition.Weight,
		}
		info, err := a.inspector.GetQueueInfo(queue)
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] queue info queue=%s: %v", queue, err)
			}
			// Zeroed row: queue not created yet (or transient error).
			stats = append(stats, stat)
			continue
		}
		stat.Size = info.Size
		stat.Pending = info.Pending
		stat.Active = info.Active
		stat.Scheduled = info.Scheduled
		stat.Retry = info.Retry
		stat.Archived = info.Archived
		stat.Completed = info.Completed
		stat.Processed = info.Processed
		stat.Failed = info.Failed
		stat.Paused = info.Paused
		stat.LatencyMs = info.Latency.Milliseconds()
		stat.MemoryUsageBytes = info.MemoryUsage
		stats = append(stats, stat)
	}
	return stats, true, nil
}

type runtimeWorkerMetadata struct {
	started time.Time
	worker  string
}

func runtimeTaskState(state asynq.TaskState) (types.RuntimeTaskState, error) {
	switch state {
	case asynq.TaskStatePending:
		return types.RuntimeTaskPending, nil
	case asynq.TaskStateActive:
		return types.RuntimeTaskActive, nil
	case asynq.TaskStateScheduled:
		return types.RuntimeTaskScheduled, nil
	case asynq.TaskStateRetry:
		return types.RuntimeTaskRetry, nil
	case asynq.TaskStateArchived:
		return types.RuntimeTaskArchived, nil
	case asynq.TaskStateCompleted:
		return types.RuntimeTaskCompleted, nil
	default:
		return "", fmt.Errorf("unsupported runtime task state %v", state)
	}
}

func runtimeTaskTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	copy := value
	return &copy
}

func runtimePayloadTime(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	// Payload timestamps in this repository are seconds today. Accept the
	// common higher-precision Unix forms as well for connector-originated jobs.
	var parsed time.Time
	if value > 100_000_000_000_000_000 {
		parsed = time.Unix(0, value)
	} else if value > 100_000_000_000_000 {
		parsed = time.UnixMicro(value)
	} else if value > 10_000_000_000 {
		parsed = time.UnixMilli(value)
	} else {
		parsed = time.Unix(value, 0)
	}
	return &parsed
}

func runtimeTaskActions(info types.RuntimeTaskInfo) []types.RuntimeTaskAction {
	actions := make([]types.RuntimeTaskAction, 0, 3)
	if _, cancellable := taskTypesForKnowledgeCancel[info.Type]; cancellable &&
		info.TenantID > 0 && info.KnowledgeID != "" {
		switch info.State {
		case types.RuntimeTaskPending, types.RuntimeTaskActive,
			types.RuntimeTaskScheduled, types.RuntimeTaskRetry:
			actions = append(actions, types.RuntimeTaskActionCancel)
		}
	}
	switch info.State {
	case types.RuntimeTaskScheduled, types.RuntimeTaskRetry:
		actions = append(actions, types.RuntimeTaskActionRunNow)
	case types.RuntimeTaskArchived:
		actions = append(actions, types.RuntimeTaskActionRunNow, types.RuntimeTaskActionDelete)
	}
	return actions
}

func projectRuntimeTask(task *asynq.TaskInfo, worker runtimeWorkerMetadata) (types.RuntimeTaskInfo, error) {
	state, err := runtimeTaskState(task.State)
	if err != nil {
		return types.RuntimeTaskInfo{}, err
	}
	probe := runtimeTaskPayloadProbe{}
	_ = json.Unmarshal(task.Payload, &probe)
	kbID := probe.KnowledgeBaseID
	if kbID == "" {
		kbID = probe.KBID
	}
	enqueuedAt := probe.EnqueuedAt
	if enqueuedAt == 0 {
		enqueuedAt = probe.CreatedAt
	}
	info := types.RuntimeTaskInfo{
		ID:              task.ID,
		Queue:           task.Queue,
		Type:            task.Type,
		State:           state,
		LastError:       task.LastErr,
		LastFailedAt:    runtimeTaskTime(task.LastFailedAt),
		NextProcessAt:   runtimeTaskTime(task.NextProcessAt),
		StartedAt:       runtimeTaskTime(worker.started),
		CompletedAt:     runtimeTaskTime(task.CompletedAt),
		Deadline:        runtimeTaskTime(task.Deadline),
		EnqueuedAt:      runtimePayloadTime(enqueuedAt),
		Retried:         task.Retried,
		MaxRetry:        task.MaxRetry,
		IsOrphaned:      task.IsOrphaned,
		Worker:          worker.worker,
		TenantID:        probe.TenantID,
		KnowledgeBaseID: kbID,
		KnowledgeID:     probe.KnowledgeID,
		TaskID:          probe.TaskID,
		SourceID:        probe.SourceID,
		TargetID:        probe.TargetID,
		SourceKBID:      probe.SourceKBID,
		TargetKBID:      probe.TargetKBID,
		DataSourceID:    probe.DataSourceID,
		SyncLogID:       probe.SyncLogID,
		KnowledgeCount:  len(probe.KnowledgeIDs),
	}
	info.AllowedActions = runtimeTaskActions(info)
	return info, nil
}

func (a *asynqTaskInspector) activeWorkerMetadata() map[string]runtimeWorkerMetadata {
	result := make(map[string]runtimeWorkerMetadata)
	servers, err := a.inspector.Servers()
	if err != nil {
		return result
	}
	for _, server := range servers {
		if server == nil {
			continue
		}
		workerName := server.Host
		if server.PID > 0 {
			workerName = fmt.Sprintf("%s:%d", server.Host, server.PID)
		}
		for _, worker := range server.ActiveWorkers {
			if worker == nil {
				continue
			}
			result[worker.Queue+"\x00"+worker.TaskID] = runtimeWorkerMetadata{
				started: worker.Started,
				worker:  workerName,
			}
		}
	}
	return result
}

const (
	runtimeTaskCursorVersion    = 1
	runtimeTaskCursorMaxAnchors = 32
	runtimeTaskCursorMaxBytes   = 16 * 1024
)

type runtimeTaskCursor struct {
	Version int                    `json:"v"`
	Queue   string                 `json:"q"`
	State   types.RuntimeTaskState `json:"s"`
	Anchors []string               `json:"a"`
}

type runtimeTaskStorageOrder int

const (
	runtimeTaskListNewestFirst runtimeTaskStorageOrder = iota
	runtimeTaskZSetEarliestFirst
	runtimeTaskZSetNewestFirst
)

func runtimeTaskStorage(state types.RuntimeTaskState) (suffix string, order runtimeTaskStorageOrder) {
	switch state {
	case types.RuntimeTaskPending, types.RuntimeTaskActive:
		// Asynq LPUSHes newly enqueued/started tasks, so index zero is the
		// newest task in these live-state lists.
		return string(state), runtimeTaskListNewestFirst
	case types.RuntimeTaskScheduled, types.RuntimeTaskRetry:
		// Sorted-set scores are NextProcessAt. Earliest-first keeps the next
		// operational action visible instead of hiding it below later work.
		return string(state), runtimeTaskZSetEarliestFirst
	case types.RuntimeTaskArchived, types.RuntimeTaskCompleted:
		// Archived scores are LastFailedAt; completed scores are expiry time.
		// Reverse score order presents the newest failures and the newest
		// retained completion records first.
		return string(state), runtimeTaskZSetNewestFirst
	default:
		return "", runtimeTaskListNewestFirst
	}
}

func runtimeTaskStateKey(queue string, state types.RuntimeTaskState) (string, runtimeTaskStorageOrder) {
	suffix, order := runtimeTaskStorage(state)
	// Asynq's public Inspector only supports offset pages and does not expose
	// sort direction or continuation cursors. Keep the v0.26 queue-key schema
	// isolated here and covered by cursor-order integration tests.
	return "asynq:{" + queue + "}:" + suffix, order
}

func decodeRuntimeTaskCursor(raw, queue string, state types.RuntimeTaskState) ([]string, error) {
	if raw == "" {
		return nil, nil
	}
	if len(raw) > runtimeTaskCursorMaxBytes {
		return nil, types.ErrInvalidRuntimeTaskCursor
	}
	data, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return nil, types.ErrInvalidRuntimeTaskCursor
	}
	var cursor runtimeTaskCursor
	if err = json.Unmarshal(data, &cursor); err != nil ||
		cursor.Version != runtimeTaskCursorVersion || cursor.Queue != queue || cursor.State != state ||
		len(cursor.Anchors) == 0 || len(cursor.Anchors) > runtimeTaskCursorMaxAnchors {
		return nil, types.ErrInvalidRuntimeTaskCursor
	}
	for _, anchor := range cursor.Anchors {
		if anchor == "" {
			return nil, types.ErrInvalidRuntimeTaskCursor
		}
	}
	return cursor.Anchors, nil
}

func encodeRuntimeTaskCursor(queue string, state types.RuntimeTaskState, anchors []string) (string, error) {
	if len(anchors) > runtimeTaskCursorMaxAnchors {
		anchors = anchors[len(anchors)-runtimeTaskCursorMaxAnchors:]
	}
	data, err := json.Marshal(runtimeTaskCursor{
		Version: runtimeTaskCursorVersion,
		Queue:   queue,
		State:   state,
		Anchors: anchors,
	})
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(data), nil
}

// runtimeTaskAnchorOffset finds the newest surviving anchor. Retaining a
// small window of anchors lets pagination continue when the last item from a
// previous live-state page finishes, retries, or is deleted between requests.
func (a *asynqTaskInspector) runtimeTaskAnchorOffset(
	ctx context.Context,
	key string,
	order runtimeTaskStorageOrder,
	anchors []string,
) (int64, error) {
	if len(anchors) == 0 {
		return 0, nil
	}
	pipe := a.redis.Pipeline()
	ranks := make([]*redis.IntCmd, 0, len(anchors))
	for i := len(anchors) - 1; i >= 0; i-- {
		switch order {
		case runtimeTaskListNewestFirst:
			ranks = append(ranks, pipe.LPos(ctx, key, anchors[i], redis.LPosArgs{}))
		case runtimeTaskZSetEarliestFirst:
			ranks = append(ranks, pipe.ZRank(ctx, key, anchors[i]))
		case runtimeTaskZSetNewestFirst:
			ranks = append(ranks, pipe.ZRevRank(ctx, key, anchors[i]))
		}
	}
	_, err := pipe.Exec(ctx)
	if err != nil && !errors.Is(err, redis.Nil) {
		return 0, err
	}
	for _, rank := range ranks {
		if rank.Err() == nil {
			return rank.Val() + 1, nil
		}
		if !errors.Is(rank.Err(), redis.Nil) {
			return 0, rank.Err()
		}
	}
	return 0, types.ErrExpiredRuntimeTaskCursor
}

func (a *asynqTaskInspector) listRuntimeTaskIDs(
	ctx context.Context,
	queue string,
	state types.RuntimeTaskState,
	anchors []string,
	limit int,
) ([]string, error) {
	key, order := runtimeTaskStateKey(queue, state)
	start, err := a.runtimeTaskAnchorOffset(ctx, key, order, anchors)
	if err != nil {
		return nil, err
	}
	stop := start + int64(limit) - 1
	switch order {
	case runtimeTaskListNewestFirst:
		return a.redis.LRange(ctx, key, start, stop).Result()
	case runtimeTaskZSetEarliestFirst:
		return a.redis.ZRange(ctx, key, start, stop).Result()
	case runtimeTaskZSetNewestFirst:
		return a.redis.ZRevRange(ctx, key, start, stop).Result()
	default:
		return nil, fmt.Errorf("unsupported runtime task storage order %d", order)
	}
}

// ListRuntimeTasks returns one cursor page in state-appropriate time order:
// newest first for pending/active/archived/completed, and next-to-run first
// for scheduled/retry. Only allow-listed routing metadata is projected from
// payloads so the dashboard never exposes document content or secrets.
func (a *asynqTaskInspector) ListRuntimeTasks(
	ctx context.Context,
	queue string,
	state types.RuntimeTaskState,
	cursor string,
	pageSize int,
) (types.RuntimeTaskPage, bool, error) {
	if a == nil || a.inspector == nil || a.redis == nil {
		return types.RuntimeTaskPage{}, false, nil
	}
	if !state.Valid() {
		return types.RuntimeTaskPage{}, true, fmt.Errorf("unsupported runtime task state %q", state)
	}
	if pageSize < 1 {
		pageSize = 20
	}
	if pageSize > 100 {
		pageSize = 100
	}
	anchors, err := decodeRuntimeTaskCursor(cursor, queue, state)
	if err != nil {
		return types.RuntimeTaskPage{}, true, err
	}
	workers := map[string]runtimeWorkerMetadata{}
	if state == types.RuntimeTaskActive {
		workers = a.activeWorkerMetadata()
	}
	result := make([]types.RuntimeTaskInfo, 0, pageSize)
	hasMore := false

	for len(result) < pageSize {
		// The extra ID proves there is another raw item without consuming it
		// into this page's continuation cursor.
		batchLimit := pageSize - len(result) + 1
		ids, listErr := a.listRuntimeTaskIDs(ctx, queue, state, anchors, batchLimit)
		if listErr != nil {
			if errors.Is(listErr, types.ErrExpiredRuntimeTaskCursor) && cursor == "" {
				return types.RuntimeTaskPage{Tasks: result}, true, nil
			}
			return types.RuntimeTaskPage{}, true, listErr
		}
		if len(ids) == 0 {
			break
		}
		for i, id := range ids {
			if len(result) == pageSize {
				hasMore = true
				break
			}
			anchors = append(anchors, id)
			if len(anchors) > runtimeTaskCursorMaxAnchors {
				anchors = anchors[len(anchors)-runtimeTaskCursorMaxAnchors:]
			}
			task, getErr := a.inspector.GetTaskInfo(queue, id)
			if errors.Is(getErr, asynq.ErrTaskNotFound) || errors.Is(getErr, asynq.ErrQueueNotFound) {
				continue
			}
			if getErr != nil {
				return types.RuntimeTaskPage{}, true, getErr
			}
			info, projectErr := projectRuntimeTask(task, workers[task.Queue+"\x00"+task.ID])
			if projectErr != nil {
				logger.Warnf(ctx, "[TaskInspector] project runtime task queue=%s id=%s: %v", queue, task.ID, projectErr)
				continue
			}
			if info.State != state {
				continue
			}
			result = append(result, info)
			if len(result) == pageSize {
				// A full raw batch may have contained stale IDs that were
				// skipped above. In that case the last returned item can also be
				// the last fetched ID even though older Redis entries still exist.
				hasMore = i < len(ids)-1 || len(ids) == batchLimit
			}
		}
		if hasMore || len(ids) < batchLimit {
			break
		}
	}

	page := types.RuntimeTaskPage{Tasks: result, HasMore: hasMore}
	if hasMore {
		page.NextCursor, err = encodeRuntimeTaskCursor(queue, state, anchors)
		if err != nil {
			return types.RuntimeTaskPage{}, true, err
		}
	}
	return page, true, nil
}

func (a *asynqTaskInspector) GetRuntimeTask(
	ctx context.Context, queue, taskID string,
) (*types.RuntimeTaskInfo, bool, error) {
	if a == nil || a.inspector == nil {
		return nil, false, nil
	}
	task, err := a.inspector.GetTaskInfo(queue, taskID)
	if err != nil {
		return nil, true, err
	}
	workers := map[string]runtimeWorkerMetadata{}
	if task.State == asynq.TaskStateActive {
		workers = a.activeWorkerMetadata()
	}
	info, err := projectRuntimeTask(task, workers[task.Queue+"\x00"+task.ID])
	if err != nil {
		return nil, true, err
	}
	return &info, true, nil
}

// RunRuntimeTask moves a scheduled, retry, or archived task to pending. Asynq
// deliberately preserves the retry counter.
func (a *asynqTaskInspector) RunRuntimeTask(ctx context.Context, queue, taskID string) (bool, error) {
	if a == nil || a.inspector == nil {
		return false, nil
	}
	task, _, err := a.GetRuntimeTask(ctx, queue, taskID)
	if err != nil {
		return true, err
	}
	if task == nil || !task.Allows(types.RuntimeTaskActionRunNow) {
		return true, fmt.Errorf("task %s in queue %s cannot run now", taskID, queue)
	}
	return true, a.inspector.RunTask(queue, taskID)
}

func (a *asynqTaskInspector) DeleteRuntimeTask(ctx context.Context, queue, taskID string) (bool, error) {
	if a == nil || a.inspector == nil {
		return false, nil
	}
	task, _, err := a.GetRuntimeTask(ctx, queue, taskID)
	if err != nil {
		return true, err
	}
	if task == nil || !task.Allows(types.RuntimeTaskActionDelete) {
		return true, fmt.Errorf("task %s in queue %s cannot be deleted", taskID, queue)
	}
	return true, a.inspector.DeleteTask(queue, taskID)
}

func (a *asynqTaskInspector) ForceDeleteRuntimeTask(ctx context.Context, queue, taskID string) (bool, error) {
	if a == nil || a.inspector == nil {
		return false, nil
	}
	return true, a.inspector.DeleteTask(queue, taskID)
}

func (a *asynqTaskInspector) WorkerServerStats(
	ctx context.Context,
) ([]types.WorkerServerStat, bool, error) {
	if a == nil || a.inspector == nil {
		return nil, false, nil
	}
	servers, err := a.inspector.Servers()
	if err != nil {
		return nil, true, err
	}
	stats := make([]types.WorkerServerStat, 0, len(servers))
	for _, server := range servers {
		if server == nil {
			continue
		}
		queues := make(map[string]int, len(server.Queues))
		for name, weight := range server.Queues {
			queues[name] = weight
		}
		stats = append(stats, types.WorkerServerStat{
			Concurrency: server.Concurrency,
			Active:      len(server.ActiveWorkers),
			Status:      server.Status,
			Queues:      queues,
		})
	}
	return stats, true, nil
}

// queueStateHasMatch pages through one (queue, state) list looking for a
// task that references knowledgeID. Mirrors the delete* scanners but is
// strictly read-only and returns early on the first hit. A backend error
// is logged and treated as "no match" (false); the caller's fail-safe
// then errs toward recovering the row rather than preserving it forever.
func (a *asynqTaskInspector) queueStateHasMatch(
	ctx context.Context, queue, knowledgeID, state string,
	list func(string, ...asynq.ListOption) ([]*asynq.TaskInfo, error),
) bool {
	page := 1
	for {
		tasks, err := list(queue, asynq.PageSize(listPageSize), asynq.Page(page))
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] probe %s queue=%s page=%d: %v", state, queue, page, err)
			}
			return false
		}
		if len(tasks) == 0 {
			return false
		}
		for _, t := range tasks {
			if matchesKnowledge(t.Type, t.Payload, knowledgeID) {
				return true
			}
		}
		if len(tasks) < listPageSize {
			return false
		}
		page++
	}
}

// matchesKnowledge returns true when the task type is one we cancel
// AND its payload references the target knowledge ID.
func matchesKnowledge(taskType string, payload []byte, knowledgeID string) bool {
	if _, ok := taskTypesForKnowledgeCancel[taskType]; !ok {
		return false
	}
	var probe knowledgeIDProbe
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	return probe.KnowledgeID == knowledgeID
}

func (a *asynqTaskInspector) deletePendingMatches(ctx context.Context, queue, knowledgeID string) int {
	deleted := 0
	page := 1
	for {
		tasks, err := a.inspector.ListPendingTasks(queue, asynq.PageSize(listPageSize), asynq.Page(page))
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] list pending queue=%s page=%d: %v", queue, page, err)
			}
			return deleted
		}
		if len(tasks) == 0 {
			return deleted
		}
		for _, t := range tasks {
			if !matchesKnowledge(t.Type, t.Payload, knowledgeID) {
				continue
			}
			if err := a.inspector.DeleteTask(queue, t.ID); err != nil {
				logger.Warnf(ctx, "[TaskInspector] delete pending type=%s id=%s: %v", t.Type, t.ID, err)
				continue
			}
			deleted++
		}
		if len(tasks) < listPageSize {
			return deleted
		}
		page++
	}
}

func (a *asynqTaskInspector) deleteScheduledMatches(ctx context.Context, queue, knowledgeID string) int {
	deleted := 0
	page := 1
	for {
		tasks, err := a.inspector.ListScheduledTasks(queue, asynq.PageSize(listPageSize), asynq.Page(page))
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] list scheduled queue=%s page=%d: %v", queue, page, err)
			}
			return deleted
		}
		if len(tasks) == 0 {
			return deleted
		}
		for _, t := range tasks {
			if !matchesKnowledge(t.Type, t.Payload, knowledgeID) {
				continue
			}
			if err := a.inspector.DeleteTask(queue, t.ID); err != nil {
				logger.Warnf(ctx, "[TaskInspector] delete scheduled type=%s id=%s: %v", t.Type, t.ID, err)
				continue
			}
			deleted++
		}
		if len(tasks) < listPageSize {
			return deleted
		}
		page++
	}
}

func (a *asynqTaskInspector) deleteRetryMatches(ctx context.Context, queue, knowledgeID string) int {
	deleted := 0
	page := 1
	for {
		tasks, err := a.inspector.ListRetryTasks(queue, asynq.PageSize(listPageSize), asynq.Page(page))
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] list retry queue=%s page=%d: %v", queue, page, err)
			}
			return deleted
		}
		if len(tasks) == 0 {
			return deleted
		}
		for _, t := range tasks {
			if !matchesKnowledge(t.Type, t.Payload, knowledgeID) {
				continue
			}
			if err := a.inspector.DeleteTask(queue, t.ID); err != nil {
				logger.Warnf(ctx, "[TaskInspector] delete retry type=%s id=%s: %v", t.Type, t.ID, err)
				continue
			}
			deleted++
		}
		if len(tasks) < listPageSize {
			return deleted
		}
		page++
	}
}

// cancelActiveMatches signals active workers to abort via
// Inspector.CancelProcessing. The worker's ctx becomes Done() so the
// next blocking call (or our checkpoint reads) bails. The DB-level
// abort flag (parse_status=cancelled) remains the durable signal —
// this is a latency optimization, not the correctness mechanism.
func (a *asynqTaskInspector) cancelActiveMatches(ctx context.Context, queue, knowledgeID string) int {
	cancelled := 0
	page := 1
	for {
		tasks, err := a.inspector.ListActiveTasks(queue, asynq.PageSize(listPageSize), asynq.Page(page))
		if err != nil {
			if !errors.Is(err, asynq.ErrQueueNotFound) {
				logger.Warnf(ctx, "[TaskInspector] list active queue=%s page=%d: %v", queue, page, err)
			}
			return cancelled
		}
		if len(tasks) == 0 {
			return cancelled
		}
		for _, t := range tasks {
			if !matchesKnowledge(t.Type, t.Payload, knowledgeID) {
				continue
			}
			if err := a.inspector.CancelProcessing(t.ID); err != nil {
				logger.Warnf(ctx, "[TaskInspector] cancel active type=%s id=%s: %v", t.Type, t.ID, err)
				continue
			}
			cancelled++
		}
		if len(tasks) < listPageSize {
			return cancelled
		}
		page++
	}
}

// noopTaskInspector is the Lite-mode (no Redis) inspector. Inline
// goroutines spawned by SyncTaskExecutor cannot be dequeued before
// they start; the checkpoint-based abort in worker code is the only
// stop signal in that mode.
type noopTaskInspector struct{}

// NewNoopTaskInspector returns a no-op TaskInspector for Lite mode.
func NewNoopTaskInspector() interfaces.TaskInspector { return noopTaskInspector{} }

func (noopTaskInspector) CancelTasksForKnowledge(
	ctx context.Context, knowledgeID string,
) (int, int, error) {
	return 0, 0, nil
}

// HasQueuedTasksForKnowledge always reports false in Lite mode: inline
// executors never enqueue, so there is no backlog to protect against and
// the housekeeping sweep's span/updated_at checks stay authoritative.
func (noopTaskInspector) HasQueuedTasksForKnowledge(
	ctx context.Context, knowledgeID string,
) (bool, error) {
	return false, nil
}

// QueueStats reports "not supported" in Lite mode: there is no Redis /
// asynq backend to inspect, so the runtime dashboard renders an
// "unavailable in this deployment" state instead of an empty table.
func (noopTaskInspector) QueueStats(
	ctx context.Context,
) ([]types.QueueStat, bool, error) {
	return nil, false, nil
}

func (noopTaskInspector) WorkerServerStats(
	ctx context.Context,
) ([]types.WorkerServerStat, bool, error) {
	return nil, false, nil
}
