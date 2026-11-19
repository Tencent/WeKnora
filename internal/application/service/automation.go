package service

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/hitl"
	"github.com/Tencent/WeKnora/internal/agent/policy"
	"github.com/Tencent/WeKnora/internal/automation"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/event"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// AutomationConfig bounds the scheduler and the automations it runs.
type AutomationConfig struct {
	// Tick is how often each instance looks for due work.
	Tick time.Duration
	// Lease is how long a running run stays owned without a heartbeat.
	Lease time.Duration
	// MaxConcurrent is how many runs one instance executes at once.
	MaxConcurrent int
	// MaxPerTenant caps the automations of one workspace.
	MaxPerTenant int64
	// Retention is how many runs each automation keeps.
	Retention int
	// MinInterval is the shortest gap allowed between two runs.
	MinInterval time.Duration
	// DefaultMaxDuration caps a run that sets no limit of its own.
	DefaultMaxDuration time.Duration
	// FailuresBeforePause pauses an automation after that many failed runs
	// in a row.
	FailuresBeforePause int
}

// DefaultAutomationConfig is used for every zero field.
var DefaultAutomationConfig = AutomationConfig{
	Tick:                15 * time.Second,
	Lease:               2 * time.Minute,
	MaxConcurrent:       4,
	MaxPerTenant:        100,
	Retention:           200,
	MinInterval:         automation.DefaultMinInterval,
	DefaultMaxDuration:  30 * time.Minute,
	FailuresBeforePause: 3,
}

const (
	maxAutomationDuration        = 4 * time.Hour
	minApprovalTimeoutMinutes    = 5
	maxApprovalTimeoutMinutes    = 7 * 24 * 60
	defaultApprovalTimeout       = 24 * 60
	maxAutomationInstructionRune = 20000
)

// AutomationService manages automations and runs them on schedule as their
// owners. No credential is stored: each run reloads and rechecks the owner.
type AutomationService struct {
	db       *gorm.DB
	agents   interfaces.CustomAgentService
	users    interfaces.UserService
	members  interfaces.TenantMemberService
	tenants  interfaces.TenantService
	messages interfaces.MessageService
	grants   interfaces.AgentGrantRepository
	notices  *NotificationService
	models   interfaces.ModelService

	sessions interfaces.SessionService
	broker   *hitl.Broker
	runs     *SuspendedRunService
	streams  interfaces.StreamManager
	// Events publishes a run to its message's event stream; see
	// AgentCollaboration.ChildEvents.
	Events func(context.Context, *types.Session, *types.Message, *event.EventBus) func(context.Context) error
	// StopDelegation fences children and discards recoverable parent waits.
	StopDelegation func(context.Context, uint64, string, string, string) error

	cfg      AutomationConfig
	instance string
	now      func() time.Time
	wake     chan struct{}
	slots    chan struct{}

	mu      sync.Mutex
	running map[string]context.CancelFunc
}

// NewAutomationService builds the service. Bind it before Start.
func NewAutomationService(
	db *gorm.DB,
	agents interfaces.CustomAgentService,
	users interfaces.UserService,
	members interfaces.TenantMemberService,
	tenants interfaces.TenantService,
	messages interfaces.MessageService,
	grants interfaces.AgentGrantRepository,
	notices *NotificationService,
	models interfaces.ModelService,
) *AutomationService {
	s := &AutomationService{
		db: db, agents: agents, users: users, members: members, tenants: tenants,
		messages: messages, grants: grants, notices: notices, models: models,
		instance: uuid.NewString(),
		now:      time.Now,
		wake:     make(chan struct{}, 1),
		running:  map[string]context.CancelFunc{},
	}
	s.Configure(AutomationConfig{})
	return s
}

// Configure overrides the defaults with cfg's non-zero fields.
func (s *AutomationService) Configure(cfg AutomationConfig) {
	d := DefaultAutomationConfig
	if cfg.Tick > 0 {
		d.Tick = cfg.Tick
	}
	if cfg.Lease > 0 {
		d.Lease = cfg.Lease
	}
	if cfg.MaxConcurrent > 0 {
		d.MaxConcurrent = cfg.MaxConcurrent
	}
	if cfg.MaxPerTenant > 0 {
		d.MaxPerTenant = cfg.MaxPerTenant
	}
	if cfg.Retention > 0 {
		d.Retention = cfg.Retention
	}
	if cfg.MinInterval > 0 {
		d.MinInterval = cfg.MinInterval
	}
	if cfg.DefaultMaxDuration > 0 {
		d.DefaultMaxDuration = cfg.DefaultMaxDuration
	}
	if cfg.FailuresBeforePause > 0 {
		d.FailuresBeforePause = cfg.FailuresBeforePause
	}
	s.cfg = d
	s.slots = make(chan struct{}, d.MaxConcurrent)
}

// Bind attaches what runs need once DI has built it.
func (s *AutomationService) Bind(
	sessions interfaces.SessionService, broker *hitl.Broker,
	runs *SuspendedRunService, streams interfaces.StreamManager,
) {
	s.sessions, s.broker, s.runs, s.streams = sessions, broker, runs, streams
}

// automationOwner is the signed-in web user an automation of ctx belongs to.
func automationOwner(ctx context.Context) (uint64, string, error) {
	principal, ok := types.PrincipalFromContext(ctx)
	caller := types.CallerFromContext(ctx)
	if _, scoped := types.TenantAPIKeyScopeFromContext(ctx); scoped || !ok ||
		principal.Type != types.PrincipalWebUser || types.ActingViaFromContext(ctx) != "" ||
		caller.TenantID == 0 || caller.UserID == "" || caller.UserID != principal.ID ||
		types.IsSyntheticUserID(caller.UserID) {
		return 0, "", apperrors.NewForbiddenError("automations belong to a signed-in user")
	}
	return caller.TenantID, caller.UserID, nil
}

func (s *AutomationService) owned(ctx context.Context) (*gorm.DB, error) {
	tenantID, userID, err := automationOwner(ctx)
	if err != nil {
		return nil, err
	}
	return s.db.WithContext(ctx).Where("tenant_id = ? AND owner_user_id = ?", tenantID, userID), nil
}

func (s *AutomationService) load(ctx context.Context, id string) (*types.Automation, error) {
	query, err := s.owned(ctx)
	if err != nil {
		return nil, err
	}
	var a types.Automation
	if err := query.Where("id = ?", id).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NewNotFoundError("automation not found")
		}
		return nil, err
	}
	return &a, nil
}

// List returns the caller's automations, newest first.
func (s *AutomationService) List(ctx context.Context) ([]*types.Automation, error) {
	query, err := s.owned(ctx)
	if err != nil {
		return nil, err
	}
	out := []*types.Automation{}
	if err := query.Order("created_at DESC").Find(&out).Error; err != nil {
		return nil, err
	}
	return out, s.populateActiveRunStatus(ctx, out)
}

// Get returns one of the caller's automations.
func (s *AutomationService) Get(ctx context.Context, id string) (*types.Automation, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	return a, s.populateActiveRunStatus(ctx, []*types.Automation{a})
}

// populateActiveRunStatus batches only the caller's already-authorized automations.
// Running or approval-waiting work takes precedence over a queued future run.
func (s *AutomationService) populateActiveRunStatus(ctx context.Context, automations []*types.Automation) error {
	if len(automations) == 0 {
		return nil
	}
	ids := make([]string, 0, len(automations))
	byID := make(map[string]*types.Automation, len(automations))
	for _, a := range automations {
		ids = append(ids, a.ID)
		byID[a.ID] = a
		a.ActiveRunStatus = ""
	}
	var runs []struct {
		AutomationID string
		Status       string
	}
	if err := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
		Select("automation_id, status").Distinct().
		Where("automation_id IN ? AND status IN ?", ids, []string{
			types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
		}).Find(&runs).Error; err != nil {
		return err
	}
	priority := map[string]int{
		types.AutomationRunQueued: 1, types.AutomationRunRunning: 2, types.AutomationRunAwaitingApproval: 3,
	}
	for _, run := range runs {
		a := byID[run.AutomationID]
		if priority[run.Status] > priority[a.ActiveRunStatus] {
			a.ActiveRunStatus = run.Status
		}
	}
	return nil
}

// FindBySession returns the caller's automation whose run, or persistent
// conversation, is sessionID.
func (s *AutomationService) FindBySession(ctx context.Context, sessionID string) (*types.Automation, error) {
	query, err := s.owned(ctx)
	if err != nil {
		return nil, err
	}
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, badAutomation("a session id is required")
	}
	var a types.Automation
	runs := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
		Select("automation_id").Where("session_id = ?", sessionID)
	err = query.Where("persistent_session_id = ? OR id IN (?)", sessionID, runs).First(&a).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewNotFoundError("automation not found")
	}
	return &a, err
}

func badAutomation(format string, args ...any) error {
	return apperrors.NewBadRequestError(fmt.Sprintf(format, args...))
}

func applyAutomationPatch(a *types.Automation, p *types.AutomationPatch) {
	if p.Name != nil {
		a.Name = strings.TrimSpace(*p.Name)
	}
	if p.Description != nil {
		a.Description = strings.TrimSpace(*p.Description)
	}
	if p.ModelID != nil {
		a.ModelID = strings.TrimSpace(*p.ModelID)
	}
	if p.Instruction != nil {
		a.Instruction = strings.TrimSpace(*p.Instruction)
	}
	if p.Trigger != nil {
		a.Trigger = *p.Trigger
	}
	if p.SessionMode != nil {
		a.SessionMode = *p.SessionMode
	}
	if p.CarryLastResult != nil {
		a.CarryLastResult = *p.CarryLastResult
	}
	if p.MisfirePolicy != nil {
		a.MisfirePolicy = *p.MisfirePolicy
	}
	if p.OverlapPolicy != nil {
		a.OverlapPolicy = *p.OverlapPolicy
	}
	if p.AccessLevel != nil {
		a.AccessLevel = *p.AccessLevel
	}
	if p.ApprovalTimeoutMinutes != nil {
		a.ApprovalTimeoutMinutes = *p.ApprovalTimeoutMinutes
	}
	if p.Limits != nil {
		a.Limits = *p.Limits
	}
	if p.NotifyOnSuccess != nil {
		a.NotifyOnSuccess = *p.NotifyOnSuccess
	}
}

// validate normalizes a and checks it can run; it returns its schedule.
func (s *AutomationService) validate(ctx context.Context, a *types.Automation) (*automation.Schedule, error) {
	if a.Name == "" || len([]rune(a.Name)) > 100 {
		return nil, badAutomation("name is required, at most 100 characters")
	}
	if a.Instruction == "" || len([]rune(a.Instruction)) > maxAutomationInstructionRune {
		return nil, badAutomation("instruction is required, at most %d characters", maxAutomationInstructionRune)
	}
	if _, err := s.runnableAssistant(ctx); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	if err := s.validateModel(ctx, a.ModelID, a.TenantID); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	sched, err := automation.Parse(a.Trigger)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	a.Trigger = sched.Trigger()
	now := s.now()
	if err := sched.CheckInterval(now, s.cfg.MinInterval); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	if a.Enabled && sched.Next(now) == nil {
		return nil, badAutomation("the trigger never fires again")
	}
	switch a.SessionMode {
	case "":
		a.SessionMode = types.AutomationSessionPerRun
	case types.AutomationSessionPerRun, types.AutomationSessionPersistent:
	default:
		return nil, badAutomation("session_mode must be per_run or persistent")
	}
	if a.MisfirePolicy == "" {
		a.MisfirePolicy = types.AutomationPolicySkip
	}
	if a.MisfirePolicy != types.AutomationPolicySkip && a.MisfirePolicy != types.AutomationPolicyRunOnce {
		return nil, badAutomation("misfire_policy must be skip or run_once")
	}
	if a.OverlapPolicy == "" {
		a.OverlapPolicy = types.AutomationPolicySkip
	}
	if a.OverlapPolicy != types.AutomationPolicySkip && a.OverlapPolicy != types.AutomationPolicyQueue {
		return nil, badAutomation("overlap_policy must be skip or queue")
	}
	if a.AccessLevel == "" {
		a.AccessLevel = types.AssistantAccessConfirm
	}
	if !a.AccessLevel.Valid() {
		return nil, badAutomation("access_level must be read_only, confirm or auto_write")
	}
	if a.ApprovalTimeoutMinutes == 0 {
		a.ApprovalTimeoutMinutes = defaultApprovalTimeout
	}
	if a.ApprovalTimeoutMinutes < minApprovalTimeoutMinutes || a.ApprovalTimeoutMinutes > maxApprovalTimeoutMinutes {
		return nil, badAutomation("approval_timeout_minutes must be between %d and %d",
			minApprovalTimeoutMinutes, maxApprovalTimeoutMinutes)
	}
	if m := a.Limits.MaxDurationMinutes; m < 0 || time.Duration(m)*time.Minute > maxAutomationDuration {
		return nil, badAutomation("limits.max_duration_minutes must be at most %d",
			int(maxAutomationDuration.Minutes()))
	}
	return sched, nil
}

// runnableAssistant loads the workspace assistant, the sole automation executor.
// Tasks may ask it to delegate work to other agents through the instruction.
func (s *AutomationService) runnableAssistant(ctx context.Context) (*types.CustomAgent, error) {
	assistant, err := s.agents.GetAgentByID(ctx, types.BuiltinWeKnoraAssistantID)
	if err != nil || assistant == nil || !types.GrantsOperationCatalog(assistant) || !assistant.IsAgentMode() {
		return nil, errors.New("the WeKnora assistant is unavailable in this workspace")
	}
	return assistant, nil
}

func (s *AutomationService) validateModel(ctx context.Context, id string, tenantID uint64) error {
	if id == "" {
		return nil
	}
	if s.models == nil {
		return errors.New("model service is unavailable")
	}
	model, err := s.models.GetModelByID(ctx, id)
	if err != nil || model == nil || model.DeletedAt.Valid ||
		(!model.IsBuiltin && model.TenantID != tenantID) ||
		model.Type != types.ModelTypeKnowledgeQA || model.Status != types.ModelStatusActive {
		return errors.New("the selected chat model is unavailable in this workspace")
	}
	return nil
}

// Create adds an automation owned by the caller.
func (s *AutomationService) Create(ctx context.Context, p *types.AutomationPatch) (*types.Automation, error) {
	tenantID, userID, err := automationOwner(ctx)
	if err != nil {
		return nil, err
	}
	var count int64
	if err := s.db.WithContext(ctx).Model(&types.Automation{}).
		Where("tenant_id = ?", tenantID).Count(&count).Error; err != nil {
		return nil, err
	}
	if count >= s.cfg.MaxPerTenant {
		return nil, apperrors.NewBadRequestError(
			fmt.Sprintf("a workspace can have at most %d automations", s.cfg.MaxPerTenant))
	}
	a := &types.Automation{TenantID: tenantID, OwnerUserID: userID, Enabled: true}
	applyAutomationPatch(a, p)
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	sched, err := s.validate(ctx, a)
	if err != nil {
		return nil, err
	}
	if a.Enabled {
		a.NextRunAt = sched.Next(s.now())
	}
	if err := s.db.WithContext(ctx).Create(a).Error; err != nil {
		return nil, err
	}
	return a, nil
}

var automationEditable = []string{
	"name", "description", "model_id", "instruction", "trigger_config", "session_mode", "carry_last_result",
	"misfire_policy", "overlap_policy", "access_level", "approval_timeout_minutes", "limits",
	"notify_on_success", "enabled", "next_run_at", "paused_reason", "consecutive_failures", "version",
	"updated_at",
}

// save writes a's editable fields if nobody changed it since it was read.
func (s *AutomationService) save(ctx context.Context, a *types.Automation) error {
	read := a.Version
	a.Version++
	a.UpdatedAt = s.now()
	res := s.db.WithContext(ctx).Model(a).Where("version = ?", read).Select(automationEditable).Updates(a)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return apperrors.NewConflictError("the automation changed meanwhile; reload and try again")
	}
	return nil
}

// Update changes the fields p carries.
func (s *AutomationService) Update(
	ctx context.Context, id string, p *types.AutomationPatch,
) (*types.Automation, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	before := a.Trigger
	applyAutomationPatch(a, p)
	enable := p.Enabled != nil && *p.Enabled && !a.Enabled
	if p.Enabled != nil {
		a.Enabled = *p.Enabled
	}
	sched, err := s.validate(ctx, a)
	if err != nil {
		return nil, err
	}
	triggerChanged, _ := json.Marshal(before)
	triggerNow, _ := json.Marshal(a.Trigger)
	switch {
	case !a.Enabled:
		a.NextRunAt = nil
	case enable || a.NextRunAt == nil || string(triggerChanged) != string(triggerNow):
		a.NextRunAt = sched.Next(s.now())
	}
	if enable {
		a.PausedReason, a.ConsecutiveFailures = "", 0
	}
	if err := s.save(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Pause stops scheduling; runs already going continue.
func (s *AutomationService) Pause(ctx context.Context, id string) (*types.Automation, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	if !a.Enabled {
		return a, nil
	}
	a.Enabled, a.NextRunAt = false, nil
	if err := s.save(ctx, a); err != nil {
		return nil, err
	}
	return a, nil
}

// Resume schedules it again from now, clearing a pause and its failures.
func (s *AutomationService) Resume(ctx context.Context, id string) (*types.Automation, error) {
	t := true
	return s.Update(ctx, id, &types.AutomationPatch{Enabled: &t})
}

// Delete stops an automation's runs and removes it with its conversations
// and standing grants.
func (s *AutomationService) Delete(ctx context.Context, id string) error {
	a, err := s.load(ctx, id)
	if err != nil {
		return err
	}
	var active []*types.AutomationRun
	if err := s.db.WithContext(ctx).Where("automation_id = ? AND status IN ?", a.ID, []string{
		types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
	}).Find(&active).Error; err != nil {
		return err
	}
	for _, run := range active {
		if err := s.cancelRun(ctx, run, "the automation was deleted"); err != nil {
			return err
		}
	}
	return s.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		sessionIDs := tx.Model(&types.AutomationRun{}).Select("session_id").
			Where("automation_id = ? AND session_id <> ''", a.ID)
		if err := tx.Where("tenant_id = ? AND kind = ? AND (id IN (?) OR id = ?)",
			a.TenantID, types.SessionKindAutomation, sessionIDs, a.PersistentSessionID).
			Delete(&types.Session{}).Error; err != nil {
			return err
		}
		if err := tx.Where("tenant_id = ? AND scope = ? AND scope_id = ?",
			a.TenantID, types.AgentGrantScopeAutomation, a.ID).Delete(&types.AgentGrant{}).Error; err != nil {
			return err
		}
		if err := tx.Where("automation_id = ? AND status NOT IN ?", a.ID, []string{
			types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
		}).Delete(&types.AutomationRun{}).Error; err != nil {
			return err
		}
		return tx.Delete(a).Error
	})
}

// ValidateTrigger checks t and returns its next n firing times.
func (s *AutomationService) ValidateTrigger(t types.AutomationTrigger, n int) ([]time.Time, error) {
	sched, err := automation.Parse(t)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	now := s.now()
	if err := sched.CheckInterval(now, s.cfg.MinInterval); err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	return sched.Upcoming(now, n), nil
}

// RunNow queues a manual run. It is refused while another run is queued,
// running or waiting.
func (s *AutomationService) RunNow(ctx context.Context, id string) (*types.AutomationRun, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	busy, err := s.hasLiveRun(s.db.WithContext(ctx), a.ID)
	if err != nil {
		return nil, err
	}
	if busy {
		return nil, apperrors.NewConflictError("a run of this automation is already queued or in progress")
	}
	run := &types.AutomationRun{
		AutomationID: a.ID, TenantID: a.TenantID, ScheduledAt: s.now().UTC().Truncate(time.Millisecond),
		TriggerKind: types.AutomationRunManual, Status: types.AutomationRunQueued,
	}
	if err := s.db.WithContext(ctx).Create(run).Error; err != nil {
		return nil, err
	}
	s.Wake()
	return run, nil
}

func (s *AutomationService) hasLiveRun(db *gorm.DB, automationID string) (bool, error) {
	var n int64
	err := db.Model(&types.AutomationRun{}).Where("automation_id = ? AND status IN ?", automationID, []string{
		types.AutomationRunQueued, types.AutomationRunRunning, types.AutomationRunAwaitingApproval,
	}).Count(&n).Error
	return n > 0, err
}

// ListRuns returns a page of an automation's runs, newest first.
func (s *AutomationService) ListRuns(
	ctx context.Context, id string, limit, offset int,
) ([]*types.AutomationRun, int64, error) {
	if _, err := s.load(ctx, id); err != nil {
		return nil, 0, err
	}
	if limit <= 0 || limit > 100 {
		limit = 20
	}
	query := s.db.WithContext(ctx).Model(&types.AutomationRun{}).Where("automation_id = ?", id)
	var total int64
	if err := query.Count(&total).Error; err != nil {
		return nil, 0, err
	}
	out := []*types.AutomationRun{}
	err := query.Order("scheduled_at DESC, created_at DESC").Limit(limit).Offset(offset).Find(&out).Error
	return out, total, err
}

// GetRun returns one of the caller's runs.
func (s *AutomationService) GetRun(ctx context.Context, runID string) (*types.AutomationRun, error) {
	var run types.AutomationRun
	if err := s.db.WithContext(ctx).Where("id = ?", runID).First(&run).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, apperrors.NewNotFoundError("run not found")
		}
		return nil, err
	}
	if _, err := s.load(ctx, run.AutomationID); err != nil {
		return nil, apperrors.NewNotFoundError("run not found")
	}
	return &run, nil
}

// CancelRun stops one of the caller's runs.
func (s *AutomationService) CancelRun(ctx context.Context, runID string) (*types.AutomationRun, error) {
	run, err := s.GetRun(ctx, runID)
	if err != nil {
		return nil, err
	}
	if types.AutomationRunFinished(run.Status) {
		return run, nil
	}
	if err := s.cancelRun(ctx, run, "cancelled by the owner"); err != nil {
		return nil, err
	}
	return s.GetRun(ctx, runID)
}

// cancelRun ends a queued or waiting run at once and asks a running one to
// stop at its next check.
func (s *AutomationService) cancelRun(ctx context.Context, run *types.AutomationRun, reason string) error {
	switch run.Status {
	case types.AutomationRunQueued, types.AutomationRunAwaitingApproval:
		ok, err := s.finishRun(ctx, run, []string{run.Status}, types.AutomationRunCancelled, reason, nil)
		if err != nil || !ok {
			return err
		}
		if run.PendingPromptID != "" {
			s.closePrompt(ctx, run.PendingPromptID, types.AgentPromptCanceled, reason)
		}
		return nil
	case types.AutomationRunRunning:
		if err := s.db.WithContext(ctx).Model(&types.AutomationRun{}).
			Where("id = ? AND status = ?", run.ID, types.AutomationRunRunning).
			Update("cancel_requested", true).Error; err != nil {
			return err
		}
		stopErr := s.stopRunDelegation(ctx, run)
		s.mu.Lock()
		cancel := s.running[run.ID]
		s.mu.Unlock()
		if cancel != nil {
			cancel()
		}
		return stopErr
	}
	return nil
}

func (s *AutomationService) stopRunDelegation(ctx context.Context, run *types.AutomationRun) error {
	if s.StopDelegation == nil {
		return nil
	}
	// The caller may hold the row from before the executor created its turn.
	var current types.AutomationRun
	if err := s.db.WithContext(ctx).Where("id = ?", run.ID).Take(&current).Error; err != nil {
		return err
	}
	if current.SessionID == "" || current.AssistantMessageID == "" {
		return nil
	}
	a, err := s.automationOf(ctx, &current)
	if err != nil {
		return err
	}
	return s.StopDelegation(ctx, a.TenantID, a.OwnerUserID, current.SessionID, current.AssistantMessageID)
}

// closePrompt ends the open prompt id a run waits on and completes the
// run's message, as the prompt reaper would.
func (s *AutomationService) closePrompt(ctx context.Context, id, status, reason string) {
	if s.broker == nil {
		return
	}
	ctx = context.WithoutCancel(ctx)
	row, err := s.broker.Store().Get(ctx, id)
	if err != nil || row == nil || (!row.IsOpen() && row.Status != types.AgentPromptAnswered) {
		return
	}
	ok, err := s.broker.Store().Transition(ctx, id, types.AgentPromptTransition{
		From:        []string{types.AgentPromptPending, types.AgentPromptSuspended, types.AgentPromptAnswered},
		To:          status,
		CloseReason: &reason,
	})
	if err != nil || !ok {
		return
	}
	state, _ := s.broker.Suspensions().Get(ctx, row.AssistantMessageID)
	if state == nil || state.PromptID != id {
		return
	}
	if s.runs != nil {
		_ = s.runs.Close(ctx, row, state, reason)
	}
	_ = s.broker.Suspensions().Delete(ctx, row.AssistantMessageID, id)
}

// AutomationGrant is a standing grant of an automation, with why it no
// longer applies when it does not.
type AutomationGrant struct {
	*types.AgentGrant
	Lapse string `json:"lapse,omitempty"`
}

// AutomationGrantCandidate is a call an automation's runs asked approval for
// that its owner may allow for future runs.
type AutomationGrantCandidate struct {
	PromptID    string    `json:"prompt_id"`
	GrantKey    string    `json:"grant_key"`
	Label       string    `json:"label"`
	Risk        string    `json:"risk"`
	Tool        string    `json:"tool"`
	ServiceName string    `json:"service_name,omitempty"`
	AskedAt     time.Time `json:"asked_at"`
}

// binding is what a's standing grants are given under right now.
func (s *AutomationService) binding(ctx context.Context, a *types.Automation) (string, error) {
	agent, err := s.runnableAssistant(ctx)
	if err != nil {
		return "", err
	}
	membership, err := s.members.GetMembership(ctx, a.OwnerUserID, a.TenantID)
	if err != nil {
		return "", err
	}
	if membership == nil {
		return "", errors.New("the owner is no longer a member of this workspace")
	}
	tenant, err := s.tenants.GetTenantByID(ctx, a.TenantID)
	if err != nil {
		return "", err
	}
	return automationBinding(agent, tenant, membership.Role), nil
}

func automationBinding(agent *types.CustomAgent, tenant *types.Tenant, role types.TenantRole) string {
	ceiling := agent.Config.AccessLevel
	if types.GrantsOperationCatalog(agent) {
		var cfg *types.AssistantConfig
		if tenant != nil {
			cfg = tenant.AssistantConfig
		}
		ceiling = cfg.EffectiveAccessLevel(types.AssistantAccessAutoWrite)
	}
	services := append([]string{"mode:" + agent.Config.MCPSelectionMode}, agent.Config.MCPServices...)
	return types.AutomationBinding{
		AgentID:       agent.ID,
		Tools:         agent.Config.AllowedTools,
		MCPServiceIDs: services,
		SystemPrompt:  agent.Config.SystemPrompt,
		OwnerRole:     role,
		Ceiling:       ceiling,
	}.Hash()
}

func automationGrantUser(userID string) string {
	return types.Principal{Type: types.PrincipalWebUser, ID: userID}.StorageID()
}

// Grants lists an automation's standing grants.
func (s *AutomationService) Grants(ctx context.Context, id string) ([]AutomationGrant, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	rows, err := s.grants.ListGrants(ctx, a.TenantID, automationGrantUser(a.OwnerUserID),
		types.AgentGrantScopeAutomation, a.ID)
	if err != nil {
		return nil, err
	}
	binding, bindErr := s.binding(ctx, a)
	out := make([]AutomationGrant, 0, len(rows))
	for _, g := range rows {
		if g.RevokedAt != nil {
			continue
		}
		lapse := types.AgentGrantLapseChanged
		if bindErr == nil {
			lapse = g.LapseAt(s.now(), binding)
		}
		out = append(out, AutomationGrant{AgentGrant: g, Lapse: lapse})
	}
	return out, nil
}

// GrantCandidates lists the calls the automation's runs asked approval for
// that are not allowed for future runs yet, newest first.
func (s *AutomationService) GrantCandidates(ctx context.Context, id string) ([]AutomationGrantCandidate, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	granted := map[string]bool{}
	if existing, err := s.Grants(ctx, id); err == nil {
		for _, g := range existing {
			if g.Lapse == "" {
				granted[g.GrantKey] = true
			}
		}
	}
	var prompts []*types.AgentPrompt
	if err := s.db.WithContext(ctx).
		Where("tenant_id = ? AND user_id = ? AND kind = ?", a.TenantID, automationGrantUser(a.OwnerUserID),
			types.AgentPromptKindToolApproval).
		Where("session_id IN (?)", s.db.Model(&types.AutomationRun{}).Select("session_id").
			Where("automation_id = ? AND session_id <> ''", a.ID)).
		Order("created_at DESC").Limit(200).Find(&prompts).Error; err != nil {
		return nil, err
	}
	out := []AutomationGrantCandidate{}
	for _, p := range prompts {
		c, ok := grantCandidate(p)
		if !ok || granted[c.GrantKey] {
			continue
		}
		granted[c.GrantKey] = true
		out = append(out, c)
	}
	return out, nil
}

func grantCandidate(p *types.AgentPrompt) (AutomationGrantCandidate, bool) {
	var req types.ToolApprovalPromptRequest
	if json.Unmarshal(p.Request, &req) != nil || req.Grant == "" {
		return AutomationGrantCandidate{}, false
	}
	risk := policy.Risk(req.GrantRisk)
	if risk != policy.RiskWrite && risk != policy.RiskDestructive {
		return AutomationGrantCandidate{}, false
	}
	tool := req.RegisteredToolName
	if req.MCPToolName != "" {
		tool = req.MCPToolName
	}
	return AutomationGrantCandidate{
		PromptID: p.ID, GrantKey: req.Grant, Label: req.GrantLabel, Risk: req.GrantRisk,
		Tool: tool, ServiceName: req.ServiceName, AskedAt: p.CreatedAt,
	}, true
}

// AddGrant lets future runs make the call an earlier run asked approval
// for in prompt promptID without asking. The grant stops applying when the
// agent's configuration or the owner's role changes.
func (s *AutomationService) AddGrant(ctx context.Context, id, promptID string) (*types.AgentGrant, error) {
	a, err := s.load(ctx, id)
	if err != nil {
		return nil, err
	}
	var prompt types.AgentPrompt
	err = s.db.WithContext(ctx).
		Where("id = ? AND tenant_id = ? AND user_id = ? AND kind = ?", promptID, a.TenantID,
			automationGrantUser(a.OwnerUserID), types.AgentPromptKindToolApproval).
		Where("session_id IN (?)", s.db.Model(&types.AutomationRun{}).Select("session_id").
			Where("automation_id = ? AND session_id <> ''", a.ID)).
		First(&prompt).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, apperrors.NewNotFoundError("approval not found among this automation's runs")
	}
	if err != nil {
		return nil, err
	}
	c, ok := grantCandidate(&prompt)
	if !ok {
		return nil, apperrors.NewBadRequestError("this call must be approved every time")
	}
	binding, err := s.binding(ctx, a)
	if err != nil {
		return nil, apperrors.NewBadRequestError(err.Error())
	}
	grant := &types.AgentGrant{
		TenantID: a.TenantID, UserID: automationGrantUser(a.OwnerUserID),
		Scope: types.AgentGrantScopeAutomation, ScopeID: a.ID, GrantKey: c.GrantKey,
		Label: c.Label, BindingHash: binding, Risk: c.Risk, PromptID: prompt.ID,
	}
	if err := s.grants.SaveGrant(ctx, grant); err != nil {
		return nil, err
	}
	return grant, nil
}

// Wake makes the scheduler look for work now.
func (s *AutomationService) Wake() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}
