package session

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
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

// ResumeSuspendedRun continues the run suspended on prompt, which c has just
// answered, on this replica. It returns at once; the run streams to the
// message's event stream, where clients attach as they do to any live run.
func (h *Handler) ResumeSuspendedRun(c *gin.Context, prompt *types.AgentPrompt) {
	if h.promptBroker == nil || h.suspendedRuns == nil {
		return
	}
	ctx := logger.CloneContext(context.WithoutCancel(c.Request.Context()))
	stopLease := h.promptBroker.KeepLease(ctx, prompt.ID)
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

	requestID := uuid.New().String()
	return &qaRequestContext{
		ctx:                 ctx,
		c:                   c,
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
		skipSSE:             true,
		resume:              &types.AgentResume{Snapshot: state.Snapshot, Prompt: prompt},
	}, state, nil
}

// sharedSourceTenant is the workspace to resolve a shared agent from, or 0
// for the session's own agents.
func sharedSourceTenant(session *types.Session, req types.ResumableAgentRequest) uint64 {
	if req.AgentTenantID == 0 || req.AgentTenantID == session.TenantID {
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
	return nil
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
