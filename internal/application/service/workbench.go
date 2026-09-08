package service

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/application/repository"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/redis/go-redis/v9"
	"go.uber.org/dig"
	"gorm.io/gorm"
)

var (
	ErrWorkbenchDisabled    = errors.New("workbench is disabled")
	ErrWorkbenchDenied      = errors.New("active web user and workspace membership required")
	ErrWorkbenchSession     = errors.New("session not found")
	ErrWorkbenchPolicy      = errors.New("workspace scripts are disabled")
	ErrWorkbenchUnavailable = errors.New("workbench dependency unavailable")
	ErrWorkbenchUnbound     = errors.New("session has no sandbox configuration")
	ErrWorkbenchCapability  = errors.New("sandbox capability unavailable")
	ErrWorkbenchInvalid     = errors.New("invalid workbench request")
	ErrWorkbenchAudit       = errors.New("durable audit write failed; outcome may be unknown")
	ErrWorkbenchTicket      = errors.New("invalid or expired terminal ticket")
	ErrWorkbenchBusy        = errors.New("session console is already in use")
)

const (
	WorkbenchMaxFileBytes    = 8 << 20
	WorkbenchMaxCommandBytes = 8192
	WorkbenchMaxFrameBytes   = 16 << 10
	WorkbenchMaxOutputBytes  = 32 << 20
)

type WorkbenchLimits struct {
	CommandTimeoutSeconds int   `json:"command_timeout_seconds"`
	SessionTimeoutSeconds int   `json:"session_timeout_seconds"`
	CPUSeconds            int   `json:"cpu_seconds"`
	MemoryBytes           int64 `json:"memory_bytes"`
	MaxFileBytes          int64 `json:"max_file_bytes"`
	MaxOutputBytes        int64 `json:"max_output_bytes"`
	MaxFrameBytes         int64 `json:"max_frame_bytes"`
}

func DefaultWorkbenchLimits() WorkbenchLimits {
	return WorkbenchLimits{120, 1800, 60, 512 << 20, WorkbenchMaxFileBytes, WorkbenchMaxOutputBytes, WorkbenchMaxFrameBytes}
}

type WorkbenchStatus struct {
	Available    bool            `json:"available"`
	Reason       string          `json:"reason,omitempty"`
	ConfigID     string          `json:"config_id,omitempty"`
	Provider     string          `json:"provider,omitempty"`
	State        string          `json:"state"`
	Root         string          `json:"root"`
	Capabilities map[string]bool `json:"capabilities"`
	Limits       WorkbenchLimits `json:"limits"`
}

type WorkbenchServiceDeps struct {
	dig.In
	DB       *gorm.DB
	Redis    *redis.Client
	Sessions interfaces.SessionService
	Policy   WorkspaceSandboxPolicy
	Pinner   *SessionSandboxPinner
	Resolver sandbox.TenantSandboxResolver
	Configs  repository.TenantSandboxConfigRepository
	Audit    interfaces.AuditLogRepository
}

// WorkbenchService owns authorization and auditing. Provider managers and their
// shared configuration are never modified by workbench requests.
type WorkbenchService struct {
	deps    WorkbenchServiceDeps
	enabled bool
	store   workbenchStore
}

func NewWorkbenchService(deps WorkbenchServiceDeps) *WorkbenchService {
	s := &WorkbenchService{deps: deps, enabled: strings.EqualFold(os.Getenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED"), "true")}
	if deps.Redis != nil {
		namespace := strings.TrimSpace(os.Getenv("WEKNORA_REDIS_NAMESPACE"))
		if namespace == "" {
			namespace = "weknora"
		}
		s.store = &redisWorkbenchStore{client: deps.Redis, prefix: namespace + ":workbench:"}
	} else if strings.EqualFold(os.Getenv("WEKNORA_SANDBOX_WORKBENCH_SINGLE_INSTANCE"), "true") {
		s.store = newMemoryWorkbenchStore()
	}
	return s
}

func (s *WorkbenchService) Enabled() bool { return s != nil && s.enabled }

func (s *WorkbenchService) authorizeIdentity(ctx context.Context, sessionID string) (*types.Session, error) {
	if !s.Enabled() {
		return nil, ErrWorkbenchDisabled
	}
	// Do not use PrincipalFromContext's legacy UserID fallback on this surface.
	p, ok := ctx.Value(types.PrincipalContextKey).(types.Principal)
	uid, _ := types.UserIDFromContext(ctx)
	tid, _ := types.TenantIDFromContext(ctx)
	if !ok || p.Type != types.PrincipalWebUser || p.ID == "" || p.ID != uid || tid == 0 {
		return nil, ErrWorkbenchDenied
	}
	if _, apiKey := types.TenantAPIKeyScopeFromContext(ctx); apiKey {
		return nil, ErrWorkbenchDenied
	}
	if sessionID == "" || len(sessionID) > 128 {
		return nil, ErrWorkbenchSession
	}
	if s.deps.DB == nil || s.deps.Sessions == nil {
		return nil, ErrWorkbenchUnavailable
	}
	// Project only authorization fields, bypassing cached tenant objects and
	// all admin/home-tenant fallbacks in the general middleware.
	var user types.User
	if err := s.deps.DB.WithContext(ctx).Select("id", "is_active").Where("id = ?", uid).Take(&user).Error; err != nil {
		return nil, workbenchIdentityError(err)
	}
	if !user.IsActive {
		return nil, ErrWorkbenchDenied
	}
	var tenant types.Tenant
	if err := s.deps.DB.WithContext(ctx).Select("id", "status").Where("id = ?", tid).Take(&tenant).Error; err != nil {
		return nil, workbenchIdentityError(err)
	}
	if tenant.Status != "active" {
		return nil, ErrWorkbenchDenied
	}
	var member types.TenantMember
	if err := s.deps.DB.WithContext(ctx).Select("user_id", "tenant_id", "status").Where("user_id = ? AND tenant_id = ?", uid, tid).Take(&member).Error; err != nil {
		return nil, workbenchIdentityError(err)
	}
	if member.Status != types.TenantMemberStatusActive {
		return nil, ErrWorkbenchDenied
	}
	session, err := s.deps.Sessions.GetOwnedSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, apperrors.ErrSessionNotFound) || errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrWorkbenchSession
		}
		return nil, ErrWorkbenchUnavailable
	}
	if session == nil || session.ID != sessionID || session.TenantID != tid || session.UserID != uid || types.SessionRequiresAdminConsoleRead(session, session.IMPlatform) {
		return nil, ErrWorkbenchSession
	}
	var imCount int64
	if err := s.deps.DB.WithContext(ctx).Table("im_channel_sessions AS ics").Joins("JOIN sessions AS s ON s.id = ics.session_id").Where("s.tenant_id = ? AND ics.session_id = ?", tid, sessionID).Count(&imCount).Error; err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if imCount != 0 {
		return nil, ErrWorkbenchSession
	}
	return session, nil
}

func workbenchIdentityError(err error) error {
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return ErrWorkbenchDenied
	}
	return ErrWorkbenchUnavailable
}

// Authorize runs before socket authentication, on its five-second timer and
// when opening each command. A failed policy read must never become permission.
func (s *WorkbenchService) Authorize(ctx context.Context, sessionID string) (*types.Session, error) {
	session, err := s.authorizeIdentity(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if s.deps.Policy == nil {
		return nil, ErrWorkbenchUnavailable
	}
	disabled, err := s.deps.Policy.WorkspaceScriptsDisabled(ctx, session.TenantID)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if disabled {
		return nil, ErrWorkbenchPolicy
	}
	return session, nil
}

func (s *WorkbenchService) manager(ctx context.Context, tenantID uint64, configID string) (sandbox.Manager, error) {
	if configID == "" {
		return nil, ErrWorkbenchUnbound
	}
	if s.deps.Configs == nil || s.deps.Resolver == nil {
		return nil, ErrWorkbenchUnavailable
	}
	entity, err := s.deps.Configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if entity == nil || entity.TenantID != tenantID || entity.ID != configID || types.IsSandboxWorkspacePolicyRow(entity) {
		return nil, ErrWorkbenchCapability
	}
	if entity.IsCordoned(time.Now(), types.SandboxCordonLease) {
		return nil, ErrWorkbenchUnavailable
	}
	mgr, err := s.deps.Resolver.Resolve(ctx, tenantID, configID)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if mgr == nil {
		return nil, ErrWorkbenchCapability
	}
	return mgr, nil
}

func workbenchStatus(configID string, mgr sandbox.Manager, consoleAvailable bool) *WorkbenchStatus {
	_, terminal := sandbox.TerminalProviderFrom(mgr)
	_, files := sandbox.WorkbenchFileProviderFrom(mgr)
	terminal = terminal && consoleAvailable
	status := &WorkbenchStatus{ConfigID: configID, Available: terminal || files, State: "bound", Root: sandbox.SessionOutputRoot, Limits: DefaultWorkbenchLimits(), Capabilities: map[string]bool{"terminal": terminal, "files": files}}
	if mgr != nil {
		status.Provider = string(mgr.GetType())
	}
	if configID == "" {
		status.Reason = "unbound"
		status.State = "unbound"
	} else if !status.Available {
		status.Reason = "capability_unavailable"
	}
	return status
}

// Status only inspects the pin and capabilities, with no provider operation,
// health probe, or pin write that could allocate a sandbox.
func (s *WorkbenchService) Status(ctx context.Context, sessionID string) (*WorkbenchStatus, error) {
	session, err := s.Authorize(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.SandboxConfigID == "" {
		return workbenchStatus("", nil, s.store != nil), nil
	}
	mgr, err := s.manager(ctx, session.TenantID, session.SandboxConfigID)
	if err != nil {
		return nil, err
	}
	return workbenchStatus(session.SandboxConfigID, mgr, s.store != nil), nil
}

func (s *WorkbenchService) Bind(ctx context.Context, sessionID, configID string) (status *WorkbenchStatus, retErr error) {
	session, err := s.Authorize(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	configID = strings.TrimSpace(configID)
	if session.SandboxConfigID != "" {
		configID = session.SandboxConfigID
	}
	if configID == "" || len(configID) > 36 {
		return nil, ErrWorkbenchInvalid
	}
	mgr, err := s.manager(ctx, session.TenantID, configID)
	if err != nil {
		return nil, err
	}
	if s.deps.Pinner == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if _, ok := mgr.(sandbox.SessionWorkbenchInitializer); !ok {
		return nil, ErrWorkbenchCapability
	}
	audit, err := s.beginAudit(ctx, session, "bind", map[string]any{"config_id": configID})
	if err != nil {
		return nil, err
	}
	defer func() {
		if retErr != nil {
			if auditErr := audit.finish(-1, "unknown", retErr); auditErr != nil {
				retErr = auditErr
			}
		}
	}()
	// Authorize before the CAS, then recheck a concurrent winner by tenant.
	winner, err := s.deps.Pinner.Pin(ctx, sessionID, configID)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if winner != configID {
		mgr, err = s.manager(ctx, session.TenantID, winner)
		if err != nil {
			return nil, err
		}
	}
	initializer, ok := mgr.(sandbox.SessionWorkbenchInitializer)
	if !ok {
		return nil, ErrWorkbenchCapability
	}
	if err := initializer.EnsureWorkbenchSession(types.WithSandboxTenantID(ctx, session.TenantID), sessionID); err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if err := audit.finish(0, "completed", nil); err != nil {
		return nil, err
	}
	return workbenchStatus(winner, mgr, s.store != nil), nil
}

// ValidateWorkbenchPath does not normalize away invalid input. The provider
// additionally enforces descriptor-relative IO inside /workspace/output.
func ValidateWorkbenchPath(value string, allowRoot bool) error {
	if !utf8.ValidString(value) || len(value) > 4096 || strings.HasPrefix(value, "/") || strings.Contains(value, "\\") {
		return ErrWorkbenchInvalid
	}
	for _, r := range value {
		if unicode.IsControl(r) {
			return ErrWorkbenchInvalid
		}
	}
	if value == "" || value == "." {
		if allowRoot {
			return nil
		}
		return ErrWorkbenchInvalid
	}
	for _, part := range strings.Split(value, "/") {
		if part == "" || part == "." || part == ".." || len(part) > 255 {
			return ErrWorkbenchInvalid
		}
	}
	return nil
}
