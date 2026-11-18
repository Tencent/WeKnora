package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/automation"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// maxStoredAnswerRunes bounds the answer a run keeps for listings and for
// the next run's carried result.
const maxStoredAnswerRunes = 4000

// errOwnerUnavailable fails a run because its owner may no longer run it;
// the automation is paused rather than retried.
var errOwnerUnavailable = errors.New("owner unavailable")

// runOutcome is how one execution of a run ended.
type runOutcome struct {
	status   string
	reason   string
	answer   string
	usage    *types.TokenUsage
	promptID string
	elapsed  time.Duration
	// pause, when set, pauses the automation with this reason.
	pause string
}

// execute runs a claimed run to its next resting state and releases its
// slot.
func (s *AutomationService) execute(parent context.Context, run *types.AutomationRun) {
	defer func() { <-s.slots }()
	ctx, cancel := context.WithCancel(logger.CloneContext(context.WithoutCancel(parent)))
	defer cancel()
	s.mu.Lock()
	s.running[run.ID] = cancel
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.running, run.ID)
		s.mu.Unlock()
	}()
	go s.heartbeat(ctx, run.ID, cancel)

	started := time.Now()
	out := func() (out runOutcome) {
		defer func() {
			if r := recover(); r != nil {
				out = runOutcome{status: types.AutomationRunFailed, reason: fmt.Sprintf("the run panicked: %v", r)}
			}
		}()
		return s.runOnce(ctx, run)
	}()
	out.elapsed = time.Since(started)
	if ctx.Err() != nil {
		var row types.AutomationRun
		if err := s.db.WithContext(context.WithoutCancel(ctx)).Select("cancel_requested").
			Where("id = ?", run.ID).Take(&row).Error; err == nil && row.CancelRequested {
			out.status, out.reason = types.AutomationRunCancelled, "cancelled by the owner"
			if out.promptID != "" {
				s.closePrompt(context.WithoutCancel(ctx), out.promptID, types.AgentPromptCanceled, out.reason)
			}
		}
	}
	s.record(ctx, run, out)
}

// heartbeat renews the run's lease and cancels it when the owner asked to
// stop it or another instance took it over.
func (s *AutomationService) heartbeat(ctx context.Context, runID string, cancel func()) {
	ticker := time.NewTicker(max(s.cfg.Lease/4, time.Second))
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
		res := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
			Where("id = ? AND status = ? AND owner = ?", runID, types.AutomationRunRunning, s.instance).
			Update("lease_until", s.now().Add(s.cfg.Lease))
		if res.Error != nil {
			logger.Warnf(ctx, "Renew automation run %s: %v", runID, res.Error)
			continue
		}
		var row types.AutomationRun
		if err := s.db.WithContext(ctx).Select("cancel_requested").Where("id = ?", runID).
			Take(&row).Error; err == nil && row.CancelRequested {
			cancel()
			return
		}
		if res.RowsAffected == 0 {
			cancel()
			return
		}
	}
}

// record stores how the execution ended, if this instance still owns it.
func (s *AutomationService) record(ctx context.Context, run *types.AutomationRun, out runOutcome) {
	ctx = context.WithoutCancel(ctx)
	run.Answer = clipRunes(out.answer, maxStoredAnswerRunes)
	updates := map[string]any{
		"answer": run.Answer, "updated_at": s.now(),
		"execution_duration_ms": gorm.Expr("execution_duration_ms + ?", out.elapsed.Milliseconds()),
	}
	if out.usage != nil {
		if data, err := json.Marshal(out.usage); err == nil {
			updates["usage"] = types.JSON(data)
		}
	}
	owned := func(db *gorm.DB) *gorm.DB { return db.Where("owner = ?", s.instance) }
	if out.status == types.AutomationRunAwaitingApproval {
		updates["status"], updates["pending_prompt_id"], updates["resume"] = out.status, out.promptID, false
		updates["lease_until"] = nil
		res := owned(s.db.WithContext(ctx).Model(&types.AutomationRun{}).
			Where("id = ? AND status = ?", run.ID, types.AutomationRunRunning)).Updates(updates)
		if res.Error != nil || res.RowsAffected == 0 {
			logger.Warnf(ctx, "Record waiting automation run %s: %v", run.ID, res.Error)
			return
		}
		if a, err := s.automationOf(ctx, run); err == nil {
			s.notify(ctx, a, run, types.NotificationAutomationAwaitingApproval, "")
		}
		// The approval may have been answered before the run was marked
		// waiting; pick it up now rather than at the next tick.
		s.Wake()
		return
	}
	if err := owned(s.db.WithContext(ctx).Model(&types.AutomationRun{}).
		Where("id = ? AND status = ?", run.ID, types.AutomationRunRunning)).Updates(updates).Error; err != nil {
		logger.Warnf(ctx, "Record automation run %s: %v", run.ID, err)
	}
	running := []string{types.AutomationRunRunning}
	if _, err := s.finishRun(ctx, run, running, out.status, out.reason, owned); err != nil {
		logger.Warnf(ctx, "Finish automation run %s: %v", run.ID, err)
	}
	if out.pause != "" {
		if a, err := s.automationOf(ctx, run); err == nil {
			_ = s.pause(ctx, a, out.pause)
		}
	}
	s.Wake()
}

func (s *AutomationService) automationOf(ctx context.Context, run *types.AutomationRun) (*types.Automation, error) {
	var a types.Automation
	err := s.db.WithContext(ctx).Unscoped().Where("id = ?", run.AutomationID).First(&a).Error
	return &a, err
}

func clipRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

func failed(reason string) runOutcome {
	return runOutcome{status: types.AutomationRunFailed, reason: reason}
}

// ownerContext is ctx acting as a's owner, rechecked now: the account is
// active and still a member of the workspace.
func (s *AutomationService) ownerContext(ctx context.Context, a *types.Automation) (context.Context, error) {
	user, err := s.users.GetUserByID(ctx, a.OwnerUserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, fmt.Errorf("%w: the owner's account is inactive", errOwnerUnavailable)
	}
	membership, err := s.members.GetMembership(ctx, a.OwnerUserID, a.TenantID)
	if err != nil {
		return nil, err
	}
	if membership == nil || membership.Status != types.TenantMemberStatusActive {
		return nil, fmt.Errorf("%w: the owner is no longer a member of this workspace", errOwnerUnavailable)
	}
	tenant, err := s.tenants.GetTenantByID(ctx, a.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, fmt.Errorf("%w: the workspace is unavailable", errOwnerUnavailable)
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, a.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, a.OwnerUserID)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, membership.Role)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ctx = context.WithValue(ctx, types.UserContextKey, user)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: a.TenantID, UserID: a.OwnerUserID, Role: membership.Role})
	ctx = types.WithPrincipal(ctx, types.Principal{
		Type: types.PrincipalWebUser, ID: a.OwnerUserID, ActingVia: types.AutomationActingVia(a.ID),
	})
	return ctx, nil
}

// runOnce executes run, or continues it from its answered approval.
func (s *AutomationService) runOnce(ctx context.Context, run *types.AutomationRun) runOutcome {
	started := time.Now()
	if s.sessions == nil {
		return failed("the agent executor is unavailable")
	}
	a, err := s.automationOf(ctx, run)
	if err != nil {
		return failed("the automation is gone")
	}
	if a.DeletedAt.Valid {
		return runOutcome{status: types.AutomationRunCancelled, reason: "the automation was deleted"}
	}
	ctx, err = s.ownerContext(ctx, a)
	if err != nil {
		if errors.Is(err, errOwnerUnavailable) {
			return runOutcome{status: types.AutomationRunFailed, reason: err.Error(), pause: err.Error()}
		}
		return failed(err.Error())
	}
	agentConfig, err := s.runnableAssistant(ctx)
	if err != nil {
		return runOutcome{status: types.AutomationRunFailed, reason: err.Error(), pause: err.Error()}
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	ctx = types.WithStandingGrantScope(ctx, types.GrantScope{
		Scope: types.AgentGrantScopeAutomation, ScopeID: a.ID,
		BindingHash: automationBinding(agentConfig, tenant, types.TenantRoleFromContext(ctx)),
	})
	limit := s.cfg.DefaultMaxDuration
	if m := a.Limits.MaxDurationMinutes; m > 0 {
		limit = time.Duration(m) * time.Minute
	}
	remaining := limit - time.Duration(run.ExecutionDurationMS)*time.Millisecond - time.Since(started)
	if remaining <= 0 {
		if run.Resume && run.PendingPromptID != "" {
			s.closePrompt(ctx, run.PendingPromptID, types.AgentPromptFailed, "execution time limit exceeded")
		}
		return failed(fmt.Sprintf("the run took longer than %s", limit))
	}
	ctx, cancel := context.WithTimeout(ctx, remaining)
	defer cancel()

	var resume *types.AgentResume
	modelID := a.ModelID
	session, userMsg, answer, query := (*types.Session)(nil), (*types.Message)(nil), (*types.Message)(nil), ""
	if run.Resume {
		var stop func()
		session, userMsg, answer, resume, stop, err = s.resumeState(ctx, run)
		if err != nil {
			if run.PendingPromptID != "" {
				s.closePrompt(ctx, run.PendingPromptID, types.AgentPromptFailed, hitl.ReasonResumeStopped)
			}
			return failed("could not continue after the approval: " + err.Error())
		}
		defer stop()
		if snap, err := agent.DecodeRunSnapshot(resume.Snapshot); err == nil {
			query = snap.Query
			// A settings edit affects future runs, not an approval already in flight.
			var original types.ResumableAgentRequest
			if err := json.Unmarshal(snap.Request, &original); err == nil {
				modelID = original.SummaryModelID
			}
		}
	} else {
		session, err = s.runSession(ctx, a, run)
		if err != nil {
			return failed("could not open the conversation: " + err.Error())
		}
		if a.SessionMode == types.AutomationSessionPersistent && s.streams != nil {
			if live, _, _ := s.streams.GetLiveRun(ctx, session.ID); live != "" {
				return runOutcome{status: types.AutomationRunSkipped, reason: "its conversation was busy"}
			}
		}
		query = s.runQuery(ctx, a, run)
		userMsg, answer, err = s.startMessages(ctx, run, session, query)
		if err != nil {
			return failed("could not record the run: " + err.Error())
		}
	}
	if err := s.validateModel(ctx, modelID, a.TenantID); err != nil {
		return failed(err.Error())
	}
	if modelID != "" {
		// Keep the shared agent configuration untouched. The per-run copy also
		// allows an explicit model when the agent's default is no longer available.
		configured := *agentConfig
		configured.Config.ModelID = modelID
		agentConfig = &configured
	}
	loc := scheduleLocation(a.Trigger)
	req := &types.QARequest{
		Session: session, Query: query, AssistantMessageID: answer.ID, UserMessageID: userMsg.ID,
		CustomAgent: agentConfig, WebSearchEnabled: agentConfig.Config.WebSearchEnabled,
		SummaryModelID:  modelID,
		AssistantAccess: a.AccessLevel, Mode: types.RunModeDeferred, Suspendable: true, Resume: resume,
		RunNote: automation.RunNote(a.Name, run.ScheduledAt, loc),
	}
	out := s.agentQA(ctx, req, session, answer)
	if resume != nil && out.status != types.AutomationRunAwaitingApproval {
		_ = s.broker.Suspensions().Delete(context.WithoutCancel(ctx), answer.ID, resume.Prompt.ID)
	}
	if out.status == types.AutomationRunFailed && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		out.reason = fmt.Sprintf("the run took longer than %s", limit)
	}
	return out
}

func scheduleLocation(t types.AutomationTrigger) *time.Location {
	if loc, err := time.LoadLocation(t.Timezone); err == nil && t.Timezone != "" {
		return loc
	}
	return time.UTC
}

// runQuery is the user message of a new run.
func (s *AutomationService) runQuery(ctx context.Context, a *types.Automation, run *types.AutomationRun) string {
	vars := automation.RunVars{
		Now: run.ScheduledAt, LastRunAt: a.LastRunAt, LastSuccessAt: a.LastSuccessAt,
		RunID: run.ID, AutomationName: a.Name, Location: scheduleLocation(a.Trigger),
	}
	last := ""
	if a.SessionMode == types.AutomationSessionPerRun && a.CarryLastResult {
		var prev types.AutomationRun
		if err := s.db.WithContext(ctx).Select("answer").
			Where("automation_id = ? AND status = ? AND id <> ?", a.ID, types.AutomationRunSucceeded, run.ID).
			Order("finished_at DESC").Take(&prev).Error; err == nil {
			last = prev.Answer
		}
	}
	return automation.RunQuery(a.Instruction, vars, last)
}

// runSession is the conversation a new run writes to: a new one per run, or
// the automation's persistent one, created on first use.
func (s *AutomationService) runSession(
	ctx context.Context, a *types.Automation, run *types.AutomationRun,
) (*types.Session, error) {
	if a.SessionMode == types.AutomationSessionPersistent && a.PersistentSessionID != "" {
		if session, err := s.sessions.GetSession(ctx, a.PersistentSessionID); err == nil && session != nil {
			return session, nil
		}
	}
	title := a.Name
	if a.SessionMode == types.AutomationSessionPerRun {
		title += " · " + run.ScheduledAt.In(scheduleLocation(a.Trigger)).Format("2006-01-02 15:04")
	}
	now := s.now()
	session := &types.Session{
		ID: uuid.NewString(), TenantID: a.TenantID, UserID: a.OwnerUserID, Kind: types.SessionKindAutomation,
		Title: clipRunes(title, 200), CreatedAt: now, UpdatedAt: now,
		LastRequestState: &types.SessionLastRequestState{
			AgentID: types.BuiltinWeKnoraAssistantID, AgentEnabled: true, ModelID: a.ModelID,
		},
	}
	if err := s.db.WithContext(ctx).Session(&gorm.Session{SkipHooks: true}).
		Clauses(clause.OnConflict{DoNothing: true}).Create(session).Error; err != nil {
		return nil, err
	}
	if a.SessionMode == types.AutomationSessionPersistent {
		res := s.db.WithContext(ctx).Model(&types.Automation{}).
			Where("id = ? AND persistent_session_id = ?", a.ID, a.PersistentSessionID).
			Update("persistent_session_id", session.ID)
		if res.Error != nil {
			return nil, res.Error
		}
		if res.RowsAffected == 0 {
			return nil, errors.New("the automation's conversation changed meanwhile")
		}
	}
	return s.sessions.GetSession(ctx, session.ID)
}

func (s *AutomationService) startMessages(
	ctx context.Context, run *types.AutomationRun, session *types.Session, query string,
) (*types.Message, *types.Message, error) {
	now := s.now()
	userMsg, err := s.messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, RequestID: run.ID, Role: "user", Content: query,
		IsCompleted: true, CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return nil, nil, err
	}
	answer, err := s.messages.CreateMessage(ctx, &types.Message{
		SessionID: session.ID, RequestID: run.ID, Role: "assistant", CreatedAt: now, Channel: types.ChannelWeb,
	})
	if err != nil {
		return nil, nil, err
	}
	res := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
		Where("id = ? AND status = ? AND owner = ?", run.ID, types.AutomationRunRunning, s.instance).
		Updates(map[string]any{
			"session_id": session.ID, "user_message_id": userMsg.ID, "assistant_message_id": answer.ID,
		})
	if res.Error != nil {
		return nil, nil, res.Error
	}
	if res.RowsAffected == 0 {
		return nil, nil, errors.New("the run was taken over")
	}
	run.SessionID, run.UserMessageID, run.AssistantMessageID = session.ID, userMsg.ID, answer.ID
	return userMsg, answer, nil
}

// resumeState loads what a run waiting on an answered approval continues
// from. stop releases the prompt's lease.
func (s *AutomationService) resumeState(ctx context.Context, run *types.AutomationRun) (
	session *types.Session, userMsg, answer *types.Message, resume *types.AgentResume, stop func(), err error,
) {
	stop = func() {}
	if s.broker == nil {
		return nil, nil, nil, nil, stop, errors.New("approvals are unavailable")
	}
	prompt, err := s.broker.Store().Get(ctx, run.PendingPromptID)
	if err != nil {
		return nil, nil, nil, nil, stop, err
	}
	if prompt == nil || prompt.Status != types.AgentPromptAnswered || prompt.SessionID != run.SessionID ||
		prompt.AssistantMessageID != run.AssistantMessageID || prompt.TenantID != run.TenantID {
		return nil, nil, nil, nil, stop, errors.New("the approval can no longer be resumed")
	}
	stop = s.broker.KeepLease(ctx, prompt.ID, prompt.OwnerInstance)
	snapshot, err := s.broker.Suspensions().Get(ctx, prompt.AssistantMessageID)
	if err != nil {
		return nil, nil, nil, nil, stop, err
	}
	if snapshot == nil || snapshot.PromptID != prompt.ID {
		return nil, nil, nil, nil, stop, errors.New("the saved run state is missing")
	}
	if session, err = s.sessions.GetSession(ctx, run.SessionID); err != nil {
		return nil, nil, nil, nil, stop, err
	}
	if answer, err = s.messages.GetMessage(ctx, run.SessionID, run.AssistantMessageID); err != nil {
		return nil, nil, nil, nil, stop, err
	}
	if answer == nil || answer.IsCompleted {
		return nil, nil, nil, nil, stop, errors.New("the run's message was closed")
	}
	answer.RunState = ""
	answer.UpdatedAt = s.now()
	if err := s.messages.UpdateMessage(ctx, answer); err != nil {
		return nil, nil, nil, nil, stop, err
	}
	resume = &types.AgentResume{Snapshot: []byte(snapshot.Snapshot), Prompt: prompt}
	return session, &types.Message{ID: run.UserMessageID}, answer, resume, stop, nil
}

// agentQA executes req, streaming to the run's message, and persists the
// message.
func (s *AutomationService) agentQA(
	ctx context.Context, req *types.QARequest, session *types.Session, answer *types.Message,
) runOutcome {
	bus := event.NewEventBus()
	var finishStream func(context.Context) error
	if s.Events != nil && s.streams != nil {
		if err := s.streams.SetLiveRun(ctx, session.ID, answer.ID, answer.RequestID); err != nil {
			return failed("could not publish the run: " + err.Error())
		}
		defer func() {
			if err := s.streams.ClearLiveRun(context.WithoutCancel(ctx), session.ID, answer.ID); err != nil {
				logger.Warnf(ctx, "Clear automation live run: %v", err)
			}
		}()
		if req.Resume != nil {
			_ = s.streams.ResetEvents(ctx, session.ID, answer.ID)
		}
		answer.Content = ""
		finishStream = s.Events(ctx, session, answer, bus)
		data := map[string]any{
			"session_id": session.ID, "assistant_message_id": answer.ID, "user_message_id": req.UserMessageID,
		}
		if req.Resume != nil {
			data["resume_pending_id"] = req.Resume.Prompt.ID
			data["resume_tool_call_id"] = req.Resume.Prompt.ToolCallID
		}
		if err := s.streams.AppendEvent(ctx, session.ID, answer.ID, interfaces.StreamEvent{
			ID: "query-" + answer.ID, Type: types.ResponseTypeAgentQuery, Done: true, Timestamp: s.now(), Data: data,
		}); err != nil {
			return failed("could not publish the run: " + err.Error())
		}
	}
	var mu sync.Mutex
	var runErr error
	var suspended *types.PromptSuspension
	var usage *types.TokenUsage
	completed := false
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		completed = true
		if u, ok := data.Usage.(*types.TokenUsage); ok {
			usage = u
		}
		if finishStream == nil {
			answer.Content = data.FinalAnswer
			answer.AgentDurationMs = data.TotalDurationMs
			if steps, ok := data.AgentSteps.([]types.AgentStep); ok {
				answer.AgentSteps = tools.SanitizeAgentStepsForStorage(steps)
			}
			answer.Usage = usage
		}
		if p, ok := data.Suspended.(*types.PromptSuspension); ok && p != nil {
			suspended = p
			answer.RunState = types.MessageRunStateAwaitingInput
		}
		return nil
	})
	bus.On(event.EventError, func(_ context.Context, evt event.Event) error {
		if data, ok := evt.Data.(event.ErrorData); ok {
			mu.Lock()
			runErr = errors.New(data.Error)
			mu.Unlock()
		}
		return nil
	})
	err := func() (err error) {
		defer func() {
			if p := recover(); p != nil {
				err = fmt.Errorf("agent executor panicked: %v", p)
			}
		}()
		return s.sessions.AgentQA(ctx, req, bus)
	}()
	mu.Lock()
	if err == nil {
		err = runErr
	}
	if err == nil {
		err = ctx.Err()
	}
	missing := !completed
	mu.Unlock()
	if finishStream != nil && missing {
		if err != nil {
			_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
				Type: event.EventError, Data: event.ErrorData{Error: err.Error(), Stage: "automation"},
			})
		}
		_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
			Type: event.EventAgentComplete, Data: event.AgentCompleteData{MessageID: answer.ID},
		})
	}
	mu.Lock()
	defer mu.Unlock()
	answer.IsCompleted = suspended == nil || err != nil
	if answer.IsCompleted {
		answer.RunState = ""
	}
	answer.UpdatedAt = s.now()
	if err != nil && answer.Content == "" {
		answer.Content = err.Error()
	}
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 10*time.Second)
	defer cancel()
	if saveErr := s.messages.UpdateMessage(persistCtx, answer); saveErr != nil && err == nil {
		err = saveErr
	}
	if finishStream != nil {
		if streamErr := finishStream(persistCtx); streamErr != nil && err == nil {
			err = streamErr
		}
	}
	out := runOutcome{answer: answer.Content, usage: usage}
	switch {
	case suspended != nil && err == nil:
		out.status, out.promptID = types.AutomationRunAwaitingApproval, suspended.PromptID
	case err != nil:
		out.status, out.reason = types.AutomationRunFailed, err.Error()
	default:
		out.status = types.AutomationRunSucceeded
	}
	return out
}
