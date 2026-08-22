package types

import (
	"time"

	"gorm.io/gorm"
)

// Skill install lifecycle. removing is separate from installing so the reaper
// can tell "never finished installing" from "never finished being removed".
const (
	SkillStatusInstalling = "installing"
	SkillStatusReady      = "ready"
	SkillStatusFailed     = "failed"
	SkillStatusRemoving   = "removing"
)

// Snapshot ledger states. deleted is written only after a real provider-side
// delete, which today happens only when the whole sandbox config is removed.
const (
	SkillSnapshotStateBuilding   = "building"
	SkillSnapshotStateActive     = "active"
	SkillSnapshotStateSuperseded = "superseded"
	SkillSnapshotStateDeleted    = "deleted"
)

const (
	SkillSnapshotTriggerInstall = "install"
	SkillSnapshotTriggerRemove  = "remove"
	SkillSnapshotTriggerRebuild = "rebuild"
)

// SkillMaintenanceSessionMarker tags the sessions that skill image operations
// run in. Those sessions carry a real agent transcript that is deliberately
// kept for troubleshooting, but they are infrastructure rather than
// conversations, so the console must never list them. This mirrors how embed
// sessions are classified (EmbedSessionMarkerPrefix): a description prefix,
// so no schema change is needed.
const SkillMaintenanceSessionMarker = "skill_maintenance:"

// TenantSkillEntity is one skill installed onto one sandbox config.
//
// There is deliberately no entry_script / interpreter / smoke_command column:
// a skill may ship several executables across languages, so none of those is a
// single value. The interpreter is derived from the on-image path convention at
// execution time instead (see skills.Manager).
type TenantSkillEntity struct {
	// ID is the row key. It is not the directory name inside the image.
	ID              string `gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64
	SandboxConfigID string `gorm:"type:varchar(36)"`

	// Name is the SKILL.md frontmatter name and the directory under
	// /opt/weknora/tenant/skills. Same-name reinstalls keep this value so the
	// image path stays stable.
	Name        string `gorm:"type:varchar(255);not null"`
	Version     string `gorm:"type:varchar(64)"`
	Description string `gorm:"type:text"`
	// Instructions is the SKILL.md body (level 2 disclosure).
	Instructions string `gorm:"type:text"`

	// BundleRef locates the uploaded archive in the tenant's file service.
	BundleRef    string `gorm:"type:varchar(1024)"`
	BundleSHA256 string `gorm:"type:varchar(64)"`

	// Enabled controls visibility to the agent only. The files stay in the
	// image either way; removal is a separate flow that rebuilds the snapshot.
	Enabled bool `gorm:"default:true"`

	// InstalledSnapshotID is the snapshot produced by this skill's install,
	// kept for audit and chain troubleshooting.
	InstalledSnapshotID string `gorm:"type:varchar(255)"`

	Status string `gorm:"type:varchar(32);not null"`
	Error  string `gorm:"type:text"`
	// InstallingSince drives the stuck-run reaper for both install and remove.
	InstallingSince *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (e *TenantSkillEntity) TableName() string { return "tenant_skills" }

// TenantSkillSnapshotEntity is the image-chain ledger. It exists because
// snapshots are billable provider resources whose IDs we hand out: we must be
// able to say which generation came from which parent, and never leave a
// snapshot nobody knows about.
type TenantSkillSnapshotEntity struct {
	// ID is the install ID; it also seeds the snapshot name.
	ID              string `gorm:"type:varchar(36);primaryKey"`
	TenantID        uint64 `gorm:"index"`
	SandboxConfigID string `gorm:"type:varchar(36);index"`
	SkillID         string `gorm:"type:varchar(36);index"`

	SnapshotID       string `gorm:"type:varchar(255)"`
	ParentSnapshotID string `gorm:"type:varchar(255)"`
	Generation       int

	Trigger string `gorm:"type:varchar(16)"`
	State   string `gorm:"type:varchar(16);index"`

	SupersededAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (e *TenantSkillSnapshotEntity) TableName() string { return "tenant_skill_snapshots" }
