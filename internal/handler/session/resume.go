package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// SetSuspendedRuns wires what resuming and superseding suspended runs need.
// Without it answers to suspended prompts are recorded but never resumed, and
// new messages leave suspended prompts to expire.
func (h *Handler) SetSuspendedRuns(broker *hitl.Broker, runs *service.SuspendedRunService) {
	h.promptBroker, h.suspendedRuns = broker, runs
}

func (h *Handler) supersedeSuspendedRuns(ctx context.Context, sessionID string) {
	if h.suspendedRuns == nil {
		return
	}
	if _, err := h.suspendedRuns.SupersedeSession(context.WithoutCancel(ctx), sessionID); err != nil {
		logger.Warnf(ctx, "supersede suspended runs of session %s: %v", sessionID, err)
	}
}

// How long resuming waits for the run that suspended to finish tearing down,
// and how often it looks. A run suspends its prompt before it has persisted
// the message and released the session, so an answer can arrive first.
var (
	resumeSettleTimeout  = 15 * time.Second
	resumeSettleInterval = 250 * time.Millisecond
)

// ResumeSuspendedRun continues the run suspended on prompt, which c has just
// answered, on this replica. It returns once the run has started; the run
// streams to the message's event stream, where clients attach as they do to
// any live run. ownerGone is hitl.Resolution.OwnerGone.
func (h *Handler) ResumeSuspendedRun(
	c *gin.Context, prompt *types.AgentPrompt, ownerGone bool, ownerLeaseUntil *time.Time,
) {
	if h.HandlesDelegatedPrompt(c.Request.Context(), prompt) {
		return
	}
	if h.promptBroker == nil || h.suspendedRuns == nil {
		return
	}
	ctx := logger.CloneContext(context.WithoutCancel(c.Request.Context()))
	stopLease := h.promptBroker.KeepLease(ctx, prompt.ID, prompt.OwnerInstance)
	if err := h.awaitSuspendedRunSettled(ctx, prompt, ownerGone, ownerLeaseUntil); err != nil {
		stopLease()
		logger.Errorf(ctx, "resume run of prompt %s: %v", prompt.ID, err)
		state, _ := h.promptBroker.Suspensions().Get(ctx, prompt.AssistantMessageID)
		if state != nil && state.PromptID != prompt.ID {
			state = nil
		}
		h.abandonResume(ctx, prompt, state)
		return
	}
	reqCtx, state, err := h.buildResumeRequest(ctx, c, prompt)
	if err != nil {
		stopLease()
		logger.Errorf(ctx, "resume run of prompt %s: %v", prompt.ID, err)
		h.abandonResume(ctx, prompt, state)
		return
	}
	go func() {
		defer stopLease()
		defer func() {
			if r := recover(); r != nil {
				logger.Errorf(ctx, "resumed run of prompt %s panicked: %v", prompt.ID, r)
			}
			h.finishResume(ctx, prompt, state)
		}()
		h.executeQA(reqCtx, qaModeAgent, false)
	}()
}

// awaitSuspendedRunSettled waits until the run that suspended on prompt has
// let go of its message: the message is persisted as awaiting input and the
// session's live-run marker no longer names it. Resuming earlier would race
// that run's teardown, which rewrites the message and clears the marker the
// resumed run sets. A marker left by an owner that is gone is cleared here,
// since nothing else will.
func (h *Handler) awaitSuspendedRunSettled(
	ctx context.Context, prompt *types.AgentPrompt, ownerGone bool, ownerLeaseUntil *time.Time,
) error {
	deadline := time.Now().Add(resumeSettleTimeout)
	if ownerLeaseUntil != nil && ownerLeaseUntil.After(deadline) {
		deadline = *ownerLeaseUntil
	}
	for {
		msg, err := h.messageService.GetMessage(ctx, prompt.SessionID, prompt.AssistantMessageID)
		if err != nil {
			return fmt.Errorf("load assistant message: %w", err)
		}
		if msg == nil || msg.IsCompleted {
			return fmt.Errorf("assistant message is no longer open")
		}
		if msg.RunState != types.MessageRunStateAwaitingInput &&
			(ownerGone || (ownerLeaseUntil != nil && time.Now().After(deadline))) {
			// The waiter may have died after suspending the prompt but before
			// persisting its message. Rebuild it from the durable snapshot.
			if h.promptBroker == nil || h.suspendedRuns == nil {
				return fmt.Errorf("suspended run recovery is unavailable")
			}
			state, err := h.promptBroker.Suspensions().Get(ctx, prompt.AssistantMessageID)
			if err != nil {
				return fmt.Errorf("read suspended run state: %w", err)
			}
			if state == nil || state.PromptID != prompt.ID {
				return fmt.Errorf("no run state saved for prompt %s", prompt.ID)
			}
			if err := h.suspendedRuns.MarkAwaitingInput(ctx, prompt, state); err != nil {
				return fmt.Errorf("restore suspended message: %w", err)
			}
			msg.RunState = types.MessageRunStateAwaitingInput
		}
		if msg.RunState == types.MessageRunStateAwaitingInput {
			liveID, _, err := h.streamManager.GetLiveRun(ctx, prompt.SessionID)
			if err != nil {
				return fmt.Errorf("look up live run: %w", err)
			}
			if liveID != msg.ID {
				return nil
			}
			if ownerGone || time.Now().After(deadline) {
				if err := h.streamManager.ClearLiveRun(ctx, prompt.SessionID, msg.ID); err != nil {
					return fmt.Errorf("clear stale live run: %w", err)
				}
				return nil
			}
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the suspended run did not finish within %s", resumeSettleTimeout)
		}
		time.Sleep(resumeSettleInterval)
	}
}

// buildResumeRequest rebuilds the request of the suspended run from what it
// saved. The returned state is non-nil whenever the run state could be read.
func (h *Handler) buildResumeRequest(
	ctx context.Context, c *gin.Context, prompt *types.AgentPrompt,
) (*qaRequestContext, *types.AgentRunSuspension, error) {
	state, err := h.promptBroker.Suspensions().Get(ctx, prompt.AssistantMessageID)
	if err != nil {
		return nil, nil, fmt.Errorf("read run state: %w", err)
	}
	if state == nil || state.PromptID != prompt.ID {
		return nil, nil, fmt.Errorf("no run state saved for this prompt")
	}
	state.Snapshot, err = h.restoreResumeUserMessages(ctx, prompt.SessionID, state.Snapshot)
	if err != nil {
		return nil, state, err
	}
	var req types.ResumableAgentRequest
	if err := decodeResumableRequest(state.Snapshot, &req); err != nil {
		return nil, state, err
	}
	if req.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, req.Language)
	}

	session, err := h.sessionService.GetSession(ctx, prompt.SessionID)
	if err != nil || session == nil {
		return nil, state, fmt.Errorf("load session: %v", err)
	}
	if session.Kind == types.SessionKindAgentDelegated {
		return nil, state, fmt.Errorf("delegated prompts must resume through the scheduler")
	}
	msg, err := h.messageService.GetMessage(ctx, prompt.SessionID, prompt.AssistantMessageID)
	if err != nil || msg == nil {
		return nil, state, fmt.Errorf("load assistant message: %v", err)
	}
	if msg.IsCompleted || msg.RunState != types.MessageRunStateAwaitingInput {
		return nil, state, fmt.Errorf("assistant message is not awaiting input")
	}
	customAgent, effectiveTenantID, shared := h.resolveAgent(ctx, c, req.AgentID, sharedSourceTenant(session, req))
	if customAgent == nil {
		return nil, state, fmt.Errorf("agent %s is no longer available", req.AgentID)
	}
	if types.RunsInSharedKBWorkspace(customAgent) {
		// The saved tenant is the KB's execution workspace, not an agent
		// share. Resolve the local built-in again and recheck the current
		// caller's KB permission before borrowing that workspace's resources.
		customAgent, effectiveTenantID = h.resolveWikiFixerTenantScope(
			ctx, customAgent, session.TenantID, types.TenantRoleFromContext(ctx), req.KnowledgeBaseIDs,
		)
	}
	if types.IsBuiltinAgentID(req.AgentID) && req.AgentTenantID != 0 &&
		req.AgentTenantID != session.TenantID && effectiveTenantID != req.AgentTenantID {
		return nil, state, fmt.Errorf("shared KB workspace is no longer available for agent %s", req.AgentID)
	}

	// Mark the message as generating again before the run publishes itself
	// as live: an awaiting message is not treated as a live run.
	msg.RunState = ""
	msg.UpdatedAt = time.Now()
	if err := h.messageService.UpdateMessage(ctx, msg); err != nil {
		return nil, state, fmt.Errorf("reopen assistant message: %w", err)
	}
	if err := h.streamManager.ResetEvents(ctx, prompt.SessionID, msg.ID); err != nil {
		logger.Warnf(ctx, "reset events of message %s: %v", msg.ID, err)
	}

	// Steering messages must stay in the original assistant's history turn.
	requestID := msg.RequestID
	if requestID == "" {
		requestID = prompt.RequestID
	}
	return &qaRequestContext{
		ctx:       ctx,
		query:     req.Query,
		userInput: req.Query,
		// The run outlives this request, and gin recycles its context.
		c:                   c.Copy(),
		sessionID:           prompt.SessionID,
		requestID:           requestID,
		receivedAt:          time.Now(),
		session:             session,
		customAgent:         customAgent,
		assistantMessage:    msg,
		knowledgeBaseIDs:    req.KnowledgeBaseIDs,
		knowledgeIDs:        req.KnowledgeIDs,
		tagScopes:           req.TagScopes,
		mcpServiceIDs:       req.MCPServiceIDs,
		skillNames:          req.SkillNames,
		summaryModelID:      req.SummaryModelID,
		reasoningEffort:     req.ReasoningEffort,
		webSearchEnabled:    req.WebSearchEnabled,
		effectiveTenantID:   effectiveTenantID,
		sharedAgentReadOnly: shared && req.SharedAgentReadOnly,
		userMessageID:       req.UserMessageID,
		channel:             msg.Channel,
		questionOrigin:      req.QuestionOrigin,
		pageContext:         req.PageContext,
		assistantAccess:     req.AssistantAccess,
		skipSSE:             true,
		resume:              &types.AgentResume{Snapshot: state.Snapshot, Prompt: prompt},
	}, state, nil
}

// sharedSourceTenant is the workspace to resolve a shared agent from, or 0
// for the session's own agents.
func sharedSourceTenant(session *types.Session, req types.ResumableAgentRequest) uint64 {
	// Built-ins are never shared agents. A different tenant can only be the
	// execution scope of a shared KB, which must be revalidated separately.
	if types.IsBuiltinAgentID(req.AgentID) || req.AgentTenantID == 0 || req.AgentTenantID == session.TenantID {
		return 0
	}
	return req.AgentTenantID
}

// finishResume closes what the resumed run left open: a prompt it never got
// to act on, and the run state it no longer needs. A run that suspended
// again saved state for its new prompt, which is kept.
func (h *Handler) finishResume(ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension) {
	row, err := h.promptBroker.Store().Get(ctx, prompt.ID)
	if err == nil && row != nil && row.Status == types.AgentPromptAnswered {
		// The message now belongs to the prompt the run suspended on again;
		// closing it from the old state would complete it under that prompt.
		if cur, _ := h.promptBroker.Suspensions().Get(ctx, prompt.AssistantMessageID); cur != nil &&
			cur.PromptID != prompt.ID {
			h.promptBroker.Fail(ctx, prompt.ID, hitl.ReasonResumeStopped)
			return
		}
		h.abandonResume(ctx, prompt, state)
		return
	}
	if err := h.promptBroker.Suspensions().Delete(ctx, prompt.AssistantMessageID, prompt.ID); err != nil {
		logger.Warnf(ctx, "drop run state of message %s: %v", prompt.AssistantMessageID, err)
	}
}

// abandonResume fails a prompt whose run could not be resumed and completes
// its message, so it neither waits for the reaper nor stays open.
func (h *Handler) abandonResume(ctx context.Context, prompt *types.AgentPrompt, state *types.AgentRunSuspension) {
	h.promptBroker.Fail(ctx, prompt.ID, hitl.ReasonResumeStopped)
	if state != nil {
		if err := h.suspendedRuns.Close(ctx, prompt, state, hitl.ReasonResumeStopped); err != nil {
			logger.Warnf(ctx, "close run of message %s: %v", prompt.AssistantMessageID, err)
		}
	}
	if err := h.promptBroker.Suspensions().Delete(ctx, prompt.AssistantMessageID, prompt.ID); err != nil {
		logger.Warnf(ctx, "drop run state of message %s: %v", prompt.AssistantMessageID, err)
	}
}

func decodeResumableRequest(snapshot []byte, req *types.ResumableAgentRequest) error {
	snap, err := agent.DecodeRunSnapshot(snapshot)
	if err != nil {
		return err
	}
	if len(snap.Request) == 0 {
		return fmt.Errorf("run state carries no request")
	}
	if err := json.Unmarshal(snap.Request, req); err != nil {
		return fmt.Errorf("decode saved request: %w", err)
	}
	if req.Query == "" {
		// Snapshots written before Query was included in the request.
		req.Query = snap.Query
	}
	return nil
}

// Older snapshots retained steering boundaries but not their text. Resolve
// those IDs from the same session before replaying, including the pending round.
func (h *Handler) restoreResumeUserMessages(
	ctx context.Context, sessionID string, snapshot []byte,
) (types.JSON, error) {
	snap, err := agent.DecodeRunSnapshot(snapshot)
	if err != nil {
		return nil, err
	}
	if snap.UserMessages == nil {
		snap.UserMessages = make(map[string]string)
	}
	steps := append(append([]types.AgentStep(nil), snap.CompletedSteps...), snap.PendingRound.Step)
	for _, step := range steps {
		for _, id := range step.UserMessagesBefore {
			if _, ok := snap.UserMessages[id]; ok {
				continue
			}
			msg, err := h.messageService.GetMessage(ctx, sessionID, id)
			if err != nil {
				return nil, fmt.Errorf("load steered message %s: %w", id, err)
			}
			if msg == nil || msg.Role != "user" || msg.SessionID != sessionID {
				return nil, fmt.Errorf("steered message %s is not a user message of this session", id)
			}
			snap.UserMessages[id] = msg.Content
		}
	}
	data, err := json.Marshal(snap)
	return types.JSON(data), err
}

func (h *Handler) writeRunResumedEvent(ctx context.Context, sessionID, messageID, promptID string) {
	if err := h.streamManager.AppendEvent(ctx, sessionID, messageID, interfaces.StreamEvent{
		ID:        "resumed-" + promptID,
		Type:      types.ResponseTypeRunResumed,
		Timestamp: time.Now(),
		Data:      map[string]interface{}{"pending_id": promptID},
	}); err != nil {
		logger.Warnf(ctx, "append run_resumed of message %s: %v", messageID, err)
	}
}

// SetDelegatedPromptRouter installs the delegated prompt ownership router.
func (h *Handler) SetDelegatedPromptRouter(f func(context.Context, *types.AgentPrompt) bool) {
	h.delegatedPrompt = f
}

// HandlesDelegatedPrompt reports whether the collaboration runtime owns this prompt.
func (h *Handler) HandlesDelegatedPrompt(ctx context.Context, p *types.AgentPrompt) bool {
	return h.delegatedPrompt != nil && h.delegatedPrompt(ctx, p)
}

// CollaborationEvents publishes a child or resumed parent execution to its own stream.
func (h *Handler) CollaborationEvents(ctx context.Context,
	s *types.Session,
	m *types.Message,
	bus *event.EventBus,
) func(context.Context) error {
	stream := h.setupStreamHandler(ctx, s.ID, m.ID, m.RequestID, s.TenantID, time.Now(), m, bus)
	return stream.publishCompletion
}

// CollaborationSteer builds a steer sink for a collaboration execution.
func (h *Handler) CollaborationSteer(ctx context.Context, s *types.Session, m *types.Message) types.SteerSink {
	return newSteerSink(ctx, s.ID, m.RequestID, m, h.messageService, h.streamManager)
}
