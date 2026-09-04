package container

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/Tencent/WeKnora/internal/workspaceevent"
	"github.com/hibiken/asynq"
)

func wireWebhookDispatcher(sink *workspaceevent.Sink, dispatcher interfaces.WebhookDispatcher) {
	if sink != nil {
		sink.SetDispatcher(dispatcher)
	}
}

func startWebhookSweepRedis(enqueuer interfaces.TaskEnqueuer, cleaner interfaces.ResourceCleaner) {
	startWebhookTicker(cleaner, "WebhookOutboxSweep", 10*time.Second, func() {
		slot := time.Now().Unix() / 10
		task := asynq.NewTask(types.TypeWebhookOutboxSweep, nil)
		_, err := enqueuer.Enqueue(task,
			asynq.Queue(types.QueueWebhook),
			asynq.TaskID(fmt.Sprintf("wh-sweep:%d", slot)),
			asynq.MaxRetry(0),
		)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			logger.Warnf(context.Background(), "[Webhook] enqueue sweep: %v", err)
		}
	})
	startWebhookTicker(cleaner, "WebhookDeliveryPrune", time.Hour, func() {
		hour := time.Now().Unix() / 3600
		task := asynq.NewTask(types.TypeWebhookDeliveryPrune, nil)
		_, err := enqueuer.Enqueue(task,
			asynq.Queue(types.QueueWebhook),
			asynq.TaskID(fmt.Sprintf("wh-prune:%d", hour)),
			asynq.MaxRetry(0),
		)
		if err != nil && !errors.Is(err, asynq.ErrTaskIDConflict) && !errors.Is(err, asynq.ErrDuplicateTask) {
			logger.Warnf(context.Background(), "[Webhook] enqueue prune: %v", err)
		}
	})
}

func startWebhookSweepLite(dispatcher interfaces.WebhookDispatcher, cleaner interfaces.ResourceCleaner) {
	startWebhookTicker(cleaner, "WebhookOutboxSweepLite", 10*time.Second, func() {
		if err := dispatcher.SweepPending(context.Background()); err != nil {
			logger.Warnf(context.Background(), "[Webhook] lite sweep: %v", err)
		}
	})
	startWebhookTicker(cleaner, "WebhookDeliveryPruneLite", time.Hour, func() {
		if err := dispatcher.Prune(context.Background()); err != nil {
			logger.Warnf(context.Background(), "[Webhook] lite prune: %v", err)
		}
	})
}

func startWebhookTicker(cleaner interfaces.ResourceCleaner, name string, interval time.Duration, fn func()) {
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		fn()
		for {
			select {
			case <-ticker.C:
				fn()
			case <-stop:
				return
			}
		}
	}()
	cleaner.RegisterWithName(name, func() error {
		close(stop)
		return nil
	})
}
