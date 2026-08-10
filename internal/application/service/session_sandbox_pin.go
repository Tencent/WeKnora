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
// The pin records which config the session's CURRENT sandbox lives on. It is
// deliberately ephemeral: sessions outlive sandboxes by months, so a permanent
// pin would make "nothing references this config" never become true.
package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/types"
)

// SessionSandboxPinner is the only place session sandbox pins are read or
// written. Centralising it is what keeps the four entry points consistent.
type SessionSandboxPinner struct {
	db *gorm.DB
}

// NewSessionSandboxPinner returns a pinner over the sessions table.
func NewSessionSandboxPinner(db *gorm.DB) *SessionSandboxPinner {
	return &SessionSandboxPinner{db: db}
}

// Read returns the pinned config ID, or "" when the session has no live
// sandbox. Note the pin is an optimistic hint: the sandbox may have been
// reclaimed out of band (see Clear).
func (p *SessionSandboxPinner) Read(ctx context.Context, sessionID string) (string, error) {
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
func (p *SessionSandboxPinner) read(ctx context.Context, sessionID string) (string, bool, error) {
	var pinned sql.NullString
	result := p.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("id = ?", sessionID).
		Select("sandbox_config_id").
		Scan(&pinned)
	if result.Error != nil {
		return "", false, result.Error
	}
	if result.RowsAffected == 0 {
		return "", false, nil
	}
	if !pinned.Valid {
		return "", true, nil
	}
	return pinned.String, true, nil
}

// Pin claims the session for configID and returns the winning value.
//
// The conditional update is what makes concurrent first-sandbox creations
// safe: losers adopt the winner's config instead of building a second sandbox
// on a second backend.
//
// A missing or soft-deleted session is an error rather than an empty result.
// Pin is called just after a sandbox was created, so answering "" — which
// means "no live sandbox" everywhere else — would hide a real sandbox whose
// backend nobody recorded, leaving the orphan reaper as the only way back.
func (p *SessionSandboxPinner) Pin(
	ctx context.Context, sessionID, configID string,
) (string, error) {
	configID = strings.TrimSpace(configID)
	if configID == "" {
		configID = types.SandboxConfigIDGlobalDefault
	}
	// Two attempts: a concurrent Clear can hand the row back unpinned between
	// our update and our read, and returning that empty value would be the
	// same silent lie as a missing session.
	for attempt := 0; attempt < 2; attempt++ {
		result := p.db.WithContext(ctx).
			Model(&types.Session{}).
			Where("id = ? AND (sandbox_config_id IS NULL OR sandbox_config_id = ?)",
				sessionID, "").
			Update("sandbox_config_id", configID)
		if result.Error != nil {
			return "", result.Error
		}
		if result.RowsAffected > 0 {
			return configID, nil
		}
		pinned, found, err := p.read(ctx, sessionID)
		if err != nil {
			return "", err
		}
		if !found {
			return "", fmt.Errorf(
				"sandbox: pin session %s to config %s: %w",
				sessionID, configID, gorm.ErrRecordNotFound)
		}
		if pinned != "" {
			// Someone else pinned first; adopt their choice.
			return pinned, nil
		}
	}
	return "", fmt.Errorf(
		"sandbox: pin session %s to config %s: lost the claim race twice",
		sessionID, configID)
}

// Clear releases the pin once the sandbox is gone, so the session's next
// sandbox follows its agent's current backend choice.
//
// Best effort by nature: when the reaper reclaims an orphan from the provider
// side the binding is already gone, so there is nothing to map back to a
// session and the pin is left stale. Readers must therefore treat a non-empty
// pin as a hint, not a guarantee that the sandbox still exists.
func (p *SessionSandboxPinner) Clear(ctx context.Context, sessionID string) error {
	return p.db.WithContext(ctx).
		Model(&types.Session{}).
		Where("id = ?", sessionID).
		Update("sandbox_config_id", nil).Error
}

// sandboxConfigForExecution decides which config a session's sandbox runs on.
//
// Skill execution and attachment staging can be the first operation that creates
// a session sandbox, so they adopt an existing pin and otherwise claim the
// agent's current choice. Later operations must follow the pin instead.
func sandboxConfigForExecution(
	ctx context.Context,
	pinner *SessionSandboxPinner,
	sessionID string,
	agentConfigID string,
) (string, error) {
	if pinner == nil || strings.TrimSpace(sessionID) == "" {
		return agentConfigID, nil
	}
	pinned, err := pinner.Read(ctx, sessionID)
	if err != nil {
		return "", err
	}
	if pinned != "" {
		return pinned, nil
	}
	return pinner.Pin(ctx, sessionID, agentConfigID)
}

// sandboxConfigForExistingSandbox returns the config an already-created
// sandbox lives on, or "" when the session has none.
//
// Artifact collection and teardown deliberately never consult the agent: the
// agent's current choice may no longer describe the long-lived sandbox they are
// acting on.
func sandboxConfigForExistingSandbox(
	ctx context.Context,
	pinner *SessionSandboxPinner,
	sessionID string,
) (string, error) {
	if pinner == nil || strings.TrimSpace(sessionID) == "" {
		return "", nil
	}
	return pinner.Read(ctx, sessionID)
}
