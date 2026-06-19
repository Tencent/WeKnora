package router

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"go.uber.org/dig"
)

// SyncTaskExecutor executes tasks synchronously (in a goroutine) without Redis.
// Used in Lite mode as a drop-in replacement for *asynq.Client.
type SyncTaskExecutor struct {
	mu       sync.RWMutex
	handlers map[string]func(context.Context, *asynq.Task) error
	ledger   interfaces.TaskJobRepository
}

func NewSyncTaskExecutor() *SyncTaskExecutor {
	return &SyncTaskExecutor{
		handlers: make(map[string]func(context.Context, *asynq.Task) error),
	}
}

func (e *SyncTaskExecutor) SetLedger(repo interfaces.TaskJobRepository) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.ledger = repo
}

// RegisterHandler registers a handler for a given task type pattern.
func (e *SyncTaskExecutor) RegisterHandler(pattern string, handler func(context.Context, *asynq.Task) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[pattern] = handler
}

// Enqueue satisfies interfaces.TaskEnqueuer.
// Instead of queuing to Redis, it dispatches the task to a goroutine.
// Supports ProcessIn (delay) and MaxRetry options for parity with asynq.
func (e *SyncTaskExecutor) Enqueue(task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.RLock()
	handler, ok := e.handlers[task.Type()]
	ledger := e.ledger
	e.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sync task executor: no handler registered for type %q", task.Type())
	}

	var delay time.Duration
	maxRetry := 25 // asynq default
	maxRetrySet := false
	queueName := "sync"
	taskID := ""
	for _, opt := range opts {
		switch opt.Type() {
		case asynq.ProcessInOpt:
			if d, ok := opt.Value().(time.Duration); ok {
				delay = d
			}
		case asynq.MaxRetryOpt:
			if n, ok := opt.Value().(int); ok {
				maxRetry = n
				maxRetrySet = true
			}
		case asynq.QueueOpt:
			if q, ok := opt.Value().(string); ok && q != "" {
				queueName = q
			}
		case asynq.TaskIDOpt:
			if id, ok := opt.Value().(string); ok {
				taskID = id
			}
		}
	}
	// Callers that explicitly pass MaxRetry(0) want no retries.
	// Without the flag we can't distinguish "not set" from "set to 0".
	if maxRetrySet && maxRetry < 0 {
		maxRetry = 0
	}

	if taskID == "" {
		taskID = uuid.New().String()
	}
	info := &asynq.TaskInfo{
		ID:    taskID,
		Queue: queueName,
		Type:  task.Type(),
	}

	go func() {
		if delay > 0 {
			time.Sleep(delay)
		}

		ctx := context.Background()
		start := time.Now()
		logger.Infof(ctx, "[SyncTask] Executing task type=%s id=%s", task.Type(), taskID)

		var lastErr error
		for attempt := 0; attempt <= maxRetry; attempt++ {
			if ledger != nil {
				_, _ = ledger.MarkExecActiveIfExists(ctx, taskID, attempt, time.Now())
			}
			if attempt > 0 {
				backoff := time.Duration(attempt) * 5 * time.Second
				if backoff > 30*time.Second {
					backoff = 30 * time.Second
				}
				logger.Infof(ctx, "[SyncTask] Retrying task type=%s id=%s attempt=%d/%d backoff=%s",
					task.Type(), taskID, attempt, maxRetry, backoff)
				time.Sleep(backoff)
			}

			lastErr = handler(ctx, task)
			if lastErr == nil {
				if ledger != nil {
					_, _ = ledger.MarkExecSucceededIfNonTerminal(context.Background(), taskID, time.Now())
				}
				logger.Infof(ctx, "[SyncTask] Task completed type=%s id=%s elapsed=%v",
					task.Type(), taskID, time.Since(start))
				return
			}
			if ledger != nil && attempt < maxRetry {
				_, _ = ledger.MarkExecRetryingIfNonTerminal(context.Background(), taskID, attempt+1, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(lastErr),
					LastError:  truncateSyncTaskError(lastErr),
				})
			}
		}

		if ledger != nil {
			if types.ClassifyTaskError(lastErr) == types.TaskErrorClassCanceled {
				_, _ = ledger.MarkExecCanceledIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.TaskErrorClassCanceled,
					LastError:  truncateSyncTaskError(lastErr),
				}, time.Now())
			} else {
				_, _ = ledger.MarkExecFailedIfNonTerminal(context.Background(), taskID, interfaces.TaskLedgerFailure{
					ErrorClass: types.ClassifyTaskError(lastErr),
					LastError:  truncateSyncTaskError(lastErr),
				}, time.Now())
			}
		}
		logger.Errorf(ctx, "[SyncTask] Task failed (exhausted retries) type=%s id=%s elapsed=%v err=%v",
			task.Type(), taskID, time.Since(start), lastErr)
	}()

	return info, nil
}

func truncateSyncTaskError(err error) string {
	if err == nil {
		return ""
	}
	msg := err.Error()
	if len(msg) > 8192 {
		return msg[:8192]
	}
	return msg
}

type SyncTaskParams struct {
	dig.In

	Executor             *SyncTaskExecutor
	KnowledgeService     interfaces.KnowledgeService
	KnowledgeBaseService interfaces.KnowledgeBaseService
	TagService           interfaces.KnowledgeTagService
	DataSourceService    interfaces.DataSourceService
	ChunkExtractor       interfaces.TaskHandler `name:"chunkExtractor"`
	DataTableSummary     interfaces.TaskHandler `name:"dataTableSummary"`
	ImageMultimodal      interfaces.TaskHandler `name:"imageMultimodal"`
	KnowledgePostProcess interfaces.TaskHandler `name:"knowledgePostProcess"`
	WikiIngest           interfaces.TaskHandler `name:"wikiIngest"`
}

// RegisterSyncHandlers registers all task handlers on the SyncTaskExecutor.
// Used in Lite mode instead of RunAsynqServer.
func RegisterSyncHandlers(params SyncTaskParams) {
	params.Executor.RegisterHandler(types.TypeChunkExtract, params.ChunkExtractor.Handle)
	params.Executor.RegisterHandler(types.TypeDataTableSummary, params.DataTableSummary.Handle)
	params.Executor.RegisterHandler(types.TypeDocumentProcess, params.KnowledgeService.ProcessDocument)
	params.Executor.RegisterHandler(types.TypeManualProcess, params.KnowledgeService.ProcessManualUpdate)
	params.Executor.RegisterHandler(types.TypeFAQImport, params.KnowledgeService.ProcessFAQImport)
	params.Executor.RegisterHandler(types.TypeQuestionGeneration, params.KnowledgeService.ProcessQuestionGeneration)
	params.Executor.RegisterHandler(types.TypeSummaryGeneration, params.KnowledgeService.ProcessSummaryGeneration)
	params.Executor.RegisterHandler(types.TypeKBClone, params.KnowledgeService.ProcessKBClone)
	params.Executor.RegisterHandler(types.TypeKnowledgeMove, params.KnowledgeService.ProcessKnowledgeMove)
	params.Executor.RegisterHandler(types.TypeKnowledgeListDelete, params.KnowledgeService.ProcessKnowledgeListDelete)
	params.Executor.RegisterHandler(types.TypeIndexDelete, params.TagService.ProcessIndexDelete)
	params.Executor.RegisterHandler(types.TypeKBDelete, params.KnowledgeBaseService.ProcessKBDelete)
	params.Executor.RegisterHandler(types.TypeImageMultimodal, params.ImageMultimodal.Handle)
	params.Executor.RegisterHandler(types.TypeKnowledgePostProcess, params.KnowledgePostProcess.Handle)
	params.Executor.RegisterHandler(types.TypeDataSourceSync, params.DataSourceService.ProcessSync)
	params.Executor.RegisterHandler(types.TypeWikiIngest, params.WikiIngest.Handle)
	logger.Infof(context.Background(), "[SyncTask] All task handlers registered (Lite mode, no Redis)")
}
