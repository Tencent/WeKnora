package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent"
	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/collaboration"
	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/operations"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// AgentCollaboration owns durable work and binds SessionService after DI has
// resolved its dependency on AgentService. No request context or credentials
// survive in the work record; workers reauthorize the user and membership.
type AgentCollaboration struct {
	artifactFiles func(context.Context, *collaboration.Binding, string) tools.ArtifactFileSource
	Runtime       *collaboration.Runtime
	db            *gorm.DB
	agents        interfaces.CustomAgentService
	messages      interfaces.MessageService
	users         interfaces.UserService
	members       interfaces.TenantMemberService
	tenants       interfaces.TenantService
	sessions      interfaces.SessionService
	broker        *hitl.Broker
	gate          approval.BuiltinApproval
	streams       interfaces.StreamManager
	ParentSteer   func(context.Context, *types.Session, *types.Message) types.SteerSink
	ParentEvents  func(context.Context, *types.Session, *types.Message, *event.EventBus) func(context.Context) error
	ChildEvents   func(context.Context, *types.Session, *types.Message, *event.EventBus) func(context.Context) error
}

// NewAgentCollaboration constructs the service and its durable execution runtime.
func NewAgentCollaboration(
	db *gorm.DB,
	agents interfaces.CustomAgentService,
	messages interfaces.MessageService,
	users interfaces.UserService,
	members interfaces.TenantMemberService,
	tenants interfaces.TenantService,
) *AgentCollaboration {
	s := &AgentCollaboration{
		db:       db,
		agents:   agents,
		messages: messages,
		users:    users,
		members:  members,
		tenants:  tenants,
	}
	s.Runtime = collaboration.New(collaboration.NewStore(db), s.run)
	s.Runtime.WakeParent = s.resumeParent
	s.Runtime.Reconcile = s.reconcile
	s.Runtime.ParentInput = s.parentHasInput
	s.Runtime.Prune = s.prune
	return s
}

// Bind attaches the session executor used by child runs.
func (s *AgentCollaboration) Bind(sessions interfaces.SessionService) {
	s.sessions = sessions
	if owner, ok := sessions.(interface {
		SetDeletedSessionsHook(func(context.Context, uint64, string, []string))
	}); ok {
		owner.SetDeletedSessionsHook(s.forgetSessions)
	}
}

// forgetSessions stops collaborations of deleted conversations, soft-deletes
// their child conversations like the parent, and drops the internal records.
func (s *AgentCollaboration) forgetSessions(ctx context.Context, tenantID uint64, userID string, sessionIDs []string) {
	if len(sessionIDs) == 0 {
		return
	}
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND scope = ? AND scope_id IN ?", tenantID, types.AgentGrantScopeSession, sessionIDs).
		Delete(&types.AgentGrant{}).Error; err != nil {
		logger.Warnf(ctx, "Delete grants of deleted sessions: %v", err)
	}
	var rows []collaboration.Record
	query := s.db.WithContext(ctx).Where("tenant_id = ? AND session_id IN ?", tenantID, sessionIDs)
	if userID != "" {
		query = query.Where("user_id = ?", userID)
	}
	if err := query.Find(&rows).Error; err != nil {
		logger.Warnf(ctx, "List collaborations of deleted sessions: %v", err)
		return
	}
	childIDs := map[string][]string{}
	groupIDs := make([]string, 0, len(rows))
	for _, row := range rows {
		groupIDs = append(groupIDs, row.ID)
		scope := collaboration.Scope{TenantID: row.TenantID, UserID: row.UserID, SessionID: row.SessionID, ID: row.ID}
		if row.Status == "active" || row.Status == "cancelling" {
			if err := s.Runtime.Cancel(ctx, scope); err != nil && !errors.Is(err, collaboration.ErrHistoryLimit) {
				logger.Warnf(ctx, "Cancel collaboration %s of deleted session: %v", row.ID, err)
			}
		}
		var g collaboration.Group
		if err := json.Unmarshal([]byte(row.Data), &g); err != nil {
			continue
		}
		for _, m := range g.Members {
			childIDs[row.UserID] = append(childIDs[row.UserID], m.SessionID)
		}
	}
	for owner, ids := range childIDs {
		if err := s.db.WithContext(ctx).
			Where("tenant_id = ? AND user_id = ? AND kind = ? AND id IN ?",
				tenantID, owner, types.SessionKindAgentDelegated, ids).
			Delete(&types.Session{}).Error; err != nil {
			logger.Warnf(ctx, "Delete child conversations of deleted sessions: %v", err)
		}
	}
	// Active records stay until their workers observe cancellation; the
	// scheduler's prune removes them once terminal.
	if len(groupIDs) == 0 {
		return
	}
	if err := s.db.WithContext(ctx).
		Where("id IN ? AND status IN ?", groupIDs, []string{"completed", "cancelled"}).
		Delete(&collaboration.Record{}).Error; err != nil {
		logger.Warnf(ctx, "Delete collaborations of deleted sessions: %v", err)
	}
}

// collaborationRetention is how long a terminal record stays untouched before
// pruning considers it. Recent records still serve history and inheritance.
const collaborationRetention = time.Hour

// prune bounds durable collaboration storage. It removes terminal records of
// deleted conversations and terminal records that never delegated, and
// compacts the remaining old terminal records. Child conversations keep the
// complete transcripts.
func (s *AgentCollaboration) prune(ctx context.Context) error {
	terminal := []string{"completed", "cancelled"}
	cutoff := time.Now().Add(-collaborationRetention).UTC()
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND updated_at < ?", terminal, cutoff).
		Where("session_id IN (?)", s.db.Table("sessions").Select("id").Where("deleted_at IS NOT NULL")).
		Delete(&collaboration.Record{}).Error; err != nil {
		return err
	}
	// Each record is revisited only while it is inside this window.
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).
		Where("status IN ? AND updated_at < ? AND updated_at >= ?", terminal, cutoff, cutoff.Add(-24*time.Hour)).
		Order("updated_at").Limit(500).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		var g collaboration.Group
		if err := json.Unmarshal([]byte(row.Data), &g); err != nil {
			continue
		}
		scope := collaboration.Scope{TenantID: row.TenantID, UserID: row.UserID, SessionID: row.SessionID, ID: row.ID}
		if len(g.Members) == 0 {
			if err := s.db.WithContext(ctx).Where("id = ? AND version = ?", row.ID, row.Version).
				Delete(&collaboration.Record{}).Error; err != nil {
				return err
			}
			continue
		}
		if err := s.Runtime.Compact(ctx, scope); err != nil {
			logger.Warnf(ctx, "Compact collaboration %s: %v", row.ID, err)
		}
	}
	return nil
}

type delegatedConfig struct {
	Agent  types.CustomAgent          `json:"agent"`
	Access types.AssistantAccessLevel `json:"access"`
	// Durable execution identity follows children across scheduler instances
	// and approval resumes; it must not turn an automation into a human turn.
	ActingVia string        `json:"acting_via,omitempty"`
	Mode      types.RunMode `json:"mode,omitempty"`
	// AssistantParent records that the delegating run was the WeKnora
	// assistant, whose level stays under the workspace's assistant ceiling.
	AssistantParent bool                  `json:"assistant_parent,omitempty"`
	Inherited       *inheritedAgentConfig `json:"inherited,omitempty"`
}

func (d *delegatedConfig) captureExecution(ctx context.Context) {
	d.ActingVia = types.ActingViaFromContext(ctx)
	d.Mode = types.RunModeAttended
	if mode, ok := ctx.Value(types.RunModeContextKey).(types.RunMode); ok {
		d.Mode = mode.Normalize()
	}
	if d.ActingVia != "" {
		d.Mode = types.RunModeDeferred
	}
}

func (d *delegatedConfig) runMode() types.RunMode {
	if d.ActingVia != "" {
		return types.RunModeDeferred
	}
	if d.Mode == "" {
		return types.RunModeAttended // snapshots created before mode was stored
	}
	return d.Mode.Normalize()
}

// level is the child's access level: the delegating run's, capped by the
// child agent's own ceiling and, under an assistant parent, by the
// workspace's current assistant ceiling.
func (d *delegatedConfig) level(tenant *types.Tenant) types.AssistantAccessLevel {
	level := types.CapAccessLevel(d.Access, d.Agent.Config.AccessLevel)
	if d.AssistantParent || (d.Inherited != nil && d.Inherited.OperationsAgentID != "") {
		var assistant *types.AssistantConfig
		if tenant != nil {
			assistant = tenant.AssistantConfig
		}
		level = types.CapAccessLevel(level, assistant.EffectiveAccessLevel(d.Access))
	}
	return level
}

func (s *AgentCollaboration) resolve(ctx context.Context, id string, level types.AssistantAccessLevel) (string, error) {
	return s.resolveDelegated(ctx, id, level, false)
}

func (s *AgentCollaboration) resolveDelegated(
	ctx context.Context, id string, level types.AssistantAccessLevel, assistantParent bool,
) (string, error) {
	a, err := s.agents.GetAgentByID(ctx, id)
	if err != nil {
		return "", err
	}
	tenant, _ := types.TenantIDFromContext(ctx)
	if a == nil || a.TenantID != tenant || !a.IsAgentMode() || types.GrantsOperationCatalog(a) ||
		id == types.BuiltinWikiFixerID ||
		id == types.BuiltinSkillInstallerID {
		return "", errors.New("only same-workspace reasoning agents without management privileges may be delegated")
	}
	saved := delegatedConfig{Agent: *a, Access: level, AssistantParent: assistantParent}
	saved.captureExecution(ctx)
	data, err := json.Marshal(saved)
	return string(data), err
}

func (s *AgentCollaboration) attach(
	ctx context.Context,
	engine *agent.AgentEngine,
	registry *tools.ToolRegistry,
	cfg *types.AgentConfig,
	sessionID, messageID string,
) error {
	b := collaboration.FromContext(ctx)
	if b == nil {
		if cfg.SharedAgentReadOnly || cfg.SkillInstallMode() {
			return nil
		}
		if _, ok := tools.OperationIdentityFromContext(ctx); !ok {
			return nil
		}
		caller := types.CallerFromContext(ctx)
		scope := collaboration.Scope{
			TenantID:  caller.TenantID,
			UserID:    caller.UserID,
			SessionID: sessionID,
			ID:        messageID,
		}
		var err error
		b, err = s.Runtime.AcquireParent(ctx, scope)
		if err != nil {
			return err
		}
	}
	engine.SetCollaboration(b)
	if s.artifactFiles != nil {
		if source := s.artifactFiles(ctx, b, sessionID); source != nil {
			if existing, err := registry.GetTool(tools.ToolReadFile); err == nil {
				if reader, ok := existing.(*tools.ReadFileTool); ok {
					reader.WithArtifacts(source)
				}
			} else {
				registry.RegisterTool(tools.NewReadFileTool(nil).WithArtifacts(source))
			}
		}
	}
	s.replayStatus(ctx, b)
	for _, tool := range tools.NewCollaborationTools(b,
		func(ctx context.Context,
			id string) (string,
			error,
		) {
			if strings.TrimSpace(id) == "" {
				return inheritedChildSnapshot(ctx, cfg, registry.ListTools())
			}
			return s.resolveDelegated(ctx, id, cfg.AssistantAccess, cfg.OperationsAgentID != "")
		}) {
		registry.RegisterTool(tool)
	}
	if b.Child == nil {
		registry.SetMCPCallGuard(
			func(ctx context.Context, serviceID, toolName string, args json.RawMessage) (*types.ToolResult, error) {
				return s.guardDelegatedMCPCall(ctx, b, serviceID, toolName, args)
			},
		)
		registry.SetExecutionGuard(func(ctx context.Context, _ types.Tool) error {
			return b.Runtime.CheckParent(ctx, b.Scope, b.ParentOwner)
		})
	}
	if b.Child != nil {
		var pinned delegatedConfig
		if err := json.Unmarshal([]byte(b.Child.Member.ConfigJSON), &pinned); err != nil {
			return err
		}
		workerCtx, err := s.workerContext(ctx, b.Scope)
		if err != nil {
			return err
		}
		tenant, _ := workerCtx.Value(types.TenantInfoContextKey).(*types.Tenant)
		cfg.AssistantAccess = pinned.level(tenant)
		if pinned.Inherited != nil {
			registry.RestrictTools(append(append([]string(nil), pinned.Inherited.ToolNames...), "send_message"))
		}
		for name, risk := range assistantWriteToolRisk {
			if _, err := registry.GetTool(name); err == nil {
				access := operations.Access{Level: cfg.AssistantAccess}
				if err := registry.RequireConfirmation(name, risk, access, s.gate); err != nil {
					return err
				}
			}
		}
		// Workers may use arbitrary sandbox tools. Give mutations the same
		// approval path as the parent's instead of refusing them.
		for _, name := range registry.ListTools() {
			if _, wrapped := assistantWriteToolRisk[name]; wrapped || inheritedToolOwnsPolicy(name) {
				continue
			}
			if err := registry.RequireConfirmation(name, operations.RiskWrite,
				operations.Access{Level: cfg.AssistantAccess}, s.gate); err != nil {
				return err
			}
		}
		registry.SetExecutionGuard(func(ctx context.Context, tool types.Tool) error {
			if err := b.Runtime.CheckOwner(ctx, *b.Child); err != nil {
				return err
			}
			currentCtx, err := s.workerContext(ctx, b.Scope)
			if err != nil {
				return err
			}
			if !types.CallerFromContext(currentCtx).Role.HasPermission(types.CallerFromContext(ctx).Role) {
				return errors.New("delegating user's role was reduced; start a newly authorized turn")
			}
			var saved delegatedConfig
			if err := json.Unmarshal([]byte(b.Child.Member.ConfigJSON), &saved); err != nil {
				return err
			}
			if tool.Name() == "send_message" || tools.CanRunConcurrently(tool.Name()) {
				return nil
			}
			currentTenant, _ := currentCtx.Value(types.TenantInfoContextKey).(*types.Tenant)
			if saved.level(currentTenant) != cfg.AssistantAccess {
				return errors.New("delegated access changed; start a newly authorized turn")
			}
			return nil // Registered tools own their policy or were wrapped above.
		})
	}
	return nil
}

func (s *AgentCollaboration) workerContext(ctx context.Context, scope collaboration.Scope) (context.Context, error) {
	user, err := s.users.GetUserByID(ctx, scope.UserID)
	if err != nil {
		return nil, err
	}
	if user == nil || !user.IsActive {
		return nil, errors.New("delegating user is inactive")
	}
	membership, err := s.members.GetMembership(ctx, scope.UserID, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if membership == nil || membership.Status != types.TenantMemberStatusActive {
		return nil, errors.New("workspace membership is no longer active")
	}
	tenant, err := s.tenants.GetTenantByID(ctx, scope.TenantID)
	if err != nil {
		return nil, err
	}
	if tenant == nil {
		return nil, errors.New("delegating workspace is unavailable")
	}
	ctx = context.WithValue(ctx, types.TenantIDContextKey, scope.TenantID)
	ctx = context.WithValue(ctx, types.UserIDContextKey, scope.UserID)
	ctx = context.WithValue(ctx, types.TenantRoleContextKey, membership.Role)
	ctx = context.WithValue(ctx, types.TenantInfoContextKey, tenant)
	ctx = types.WithCaller(ctx, types.Caller{TenantID: scope.TenantID, UserID: scope.UserID, Role: membership.Role})
	principal := types.Principal{Type: types.PrincipalWebUser, ID: scope.UserID}
	if previous, ok := types.PrincipalFromContext(ctx); ok && previous.ID == scope.UserID {
		principal.ActingVia = previous.ActingVia
	}
	ctx = types.WithPrincipal(ctx, principal)
	return ctx, nil
}

func (s *AgentCollaboration) run(ctx context.Context, w collaboration.Work) (collaboration.Result, error) {
	result := collaboration.Result{}
	ctx, err := s.workerContext(ctx, w.Scope)
	if err != nil {
		return result, err
	}
	if s.sessions == nil {
		return result, errors.New("session executor unavailable")
	}
	if _, err = s.sessions.GetSession(ctx, w.Scope.SessionID); err != nil {
		return result, fmt.Errorf("parent conversation unavailable: %w", err)
	}
	var saved delegatedConfig
	if err = json.Unmarshal([]byte(w.Member.ConfigJSON), &saved); err != nil {
		return result, err
	}
	ctx = types.WithPrincipal(ctx, types.Principal{
		Type: types.PrincipalWebUser, ID: w.Scope.UserID, ActingVia: saved.ActingVia,
	})
	ctx = types.WithRunMode(ctx, saved.runMode())
	// Both modes pin configuration, but reauthorize the source before each turn.
	if saved.Inherited == nil {
		if _, err = s.resolve(ctx, w.Member.AgentID, types.AssistantAccessReadOnly); err != nil {
			return result, err
		}
	} else {
		current, getErr := s.agents.GetAgentByID(ctx, saved.Agent.ID)
		if getErr != nil {
			return result, getErr
		}
		if current == nil || current.ID != saved.Agent.ID || current.TenantID != w.Scope.TenantID ||
			!current.IsAgentMode() {
			return result, errors.New("inherited agent is no longer available in this workspace")
		}
	}
	tenant, _ := ctx.Value(types.TenantInfoContextKey).(*types.Tenant)
	saved.Access = saved.level(tenant)
	// Store the attenuated permission in this worker's private binding too.
	configJSON, err := json.Marshal(saved)
	if err != nil {
		return result, err
	}
	w.Member.ConfigJSON = string(configJSON)
	b := &collaboration.Binding{Runtime: s.Runtime, Scope: w.Scope, Child: &w}
	ctx = collaboration.WithBinding(ctx, b)
	if err = s.Runtime.CheckOwner(ctx, w); err != nil {
		return result, err
	}
	session := &types.Session{
		ID:        w.Member.SessionID,
		TenantID:  w.Scope.TenantID,
		UserID:    w.Scope.UserID,
		Kind:      types.SessionKindAgentDelegated,
		Title:     w.Member.Name,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}
	if err = s.db.WithContext(ctx).Session(&gorm.Session{
		SkipHooks: true,
	}).Clauses(clause.OnConflict{
		DoNothing: true,
	}).Create(session).Error; err != nil {
		return result, err
	}
	session, err = s.sessions.GetSession(ctx, session.ID)
	if err != nil {
		return result, err
	}
	var userMsg, answer *types.Message
	var resume *types.AgentResume
	if w.Run.PromptID != "" {
		if s.broker == nil {
			return result, errors.New("prompt broker unavailable")
		}
		prompt, getErr := s.broker.Store().Get(ctx, w.Run.PromptID)
		if getErr != nil {
			return result, getErr
		}
		if prompt == nil || prompt.Status != types.AgentPromptAnswered ||
			prompt.AssistantMessageID != w.Run.MessageID ||
			prompt.SessionID != session.ID ||
			prompt.TenantID != w.Scope.TenantID ||
			prompt.UserID != delegatedPromptUser(w.Scope.UserID) {
			return result, errors.New("child approval is no longer resumable")
		}
		stopLease := s.broker.KeepLease(ctx, prompt.ID, prompt.OwnerInstance)
		defer stopLease()
		snapshot, getErr := s.broker.Suspensions().Get(ctx, prompt.AssistantMessageID)
		if getErr != nil {
			return result, getErr
		}
		if snapshot == nil || snapshot.PromptID != prompt.ID {
			return result, errors.New("child approval snapshot missing")
		}
		resume = &types.AgentResume{Snapshot: []byte(snapshot.Snapshot), Prompt: prompt}
		answer, err = s.messages.GetMessage(ctx, session.ID, w.Run.MessageID)
		if err != nil {
			return result, err
		}
		userMsg = &types.Message{ID: w.Run.UserMessageID}
		answer.RunState = ""
	} else {
		userMsg,
			err = s.messages.CreateMessage(ctx,
			&types.Message{
				SessionID:   session.ID,
				RequestID:   w.Run.ID,
				Role:        "user",
				Content:     w.Run.Query,
				IsCompleted: true,
				CreatedAt:   time.Now(),
				Channel:     types.ChannelWeb,
			})
		if err != nil {
			return result, err
		}
		answer,
			err = s.messages.CreateMessage(ctx,
			&types.Message{
				SessionID: session.ID,
				RequestID: w.Run.ID,
				Role:      "assistant",
				CreatedAt: time.Now(),
				Channel:   types.ChannelWeb,
			})
		if err != nil {
			return result, err
		}
		if err = s.Runtime.SetRunMessages(ctx, w, userMsg.ID, answer.ID); err != nil {
			return result, err
		}
	}
	ctx = hitl.WithDelegation(ctx, hitl.Delegation{
		Register: func(ctx context.Context, p *types.AgentPrompt) error {
			if p.TenantID != w.Scope.TenantID || p.UserID != delegatedPromptUser(w.Scope.UserID) ||
				p.SessionID != session.ID ||
				p.AssistantMessageID != answer.ID {
				return collaboration.ErrScope
			}
			return s.Runtime.AwaitPrompt(ctx, w, collaborationPromptState(p))
		},
		Check: func(ctx context.Context) error { return s.Runtime.CheckOwner(ctx, w) },
	})
	result.MessageID = answer.ID
	bus := event.NewEventBus()
	var finishStream func(context.Context) error
	if s.ChildEvents != nil && s.streams != nil {
		if err = s.streams.SetLiveRun(ctx, session.ID, answer.ID, answer.RequestID); err != nil {
			return result, err
		}
		defer func() {
			if err := s.streams.ClearLiveRun(context.WithoutCancel(ctx), session.ID, answer.ID); err != nil {
				logger.Warnf(ctx, "Failed to clear collaboration live stream: %v", err)
			}
		}()
		if resume != nil {
			if err = s.streams.ResetEvents(ctx, session.ID, answer.ID); err != nil {
				return result, err
			}
		}
		answer.Content = ""
		finishStream = s.ChildEvents(ctx, session, answer, bus)
		queryData := map[string]interface{}{
			"session_id": session.ID, "assistant_message_id": answer.ID, "user_message_id": userMsg.ID,
		}
		if resume != nil && resume.Prompt != nil {
			queryData["resume_pending_id"] = resume.Prompt.ID
			queryData["resume_tool_call_id"] = resume.Prompt.ToolCallID
		}
		if err = s.streams.AppendEvent(ctx, session.ID, answer.ID, interfaces.StreamEvent{
			ID: "query-" + answer.ID, Type: types.ResponseTypeAgentQuery, Done: true, Timestamp: time.Now(),
			Data: queryData,
		}); err != nil {
			return result, err
		}
	}
	var mu sync.Mutex
	var runErr error
	var suspended bool
	var completed bool
	bus.On(event.EventAgentComplete, func(_ context.Context, evt event.Event) error {
		data, ok := evt.Data.(event.AgentCompleteData)
		if !ok {
			return nil
		}
		mu.Lock()
		defer mu.Unlock()
		completed = true
		// The normal stream handler owns message assembly when enabled, including
		// references and artifacts. Do not append the final answer twice.
		if finishStream == nil {
			answer.Content = data.FinalAnswer
			answer.AgentDurationMs = data.TotalDurationMs
			if steps, ok := data.AgentSteps.([]types.AgentStep); ok {
				answer.AgentSteps = tools.SanitizeAgentStepsForStorage(steps)
			}
			if usage, ok := data.Usage.(*types.TokenUsage); ok {
				answer.Usage = usage
			}
			for _, ref := range data.KnowledgeRefs {
				if sr, ok := ref.(*types.SearchResult); ok {
					answer.KnowledgeReferences = append(answer.KnowledgeReferences, sr)
				}
			}
		}
		if suspension, ok := data.Suspended.(*types.PromptSuspension); ok && suspension != nil {
			suspended = true
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
	s.forwardPrompts(ctx, b, bus)
	s.trackProgress(b, bus)
	// Recover inside the transcript boundary so panic follows the same durable
	// completion path as a returned error. The scheduler must never replay it.
	err = func() (runErr error) {
		defer func() {
			if p := recover(); p != nil {
				runErr = fmt.Errorf("agent executor panicked: %v", p)
			}
		}()
		return s.sessions.AgentQA(
			ctx,
			&types.QARequest{
				Resume:             resume,
				Mode:               saved.runMode(),
				GrantSessionID:     w.Scope.SessionID,
				Suspendable:        true,
				Session:            session,
				Query:              w.Run.Query,
				AssistantMessageID: answer.ID,
				UserMessageID:      userMsg.ID,
				CustomAgent:        &saved.Agent,
				SummaryModelID:     saved.modelID(),
				WebSearchEnabled:   saved.Agent.Config.WebSearchEnabled,
				AssistantAccess:    saved.Access,
			},
			bus,
		)
	}()

	mu.Lock()
	if err == nil {
		err = runErr
	}
	if err == nil {
		err = ctx.Err()
	}
	missingCompletion := !completed
	mu.Unlock()
	// Startup failures can return before the engine emits its completion event.
	// Close the viewer's stream through the same handler, after persistence below.
	if finishStream != nil && missingCompletion {
		if err != nil {
			_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
				Type: event.EventError,
				Data: event.ErrorData{Error: err.Error(), Stage: "child_agent"},
			})
		}
		_ = bus.Emit(context.WithoutCancel(ctx), event.Event{
			Type: event.EventAgentComplete,
			Data: event.AgentCompleteData{MessageID: answer.ID},
		})
	}
	mu.Lock()
	defer mu.Unlock()
	result.ConsumedSequence = b.Seen
	result.Suspended = suspended
	answer.IsCompleted = !suspended
	answer.UpdatedAt = time.Now()
	if err != nil && answer.Content == "" {
		answer.Content = err.Error()
	}
	// Persist the complete transcript before the scheduler publishes completion.
	persistCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	stored, readErr := s.Runtime.Store.Read(persistCtx, w.Scope)
	if readErr != nil {
		return result, readErr
	}
	owned := false
	for _, m := range stored.Members {
		if m.ID == w.Member.ID {
			v := m.Runs[len(m.Runs)-1]
			owned = v.ID == w.Run.ID && v.Owner == w.Run.Owner
		}
	}
	if !owned {
		return result, collaboration.ErrConflict
	}
	if saveErr := s.messages.UpdateMessage(persistCtx, answer); saveErr != nil {
		return result, saveErr
	}
	if finishStream != nil {
		if streamErr := finishStream(persistCtx); streamErr != nil {
			return result, streamErr
		}
	}
	if resume != nil && !suspended {
		_ = s.broker.Suspensions().Delete(persistCtx, answer.ID, resume.Prompt.ID)
	}
	result.Answer = answer.Content
	result.Artifacts = answer.Artifacts.Live()
	if n := len(answer.AgentSteps); n > 0 {
		result.Truncated = answer.AgentSteps[n-1].Truncated
	}
	var evidence strings.Builder
	for _, step := range answer.AgentSteps {
		for _, call := range step.ToolCalls {
			if evidence.Len() > 6000 {
				break
			}
			success := call.Result != nil && call.Result.Success
			fmt.Fprintf(&evidence, "tool %s: success=%t\n", call.Name, success)
		}
	}
	for i, ref := range answer.KnowledgeReferences {
		if i >= 10 {
			break
		}
		if ref != nil {
			fmt.Fprintf(
				&evidence,
				"reference: %s (document %s, chunk %d)\n",
				ref.KnowledgeTitle,
				ref.KnowledgeID,
				ref.ChunkIndex,
			)
		}
	}
	result.Evidence = evidence.String()
	return result, err
}

// StopOwned is called only after the HTTP owner check or with a broker-owned
// prompt identity. Persist stop even when the parent process is gone.
func (s *AgentCollaboration) StopOwned(
	ctx context.Context,
	tenantID uint64,
	userID, sessionID, messageID string,
) error {
	var rows []collaboration.Record
	if err := s.db.WithContext(ctx).Where("tenant_id = ? AND user_id = ? AND session_id = ? AND status IN ?",
		tenantID,
		userID,
		sessionID,
		[]string{
			"active",
			"cancelling",
		}).Find(&rows).Error; err != nil {
		return err
	}
	for _, row := range rows {
		scope := collaboration.Scope{TenantID: tenantID, UserID: userID, SessionID: sessionID, ID: row.ID}
		if err := s.Runtime.CancelMessage(ctx, scope, messageID); err != nil {
			return err
		}
	}
	return nil
}

// Stop cancels the scoped collaboration for a session.
func (s *AgentCollaboration) Stop(ctx context.Context, sessionID, messageID string) error {
	caller := types.CallerFromContext(ctx)
	return s.StopOwned(ctx, caller.TenantID, caller.UserID, sessionID, messageID)
}
