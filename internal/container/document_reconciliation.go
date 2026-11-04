package container

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type documentReconciler interface {
	ReconcileDocuments(context.Context) error
}

type documentDispatchStopper interface {
	StopDocumentDispatches(context.Context) error
}

// startDocumentReconciliation runs only after both document handlers exist.
// Registered last, this cleanup cancels recovery before task workers or DB close.
func startDocumentReconciliation(svc interfaces.KnowledgeService, cleaner interfaces.ResourceCleaner) {
	reconciler, ok := svc.(documentReconciler)
	if !ok {
		return
	}
	stop := runDocumentReconciliation(reconciler.ReconcileDocuments, 15*time.Second)
	cleaner.RegisterWithName("DocumentReconciliation", func() error {
		ctx, cancel := context.WithTimeout(context.Background(), cleanupStepTimeout)
		defer cancel()
		loopErr := stop()
		var dispatchErr error
		if workers, ok := svc.(documentDispatchStopper); ok {
			dispatchErr = workers.StopDocumentDispatches(ctx)
		}
		return errors.Join(loopErr, dispatchErr)
	})
}

func runDocumentReconciliation(reconcile func(context.Context) error, interval time.Duration) func() error {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			// Bound each recovery pass as well as the loop's lifetime. Failed
			// passes are retried on the next tick, not treated as empty queues.
			passCtx, passCancel := context.WithTimeout(ctx, 30*time.Second)
			err := reconcile(passCtx)
			passCancel()
			if err != nil && ctx.Err() == nil {
				logger.Warnf(ctx, "[DocumentReconciliation] recovery failed: %v", err)
			}
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	var once sync.Once
	return func() error {
		once.Do(cancel)
		timer := time.NewTimer(cleanupStepTimeout)
		defer timer.Stop()
		select {
		case <-done:
			return nil
		case <-timer.C:
			return fmt.Errorf("document reconciliation did not stop within %s", cleanupStepTimeout)
		}
	}
}
