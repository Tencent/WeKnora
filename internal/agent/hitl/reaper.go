package hitl

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// reapBatch bounds how many prompts one sweep handles per rule.
const reapBatch = 100

// Reasons recorded on prompts the system closes, and shown for the calls
// they blocked.
const (
	ReasonOwnerStopped    = "the replica waiting on this prompt stopped"
	ReasonResumeStopped   = "the run resuming after this prompt was interrupted before the tool ran"
	ReasonExecuteStopped  = "the run was interrupted while the approved tool was running; its outcome is unknown"
	ReasonPromptExpired   = "the prompt expired before it was answered"
	ReasonPromptSupersede = "the user moved on without answering this prompt"
)

// RunHooks finish the assistant message of a run the reaper or a supersede
// takes over.
type RunHooks struct {
	// Suspended shows a run whose owner disappeared while waiting the way a
	// run that suspended itself looks: the saved steps, waiting on prompt.
	Suspended func(ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension) error
	// Closed completes a run that will never resume. reason explains what
	// happened to the blocked call and the calls after it.
	Closed func(ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension, reason string) error
}

// Reap takes over prompts their owner can no longer finish:
//
//  1. pending, lease lapsed: the owner died during the in-memory wait. A
//     suspendable prompt with saved state is suspended, any other expires.
//  2. answered or executing, lease lapsed, saved state present: the replica
//     resuming the run died. The prompt fails and the run is closed; an
//     executing tool is never replayed.
//  3. suspended past its expiry: the prompt expires and the run is closed.
//
// It returns how many prompts it took over.
func (b *Broker) Reap(ctx context.Context, now time.Time, hooks RunHooks) (int, error) {
	reaped := 0

	pending, err := b.store.ListStaleLeases(ctx, []string{types.AgentPromptPending}, now, reapBatch)
	if err != nil {
		return 0, err
	}
	for _, row := range pending {
		if b.reapPending(ctx, now, row, hooks) {
			reaped++
		}
	}

	resuming, err := b.store.ListStaleLeases(ctx,
		[]string{types.AgentPromptAnswered, types.AgentPromptExecuting}, now, reapBatch)
	if err != nil {
		return reaped, err
	}
	for _, row := range resuming {
		state := b.runState(ctx, row)
		if state == nil {
			// An in-memory waiter consumed it; nothing to resume or close.
			continue
		}
		reason := ReasonResumeStopped
		if row.Status == types.AgentPromptExecuting {
			reason = ReasonExecuteStopped
		}
		if b.closeRun(ctx, row, state, []string{row.Status}, types.AgentPromptFailed, reason, hooks, now) {
			reaped++
		}
	}

	expired, err := b.store.ListExpired(ctx, now, reapBatch)
	if err != nil {
		return reaped, err
	}
	for _, row := range expired {
		if b.closeRun(ctx, row, b.runState(ctx, row),
			[]string{types.AgentPromptSuspended}, types.AgentPromptExpired, ReasonPromptExpired, hooks) {
			reaped++
		}
	}
	return reaped, nil
}

// SupersedeSession closes the session's suspended prompts because the user
// moved on (sent a new message, rewound), completing their runs so the
// conversation history stays well formed. It returns how many it closed.
func (b *Broker) SupersedeSession(ctx context.Context, sessionID string, hooks RunHooks) (int, error) {
	rows, err := b.store.ListBySession(ctx, sessionID, []string{types.AgentPromptSuspended})
	if err != nil {
		return 0, err
	}
	n := 0
	for _, row := range rows {
		if b.closeRun(ctx, row, b.runState(ctx, row),
			[]string{types.AgentPromptSuspended}, types.AgentPromptSuperseded, ReasonPromptSupersede, hooks) {
			n++
		}
	}
	return n, nil
}

// runState returns the saved state of row's run, or nil when there is none
// or it belongs to another prompt.
func (b *Broker) runState(ctx context.Context, row *types.AgentPrompt) *types.AgentRunSuspension {
	if !row.Suspendable || row.AssistantMessageID == "" {
		return nil
	}
	saved, err := b.suspensions.Get(ctx, row.AssistantMessageID)
	if err != nil {
		logger.GetLogger(ctx).Warnf("read run state of prompt %s: %v", row.ID, err)
		return nil
	}
	if saved == nil || saved.PromptID != row.ID {
		return nil
	}
	return saved
}

func (b *Broker) reapPending(ctx context.Context, now time.Time, row *types.AgentPrompt, hooks RunHooks) bool {
	log := logger.GetLogger(ctx)
	state := b.runState(ctx, row)
	if state == nil {
		reason := ReasonOwnerStopped
		ok, err := b.store.Transition(ctx, row.ID, types.AgentPromptTransition{
			From:          []string{types.AgentPromptPending},
			To:            types.AgentPromptExpired,
			ExpectedOwner: &row.OwnerInstance,
			LeaseBefore:   &now,
			CloseReason:   &reason,
		})
		if err != nil {
			log.Warnf("expire orphaned agent prompt %s: %v", row.ID, err)
		}
		return ok
	}

	expiresAt := b.expiresAt(now)
	// Clearing the owner records that the run was suspended for it, which
	// Resolve reports as Resolution.OwnerGone.
	noOwner := ""
	ok, err := b.store.Transition(ctx, row.ID, types.AgentPromptTransition{
		From:          []string{types.AgentPromptPending},
		To:            types.AgentPromptSuspended,
		ExpiresAt:     expiresAt,
		Owner:         &noOwner,
		ExpectedOwner: &row.OwnerInstance,
		LeaseBefore:   &now,
	})
	if err != nil {
		log.Warnf("suspend orphaned agent prompt %s: %v", row.ID, err)
	}
	if !ok {
		return false
	}
	row.Status = types.AgentPromptSuspended
	row.ExpiresAt = expiresAt
	row.OwnerInstance = ""
	if hooks.Suspended != nil {
		if err := hooks.Suspended(ctx, row, state); err != nil {
			log.Warnf("finish suspended run of message %s: %v", row.AssistantMessageID, err)
		}
	}
	return true
}

// closeRun moves row from one of from to `to`, then completes its run and
// drops the saved state. state may be nil when the run left none.
func (b *Broker) closeRun(
	ctx context.Context, row *types.AgentPrompt, state *types.AgentRunSuspension,
	from []string, to, reason string, hooks RunHooks, staleBefore ...time.Time,
) bool {
	log := logger.GetLogger(ctx)
	transition := types.AgentPromptTransition{
		From:        from,
		To:          to,
		CloseReason: &reason,
	}
	if len(staleBefore) > 0 {
		transition.ExpectedOwner = &row.OwnerInstance
		transition.LeaseBefore = &staleBefore[0]
	}
	ok, err := b.store.Transition(ctx, row.ID, transition)
	if err != nil {
		log.Warnf("close agent prompt %s as %s: %v", row.ID, to, err)
	}
	if !ok {
		return false
	}
	row.Status, row.CloseReason = to, reason
	if state == nil {
		return true
	}
	if hooks.Closed != nil {
		if err := hooks.Closed(ctx, row, state, reason); err != nil {
			log.Warnf("close run of message %s: %v", row.AssistantMessageID, err)
			return true
		}
	}
	b.dropSnapshot(ctx, row.AssistantMessageID, row.ID)
	return true
}
