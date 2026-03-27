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
}

func NewSyncTaskExecutor() *SyncTaskExecutor {
	return &SyncTaskExecutor{
		handlers: make(map[string]func(context.Context, *asynq.Task) error),
	}
}

// RegisterHandler registers a handler for a given task type pattern.
func (e *SyncTaskExecutor) RegisterHandler(pattern string, handler func(context.Context, *asynq.Task) error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.handlers[pattern] = handler
}

// Enqueue satisfies interfaces.TaskEnqueuer.
// Instead of queuing to Redis, it dispatches the task to a goroutine.
func (e *SyncTaskExecutor) Enqueue(task *asynq.Task, _ ...asynq.Option) (*asynq.TaskInfo, error) {
	e.mu.RLock()
	handler, ok := e.handlers[task.Type()]
	e.mu.RUnlock()

	if !ok {
		return nil, fmt.Errorf("sync task executor: no handler registered for type %q", task.Type())
	}

	taskID := uuid.New().String()
	info := &asynq.TaskInfo{
		ID:    taskID,
		Queue: "sync",
		Type:  task.Type(),
	}

	go func() {
		ctx := context.Background()
		start := time.Now()
		logger.Infof(ctx, "[SyncTask] Executing task type=%s id=%s", task.Type(), taskID)
		if err := handler(ctx, task); err != nil {
			logger.Errorf(ctx, "[SyncTask] Task failed type=%s id=%s elapsed=%v err=%v",
				task.Type(), taskID, time.Since(start), err)
		} else {
			logger.Infof(ctx, "[SyncTask] Task completed type=%s id=%s elapsed=%v",
				task.Type(), taskID, time.Since(start))
		}
	}()

	return info, nil
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
	params.Executor.RegisterHandler(types.TypeDataSourceSync, params.DataSourceService.ProcessSync)
	logger.Infof(context.Background(), "[SyncTask] All task handlers registered (Lite mode, no Redis)")
}
