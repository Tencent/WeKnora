package container

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

func startLearningRecovery(svc interfaces.LearningService, cleaner interfaces.ResourceCleaner) {
	if svc == nil || cleaner == nil {
		return
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()
		for {
			if ctx.Err() != nil {
				return
			}
			passCtx, passCancel := context.WithTimeout(ctx, 20*time.Second)
			if err := svc.Recover(passCtx); err != nil && ctx.Err() == nil {
				logger.Warn(ctx, "learning recovery pass failed; durable jobs will be retried")
			}
			passCancel()
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
		}
	}()
	cleaner.RegisterWithName("learning-recovery", func() error {
		shutdown, stop := context.WithTimeout(context.Background(), 5*time.Second)
		defer stop()
		cancel()
		select {
		case <-done:
			return nil
		case <-shutdown.Done():
			return shutdown.Err()
		}
	})
}
