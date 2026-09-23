// Package service: session -> sandbox config pin.
//
// A session's remote sandbox is long-lived (created with onTimeout=pause, so
// its TTL pauses rather than destroys it), while the operations around it -
// skill execution, attachment upload, artifact collection, teardown - each
// resolve a backend independently at different points in time.
//
// If those points read "whichever config the agent points at right now", an
// admin re-pointing an agent mid-conversation would make artifact collection
// query the wrong account (artifacts silently vanish) and teardown fail
// (a paused sandbox nobody knows the ID of, billing forever).
//
// The pin records which config the session's CURRENT sandbox lives on, and
// which workspace owns that config. Both halves are needed: configs are keyed
// by (tenant, id), so an id alone does not name a backend. It is deliberately
// ephemeral: sessions outlive sandboxes by months, so a permanent pin would
// make "nothing references this config" never become true.
package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/sandbox"
	"github.com/Tencent/WeKnora/internal/types"
)

// SandboxPin addresses the backend a session's live sandbox runs on.
//
// ConfigID alone is not an address. tenant_sandbox_configs is keyed by
// (tenant_id, id), so the same id resolves in exactly one workspace - and a
// shared agent runs on ITS OWNER's config while the session belongs to the
// borrower. Reading the workspace from the ambient request context instead is
// what used to strand those MicroVMs: teardown, the terminal and desktop
// panels and fork snapshots all run outside the chat turn, where the ambient
// tenant is the borrower.
//
// Keeping the pair in one value is the point of the type: an API that can hand
// out a bare config id is an API where the two can drift apart again.
type SandboxPin struct {
	// ConfigID is the sandbox config, or "" when the session has no live
	// sandbox. types.SandboxConfigIDGlobalDefault means the deployment default.
	ConfigID string

	// TenantID owns ConfigID. Zero means the session's own workspace, which
	// covers every sandbox created by an agent that workspace owns as well as
	// pins written before the column existed.
	TenantID uint64
}

// IsZero reports whether the pin names no sandbox.
func (p SandboxPin) IsZero() bool { return strings.TrimSpace(p.ConfigID) == "" }

// TenantOr resolves the workspace to look ConfigID up in, falling back to the
// session's own tenant for pins that carry no owner.
func (p SandboxPin) TenantOr(sessionTenantID uint64) uint64 {
	if p.TenantID != 0 {
		return p.TenantID
	}
	return sessionTenantID
}

// SessionSandboxPinner is the only place session sandbox pins are read or
// written. Centralising it is what keeps the four entry points consistent.
type SessionSandboxPinner struct {
	db *gorm.DB
}

// NewSessionSandboxPinner returns a pinner over the sessions table.
func NewSessionSandboxPinner(db *gorm.DB) *SessionSandboxPinner {
	return &SessionSandboxPinner{db: db}
}

// Read returns the pinned backend, or the zero pin when the session has no
// live sandbox. Note the pin is an optimistic hint: the sandbox may have been
// reclaimed out of band (see Clear).
func (p *SessionSandboxPinner) Read(ctx context.Context, sessionID string) (SandboxPin, error) {
	pinned, _, err := p.read(ctx, sessionID)
	return pinned, err
}

// read additionally reports whether the session row exists at all, which Pin
// needs and Read deliberately hides: a caller asking "does this session have a
// live sandbox" gets the same "no" either way, but a caller that just created
// a sandbox must not mistake a vanished session for a clean pin.
//
// Soft-deleted sessions count as absent — GORM scopes them out — which is what
// we want, since deleting a session also destroys its sandbox.
func (p *SessionSandboxPinner) read(ctx context.Context, sessionID string) (SandboxPin, bool, error) {
	var row struct {
		SandboxConfigID       sql.NullString
		SandboxConfigTenantID sql.NullInt64
	}
	result := p.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("id = ?", sessionID).
		Select("sandbox_config_id", "sandbox_config_tenant_id").
		Scan(&row)
	if result.Error != nil {
		return SandboxPin{}, false, result.Error
	}
	if result.RowsAffected == 0 {
		return SandboxPin{}, false, nil
	}
	pin := SandboxPin{}
	if row.SandboxConfigID.Valid {
		pin.ConfigID = row.SandboxConfigID.String
	}
	// A negative value can only come from a hand-edited row; treat it the same
	// as an absent owner rather than wrapping it into a huge tenant id.
	if row.SandboxConfigTenantID.Valid && row.SandboxConfigTenantID.Int64 > 0 {
		pin.TenantID = uint64(row.SandboxConfigTenantID.Int64)
	}
	return pin, true, nil
}

// Pin claims the session for pin and returns the winning value.
//
// The conditional update is what makes concurrent first-sandbox creations
// safe: losers adopt the winner's backend instead of building a second sandbox
// on a second backend.
//
// A missing or soft-deleted session is an error rather than an empty result.
// Pin is called just after a sandbox was created, so answering the zero pin —
// which means "no live sandbox" everywhere else — would hide a real sandbox
// whose backend nobody recorded, leaving the orphan reaper as the only way
// back.
func (p *SessionSandboxPinner) Pin(
	ctx context.Context, sessionID string, pin SandboxPin,
) (SandboxPin, error) {
	pin.ConfigID = strings.TrimSpace(pin.ConfigID)
	if pin.ConfigID == "" {
		// No workspace config means sandbox execution is disabled, so there is
		// no remote resource whose backend needs pinning.
		return SandboxPin{}, nil
	}
	// Two attempts: a concurrent Clear can hand the row back unpinned between
	// our update and our read, and returning that empty value would be the
	// same silent lie as a missing session.
	for attempt := 0; attempt < 2; attempt++ {
		result := p.db.WithContext(ctx).
			Model(&types.Session{}).
			Where("id = ? AND (sandbox_config_id IS NULL OR sandbox_config_id = ?)",
				sessionID, "").
			Updates(map[string]any{
				"sandbox_config_id":        pin.ConfigID,
				"sandbox_config_tenant_id": pin.TenantID,
			})
		if result.Error != nil {
			return SandboxPin{}, result.Error
		}
		if result.RowsAffected > 0 {
			return pin, nil
		}
		pinned, found, err := p.read(ctx, sessionID)
		if err != nil {
			return SandboxPin{}, err
		}
		if !found {
			return SandboxPin{}, fmt.Errorf(
				"sandbox: pin session %s to config %s: %w",
				sessionID, pin.ConfigID, gorm.ErrRecordNotFound)
		}
		if !pinned.IsZero() {
			// Someone else pinned first; adopt their choice.
			return pinned, nil
		}
	}
	return SandboxPin{}, fmt.Errorf(
		"sandbox: pin session %s to config %s: lost the claim race twice",
		sessionID, pin.ConfigID)
}

// Clear releases the pin once the sandbox is gone, so the session's next
// sandbox follows its agent's current backend choice.
//
// Best effort by nature: when the reaper reclaims an orphan from the provider
// side the binding is already gone, so there is nothing to map back to a
// session and the pin is left stale. Readers must therefore treat a non-zero
// pin as a hint, not a guarantee that the sandbox still exists.
func (p *SessionSandboxPinner) Clear(ctx context.Context, sessionID string) error {
	return p.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("id = ?", sessionID).
		Updates(map[string]any{
			"sandbox_config_id":        nil,
			"sandbox_config_tenant_id": 0,
		}).Error
}

// recordOwner fills sandbox_config_tenant_id on a pin written before the
// column existed (value 0). It must not overwrite a recorded owner: the pin
// is sticky, and a later shared agent from a different workspace is not the
// owner of this config.
func (p *SessionSandboxPinner) recordOwner(
	ctx context.Context, sessionID string, tenantID uint64,
) error {
	if p == nil || p.db == nil || tenantID == 0 || strings.TrimSpace(sessionID) == "" {
		return nil
	}
	return p.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("id = ? AND sandbox_config_tenant_id = 0 AND sandbox_config_id IS NOT NULL AND sandbox_config_id <> ?",
			sessionID, "").
		Update("sandbox_config_tenant_id", tenantID).Error
}

// resolveSandboxForExecution resolves before pinning so non-remote workspace
// backends never leave a permanent session pin. Remote backends
// pin before their first Create; concurrent callers adopt and re-resolve the
// winning config before executing anything.
//
// tenantID is the workspace that owns agentConfigID, which for a shared agent
// is its owner rather than the session's. An existing pin carries its own
// owner and is resolved under that; tenantID only fills in for pins written
// before the owner was recorded.
func resolveSandboxForExecution(
	ctx context.Context,
	resolver sandbox.TenantSandboxResolver,
	fallback sandbox.Manager,
	pinner *SessionSandboxPinner,
	tenantID uint64,
	sessionID string,
	agentConfigID string,
	policy WorkspaceSandboxPolicy,
	opts ...resolveOption,
) (sandbox.Manager, SandboxPin, error) {
	o := applyResolveOptions(opts)

	if pinner != nil && strings.TrimSpace(sessionID) != "" {
		pinned, err := pinner.Read(ctx, sessionID)
		if err != nil {
			return nil, SandboxPin{}, err
		}
		if !pinned.IsZero() {
			owner := pinned.TenantOr(tenantID)
			mgr, err := resolveTenantSandboxForConfig(
				ctx, resolver, fallback, owner, pinned.ConfigID, policy,
			)
			// Only persist an owner that actually resolved this config. Writing
			// the caller's tenant on a failed lookup would stamp the wrong
			// workspace onto a sticky pin left by a previous shared agent.
			//
			// A nil error is not that proof on its own. resolveTenantSandboxForConfig
			// returns a disabled manager with no error before it ever reads the
			// config row: once for the workspace kill switch, once for an empty
			// or deployment-default config id. A named backend is the proof,
			// and it is the same condition that is allowed to CREATE a pin
			// below — what may write a pin may update whose it is.
			//
			// A config that exists but resolves to type "disabled" is skipped
			// too. Ownership is genuine there, but such a config never creates
			// a sandbox, so there is nothing whose teardown could go missing.
			proven := err == nil && mgr != nil &&
				sandbox.IsNamedSandboxBackendType(string(mgr.GetType()))
			if proven && pinned.TenantID == 0 && owner != 0 {
				if recErr := pinner.recordOwner(ctx, sessionID, owner); recErr != nil {
					logger.Warnf(ctx, "Failed to record sandbox pin owner for session %s: %v",
						sessionID, recErr)
				}
				pinned.TenantID = owner
			}
			return mgr, pinned, err
		}
	}

	configID := strings.TrimSpace(agentConfigID)
	if !hasNamedSandboxConfig(configID) {
		if workspaceScriptsDisabled(ctx, policy, tenantID) {
			return sandbox.NewDisabledManager(), SandboxPin{}, nil
		}
		if lite := liteHostSandbox(o.liteHost); lite != nil {
			return lite, SandboxPin{}, nil
		}
		return sandbox.NewDisabledManager(), SandboxPin{}, nil
	}

	pin := SandboxPin{ConfigID: configID, TenantID: tenantID}
	mgr, err := resolveTenantSandboxForConfig(
		ctx, resolver, fallback, tenantID, pin.ConfigID, policy,
	)
	if err != nil || mgr == nil {
		return mgr, pin, err
	}
	// Named backends (Cube, E2B, Docker) all keep a session-scoped sandbox.
	// Artifact collection and teardown resolve that sandbox from this pin, so
	// skipping Docker here leaves /workspace/output files uncollected.
	if !sandbox.IsNamedSandboxBackendType(string(mgr.GetType())) {
		return mgr, pin, nil
	}
	if pinner == nil || strings.TrimSpace(sessionID) == "" {
		return mgr, pin, nil
	}

	winner, err := pinner.Pin(ctx, sessionID, pin)
	if err != nil {
		return nil, SandboxPin{}, err
	}
	if winner == pin {
		return mgr, winner, nil
	}
	mgr, err = resolveTenantSandboxForConfig(
		ctx, resolver, fallback, winner.TenantOr(tenantID), winner.ConfigID, policy,
	)
	return mgr, winner, err
}

// sandboxConfigForExistingSandbox returns the backend an already-created
// sandbox lives on, or the zero pin when the session has none.
//
// Artifact collection and teardown deliberately never consult the agent: the
// agent's current choice may no longer describe the long-lived sandbox they are
// acting on.
func sandboxConfigForExistingSandbox(
	ctx context.Context,
	pinner *SessionSandboxPinner,
	sessionID string,
) (SandboxPin, error) {
	if pinner == nil || strings.TrimSpace(sessionID) == "" {
		return SandboxPin{}, nil
	}
	return pinner.Read(ctx, sessionID)
}
