// Package sandboxalloc decides and records how sessions are placed on remote
// sandboxes. It is the only writer of session_sandbox_assignments and, once
// wired, of the session's sandbox pin.
package sandboxalloc

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/sandbox/allocation"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"gorm.io/gorm"
)

// ConfigReader loads a sandbox config by its owning workspace.
type ConfigReader interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.TenantSandboxConfigEntity, error)
}

// SessionReader loads a session row.
type SessionReader interface {
	GetByID(ctx context.Context, tenantID uint64, id string) (*types.Session, error)
}

// Service is the allocation service.
type Service struct {
	repo      interfaces.SandboxAllocationRepository
	configs   ConfigReader
	sessions  SessionReader
	namespace string
	now       func() time.Time
}

// ErrMachineUnregistered is returned when a user-mode assignment names a
// machine whose registry row is gone (its config was deleted). The session
// cannot run, and must never be mistaken for one with a machine of its own.
var ErrMachineUnregistered = errors.New("sandboxalloc: shared machine is not registered")

// New wires the service. namespace must be the Redis binding namespace.
func New(
	repo interfaces.SandboxAllocationRepository, configs ConfigReader,
	sessions SessionReader, namespace string,
) *Service {
	return &Service{
		repo: repo, configs: configs, sessions: sessions, namespace: namespace,
		now: func() time.Time { return time.Now().UTC().Truncate(time.Microsecond) },
	}
}

// Namespace is the deployment namespace machine digests use.
func (s *Service) Namespace() string { return s.namespace }

// Digest is the shared-machine digest of m in this deployment.
func (s *Service) Digest(m allocation.UserMachine) string { return m.Digest(s.namespace) }

// Repo is the allocation repository.
func (s *Service) Repo() interfaces.SandboxAllocationRepository { return s.repo }

// Now is the clock placements and releases use.
func (s *Service) Now() time.Time { return s.now() }

// PlaceRequest names the session and the config address the caller resolved
// as its candidate.
type PlaceRequest struct {
	SessionTenantID uint64
	SessionID       string
	ConfigTenantID  uint64
	ConfigID        string
	Purpose         allocation.Purpose
}

const placeAttempts = 3

// Place returns the session's placement, recording it on first use together
// with the session's pin. The stored row always wins: neither the config
// switch, the candidate, nor the request identity can move a placed session.
func (s *Service) Place(ctx context.Context, req PlaceRequest) (allocation.Assignment, error) {
	a, _, err := s.place(ctx, req)
	return a, err
}

// place is Place that also reports why a placement it recorded differs from
// the config's mode. The reason is logged, not stored.
func (s *Service) place(
	ctx context.Context, req PlaceRequest,
) (allocation.Assignment, allocation.FallbackReason, error) {
	for attempt := 0; attempt < placeAttempts; attempt++ {
		existing, ok, err := s.Lookup(ctx, req.SessionID)
		if err != nil {
			return allocation.Assignment{}, "", err
		}
		if ok && existing.Released() {
			// Lookup keeps released rows for cleanup and inventory; nothing
			// may run on them again.
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: session %s is being deleted: %w",
				req.SessionID, sandbox.ErrSandboxSessionDeleted)
		}
		if ok {
			return existing, allocation.ReasonNone, nil
		}
		sess, err := s.sessions.GetByID(ctx, req.SessionTenantID, req.SessionID)
		if err != nil {
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: load session %s: %w", req.SessionID, err)
		}
		if sess == nil {
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: session %s not found", req.SessionID)
		}
		pin := interfaces.PlacementPin{ConfigID: sess.SandboxConfigID, TenantID: sess.SandboxConfigTenantID}
		a, reason, err := s.decide(ctx, req, sess, pin)
		if err != nil {
			return allocation.Assignment{}, "", err
		}
		row, machine := ToEntity(a, s.digestOf(a))
		winner, err := s.repo.ClaimPlacement(ctx, row, machine, pin)
		if errors.Is(err, interfaces.ErrPlacementConflict) {
			continue
		}
		if errors.Is(err, interfaces.ErrSessionRetired) {
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: session %s is being deleted: %w",
				req.SessionID, sandbox.ErrSandboxSessionDeleted)
		}
		if err != nil {
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: record placement: %w", err)
		}
		if winner.ConfigTenantID != row.ConfigTenantID || winner.ConfigID != row.ConfigID ||
			winner.MachineDigest != row.MachineDigest || winner.ReleasedAt != nil {
			// Someone else placed the session first; their row is the answer.
			got, _, err := s.Lookup(ctx, req.SessionID)
			return got, allocation.ReasonNone, err
		}
		logger.Infof(ctx, "[sandbox-alloc] session=%s mode=%s reason=%s config=%d/%s",
			a.SessionID, a.Mode(), reason, a.ConfigTenantID, a.ConfigID)
		return a, reason, nil
	}
	return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: placement of %s kept conflicting", req.SessionID)
}

// digestOf names a's shared machine, or "" for a machine of its own.
func (s *Service) digestOf(a allocation.Assignment) string {
	m, ok := a.UserMachine()
	if !ok {
		return ""
	}
	return s.Digest(m)
}

// decide picks the mode and config for a session without a placement. A
// session that already used a per-session sandbox — it has a pin, an
// unopened snapshot fork, or a checkpoint — stays per-session on that config.
func (s *Service) decide(
	ctx context.Context, req PlaceRequest, sess *types.Session, pin interfaces.PlacementPin,
) (allocation.Assignment, allocation.FallbackReason, error) {
	purpose := req.Purpose
	if purpose == "" {
		purpose = allocation.PurposeChat
	}
	if types.IsSkillMaintenanceDescription(sess.Description) {
		purpose = allocation.PurposeMaintenance
	}
	configID, configTenant := req.ConfigID, req.ConfigTenantID
	if pin.ConfigID != "" {
		configID = pin.ConfigID
		if pin.TenantID != 0 {
			configTenant = pin.TenantID
		} else if req.ConfigID != pin.ConfigID {
			configTenant = 0
		}
	}
	if configTenant == 0 {
		configTenant = sess.TenantID
	}
	configID = sandbox.NormalizeConfigID(configID)
	owner := ownerFromSession(sess)

	legacy := pin.ConfigID != "" || (sess.ForkBootstrap != nil && !sess.ForkBootstrap.Consumed())
	if !legacy {
		has, err := s.repo.HasSandboxCheckpoint(ctx, sess.ID)
		if err != nil {
			return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: check history of %s: %w", sess.ID, err)
		}
		legacy = has
	}
	decision := allocation.Decision{Mode: allocation.ModeSession, Reason: allocation.ReasonLegacySessionSandbox}
	if !legacy {
		mode, err := s.configMode(ctx, configTenant, configID)
		if err != nil {
			return allocation.Assignment{}, "", err
		}
		decision = allocation.Decide(allocation.DecideInput{
			ConfigMode:        mode,
			Purpose:           purpose,
			Owner:             owner,
			SessionID:         sess.ID,
			UserModeAvailable: allocation.UserModeAvailable(),
		})
	}
	a := allocation.Assignment{SessionID: sess.ID, ConfigTenantID: configTenant, ConfigID: configID}
	if decision.Mode != allocation.ModeUser {
		return a, decision.Reason, nil
	}
	a.Machine = allocation.UserMachine{
		OwnerTenantID: sess.TenantID, OwnerID: sess.UserID,
		ConfigTenantID: configTenant, ConfigID: configID,
	}
	if err := a.Machine.Validate(); err != nil {
		return allocation.Assignment{}, "", fmt.Errorf("sandboxalloc: %w", err)
	}
	return a, decision.Reason, nil
}

// configMode reads the config's switch. A missing row or an unparsable value
// is session: a failure to load must never widen sharing. A transport error
// is not a decision — the caller retries instead of storing session mode.
func (s *Service) configMode(ctx context.Context, tenantID uint64, configID string) (allocation.Mode, error) {
	if s.configs == nil || configID == "" || configID == types.SandboxConfigIDGlobalDefault {
		return allocation.ModeSession, nil
	}
	entity, err := s.configs.GetByID(ctx, tenantID, configID)
	if err != nil {
		return "", fmt.Errorf("sandboxalloc: read config %d/%s: %w", tenantID, configID, err)
	}
	if entity == nil || entity.Config == nil {
		return allocation.ModeSession, nil
	}
	mode, err := allocation.ParseMode(entity.Config.AllocationMode)
	if err != nil {
		return allocation.ModeSession, nil
	}
	return mode, nil
}

// Lookup returns the stored assignment without creating one. It always reads
// the database: callers use it to verify validity. A session retired before
// it was ever placed has no assignment.
func (s *Service) Lookup(ctx context.Context, sessionID string) (allocation.Assignment, bool, error) {
	if strings.TrimSpace(sessionID) == "" {
		return allocation.Assignment{}, false, nil
	}
	row, err := s.repo.GetAssignment(ctx, sessionID)
	if err != nil {
		return allocation.Assignment{}, false, fmt.Errorf("sandboxalloc: read assignment %s: %w", sessionID, err)
	}
	if row == nil || row.IsTombstone() {
		return allocation.Assignment{}, false, nil
	}
	var machine *types.SandboxUserMachine
	if row.MachineDigest != "" {
		machine, err = s.repo.GetUserMachine(ctx, row.MachineDigest)
		if err != nil {
			return allocation.Assignment{}, false, fmt.Errorf("sandboxalloc: read machine of %s: %w", sessionID, err)
		}
		if machine == nil {
			return allocation.Assignment{}, false, fmt.Errorf("%w: session %s", ErrMachineUnregistered, sessionID)
		}
	}
	return FromEntity(row, machine), true, nil
}

// Retire is the first step of deleting sessions (C5): one transaction, under
// the lock first placement takes, releases each session's assignment and
// schedules its workspace cleanup. A session without an assignment gets a
// tombstone, so no placement can land after the deletion began.
func (s *Service) Retire(ctx context.Context, sessionIDs []string) ([]interfaces.RetiredSession, error) {
	out, err := s.repo.RetireSessions(ctx, sessionIDs, s.now())
	if err != nil {
		return nil, fmt.Errorf("sandboxalloc: retire sessions: %w", err)
	}
	return out, nil
}

// Release retires one session.
func (s *Service) Release(ctx context.Context, sessionID string) (interfaces.RetiredSession, error) {
	if strings.TrimSpace(sessionID) == "" {
		return interfaces.RetiredSession{}, errors.New("sandboxalloc: release needs a session id")
	}
	out, err := s.Retire(ctx, []string{sessionID})
	if err != nil {
		return interfaces.RetiredSession{}, err
	}
	if len(out) != 1 {
		return interfaces.RetiredSession{}, fmt.Errorf(
			"sandboxalloc: retiring %s returned %d results", sessionID, len(out),
		)
	}
	return out[0], nil
}

// SessionAllocation implements sandbox.AllocationDirectory.
func (s *Service) SessionAllocation(ctx context.Context, sessionID string) (allocation.Assignment, bool, error) {
	return s.Lookup(ctx, sessionID)
}

// VerifySessionAllocation implements sandbox.AllocationDirectory (C4, C15,
// C17). The row must still be live and name the expected address. For a
// shared machine, the digest recomputed from the session's CURRENT owner must
// be the stored one — the digest binds the owner, so this is stricter than
// comparing a snapshot — and the session's current auth source must still
// qualify. A retired session's row is released (or a tombstone), so the
// release check covers retirement.
func (s *Service) VerifySessionAllocation(
	ctx context.Context, sessionTenantID uint64, sessionID string, expected allocation.Assignment,
) error {
	row, err := s.repo.GetAssignment(ctx, sessionID)
	if err != nil {
		return fmt.Errorf("sandboxalloc: read assignment %s: %w", sessionID, err)
	}
	if row == nil || row.ReleasedAt != nil {
		return sandbox.ErrSandboxSessionDeleted
	}
	if row.ConfigTenantID != expected.ConfigTenantID ||
		sandbox.NormalizeConfigID(row.ConfigID) != sandbox.NormalizeConfigID(expected.ConfigID) ||
		row.MachineDigest != s.digestOf(expected) {
		return fmt.Errorf("%w: session %s was re-addressed", sandbox.ErrUserAllocationRejected, sessionID)
	}
	sess, err := s.sessions.GetByID(ctx, sessionTenantID, sessionID)
	if errors.Is(err, gorm.ErrRecordNotFound) || errors.Is(err, apperrors.ErrSessionNotFound) ||
		(err == nil && sess == nil) {
		return sandbox.ErrSandboxSessionDeleted
	}
	if err != nil {
		return fmt.Errorf("sandboxalloc: read session %s: %w", sessionID, err)
	}
	if row.MachineDigest == "" {
		return nil
	}
	current := allocation.UserMachine{
		OwnerTenantID: sess.TenantID, OwnerID: sess.UserID,
		ConfigTenantID: row.ConfigTenantID, ConfigID: row.ConfigID,
	}
	if s.Digest(current) != row.MachineDigest {
		return fmt.Errorf("%w: session %s no longer belongs to the machine's owner",
			sandbox.ErrUserAllocationRejected, sessionID)
	}
	owner := ownerFromSession(sess)
	if !allocation.EligibleForUser(owner.Kind, owner.Source) {
		return sandbox.ErrUserAllocationRejected
	}
	return nil
}

// UserMachinesByConfig implements sandbox.AllocationDirectory.
func (s *Service) UserMachinesByConfig(
	ctx context.Context, configTenantID uint64, configID string,
) ([]allocation.UserMachine, error) {
	rows, err := s.repo.ListUserMachinesByConfig(ctx, configTenantID, configID)
	if err != nil {
		return nil, err
	}
	out := make([]allocation.UserMachine, 0, len(rows))
	for _, r := range rows {
		out = append(out, allocation.UserMachine{
			OwnerTenantID: r.OwnerTenantID, OwnerID: r.OwnerID,
			ConfigTenantID: r.ConfigTenantID, ConfigID: r.ConfigID,
		})
	}
	return out, nil
}

var _ sandbox.AllocationDirectory = (*Service)(nil)

// ForkAssignment is the child's assignment: same mode, machine and config,
// fresh session id. The caller persists it with the forked session.
func (s *Service) ForkAssignment(source allocation.Assignment, targetSessionID string) allocation.Assignment {
	child := source
	child.SessionID = targetSessionID
	child.ReleasedAt = nil
	return child
}
