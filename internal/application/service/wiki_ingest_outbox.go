package service

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/tracing/langfuse"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/hibiken/asynq"
)

func newWikiIngestPendingRow(
	ctx context.Context, tenantID uint64, kbID, knowledgeID string, attempt int,
) (*types.TaskPendingOp, error) {
	lang, _ := types.LanguageFromContext(ctx)
	payload, err := json.Marshal(WikiPendingOp{
		Op: WikiOpIngest, KnowledgeID: knowledgeID, Attempt: attempt,
		OwnsFinalizingSlot: wikiBoolPointer(false), Language: lang,
	})
	if err != nil {
		return nil, err
	}
	return &types.TaskPendingOp{
		TenantID: tenantID,
		TaskType: wikiTaskType,
		Scope:    wikiTaskScope,
		ScopeID:  kbID,
		Op:       WikiOpIngest,
		DedupKey: knowledgeID,
		Payload:  payload,
	}, nil
}

// scheduleWikiIngestTrigger wakes the consumer for an operation that has
// already been durably persisted. The database row is the source of truth;
// if this ephemeral enqueue fails, the periodic/startup recovery sweep will
// recreate the trigger without losing the operation.
func scheduleWikiIngestTrigger(
	ctx context.Context,
	task interfaces.TaskEnqueuer,
	tenantID uint64,
	kbID, language string,
	delay time.Duration,
) error {
	if task == nil {
		return errors.New("wiki task enqueuer is unavailable")
	}
	trigger := WikiIngestPayload{
		TenantID: tenantID, KnowledgeBaseID: kbID, Language: language,
	}
	langfuse.InjectTracing(ctx, &trigger)
	payload, err := json.Marshal(trigger)
	if err != nil {
		return err
	}
	opts := []asynq.Option{
		asynq.Queue(types.QueueWiki),
		asynq.MaxRetry(wikiIngestMaxRetry),
		asynq.Timeout(60 * time.Minute),
	}
	if delay > 0 {
		opts = append(opts, asynq.ProcessIn(delay))
	}
	_, err = task.Enqueue(asynq.NewTask(types.TypeWikiIngest, payload, opts...))
	return err
}
