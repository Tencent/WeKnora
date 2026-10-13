package hitl

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// reapBatch bounds how many prompts one sweep handles.
const reapBatch = 100

// SuspendedHook finishes a run whose owner disappeared and whose prompt was
// suspended on its behalf: the assistant message must show the saved steps
// and wait for the answer like a run that suspended itself.
type SuspendedHook func(ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension) error

// ReapStaleLeases takes over pending prompts whose owner stopped renewing its
// lease. A suspendable prompt with saved run state is suspended and handed to
// onSuspended; any other is closed as expired. It returns how many prompts it
// took over.
func (b *Broker) ReapStaleLeases(ctx context.Context, now time.Time, onSuspended SuspendedHook) (int, error) {
	rows, err := b.store.ListStaleLeases(ctx, now, reapBatch)
	if err != nil {
		return 0, err
	}
	reaped := 0
	for _, row := range rows {
		if b.reap(ctx, now, row, onSuspended) {
			reaped++
		}
	}
	return reaped, nil
}

func (b *Broker) reap(ctx context.Context, now time.Time, row *types.AgentPrompt, onSuspended SuspendedHook) bool {
	log := logger.GetLogger(ctx)
	var state *types.AgentRunSuspension
	if row.Suspendable && row.AssistantMessageID != "" {
		saved, err := b.suspensions.Get(ctx, row.AssistantMessageID)
		if err != nil {
			log.Warnf("read run state of prompt %s: %v", row.ID, err)
			return false
		}
		if saved != nil && saved.PromptID == row.ID {
			state = saved
		}
	}

	if state == nil {
		reason := "the replica waiting on this prompt stopped"
		ok, err := b.store.Transition(ctx, row.ID, types.AgentPromptTransition{
			From:        []string{types.AgentPromptPending},
			To:          types.AgentPromptExpired,
			CloseReason: &reason,
		})
		if err != nil {
			log.Warnf("expire orphaned agent prompt %s: %v", row.ID, err)
		}
		return ok
	}

	expiresAt := b.expiresAt(now)
	ok, err := b.store.Transition(ctx, row.ID, types.AgentPromptTransition{
		From:      []string{types.AgentPromptPending},
		To:        types.AgentPromptSuspended,
		ExpiresAt: expiresAt,
	})
	if err != nil {
		log.Warnf("suspend orphaned agent prompt %s: %v", row.ID, err)
	}
	if !ok {
		return false
	}
	row.Status = types.AgentPromptSuspended
	row.ExpiresAt = expiresAt
	if onSuspended != nil {
		if err := onSuspended(ctx, row, state); err != nil {
			log.Warnf("finish suspended run of message %s: %v", row.AssistantMessageID, err)
		}
	}
	return true
}
