package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// SandboxCheckpoint records the git commit an agent turn produced inside the
// session sandbox's /workspace. It is written on assistant messages only.
//
// SandboxID is stored alongside CommitSHA because commit hashes are only
// meaningful within one sandbox's repository. When a session's sandbox is
// reaped and rebuilt mid-conversation the in-sandbox git history restarts from
// zero, so every checkpoint written before the rebuild becomes unreachable.
// Comparing SandboxID against the session's current binding detects that
// purely from the database, without probing the sandbox.
type SandboxCheckpoint struct {
	SandboxID   string    `json:"sandbox_id"`
	CommitSHA   string    `json:"commit_sha"`
	CommittedAt time.Time `json:"committed_at"`
}

// Value implements the driver.Valuer interface for database serialization.
func (c SandboxCheckpoint) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface for database deserialization.
func (c *SandboxCheckpoint) Scan(value any) error {
	if value == nil {
		*c = SandboxCheckpoint{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("types: cannot scan sandbox checkpoint from unsupported type")
	}
	if len(b) == 0 {
		*c = SandboxCheckpoint{}
		return nil
	}
	return json.Unmarshal(b, c)
}

// ForkBootstrap carries the one-shot instructions a forked session needs the
// first time it provisions a sandbox: boot from SnapshotID instead of the
// config's template, then roll /workspace back to CommitSHA.
//
// It is consumed exactly once. After ConsumedAt is set the forked session is
// indistinguishable from an ordinary session — matching the durability
// semantics every session already has, where a reaped sandbox loses
// /workspace.
type ForkBootstrap struct {
	SnapshotID      string     `json:"snapshot_id"`
	CommitSHA       string     `json:"commit_sha"`
	SourceSandboxID string     `json:"source_sandbox_id"`
	CreatedAt       time.Time  `json:"created_at"`
	ConsumedAt      *time.Time `json:"consumed_at,omitempty"`
}

// Consumed reports whether the bootstrap has already been applied.
func (f ForkBootstrap) Consumed() bool {
	return f.ConsumedAt != nil
}

// Value implements the driver.Valuer interface for database serialization.
func (f ForkBootstrap) Value() (driver.Value, error) {
	return json.Marshal(f)
}

// Scan implements the sql.Scanner interface for database deserialization.
func (f *ForkBootstrap) Scan(value any) error {
	if value == nil {
		*f = ForkBootstrap{}
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("types: cannot scan fork bootstrap from unsupported type")
	}
	if len(b) == 0 {
		*f = ForkBootstrap{}
		return nil
	}
	return json.Unmarshal(b, f)
}

// ForkSnapshotLease is the durable record of a provider snapshot taken before
// the forked session row exists. CreateForked can fail or the process can
// crash in that window; the reaper scans these rows so a snapshot without a
// session is still collectable.
type ForkSnapshotLease struct {
	SnapshotID      string    `json:"snapshot_id" gorm:"column:snapshot_id;primaryKey;type:varchar(128)"`
	TenantID        uint64    `json:"tenant_id" gorm:"column:tenant_id"`
	SandboxConfigID string    `json:"sandbox_config_id" gorm:"column:sandbox_config_id;type:varchar(36)"`
	CreatedAt       time.Time `json:"created_at" gorm:"column:created_at"`
}

// TableName is the lease table. It is separate from sessions: a rolled-back
// CreateForked must not erase the only copy of the snapshot ID.
func (ForkSnapshotLease) TableName() string {
	return "fork_snapshot_leases"
}
