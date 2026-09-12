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
)

// Workbench errors are safe sentinels shared with the HTTP and socket adapters.
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

// Workbench request and stream limits apply independently of provider limits.
const (
	WorkbenchMaxFileBytes    = 8 << 20
	WorkbenchMaxCommandBytes = 8192
	WorkbenchMaxFrameBytes   = 16 << 10
	WorkbenchMaxOutputBytes  = 32 << 20
)

// WorkbenchLimits describes the resource limits enforced on each console.
type WorkbenchLimits struct {
	CommandTimeoutSeconds int   `json:"command_timeout_seconds"`
	SessionTimeoutSeconds int   `json:"session_timeout_seconds"`
	CPUSeconds            int   `json:"cpu_seconds"`
	MemoryBytes           int64 `json:"memory_bytes"`
	// MemoryEnforcement describes both budgets using MemoryBytes, not a container quota.
	MemoryEnforcement string `json:"memory_enforcement"`
	MaxFileBytes      int64  `json:"max_file_bytes"`
	MaxOutputBytes    int64  `json:"max_output_bytes"`
	MaxFrameBytes     int64  `json:"max_frame_bytes"`
}

// DefaultWorkbenchLimits returns the server-enforced console limits.
func DefaultWorkbenchLimits() WorkbenchLimits {
	return WorkbenchLimits{
		CommandTimeoutSeconds: 120, SessionTimeoutSeconds: 1800,
		CPUSeconds: sandbox.DefaultTerminalCPUSeconds, MemoryBytes: sandbox.DefaultTerminalMemoryBytes,
		MemoryEnforcement: "per_process_as_and_aggregate_rss_sampled",
		MaxFileBytes:      WorkbenchMaxFileBytes, MaxOutputBytes: WorkbenchMaxOutputBytes,
		MaxFrameBytes: WorkbenchMaxFrameBytes,
	}
}

// WorkbenchStatus describes the session binding and available capabilities.
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

// WorkbenchServiceDeps supplies authorization, configuration and audit stores.
type WorkbenchServiceDeps struct {
	dig.In
	Authorization interfaces.WorkbenchAuthorizationRepository
	Redis         *redis.Client
	Sessions      interfaces.SessionService
	Users         interfaces.UserService
	Policy        WorkspaceSandboxPolicy
	Pinner        *SessionSandboxPinner
	Resolver      sandbox.TenantSandboxResolver
	Configs       repository.TenantSandboxConfigRepository
	Audit         interfaces.AuditLogRepository
}

// WorkbenchService owns authorization and auditing. Provider managers and their
// shared configuration are never modified by workbench requests.
type WorkbenchService struct {
	deps    WorkbenchServiceDeps
	enabled bool
	store   workbenchStore
}

// NewWorkbenchService enables workbench access only when explicitly configured.
func NewWorkbenchService(deps WorkbenchServiceDeps) *WorkbenchService {
	s := &WorkbenchService{
		deps: deps, enabled: strings.EqualFold(os.Getenv("WEKNORA_SANDBOX_WORKBENCH_ENABLED"), "true"),
	}
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

// Enabled reports whether this deployment opted into workbench access.
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
	if tokenID, ok := ctx.Value(workbenchTokenContextKey{}).(string); ok {
		if err := s.checkTerminalToken(ctx, tokenID, uid, false); err != nil {
			return nil, err
		}
	}
	if sessionID == "" || len(sessionID) > 128 {
		return nil, ErrWorkbenchSession
	}
	if s.deps.Authorization == nil || s.deps.Sessions == nil {
		return nil, ErrWorkbenchUnavailable
	}
	identity, err := s.deps.Authorization.GetAuthorization(ctx, tid, uid)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if identity == nil || identity.UserID != uid || identity.TenantID != tid ||
		!identity.UserActive || identity.TenantStatus != "active" ||
		identity.MemberStatus != types.TenantMemberStatusActive {
		return nil, ErrWorkbenchDenied
	}
	session, err := s.deps.Sessions.GetOwnedSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, apperrors.ErrSessionNotFound) {
			return nil, ErrWorkbenchSession
		}
		return nil, ErrWorkbenchUnavailable
	}
	if session == nil || session.ID != sessionID || session.TenantID != tid || session.UserID != uid ||
		types.SessionRequiresAdminConsoleRead(session, session.IMPlatform) {
		return nil, ErrWorkbenchSession
	}
	hasIM, err := s.deps.Authorization.HasIMSession(ctx, tid, sessionID)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	if hasIM {
		return nil, ErrWorkbenchSession
	}
	return session, nil
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

func (s *WorkbenchService) manager(
	ctx context.Context, tenantID uint64, configID string,
) (sandbox.Manager, error) {
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
	if entity == nil || entity.TenantID != tenantID || entity.ID != configID ||
		types.IsSandboxWorkspacePolicyRow(entity) {
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

func workbenchStatus(
	ctx context.Context, sessionID, configID string, mgr sandbox.Manager, consoleAvailable bool,
) (*WorkbenchStatus, error) {
	_, terminal := sandbox.CommandTerminalProviderFrom(mgr)
	_, files := sandbox.WorkbenchFileProviderFrom(mgr)
	terminal = terminal && consoleAvailable
	if reporter, ok := mgr.(sandbox.SessionWorkbenchRuntimeReporter); ok {
		runtime, err := reporter.WorkbenchRuntimeStatus(ctx, sessionID)
		if err != nil {
			return nil, ErrWorkbenchUnavailable
		}
		if runtime.Known {
			terminal = terminal && runtime.Terminal
			files = files && runtime.Files
		}
	}
	status := &WorkbenchStatus{
		ConfigID: configID, Available: terminal || files, State: "bound", Root: sandbox.SessionOutputRoot,
		Limits: DefaultWorkbenchLimits(), Capabilities: map[string]bool{"terminal": terminal, "files": files},
	}
	if mgr != nil {
		status.Provider = string(mgr.GetType())
	}
	if configID == "" {
		status.Reason = "unbound"
		status.State = "unbound"
	} else if !status.Available {
		status.Reason = "capability_unavailable"
	}
	return status, nil
}

// Status only inspects the pin and capabilities, with no provider operation,
// health probe, or pin write that could allocate a sandbox.
func (s *WorkbenchService) Status(ctx context.Context, sessionID string) (*WorkbenchStatus, error) {
	session, err := s.Authorize(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if session.SandboxConfigID == "" {
		return workbenchStatus(ctx, sessionID, "", nil, s.store != nil)
	}
	mgr, err := s.manager(ctx, session.TenantID, session.SandboxConfigID)
	if err != nil {
		return nil, err
	}
	sandboxCtx := types.WithSandboxTenantID(ctx, session.TenantID)
	return workbenchStatus(sandboxCtx, sessionID, session.SandboxConfigID, mgr, s.store != nil)
}

// Bind pins an authorized configuration and initializes its session workspace.
func (s *WorkbenchService) Bind(
	ctx context.Context, sessionID, configID string,
) (status *WorkbenchStatus, retErr error) {
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
	sandboxCtx := types.WithSandboxTenantID(ctx, session.TenantID)
	if err := initializer.EnsureWorkbenchSession(sandboxCtx, sessionID); err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	status, err = workbenchStatus(sandboxCtx, sessionID, winner, mgr, s.store != nil)
	if err != nil {
		return nil, err
	}
	if err := audit.finish(0, "completed", nil); err != nil {
		return nil, err
	}
	return status, nil
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
