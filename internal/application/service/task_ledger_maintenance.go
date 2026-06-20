package service

import (
	"context"
	"encoding/json"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/observability"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

const (
	defaultTaskLedgerStaleDispatchAfter = 5 * time.Minute
	defaultTaskLedgerSweepInterval      = time.Minute
)

type TaskLedgerMaintenanceRunner struct {
	repo          interfaces.TaskJobRepository
	inspector     interfaces.TaskInspector
	enqueuer      interfaces.TaskEnqueuer
	staleAfter    time.Duration
	sweepInterval time.Duration
}

func NewTaskLedgerMaintenanceRunner(
	repo interfaces.TaskJobRepository,
	inspector interfaces.TaskInspector,
	enqueuer interfaces.TaskEnqueuer,
) *TaskLedgerMaintenanceRunner {
	return &TaskLedgerMaintenanceRunner{
		repo:          repo,
		inspector:     inspector,
		enqueuer:      enqueuer,
		staleAfter:    defaultTaskLedgerStaleDispatchAfter,
		sweepInterval: taskLedgerSweepIntervalFromEnv(),
	}
}

func (r *TaskLedgerMaintenanceRunner) Start(ctx context.Context) {
	if r == nil || r.repo == nil {
		return
	}
	go func() {
		ticker := time.NewTicker(r.sweepInterval)
		defer ticker.Stop()
		r.sweepStaleDispatch(ctx)
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				r.sweepStaleDispatch(ctx)
			}
		}
	}()
}

func (r *TaskLedgerMaintenanceRunner) sweepStaleDispatch(ctx context.Context) {
	cutoff := time.Now().Add(-r.staleAfter)
	rows, err := r.repo.FindStaleDispatches(ctx, cutoff, 100)
	if err != nil {
		observability.RecordTaskLedgerWriteFailure("maintenance", "stale_dispatch_scan")
		logger.Warnf(ctx, "task ledger: stale-dispatch scan failed: %v", err)
		return
	}
	marked := int64(0)
	for _, exec := range rows {
		if exec == nil {
			continue
		}
		if r.inspector != nil {
			exists, err := r.inspector.HasTask(ctx, exec.ExecutionID)
			if err != nil {
				logger.Warnf(ctx, "task ledger: stale-dispatch queue probe failed exec=%s: %v", exec.ExecutionID, err)
				continue
			}
			if exists {
				if changed, err := r.repo.MarkDispatched(ctx, exec.ExecutionID, time.Now()); err != nil {
					observability.RecordTaskLedgerWriteFailure("maintenance", "stale_dispatch_repair")
					logger.Warnf(ctx, "task ledger: stale-dispatch repair failed exec=%s: %v", exec.ExecutionID, err)
				} else if changed {
					logger.Infof(ctx, "task ledger: repaired dispatched_at for queued exec=%s job=%s", exec.ExecutionID, exec.JobID)
				}
				continue
			}
		}
		if r.enqueuer != nil {
			if repaired, err := r.reenqueueStaleExecution(ctx, exec); err != nil {
				logger.Warnf(ctx, "task ledger: stale-dispatch re-enqueue failed exec=%s job=%s: %v", exec.ExecutionID, exec.JobID, err)
				continue
			} else if repaired {
				logger.Infof(ctx, "task ledger: re-enqueued stale dispatch exec=%s job=%s", exec.ExecutionID, exec.JobID)
				continue
			}
		}
		if changed, err := r.repo.MarkStaleDispatchFailed(ctx, exec.ExecutionID, time.Now()); err != nil {
			observability.RecordTaskLedgerWriteFailure("maintenance", "stale_dispatch_mark")
			logger.Warnf(ctx, "task ledger: stale-dispatch mark failed exec=%s: %v", exec.ExecutionID, err)
		} else if changed {
			marked++
			logger.Warnf(ctx, "task ledger: marked stale dispatch failed exec=%s job=%s", exec.ExecutionID, exec.JobID)
		}
	}
	observability.RecordStaleDispatch(marked)
}

func (r *TaskLedgerMaintenanceRunner) reenqueueStaleExecution(ctx context.Context, exec *types.TaskExecution) (bool, error) {
	if r == nil || r.repo == nil || r.enqueuer == nil || exec == nil || exec.ExecutionID == "" {
		return false, nil
	}
	job, err := r.repo.GetJobByExecutionID(ctx, exec.ExecutionID)
	if err != nil || job == nil {
		return false, err
	}
	payload, queue, ok := replayDocumentTaskFromJob(job, exec.ProcessAttempt)
	if !ok {
		return false, nil
	}
	task := asynq.NewTask(types.TypeDocumentProcess, payload)
	if _, err := r.enqueuer.Enqueue(task,
		asynq.Queue(queue),
		asynq.TaskID(exec.ExecutionID),
		asynq.MaxRetry(3),
	); err != nil {
		return false, err
	}
	_, err = r.repo.MarkDispatched(ctx, exec.ExecutionID, time.Now())
	return true, err
}

func replayDocumentTaskFromJob(job *types.TaskJob, attempt int) ([]byte, string, bool) {
	if job == nil || (job.Kind != types.TaskJobKindUpload && job.Kind != types.TaskJobKindReparse) {
		return nil, "", false
	}
	var spec struct {
		Version   int `json:"version"`
		SourceRef struct {
			Type string `json:"type"`
			ID   string `json:"id"`
		} `json:"source_ref"`
		Scope struct {
			KnowledgeID     string `json:"knowledge_id"`
			KnowledgeBaseID string `json:"knowledge_base_id"`
		} `json:"scope"`
		ProcessConfig struct {
			FileName                 string `json:"file_name,omitempty"`
			FileType                 string `json:"file_type,omitempty"`
			EnableMultimodel         bool   `json:"enable_multimodel,omitempty"`
			EnableQuestionGeneration bool   `json:"enable_question_generation,omitempty"`
			QuestionCount            int    `json:"question_count,omitempty"`
			Language                 string `json:"language,omitempty"`
		} `json:"process_config"`
	}
	if err := json.Unmarshal(job.ReplaySpec, &spec); err != nil || spec.Version != 1 ||
		spec.Scope.KnowledgeID == "" || spec.Scope.KnowledgeBaseID == "" {
		return nil, "", false
	}
	payload := types.DocumentProcessPayload{
		TenantID:                 job.TenantID,
		KnowledgeID:              spec.Scope.KnowledgeID,
		KnowledgeBaseID:          spec.Scope.KnowledgeBaseID,
		FileName:                 spec.ProcessConfig.FileName,
		FileType:                 spec.ProcessConfig.FileType,
		EnableMultimodel:         spec.ProcessConfig.EnableMultimodel,
		EnableQuestionGeneration: spec.ProcessConfig.EnableQuestionGeneration,
		QuestionCount:            spec.ProcessConfig.QuestionCount,
		Language:                 spec.ProcessConfig.Language,
		Attempt:                  attempt,
	}
	switch spec.SourceRef.Type {
	case "object_storage":
		payload.FilePath = spec.SourceRef.ID
	case "file_url":
		payload.FileURL = spec.SourceRef.ID
	case "url":
		payload.URL = spec.SourceRef.ID
	default:
		return nil, "", false
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return nil, "", false
	}
	return raw, types.QueueCritical, true
}

func taskLedgerSweepIntervalFromEnv() time.Duration {
	raw := os.Getenv("WEKNORA_TASK_LEDGER_SWEEP_INTERVAL_SECONDS")
	if raw == "" {
		return defaultTaskLedgerSweepInterval
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return defaultTaskLedgerSweepInterval
	}
	return time.Duration(n) * time.Second
}
