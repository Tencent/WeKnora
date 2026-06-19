package service

import (
	"context"
	"os"
	"strconv"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	defaultTaskLedgerStaleDispatchAfter = 5 * time.Minute
	defaultTaskLedgerSweepInterval      = time.Minute
)

type TaskLedgerMaintenanceRunner struct {
	repo          interfaces.TaskJobRepository
	staleAfter    time.Duration
	sweepInterval time.Duration
}

func NewTaskLedgerMaintenanceRunner(repo interfaces.TaskJobRepository) *TaskLedgerMaintenanceRunner {
	return &TaskLedgerMaintenanceRunner{
		repo:          repo,
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
		logger.Warnf(ctx, "task ledger: stale-dispatch scan failed: %v", err)
		return
	}
	for _, exec := range rows {
		if exec == nil {
			continue
		}
		if changed, err := r.repo.MarkStaleDispatchFailed(ctx, exec.ExecutionID, time.Now()); err != nil {
			logger.Warnf(ctx, "task ledger: stale-dispatch mark failed exec=%s: %v", exec.ExecutionID, err)
		} else if changed {
			logger.Warnf(ctx, "task ledger: marked stale dispatch failed exec=%s job=%s", exec.ExecutionID, exec.JobID)
		}
	}
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
