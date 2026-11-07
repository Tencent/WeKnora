package service

import (
	"context"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// SuspendedRunService keeps the assistant message of a suspended run in step
// with its prompt when the system, not the run itself, moves the prompt on:
// the reaper taking over, or the user moving on without answering.
type SuspendedRunService struct {
	RecoverAnswered func(context.Context, *types.AgentPrompt) (bool, error)
	StopDelegation  func(context.Context, *types.AgentPrompt) error
	broker          *hitl.Broker
	messages        interfaces.MessageRepository
}

// NewSuspendedRunService builds the service.
func NewSuspendedRunService(broker *hitl.Broker, messages interfaces.MessageRepository) *SuspendedRunService {
	s := &SuspendedRunService{broker: broker, messages: messages}
	broker.SetRunHooks(s.Hooks())
	return s
}

// Hooks returns the message updates the broker runs as it takes prompts over.
func (s *SuspendedRunService) Hooks() hitl.RunHooks {
	return hitl.RunHooks{RecoverAnswered: s.RecoverAnswered, Suspended: s.MarkAwaitingInput, Closed: s.Close}
}

// Reap runs one reaper sweep.
func (s *SuspendedRunService) Reap(ctx context.Context, now time.Time) (int, error) {
	return s.broker.Reap(ctx, now, s.Hooks())
}

// SupersedeSession closes the session's suspended runs because the user moved
// on. Call it before the session's history is read or cut.
func (s *SuspendedRunService) SupersedeSession(ctx context.Context, sessionID string) (int, error) {
	return s.broker.SupersedeSession(ctx, sessionID, s.Hooks())
}

// MarkAwaitingInput shows a run suspended on its owner's behalf the way a
// self-suspended run looks: the saved steps, waiting on the prompt.
func (s *SuspendedRunService) MarkAwaitingInput(
	ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension,
) error {
	return s.update(ctx, prompt, state, func(msg *types.Message, snap *agent.RunSnapshot) {
		msg.AgentSteps = agenttools.SanitizeAgentStepsForStorage(snap.DisplaySteps())
		msg.RunState = types.MessageRunStateAwaitingInput
	})
}

// Close completes a run that will never resume. The blocked call and the
// calls after it are recorded as failed with reason.
func (s *SuspendedRunService) Close(
	ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension, reason string,
) error {
	if s.StopDelegation != nil {
		if err := s.StopDelegation(ctx, prompt); err != nil {
			return err
		}
	}
	return s.update(ctx, prompt, state, func(msg *types.Message, snap *agent.RunSnapshot) {
		msg.AgentSteps = agenttools.SanitizeAgentStepsForStorage(snap.ClosedSteps(reason))
		msg.KnowledgeReferences = mergeSnapshotReferences(msg.KnowledgeReferences, snap.KnowledgeRefs)
		msg.RunState = ""
		msg.IsCompleted = true
	})
}

func (s *SuspendedRunService) update(
	ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension,
	apply func(*types.Message, *agent.RunSnapshot),
) error {
	snap, err := agent.DecodeRunSnapshot(state.Snapshot)
	if err != nil {
		return err
	}
	msg, err := s.messages.GetMessage(ctx, prompt.SessionID, prompt.AssistantMessageID)
	if err != nil {
		return err
	}
	if msg == nil || msg.IsCompleted {
		return nil
	}
	apply(msg, snap)
	msg.UpdatedAt = time.Now()
	return s.messages.UpdateMessage(ctx, msg)
}

func mergeSnapshotReferences(existing types.References, refs []*types.SearchResult) types.References {
	if len(existing) > 0 || len(refs) == 0 {
		return existing
	}
	return types.References(refs)
}
