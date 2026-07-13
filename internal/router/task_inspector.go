package router

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

// NewAsynqInspector constructs an *asynq.Inspector pointed at the same
// Redis used by the asynq client. Only registered in asynq mode.
func NewAsynqInspector() *asynq.Inspector {
	return asynq.NewInspector(getAsynqRedisClientOpt())
}

// asynqTaskInspector implements interfaces.TaskInspector backed by an
// *asynq.Inspector. Scans the queues we actually use and matches tasks
// whose payload carries the given
// knowledge_id. Best-effort: any scan/delete error is logged and
// swallowed so the cancel API still returns success even when Redis is
// flaky.
type asynqTaskInspector struct {
	inspector *asynq.Inspector
}

// NewAsynqTaskInspector returns a TaskInspector wrapping the given
// *asynq.Inspector. nil-safe: a nil inspector degrades to a no-op so
// the cancel path remains usable when the inspector failed to init.
func NewAsynqTaskInspector(inspector *asynq.Inspector) interfaces.TaskInspector {
	if inspector == nil {
		return noopTaskInspector{}
	}
	return &asynqTaskInspector{inspector: inspector}
}

// knowledgeIDProbe is the minimal payload shape we need to filter
// tasks. All pipeline payload types embed a json:"knowledge_id" field,
// so a single struct covers Document / ImageMultimodal / PostProcess /
// Question / Summary / Extract / Manual.
type knowledgeIDProbe struct {
	KnowledgeID string `json:"knowledge_id,omitempty"`
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
