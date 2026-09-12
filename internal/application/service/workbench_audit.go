package service

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

const workbenchAuditScope = "sandbox_workbench"

func redactWorkbenchCommand(_ string) string {
	// Shell syntax has too many credential forms to redact safely with a
	// denylist. Keep the command out of durable storage; execution_id remains
	// the correlation handle without exposing even a brute-forceable digest.
	return "[REDACTED]"
}

func workbenchCommandAuditDetails(command string) map[string]any {
	return map[string]any{
		"command":       redactWorkbenchCommand(command),
		"command_bytes": len(command),
	}
}

type workbenchAudit struct {
	service       *WorkbenchService
	identity      WorkbenchIdentity
	id, operation string
	started       time.Time
	once          sync.Once
	err           error
}

func (s *WorkbenchService) beginAudit(
	ctx context.Context,
	session *types.Session,
	operation string,
	details map[string]any,
) (*workbenchAudit, error) {
	if s.deps.Audit == nil {
		return nil, ErrWorkbenchAudit
	}
	a := &workbenchAudit{
		service:   s,
		identity:  WorkbenchIdentity{TenantID: session.TenantID, UserID: session.UserID, SessionID: session.ID},
		id:        uuid.NewString(),
		operation: operation,
		started:   time.Now(),
	}
	details["execution_id"] = a.id
	details["session_id"] = session.ID
	if err := a.write(ctx, types.AuditOutcomeAccepted, details); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *workbenchAudit) write(ctx context.Context, outcome types.AuditOutcome, details map[string]any) error {
	data, err := json.Marshal(details)
	if err != nil {
		return ErrWorkbenchAudit
	}
	entry := &types.AuditLog{
		TenantID:    a.identity.TenantID,
		ActorUserID: a.identity.UserID,
		Action:      types.AuditAction(workbenchAuditScope + "." + a.operation),
		ScopeType:   workbenchAuditScope,
		ScopeID:     a.identity.SessionID,
		TargetType:  "execution",
		TargetID:    a.id,
		Outcome:     outcome,
		Details:     types.JSON(data),
		CreatedAt:   time.Now().UTC(),
	}
	if err := a.service.deps.Audit.Create(ctx, entry); err != nil {
		return ErrWorkbenchAudit
	}
	return nil
}

func (a *workbenchAudit) finish(exitCode int, reason string, operationErr error) error {
	a.once.Do(func() {
		exit := NormalizeWorkbenchTerminalExit(
			sandbox.CommandTerminalExit{ExitCode: exitCode, Reason: reason},
			operationErr,
		)
		outcome := types.AuditOutcomeSuccess
		if operationErr != nil {
			outcome = types.AuditOutcome("unknown")
		} else if exitCode != 0 {
			outcome = types.AuditOutcomeFailed
		}
		// Disconnects must not cancel the terminal audit write. Never serialize
		// raw provider errors, output, stdin, tickets, environment or credentials.
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.err = a.write(ctx, outcome, map[string]any{
			"execution_id": a.id, "session_id": a.identity.SessionID,
			"duration_ms": time.Since(a.started).Milliseconds(), "exit_code": exit.ExitCode, "reason": exit.Reason,
		})
	})
	return a.err
}

func workbenchExitReason(reason string) string {
	switch reason {
	case "exited",
		"completed",
		"timeout",
		"interrupted",
		"canceled",
		"killed",
		"output_limit",
		"cpu_limit",
		"memory_limit",
		"closed",
		"lease_lost",
		"signal",
		"unknown",
		"error":
		return reason
	default:
		return "unknown"
	}
}

// NormalizeWorkbenchTerminalExit keeps known provider limit/revocation reasons
// even when cleanup also fails, without exposing arbitrary provider messages.
func NormalizeWorkbenchTerminalExit(exit sandbox.CommandTerminalExit, operationErr error) sandbox.CommandTerminalExit {
	exit.Reason = workbenchExitReason(exit.Reason)
	if operationErr != nil && (exit.Reason == "exited" || exit.Reason == "completed" || exit.Reason == "unknown") {
		exit.ExitCode, exit.Reason = -1, "unknown"
	}
	return exit
}

// Audit lists the caller's workbench records for one owned session.
func (s *WorkbenchService) Audit(
	ctx context.Context, sessionID string, afterID uint64, limit int,
) ([]*types.AuditLog, error) {
	session, err := s.authorizeIdentity(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	if s.deps.Audit == nil {
		return nil, ErrWorkbenchUnavailable
	}
	if limit <= 0 || limit > 100 {
		limit = 100
	}
	rows, err := s.deps.Audit.List(
		ctx,
		session.TenantID,
		&interfaces.AuditLogQuery{
			AfterID:     afterID,
			Limit:       limit,
			ScopeType:   workbenchAuditScope,
			ScopeID:     sessionID,
			ActorUserID: session.UserID,
		},
	)
	if err != nil {
		return nil, ErrWorkbenchUnavailable
	}
	result := make([]*types.AuditLog, 0, len(rows))
	for _, row := range rows {
		if row != nil && row.TenantID == session.TenantID && row.ScopeType == workbenchAuditScope &&
			row.ScopeID == sessionID &&
			row.ActorUserID == session.UserID && (afterID == 0 || row.ID < afterID) {
			result = append(result, row)
			if len(result) == limit {
				break
			}
		}
	}
	return result, nil
}

// WorkbenchExecution pairs a live command with its durable audit identity.
type WorkbenchExecution struct {
	Terminal    sandbox.CommandTerminal
	ExecutionID string
	audit       *workbenchAudit
}

// Finish records the sanitized command outcome once, after process cleanup.
func (e *WorkbenchExecution) Finish(exit sandbox.CommandTerminalExit, err error) error {
	return e.audit.finish(exit.ExitCode, exit.Reason, err)
}

// OpenTerminal authorizes and audits one bounded command before starting it.
func (s *WorkbenchService) OpenTerminal(
	ctx context.Context,
	sessionID string,
	request sandbox.CommandTerminalRequest,
) (*WorkbenchExecution, error) {
	if strings.TrimSpace(request.Command) == "" || len(request.Command) > WorkbenchMaxCommandBytes ||
		strings.ContainsRune(request.Command, 0) ||
		!utf8.ValidString(request.Command) ||
		request.Cols == 0 ||
		request.Rows == 0 ||
		request.Cols > 500 ||
		request.Rows > 500 {
		return nil, ErrWorkbenchInvalid
	}
	session, err := s.Authorize(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	mgr, err := s.manager(ctx, session.TenantID, session.SandboxConfigID)
	if err != nil {
		return nil, err
	}
	provider, ok := sandbox.CommandTerminalProviderFrom(mgr)
	if !ok {
		return nil, ErrWorkbenchCapability
	}
	audit, err := s.beginAudit(ctx, session, "command", workbenchCommandAuditDetails(request.Command))
	if err != nil {
		return nil, err
	}
	limits := DefaultWorkbenchLimits()
	request.Timeout = time.Duration(limits.CommandTimeoutSeconds) * time.Second
	request.CPUSeconds, request.MemoryBytes = limits.CPUSeconds, limits.MemoryBytes
	terminal, err := provider.OpenSessionCommandTerminal(
		types.WithSandboxTenantID(ctx, session.TenantID),
		sessionID,
		request,
	)
	if err != nil || terminal == nil {
		if terminal != nil {
			_ = terminal.Close()
		}
		if auditErr := audit.finish(-1, "unknown", ErrWorkbenchUnavailable); auditErr != nil {
			return nil, auditErr
		}
		return nil, ErrWorkbenchUnavailable
	}
	return &WorkbenchExecution{Terminal: terminal, ExecutionID: audit.id, audit: audit}, nil
}

// Files authorizes session-scoped file access and durably audits mutations.
func (s *WorkbenchService) Files(
	ctx context.Context,
	sessionID string,
	request sandbox.WorkbenchFileRequest,
) (*sandbox.WorkbenchFileResult, error) {
	switch request.Operation {
	case "list", "read", "write", "mkdir", "rename", "remove":
	default:
		return nil, ErrWorkbenchInvalid
	}
	if err := ValidateWorkbenchPath(request.Path, request.Operation == "list"); err != nil {
		return nil, err
	}
	if request.Operation == "rename" {
		if err := ValidateWorkbenchPath(request.NewPath, false); err != nil {
			return nil, err
		}
	}
	if len(request.Content) > WorkbenchMaxFileBytes {
		return nil, ErrWorkbenchInvalid
	}
	session, err := s.Authorize(ctx, sessionID)
	if err != nil {
		return nil, err
	}
	mgr, err := s.manager(ctx, session.TenantID, session.SandboxConfigID)
	if err != nil {
		return nil, err
	}
	provider, ok := sandbox.WorkbenchFileProviderFrom(mgr)
	if !ok {
		return nil, ErrWorkbenchCapability
	}
	var audit *workbenchAudit
	if request.Operation != "list" && request.Operation != "read" {
		audit, err = s.beginAudit(
			ctx,
			session,
			request.Operation,
			map[string]any{"path": request.Path, "new_path": request.NewPath},
		)
		if err != nil {
			return nil, err
		}
	}
	result, err := provider.WorkbenchFiles(types.WithSandboxTenantID(ctx, session.TenantID), sessionID, request)
	if result == nil && err == nil {
		err = ErrWorkbenchUnavailable
	}
	if audit != nil {
		if auditErr := audit.finish(0, "completed", err); auditErr != nil {
			return nil, auditErr
		}
	}
	if err != nil {
		return nil, err
	}
	if len(result.Content) > WorkbenchMaxFileBytes || len(result.Entries) > 500 {
		return nil, ErrWorkbenchUnavailable
	}
	return result, nil
}
