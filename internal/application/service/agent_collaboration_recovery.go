package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// BindRecovery connects approval recovery and live stream persistence.
func (s *AgentCollaboration) BindRecovery(
	broker *hitl.Broker,
	gate approval.BuiltinApproval,
	streams interfaces.StreamManager,
) {
	s.broker, s.gate, s.streams = broker, gate, streams
	s.Runtime.Store.OnStatus = s.publishStatus
}

// Only broker data can establish approval. Missing/malformed decisions remain
// unknown; an answered prompt is not itself evidence that approval was granted.
func collaborationPromptState(p *types.AgentPrompt) collaboration.PromptState {
	state := collaboration.PromptState{ID: p.ID, Kind: p.Kind, Status: p.Status, ToolCallID: p.ToolCallID}
	if p.Kind == types.AgentPromptKindToolApproval {
		var req types.ToolApprovalPromptRequest
		if json.Unmarshal(p.Request, &req) == nil {
			state.Service, state.Tool = req.ServiceName, req.MCPToolName
		}
		var answer struct {
			Approved *bool `json:"approved"`
		}
		if !p.IsOpen() && json.Unmarshal(p.Response, &answer) == nil && answer.Approved != nil {
			state.Decision = "rejected"
			if *answer.Approved {
				state.Decision = "approved"
			}
		}
	}
	// Lifecycle cancellation takes precedence over a previously stored response.
	switch p.Status {
	case types.AgentPromptCanceled:
		state.Decision = "canceled"
	case types.AgentPromptExpired:
		state.Decision = "expired"
	case types.AgentPromptSuperseded:
		state.Decision = "superseded"
	}
	return state
}

func (s *AgentCollaboration) reconcile(ctx context.Context, scope collaboration.Scope) error {
	if s.broker == nil {
		return nil
	}
	g, err := s.Runtime.Store.Read(ctx, scope)
	if err != nil {
		return err
	}
	for _, m := range g.Members {
		for _, v := range m.Runs {
			if v.PromptID == "" {
				continue
			}
			p, err := s.broker.Store().Get(ctx, v.PromptID)
			if err != nil {
				return err
			}
			if p == nil {
				if g.Status != "active" || v.CancelRequested {
					if err = s.broker.Suspensions().Delete(ctx, v.MessageID, v.PromptID); err != nil {
						return err
					}
					if err = s.Runtime.ClearCanceledPrompt(ctx,
						collaboration.Work{
							Scope:  scope,
							Member: *m,
							Run:    *v,
						}); err != nil {
						return err
					}
				}
				continue
			} // snapshot/registration may precede prompt creation
			if p.TenantID != scope.TenantID || p.UserID != delegatedPromptUser(scope.UserID) ||
				p.SessionID != m.SessionID ||
				p.AssistantMessageID != v.MessageID {
				return collaboration.ErrScope
			}
			if g.Status != "active" || v.CancelRequested {
				reason := "delegated task canceled"
				_, err = s.broker.Store().
					Transition(ctx,
						p.ID,
						types.AgentPromptTransition{
							From: []string{
								types.AgentPromptPending,
								types.AgentPromptSuspended,
								types.AgentPromptAnswered,
							},
							To:          types.AgentPromptCanceled,
							CloseReason: &reason,
						})
				if err != nil {
					return err
				}
				if err = s.broker.Suspensions().Delete(ctx, p.AssistantMessageID, p.ID); err != nil {
					return err
				}
				// The suspended child has no engine left to emit its resolved
				// event. Publish from the scheduler before clearing the durable
				// prompt reference so a failed delivery is retried after restart.
				if err = s.forwardCanceledPrompt(ctx, g, m, p); err != nil {
					return err
				}
				if err = s.Runtime.ClearCanceledPrompt(ctx,
					collaboration.Work{
						Scope:  scope,
						Member: *m,
						Run:    *v,
					}); err != nil {
					return err
				}
				continue
			}
			if err = s.Runtime.ReconcilePrompt(ctx,
				collaboration.Work{
					Scope:  scope,
					Member: *m,
					Run:    *v,
				},
				collaborationPromptState(p)); err != nil {
				return err
			}
		}
	}
	return nil
}

// This updates only the approval UI, never the model mailbox or parent wake
// state. Repeated delivery after a crash resolves the same pending ID again.
func (s *AgentCollaboration) forwardCanceledPrompt(
	ctx context.Context,
	g *collaboration.Group,
	m *collaboration.Member,
	p *types.AgentPrompt,
) error {
	if s.streams == nil || g.ParentMessageID == "" {
		return nil
	}
	kind := event.EventAgentPromptResolved
	if p.Kind == types.AgentPromptKindToolApproval {
		kind = event.EventToolApprovalResolved
	}
	return s.streams.AppendEvent(ctx, g.Scope.SessionID, g.ParentMessageID, interfaces.StreamEvent{
		ID: "delegated-canceled-" + p.ID, Type: types.ResponseType(kind), Timestamp: time.Now(),
		Data: map[string]interface{}{
			"pending_id": p.ID, "kind": p.Kind, "canceled": true,
			"approved": false, "answered": false, "reason": "delegated task canceled",
			"session_id": g.Scope.SessionID, "assistant_message_id": g.ParentMessageID,
			"delegated_task": m.Name, "delegated": true,
		},
	})
}

// HandlesPrompt is used by the HTTP resumer to leave delegated continuations
// to the durable queue. The generic HTTP path must not bypass its run fence.
func (s *AgentCollaboration) HandlesPrompt(ctx context.Context, p *types.AgentPrompt) bool {
	if p == nil {
		return false
	}
	var session *types.Session
	var err error
	for attempt := 1; ; attempt++ {
		session, err = s.sessions.GetSession(ctx, p.SessionID)
		if err == nil || errors.Is(err, apperrors.ErrSessionNotFound) || attempt == 3 {
			break
		}
		if !sleepCtx(ctx, time.Duration(attempt)*100*time.Millisecond) {
			break
		}
	}
	if err != nil && !errors.Is(err, apperrors.ErrSessionNotFound) {
		// A failed classification must never fall through to an unfenced HTTP
		// resume, which can close the prompt; the prompt reaper retries it.
		logger.Warnf(ctx, "Classify prompt %s for resume: %v", p.ID, err)
	}
	return err != nil || session == nil || session.Kind == types.SessionKindAgentDelegated
}

// sleepCtx waits for d and reports false when ctx ends first.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

func (s *AgentCollaboration) resumeParent(ctx context.Context, b *collaboration.Binding, data []byte) error {
	if len(data) == 0 {
		return s.finishInterruptedParent(ctx, b)
	}
	snap, err := agent.DecodeRunSnapshot(data)
	if err != nil {
		return err
	}
	var saved types.ResumableAgentRequest
	if err = json.Unmarshal(snap.Request, &saved); err != nil {
		return err
	}
	if saved.AgentID == "" || saved.SharedAgentReadOnly {
		return errors.New("invalid parent recovery identity")
	}
	ctx, err = s.workerContext(ctx, b.Scope)
	if err != nil {
		return err
	}
	ctx = collaboration.WithBinding(ctx, b)
	if saved.Language != "" {
		ctx = context.WithValue(ctx, types.LanguageContextKey, saved.Language)
	}
	if err = s.Runtime.CheckParent(ctx, b.Scope, b.ParentOwner); err != nil {
		return err
	}
	g, err := s.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return err
	}
	session, err := s.sessions.GetSession(ctx, b.Scope.SessionID)
	if err != nil {
		return err
	}
	if session.UserID != b.Scope.UserID || session.TenantID != b.Scope.TenantID {
		return collaboration.ErrScope
	}
	answer, err := s.messages.GetMessage(ctx, session.ID, g.ParentMessageID)
	if err != nil {
		return err
	}
	if answer.IsCompleted {
		return errors.New("parent message already closed")
	}
	custom, err := s.agents.GetAgentByID(ctx, saved.AgentID)
	if err != nil {
		return err
	}
	if custom == nil || custom.ID != saved.AgentID || custom.TenantID != b.Scope.TenantID || !custom.IsAgentMode() {
		return errors.New("parent agent is no longer available in this workspace")
	}
	if s.streams == nil || s.ParentEvents == nil {
		return errors.New("parent stream executor unavailable")
	}
	// SetLiveRun refuses to displace a different user turn.
	if err = s.streams.SetLiveRun(ctx, session.ID, answer.ID, answer.RequestID); err != nil {
		return err
	}
	defer func() {
		if err := s.streams.ClearLiveRun(context.WithoutCancel(ctx), session.ID, answer.ID); err != nil {
			logger.Warnf(ctx, "Failed to clear collaboration live stream: %v", err)
		}
	}()
	answer.Content = ""
	answer.RunState = ""
	bus := event.NewEventBus()
	finish := s.ParentEvents(ctx, session, answer, bus)
	s.replayStatus(ctx, b)
	var errorMu sync.Mutex
	var runErr error
	completed := false
	bus.On(event.EventAgentComplete, func(context.Context, event.Event) error {
		errorMu.Lock()
		completed = true
		errorMu.Unlock()
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			errorMu.Lock()
			runErr = errors.New(data.Error)
			errorMu.Unlock()
		}
		return nil
	})
	req := &types.QARequest{
		Session: session, AssistantMessageID: answer.ID, UserMessageID: saved.UserMessageID,
		CustomAgent:     custom,
		Query:           snap.Query,
		SummaryModelID:  saved.SummaryModelID,
		ReasoningEffort: saved.ReasoningEffort,

		KnowledgeBaseIDs: saved.KnowledgeBaseIDs, KnowledgeIDs: saved.KnowledgeIDs, TagScopes: saved.TagScopes,
		MCPServiceIDs: saved.MCPServiceIDs, SkillNames: saved.SkillNames, WebSearchEnabled: saved.WebSearchEnabled,
		QuestionOrigin: saved.QuestionOrigin, PageContext: saved.PageContext, AssistantAccess: saved.AssistantAccess,
		Interactive: true, Suspendable: true, Resume: &types.AgentResume{Snapshot: data},
	}
	if s.ParentSteer != nil {
		req.SteerSink = s.ParentSteer(ctx, session, answer)
	}
	err = s.sessions.AgentQA(ctx, req, bus)
	errorMu.Lock()
	if err == nil {
		err = runErr
	}
	errorMu.Unlock()
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	// A stale worker cannot overwrite a newer parent's transcript.
	current, readErr := s.Runtime.Store.Read(persistCtx, b.Scope)
	if readErr != nil {
		return readErr
	}
	if current.Status == "active" && current.ParentWait != nil && ctx.Err() != nil {
		return ctx.Err()
	}
	if current.ParentOwner != "" && current.ParentOwner != b.ParentOwner {
		return collaboration.ErrConflict
	}
	errorMu.Lock()
	missingCompletion := !completed
	errorMu.Unlock()
	if missingCompletion {
		if err == nil {
			err = errors.New("parent executor returned without completion")
		}
		if emitErr := bus.Emit(persistCtx, event.Event{Type: event.EventAgentComplete, Data: event.AgentCompleteData{
			MessageID: answer.ID, FinalAnswer: err.Error(), AgentSteps: snap.DisplaySteps(),
		}}); emitErr != nil {
			return emitErr
		}
	}
	if saveErr := s.messages.UpdateMessage(persistCtx, answer); saveErr != nil {
		return saveErr
	}
	if finish != nil {
		if finishErr := finish(persistCtx); finishErr != nil {
			return finishErr
		}
	}
	return err
}

func (s *AgentCollaboration) forwardPrompts(_ context.Context, b *collaboration.Binding, bus *event.EventBus) {
	if s.streams == nil {
		return
	}
	for _, kind := range []event.EventType{
		event.EventToolApprovalRequired,
		event.EventToolApprovalResolved,
		event.EventAgentPromptRequired,
		event.EventAgentPromptResolved,
		event.EventMCPOAuthRequired,
		event.EventMCPOAuthResolved,
	} {
		bus.On(kind, func(ctx context.Context, evt event.Event) error {
			if err := s.Runtime.CheckOwner(ctx, *b.Child); err != nil {
				return err
			}
			g, err := s.Runtime.Store.Read(ctx, b.Scope)
			if err != nil {
				return err
			}
			raw, err := json.Marshal(evt.Data)
			if err != nil {
				return err
			}
			var data map[string]interface{}
			if err = json.Unmarshal(raw, &data); err != nil {
				return err
			}
			if desc, ok := data["description"].(string); ok {
				data["description"] = b.Child.Member.Name + ": " + desc
			}
			if req, ok := data["request"].(map[string]interface{}); ok {
				if q, ok := req["question"].(string); ok {
					req["question"] = b.Child.Member.Name + ": " + q
				}
			}
			data["delegated_task"] = b.Child.Member.Name
			data["session_id"] = b.Scope.SessionID
			data["assistant_message_id"] = g.ParentMessageID
			data["delegated"] = true
			// All delegated prompts suspend immediately; the displayed lifetime is
			// the broker's expiry, not its former in-memory hot-wait duration.
			data["suspended"] = true
			return s.streams.AppendEvent(
				ctx,
				b.Scope.SessionID,
				g.ParentMessageID,
				interfaces.StreamEvent{
					ID:        evt.ID,
					Type:      types.ResponseType(evt.Type),
					Timestamp: time.Now(),
					Data:      data,
				},
			)
		})
	}
}

// PendingPrompts projects all children onto the parent without changing the
// prompt's actual owner/session. Parallel children may each need approval.
func (s *AgentCollaboration) PendingPrompts(
	ctx context.Context,
	sessionID string,
) (map[string][]*types.PendingPromptView, error) {
	out := map[string][]*types.PendingPromptView{}
	if s.broker == nil {
		return out, nil
	}
	caller := types.CallerFromContext(ctx)
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ?",
		caller.TenantID,
		caller.UserID,
		sessionID).Find(&rows).Error; err != nil {
		return nil, err
	}
	for _, row := range rows {
		g, err := s.Runtime.Store.Read(
			ctx,
			collaboration.Scope{TenantID: caller.TenantID, UserID: caller.UserID, SessionID: sessionID, ID: row.ID},
		)
		if err != nil {
			return nil, err
		}
		for _, m := range g.Members {
			// PromptID only points to the latest wait and is cleared on completion.
			// Read durable prompt rows to retain every decision, including older runs.
			prompts, err := s.broker.Store().ListBySession(ctx, m.SessionID, types.AgentPromptHistoryStatuses)
			if err != nil {
				return nil, err
			}
			runs := map[string]*collaboration.Run{}
			for _, run := range m.Runs {
				if !run.Inherited {
					runs[run.MessageID] = run
				}
			}
			for _, p := range prompts {
				if p.UserID != delegatedPromptUser(caller.UserID) || p.TenantID != caller.TenantID ||
					runs[p.AssistantMessageID] == nil ||
					p.SessionID != m.SessionID {
					continue
				}
				if p.Kind != types.AgentPromptKindToolApproval && p.Kind != types.AgentPromptKindAskUser {
					continue
				}
				view := p.TimelineView()
				view.DelegatedTask = m.Name
				run := runs[p.AssistantMessageID]
				if p.IsOpen() &&
					(g.Status != "active" || run.CancelRequested ||
						run.State == "cancelled" || run.State == "interrupted") {
					view.Status = types.AgentPromptCanceled
				}
				out[g.ParentMessageID] = append(out[g.ParentMessageID], view)
			}
		}
	}
	return out, nil
}

// RecoverAnswered retains answered prompts in the durable scheduler across restart.
// Executing prompts remain non-replayable because their effects are uncertain.
func (s *AgentCollaboration) RecoverAnswered(ctx context.Context, p *types.AgentPrompt) (bool, error) {
	userID, ok := strings.CutPrefix(p.UserID, types.PrincipalWebUser+":")
	if !ok || userID == "" {
		return false, nil
	}
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND status = ?",
		p.TenantID,
		userID,
		"active").Find(&rows).Error; err != nil {
		return false, err
	}
	for _, row := range rows {
		g, err := s.Runtime.Store.Read(
			ctx,
			collaboration.Scope{TenantID: p.TenantID, UserID: userID, SessionID: row.SessionID, ID: row.ID},
		)
		if err != nil {
			return false, err
		}
		if !time.Now().Before(g.Deadline) {
			continue
		}
		for _, m := range g.Members {
			v := m.Runs[len(m.Runs)-1]
			if v.PromptID != p.ID || m.SessionID != p.SessionID || v.MessageID != p.AssistantMessageID ||
				v.CancelRequested {
				continue
			}
			if v.State != "waiting" && v.State != "queued" && v.State != "running" {
				continue
			}
			until := time.Now().Add(30 * time.Second)
			ok, err := s.broker.Store().
				Transition(ctx,
					p.ID,
					types.AgentPromptTransition{
						From: []string{
							types.AgentPromptAnswered,
						},
						To:            types.AgentPromptAnswered,
						ExpectedOwner: &p.OwnerInstance,
						LeaseUntil:    &until,
					})
			return ok, err
		}
	}
	return false, nil
}

func (s *AgentCollaboration) trackProgress(b *collaboration.Binding, bus *event.EventBus) {
	for _, kind := range []event.EventType{event.EventAgentToolCall, event.EventAgentToolResult} {
		bus.On(kind, func(ctx context.Context, evt event.Event) error {
			var p collaboration.Progress
			switch d := evt.Data.(type) {
			case event.AgentToolCallData:
				p = collaboration.Progress{
					Phase:      "tool_running",
					ToolName:   d.ToolName,
					ToolCallID: d.ToolCallID,
					Iteration:  d.Iteration,
				}
			case event.AgentToolResultData:
				phase := "tool_succeeded"
				if !d.Success {
					phase = "tool_failed"
				}
				p = collaboration.Progress{
					Phase:      phase,
					ToolName:   d.ToolName,
					ToolCallID: d.ToolCallID,
					Iteration:  d.Iteration,
				}
			default:
				return nil
			}
			return s.Runtime.RecordProgress(ctx, *b.Child, p)
		})
	}
}

func delegatedPromptUser(userID string) string {
	return (types.Principal{Type: types.PrincipalWebUser, ID: userID}).StorageID()
}

// StopPrompt settles the run that owns a closed prompt. A parent prompt cancels
// its collaboration. A child prompt lives in the child session, so it is matched
// through the parent group and only that child is closed.
func (s *AgentCollaboration) StopPrompt(ctx context.Context, p *types.AgentPrompt) error {
	if p == nil {
		return nil
	}
	userID, ok := strings.CutPrefix(p.UserID, types.PrincipalWebUser+":")
	if !ok || userID == "" {
		return nil
	}
	if err := s.StopOwned(ctx, p.TenantID, userID, p.SessionID, p.AssistantMessageID); err != nil {
		return err
	}
	return s.stopChildPrompt(ctx, p, userID)
}

func (s *AgentCollaboration) stopChildPrompt(ctx context.Context, p *types.AgentPrompt, userID string) error {
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND status IN ?",
		p.TenantID, userID, []string{"active", "cancelling"}).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		if err := s.Runtime.CloseChildPrompt(ctx, collaboration.Scope{
			TenantID: p.TenantID, UserID: userID, SessionID: row.SessionID, ID: row.ID,
		}, p.SessionID, p.AssistantMessageID, p.ID, p.Status); err != nil {
			return err
		}
	}
	return nil
}

func (s *AgentCollaboration) parentHasInput(
	ctx context.Context,
	scope collaboration.Scope,
	messageID string,
) (bool, error) {
	if s.ParentSteer == nil {
		return false, nil
	}
	sink := s.ParentSteer(
		ctx,
		&types.Session{ID: scope.SessionID, TenantID: scope.TenantID},
		&types.Message{ID: messageID},
	)
	if sink == nil {
		return false, nil
	}
	input, _, err := sink.PollSteer(ctx, scope.SessionID, messageID, 0)
	return len(input) > 0, err
}

// A lost parent lease without a wait checkpoint cannot safely replay a tool.
// Close the old transcript explicitly and fence its children before returning.
func (s *AgentCollaboration) finishInterruptedParent(ctx context.Context, b *collaboration.Binding) error {
	if s.streams == nil {
		return errors.New("parent stream executor unavailable")
	}
	ctx, err := s.workerContext(ctx, b.Scope)
	if err != nil {
		return err
	}
	if err = s.Runtime.CheckParentFinalization(ctx, b.Scope, b.ParentOwner); err != nil {
		return err
	}
	g, err := s.Runtime.Store.Read(ctx, b.Scope)
	if err != nil {
		return err
	}
	answer, err := s.messages.GetMessage(ctx, b.Scope.SessionID, g.ParentMessageID)
	if err != nil {
		return err
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	defer func() {
		if err := s.streams.ClearLiveRun(persistCtx, b.Scope.SessionID, answer.ID); err != nil {
			logger.Warnf(ctx, "Failed to clear collaboration live stream: %v", err)
		}
	}()
	reason := "任务执行期间服务中断，当前步骤没有可安全恢复的检查点，已停止本轮及未完成的子任务，避免重复执行。请重新发起任务。"
	if !time.Now().Before(g.Deadline) {
		reason = "任务已超过执行时限，已停止本轮及未完成的子任务，避免重复执行。已保存的执行记录和子任务结果仍可查看。"
	}
	wasCompleted := answer.IsCompleted
	if !wasCompleted {
		answer.IsCompleted = true
		answer.RunState = ""
		if answer.Content == "" {
			answer.Content = reason
		} else {
			answer.Content += "\n\n" + reason
		}
		if err = s.messages.UpdateMessage(persistCtx, answer); err != nil {
			return err
		}
	}
	// Retry terminal delivery even when persistence succeeded on an earlier
	// attempt. Stable event IDs prevent a retry from creating another turn.

	kinds := []types.ResponseType{"complete"}
	if !wasCompleted {
		kinds = []types.ResponseType{"error", "complete"}
	}
	for _, kind := range kinds {
		if err = s.streams.AppendEvent(persistCtx, b.Scope.SessionID, answer.ID, interfaces.StreamEvent{
			ID:        "interrupted-parent-" + answer.ID + "-" + string(kind),
			Type:      kind,
			Timestamp: time.Now(),
			Content:   reason,
			Done:      true,

			Data: map[string]interface{}{
				"error": reason, "final_content": answer.Content,
				"session_id": b.Scope.SessionID, "assistant_message_id": answer.ID,
			},
		}); err != nil {
			return err
		}
	}
	return nil
}
