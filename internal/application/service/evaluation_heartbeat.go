package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

const (
	evaluationDefaultHeartbeatInterval    = 30 * time.Second
	evaluationDefaultHeartbeatTimeout     = 5 * time.Second
	evaluationDefaultRunningLeaseDuration = 2 * time.Minute
)

// errEvaluationTaskHeartbeatLeaseExpired reports that consecutive transient
// heartbeat failures outlived the last successfully renewed lease. The worker
// must stop because another instance may already have claimed the task.
var errEvaluationTaskHeartbeatLeaseExpired = errors.New(
	"evaluation task heartbeat could not renew the lease before it expired",
)

type evaluationHeartbeatHandle struct {
	cancel   context.CancelFunc
	done     chan struct{}
	stopOnce sync.Once

	// ctx carries logger values and is detached from the task deadline, but is
	// canceled when the heartbeat stops because ownership was lost. Resource
	// cleanup derives from it so ownership loss interrupts in-flight cleanup.
	ctx context.Context

	mu  sync.Mutex
	err error
}

func (h *evaluationHeartbeatHandle) complete(err error) {
	h.mu.Lock()
	h.err = err
	h.mu.Unlock()
	close(h.done)
}

func (h *evaluationHeartbeatHandle) StopAndWait() error {
	if h == nil {
		return nil
	}
	h.stopOnce.Do(h.cancel)
	<-h.done
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.err
}

func (e *EvaluationService) heartbeatEvery() time.Duration {
	if e != nil && e.heartbeatInterval > 0 {
		return e.heartbeatInterval
	}
	return evaluationDefaultHeartbeatInterval
}

func (e *EvaluationService) heartbeatCallTimeout() time.Duration {
	if e != nil && e.heartbeatTimeout > 0 {
		return e.heartbeatTimeout
	}
	return evaluationDefaultHeartbeatTimeout
}

func (e *EvaluationService) runningLeaseFor() time.Duration {
	if e != nil && e.runningLeaseDuration > 0 {
		return e.runningLeaseDuration
	}
	return evaluationDefaultRunningLeaseDuration
}

func (e *EvaluationService) evaluationLeaseExpiresAt(now time.Time) time.Time {
	return now.UTC().Add(e.runningLeaseFor())
}

func evaluationHeartbeatOwnershipLost(err error) bool {
	return errors.Is(err, interfaces.ErrEvaluationTaskNotFound) ||
		errors.Is(err, interfaces.ErrEvaluationTaskOwnerConflict) ||
		errors.Is(err, interfaces.ErrEvaluationTaskStateConflict) ||
		errors.Is(err, errEvaluationTaskHeartbeatLeaseExpired)
}

func evaluationTaskWriteAuthorityLost(err error) bool {
	return evaluationHeartbeatOwnershipLost(err) ||
		errors.Is(err, interfaces.ErrEvaluationTaskVersionConflict)
}

func (e *EvaluationService) startEvaluationHeartbeat(
	ctx context.Context,
	runState *evaluationRunState,
	cancelRun context.CancelCauseFunc,
) *evaluationHeartbeatHandle {
	heartbeatCtx, heartbeatCancel := context.WithCancel(logger.CloneContext(ctx))
	handle := &evaluationHeartbeatHandle{
		cancel: heartbeatCancel,
		done:   make(chan struct{}),
		ctx:    heartbeatCtx,
	}
	if e == nil || e.evaluationTaskRepository == nil || runState == nil {
		handle.complete(errors.New("start evaluation heartbeat: service, repository, and run state are required"))
		return handle
	}

	// leaseDeadline is the persisted expiry of the last successful start or
	// renewal. A transient heartbeat failure must not extend this local view:
	// another instance may claim the task as soon as the database lease expires.
	leaseDeadline := runState.leaseExpiresAt
	beat := func() error {
		now := time.Now().UTC()
		callCtx, callCancel := context.WithTimeout(heartbeatCtx, e.heartbeatCallTimeout())
		updated, err := e.evaluationTaskRepository.HeartbeatTask(
			callCtx,
			types.EvaluationTaskHeartbeatCommand{
				TenantID:       runState.tenantID,
				TaskID:         runState.taskID,
				OwnerID:        runState.ownerID,
				Now:            now,
				LeaseExpiresAt: e.evaluationLeaseExpiresAt(now),
			},
		)
		callCancel()
		if err == nil {
			leaseDeadline = e.evaluationLeaseExpiresAt(now)
			if updated != nil && updated.CancelRequestedAt != nil {
				// A cancel request persisted by any replica stops the local
				// worker; the owner still cleans up and publishes Canceled.
				if cancelRun != nil {
					cancelRun(errEvaluationTaskCancelRequested)
				}
				return errEvaluationTaskCancelRequested
			}
			return nil
		}
		if errors.Is(err, interfaces.ErrEvaluationTaskNotFound) ||
			errors.Is(err, interfaces.ErrEvaluationTaskOwnerConflict) ||
			errors.Is(err, interfaces.ErrEvaluationTaskStateConflict) {
			ownershipErr := fmt.Errorf("evaluation task heartbeat lost ownership: %w", err)
			if cancelRun != nil {
				cancelRun(ownershipErr)
			}
			return ownershipErr
		}
		if !time.Now().UTC().Before(leaseDeadline) {
			leaseErr := fmt.Errorf(
				"evaluation task heartbeat stopped: %w: %w",
				errEvaluationTaskHeartbeatLeaseExpired,
				err,
			)
			if cancelRun != nil {
				cancelRun(leaseErr)
			}
			return leaseErr
		}
		logger.Warnf(
			heartbeatCtx,
			"Evaluation task heartbeat failed and will retry: task ID: %s, error: %v",
			runState.taskID,
			err,
		)
		return nil
	}

	completeBeat := func(err error) {
		heartbeatCancel()
		// Observing a persistent cancel request is a normal stop, not a
		// heartbeat failure: StopAndWait reports nil so the run publishes
		// Canceled instead of a heartbeat error.
		if errors.Is(err, errEvaluationTaskCancelRequested) {
			handle.complete(nil)
			return
		}
		handle.complete(err)
	}

	if err := beat(); err != nil {
		completeBeat(err)
		return handle
	}

	go func() {
		ticker := time.NewTicker(e.heartbeatEvery())
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				if err := beat(); err != nil {
					completeBeat(err)
					return
				}
			case <-heartbeatCtx.Done():
				handle.complete(nil)
				return
			}
		}
	}()
	return handle
}
