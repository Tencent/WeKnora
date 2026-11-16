package types

import "time"

// SessionSandboxAssignment is the persisted placement of one session on remote
// sandboxes. Written once on first use by sandboxalloc; never rewritten except
// to release the session.
//
// Everything else about the placement is derived: the mode and workspace
// layout from MachineDigest (empty = a machine of the session's own), the owner
// of a shared machine from its sandbox_user_machines row, and the owner's
// eligibility from the session row.
type SessionSandboxAssignment struct {
	SessionID      string `gorm:"column:session_id;type:varchar(36);primaryKey"`
	ConfigTenantID uint64 `gorm:"column:config_tenant_id;not null;default:0"`
	ConfigID       string `gorm:"column:config_id;type:varchar(64);not null;default:''"`
	MachineDigest  string `gorm:"column:machine_digest;type:varchar(64);not null;default:''"`
	// ReleasedAt marks a deleted (retired) session. A row that has it may
	// never be placed again; a session retired before its first placement
	// gets such a row with an empty ConfigID (a tombstone).
	ReleasedAt *time.Time `gorm:"column:released_at"`
	// CleanupDueAt is set while the session's directory on a shared machine
	// still has to be removed, and is the time of the next attempt.
	CleanupDueAt *time.Time `gorm:"column:cleanup_due_at"`
}

// TableName is the assignments table.
func (SessionSandboxAssignment) TableName() string { return "session_sandbox_assignments" }

// IsTombstone reports a row written only to retire a session that was never
// placed. Real placements always carry a normalized, non-empty config id.
func (a *SessionSandboxAssignment) IsTombstone() bool {
	return a != nil && a.ConfigID == "" && a.ReleasedAt != nil
}

// SandboxUserMachine is the logical registry row of one shared machine: the
// identity behind its digest, and the empty-retention clock.
type SandboxUserMachine struct {
	MachineDigest  string     `gorm:"column:machine_digest;type:varchar(64);primaryKey"`
	OwnerTenantID  uint64     `gorm:"column:owner_tenant_id;not null"`
	OwnerID        string     `gorm:"column:owner_id;type:varchar(512);not null"`
	ConfigTenantID uint64     `gorm:"column:config_tenant_id;not null"`
	ConfigID       string     `gorm:"column:config_id;type:varchar(64);not null"`
	EmptySince     *time.Time `gorm:"column:empty_since"`
}

// TableName is the shared-machine registry table.
func (SandboxUserMachine) TableName() string { return "sandbox_user_machines" }
