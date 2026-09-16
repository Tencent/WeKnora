package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const (
	evaluationDefaultRecoveryInterval    = 30 * time.Second
	evaluationDefaultRecoveryConcurrency = 8
	// evaluationRecoveryTenantTimeout bounds the tenant lookup that prepares
	// the cleanup context for one recovered task.
	evaluationRecoveryTenantTimeout = 10 * time.Second
	// evaluationRecoveryOperationTimeout bounds the claim scan, each resource
	// deletion, and the terminal publication of one recovered task.
	evaluationRecoveryOperationTimeout = 30 * time.Second
	// evaluationRecoveryLeaseSafetyMargin is the headroom that must remain
	// between the summed operation budgets and the two minute recovery lease.
	evaluationRecoveryLeaseSafetyMargin = 20 * time.Second
	evaluationTaskInterruptedMessage    = "evaluation task execution lease expired"
)

// EvaluationTaskRecoveryRunner claims expired evaluation tasks, cleans their
// temporary resources, and publishes a stable Interrupted snapshot.
type EvaluationTaskRecoveryRunner struct {
	evaluationTaskRepository interfaces.EvaluationTaskRepository
	tenantService            interfaces.TenantService
	knowledgeBaseService     interfaces.KnowledgeBaseService
	knowledgeService         interfaces.KnowledgeService
	ownerID                  string

	recoveryInterval      time.Duration
	recoveryLeaseDuration time.Duration
	maxConcurrency        int
	now                   func() time.Time

	startOnce sync.Once
	stopOnce  sync.Once
	started   atomic.Bool
	done      chan struct{}

	lifecycleMu sync.Mutex
	cancel      context.CancelFunc
}

// NewEvaluationTaskRecoveryRunner creates a recovery runner with an
// independent owner identifier. Start activates the scan loop.
func NewEvaluationTaskRecoveryRunner(
	evaluationTaskRepository interfaces.EvaluationTaskRepository,
	tenantService interfaces.TenantService,
	knowledgeBaseService interfaces.KnowledgeBaseService,
	knowledgeService interfaces.KnowledgeService,
) *EvaluationTaskRecoveryRunner {
	return &EvaluationTaskRecoveryRunner{
		evaluationTaskRepository: evaluationTaskRepository,
		tenantService:            tenantService,
		knowledgeBaseService:     knowledgeBaseService,
		knowledgeService:         knowledgeService,
		ownerID:                  uuid.NewString(),
		recoveryInterval:         evaluationDefaultRecoveryInterval,
		recoveryLeaseDuration:    evaluationDefaultRunningLeaseDuration,
		maxConcurrency:           evaluationDefaultRecoveryConcurrency,
		now:                      time.Now,
		done:                     make(chan struct{}),
	}
}

// Start runs an immediate recovery scan and then scans periodically.
func (r *EvaluationTaskRecoveryRunner) Start(ctx context.Context) {
	if r == nil || r.evaluationTaskRepository == nil {
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

// Stop cancels the scan loop and waits for an in-flight batch. It is safe to
// call repeatedly and returns immediately if Start has not run.
func (r *EvaluationTaskRecoveryRunner) Stop() {
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

func (r *EvaluationTaskRecoveryRunner) loop(ctx context.Context) {
	defer close(r.done)
	if err := r.runOnce(ctx); err != nil && ctx.Err() == nil {
		logger.Warnf(ctx, "Evaluation task recovery scan failed: %v", err)
	}

	interval := r.recoveryInterval
	if interval <= 0 {
		interval = evaluationDefaultRecoveryInterval
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			if err := r.runOnce(ctx); err != nil && ctx.Err() == nil {
				logger.Warnf(ctx, "Evaluation task recovery scan failed: %v", err)
			}
		case <-ctx.Done():
			return
		}
	}
}

func (r *EvaluationTaskRecoveryRunner) runOnce(ctx context.Context) error {
	if r == nil || r.evaluationTaskRepository == nil {
		return errors.New("recover evaluation tasks: repository is required")
	}
	now := r.nowUTC()
	limit := r.maxConcurrency
	if limit <= 0 || limit > evaluationDefaultRecoveryConcurrency {
		limit = evaluationDefaultRecoveryConcurrency
	}
	leaseDuration := r.recoveryLeaseDuration
	if leaseDuration <= 0 {
		leaseDuration = evaluationDefaultRunningLeaseDuration
	}
	claimCtx, claimCancel := context.WithTimeout(ctx, evaluationRecoveryOperationTimeout)
	claimed, err := r.evaluationTaskRepository.ClaimExpiredTasks(
		claimCtx,
		types.EvaluationTaskClaimExpiredCommand{
			OwnerID:        r.ownerID,
			Now:            now,
			LeaseExpiresAt: now.Add(leaseDuration),
			Limit:          limit,
		},
	)
	claimCancel()
	if err != nil {
		return fmt.Errorf("claim expired evaluation tasks: %w", err)
	}

	var waitGroup sync.WaitGroup
	var errorsMu sync.Mutex
	recoveryErrors := make([]error, 0)
	for _, task := range claimed {
		task := task
		if task == nil {
			continue
		}
		waitGroup.Add(1)
		go func() {
			defer waitGroup.Done()
			if recoveryErr := r.recoverTask(ctx, task); recoveryErr != nil {
				errorsMu.Lock()
				recoveryErrors = append(recoveryErrors, recoveryErr)
				errorsMu.Unlock()
			}
		}()
	}
	waitGroup.Wait()
	return errors.Join(recoveryErrors...)
}

// validateRecoveryOwnership confirms that this runner still owns the claimed
// task at its claimed version with an unexpired lease. A stale owner must not
// delete resources or publish terminal states for another instance.
func (r *EvaluationTaskRecoveryRunner) validateRecoveryOwnership(
	ctx context.Context,
	task *types.EvaluationTaskEntity,
) (*types.EvaluationTaskEntity, error) {
	current, err := r.evaluationTaskRepository.GetTask(ctx, task.TenantID, task.ID)
	if err != nil {
		return nil, fmt.Errorf("validate recovery ownership for evaluation task %s: %w", task.ID, err)
	}
	if current.OwnerID != r.ownerID {
		return nil, fmt.Errorf(
			"validate recovery ownership for evaluation task %s: %w",
			task.ID,
			interfaces.ErrEvaluationTaskOwnerConflict,
		)
	}
	if current.Version != task.Version {
		return nil, fmt.Errorf(
			"validate recovery ownership for evaluation task %s: %w",
			task.ID,
			interfaces.ErrEvaluationTaskVersionConflict,
		)
	}
	if current.LeaseExpiresAt == nil || !current.LeaseExpiresAt.After(r.nowUTC()) {
		return nil, fmt.Errorf(
			"validate recovery ownership for evaluation task %s: %w",
			task.ID,
			interfaces.ErrEvaluationTaskStateConflict,
		)
	}
	return current, nil
}

func (r *EvaluationTaskRecoveryRunner) recoverTask(
	ctx context.Context,
	task *types.EvaluationTaskEntity,
) error {
	cleanupErrors, err := decodeEvaluationCleanupErrors(task.CleanupErrors)
	if err != nil {
		return fmt.Errorf("recover evaluation task %s: %w", task.ID, err)
	}

	tenantCtx, tenantCancel := context.WithTimeout(ctx, evaluationRecoveryTenantTimeout)
	tenant, tenantErr := r.tenantService.GetTenantByID(tenantCtx, task.TenantID)
	tenantCancel()
	if tenantErr != nil {
		return fmt.Errorf("load tenant for evaluation task %s: %w", task.ID, tenantErr)
	}
	cleanupBase := context.WithValue(ctx, types.TenantIDContextKey, task.TenantID)
	cleanupBase = context.WithValue(cleanupBase, types.TenantInfoContextKey, tenant)

	knowledgeCtx, knowledgeCancel := context.WithTimeout(cleanupBase, evaluationRecoveryOperationTimeout)
	if _, err := r.validateRecoveryOwnership(knowledgeCtx, task); err != nil {
		knowledgeCancel()
		return err
	}
	if task.TemporaryKnowledgeID != "" && r.knowledgeService != nil {
		cleanupErr := r.knowledgeService.DeleteKnowledge(knowledgeCtx, task.TemporaryKnowledgeID)
		if cleanupErr != nil && !errors.Is(cleanupErr, apprepo.ErrKnowledgeNotFound) {
			appendEvaluationCleanupError(
				&cleanupErrors,
				"knowledge",
				task.TemporaryKnowledgeID,
				cleanupErr,
			)
		}
	}
	knowledgeCancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("recover evaluation task %s: runner stopped during cleanup: %w", task.ID, err)
	}

	knowledgeBaseCtx, knowledgeBaseCancel := context.WithTimeout(cleanupBase, evaluationRecoveryOperationTimeout)
	if _, err := r.validateRecoveryOwnership(knowledgeBaseCtx, task); err != nil {
		knowledgeBaseCancel()
		return err
	}

	if task.TemporaryKnowledgeBaseID != "" && r.knowledgeBaseService != nil {
		cleanupErr := r.knowledgeBaseService.DeleteKnowledgeBase(
			knowledgeBaseCtx,
			task.TemporaryKnowledgeBaseID,
		)
		if cleanupErr != nil && !errors.Is(cleanupErr, apprepo.ErrKnowledgeBaseNotFound) {
			appendEvaluationCleanupError(
				&cleanupErrors,
				"knowledge base",
				task.TemporaryKnowledgeBaseID,
				cleanupErr,
			)
		}
	}
	knowledgeBaseCancel()
	if err := ctx.Err(); err != nil {
		return fmt.Errorf("recover evaluation task %s: runner stopped during cleanup: %w", task.ID, err)
	}

	cleanupJSON, err := encodeEvaluationCleanupErrors(cleanupErrors)
	if err != nil {
		return fmt.Errorf("recover evaluation task %s: %w", task.ID, err)
	}
	endTime := r.nowUTC()
	publicationCtx, publicationCancel := context.WithTimeout(
		cleanupBase,
		evaluationRecoveryOperationTimeout,
	)
	defer publicationCancel()
	current, err := r.validateRecoveryOwnership(publicationCtx, task)
	if err != nil {
		return err
	}
	// Select the terminal state from the publication-time database truth so a
	// cancel request persisted during cleanup completes in this recovery pass.
	terminalStatus := types.EvaluationStatueInterrupted
	terminalMessage := evaluationTaskInterruptedMessage
	if current.CancelRequestedAt != nil {
		terminalStatus = types.EvaluationStatueCanceled
		terminalMessage = evaluationTaskCanceledMessage
	}
	runtimeMetricsJSON, err := finalizeRecoveredEvaluationRuntime(
		task.RuntimeMetrics,
		task.StartTime,
		endTime,
		terminalStatus == types.EvaluationStatueCanceled,
	)
	if err != nil {
		return fmt.Errorf("finalize recovered evaluation runtime metrics %s: %w", task.ID, err)
	}
	_, err = r.evaluationTaskRepository.PublishTerminal(
		publicationCtx,
		types.EvaluationTaskTerminalCommand{
			TenantID:        task.TenantID,
			TaskID:          task.ID,
			OwnerID:         r.ownerID,
			ExpectedVersion: task.Version,
			Status:          terminalStatus,
			EndTime:         endTime,
			ErrMsg:          terminalMessage,
			CleanupErrors:   cleanupJSON,
			Metric:          append(types.JSON(nil), task.Metric...),
			RuntimeMetrics:  runtimeMetricsJSON,
		},
	)
	if err != nil {
		return fmt.Errorf("publish interrupted evaluation task %s: %w", task.ID, err)
	}
	return nil
}

func (r *EvaluationTaskRecoveryRunner) nowUTC() time.Time {
	if r != nil && r.now != nil {
		return r.now().UTC()
	}
	return time.Now().UTC()
}
