package container

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
)

// Run after task handlers are registered. The durable false marker recovers
// lost enqueue attempts and Lite-mode tasks after a process restart.
func startDocumentTagRecovery(ctx context.Context, syncer *service.DocumentTagSyncService) {
	go func() {
		ticker := time.NewTicker(10 * time.Minute)
		defer ticker.Stop()
		for {
			sweepCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
			if err := syncer.RecoverPending(sweepCtx); err != nil {
				logger.Warnf(ctx, "Document tag recovery sweep failed: %v", err)
			}
			cancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
}
