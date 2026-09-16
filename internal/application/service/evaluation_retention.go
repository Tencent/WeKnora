package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	evaluationDefaultRetentionStartDelay = 10 * time.Minute
	evaluationDefaultRetentionInterval   = 24 * time.Hour
	evaluationDefaultRetentionBatchSize  = 500
	evaluationRetentionRoundTimeout      = 30 * time.Second
)

// EvaluationTaskRetentionRunner physically removes terminal evaluation tasks
// older than the configured retention window. Pending and Running tasks are
// never eligible. Each round deletes bounded batches until the backlog is
// drained or the round budget is exhausted.
type EvaluationTaskRetentionRunner struct {
	evaluationTaskRepository interfaces.EvaluationTaskRepository
	retentionDays            int

	startDelay  time.Duration
	interval    time.Duration
	batchSize   int
	roundBudget time.Duration
	now         func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	started   atomic.Bool
	done      chan struct{}

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
}

// NewEvaluationTaskRetentionRunner creates the runner for one resolved
// positive retention window in days. Start activates the schedule.
func NewEvaluationTaskRetentionRunner(
	evaluationTaskRepository interfaces.EvaluationTaskRepository,
	retentionDays int,
) *EvaluationTaskRetentionRunner {
	return &EvaluationTaskRetentionRunner{
		evaluationTaskRepository: evaluationTaskRepository,
		retentionDays:            retentionDays,
		startDelay:               evaluationDefaultRetentionStartDelay,
		interval:                 evaluationDefaultRetentionInterval,
		batchSize:                evaluationDefaultRetentionBatchSize,
		roundBudget:              evaluationRetentionRoundTimeout,
		now:                      time.Now,
		done:                     make(chan struct{}),
	}
}

// Start waits the startup delay and then runs one bounded round per interval.
func (r *EvaluationTaskRetentionRunner) Start(ctx context.Context) {
	if r == nil || r.evaluationTaskRepository == nil || r.retentionDays <= 0 {
		return
	}
	r.startOnce.Do(func() {
		runnerCtx, cancel := context.WithCancel(logger.CloneContext(ctx))
		r.lifecycleMu.Lock()
		r.cancel = cancel
		r.lifecycleMu.Unlock()
		r.started.Store(true)
		go r.loop(runnerCtx)
	})
}

// Stop cancels an in-flight round and waits for the loop to exit.
func (r *EvaluationTaskRetentionRunner) Stop() {
	if r == nil || !r.started.Load() {
		return
	}
	r.stopOnce.Do(func() {
		r.lifecycleMu.Lock()
		cancel := r.cancel
		r.lifecycleMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
	<-r.done
}

func (r *EvaluationTaskRetentionRunner) loop(ctx context.Context) {
	defer close(r.done)
	startDelay := r.startDelay
	if startDelay < 0 {
		startDelay = evaluationDefaultRetentionStartDelay
	}
	interval := r.interval
	if interval <= 0 {
		interval = evaluationDefaultRetentionInterval
	}

	timer := time.NewTimer(startDelay)
	select {
	case <-timer.C:
		if err := r.runOnce(ctx); err != nil && ctx.Err() == nil {
			logger.Warnf(ctx, "Evaluation task retention round failed: %v", err)
		}
	case <-ctx.Done():
		timer.Stop()
		return
	}

	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.runOnce(ctx); err != nil && ctx.Err() == nil {
				logger.Warnf(ctx, "Evaluation task retention round failed: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *EvaluationTaskRetentionRunner) runOnce(ctx context.Context) error {
	if r == nil || r.evaluationTaskRepository == nil {
		return errors.New("evaluation task retention: repository is required")
	}
	if r.retentionDays <= 0 {
		return errors.New("evaluation task retention: retention days must be positive")
	}
	batchSize := r.batchSize
	if batchSize <= 0 {
		batchSize = evaluationDefaultRetentionBatchSize
	}
	roundBudget := r.roundBudget
	if roundBudget <= 0 {
		roundBudget = evaluationRetentionRoundTimeout
	}

	cutoff := r.nowUTC().AddDate(0, 0, -r.retentionDays)
	roundCtx, roundCancel := context.WithTimeout(ctx, roundBudget)
	defer roundCancel()
	for {
		if err := roundCtx.Err(); err != nil {
			return fmt.Errorf("evaluation task retention round exhausted its budget: %w", err)
		}
		deleted, err := r.evaluationTaskRepository.DeleteExpiredTerminalTasks(roundCtx, cutoff, batchSize)
		if err != nil {
			return fmt.Errorf("evaluation task retention round: %w", err)
		}
		if deleted < int64(batchSize) {
			return nil
		}
	}
}

func (r *EvaluationTaskRetentionRunner) nowUTC() time.Time {
	if r != nil && r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}
