package types

import (
	"database/sql/driver"
	"encoding/json"
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
// delete: either the whole sandbox config is removed, or the reaper has
// pruned a retired snapshot past its retention window.
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
	// CatalogID points at the tenant-level skill definition this install
	// was created from. Empty on rows that predate the catalog migration
	// and have not been backfilled yet.
	CatalogID string `gorm:"type:varchar(36);index"`

	// Name is the SKILL.md frontmatter name and the directory under
	// /opt/weknora/tenant/skills. Same-name reinstalls keep this value so the
	// image path stays stable.
	Name        string `gorm:"type:varchar(255);not null"`
	Version     string `gorm:"type:varchar(64)"`
	Description string `gorm:"type:text"`
	// Instructions is the SKILL.md body (level 2 disclosure).
	Instructions string `gorm:"type:text"`

	// BundleRef is a leftover locator from when each install owned a zip.
	// New installs leave this empty: the catalog row owns the archive, and
	// readers follow CatalogID. Older rows may still name an object.
	BundleRef    string `gorm:"type:varchar(1024)"`
	BundleSHA256 string `gorm:"type:varchar(64)"`

	// Enabled controls visibility to the agent only. The files stay in the
	// image either way; removal is a separate flow that rebuilds the snapshot.
	Enabled bool `gorm:"default:true"`

	// InstalledSnapshotID is the snapshot produced by this skill's install,
	// kept for audit and chain troubleshooting.
	InstalledSnapshotID string `gorm:"type:varchar(255)"`

	// InstallSessionID / InstallMessageID locate the installer agent's
	// transcript for the most recent install of this skill. A re-install
	// overwrites them: the previous run's conversation is superseded by the
	// one that produced the image now in service.
	InstallSessionID string `gorm:"type:varchar(36)"`
	InstallMessageID string `gorm:"type:varchar(36)"`

	// Envs is the installer agent's declaration of the environment variables
	// this skill needs, each optionally carrying a workspace-wide admin value.
	Envs SkillEnvVars `json:"envs,omitempty" gorm:"type:jsonb"`

	// Served is the version the live image still carries while a newer install
	// of this skill is in flight or has failed. The fields above describe the
	// latest install attempt from the moment it starts, but the image pointer
	// only moves when that attempt succeeds, so without this the agent would
	// lose a skill whose previous version is sitting in the image. Nil when
	// the row is ready (the fields above are what is served) or when nothing
	// of this skill ever reached the image.
	Served *SkillServedVersion `json:"served,omitempty" gorm:"type:jsonb"`

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

// ServedView is the row as the agent should see it: the row itself when it is
// ready, the version the image still carries while a newer install is in
// flight or has failed, and nil when the image serves nothing of this skill.
//
// The view is a copy marked ready, so every reader downstream of the usable
// set - discovery, instructions, resource files - answers from the served
// version without knowing an upgrade exists. Enabled and Envs stay the row's
// own: an administrator's switch and stored values apply to whichever version
// is running.
func (e *TenantSkillEntity) ServedView() *TenantSkillEntity {
	if e == nil {
		return nil
	}
	if e.Status == SkillStatusReady {
		return e
	}
	if e.Served == nil ||
		(e.Status != SkillStatusInstalling && e.Status != SkillStatusFailed) {
		return nil
	}
	view := *e
	view.Served = nil
	view.Version = e.Served.Version
	view.Description = e.Served.Description
	view.Instructions = e.Served.Instructions
	view.BundleSHA256 = e.Served.BundleSHA256
	view.BundleRef = e.Served.BundleRef
	view.InstalledSnapshotID = e.Served.SnapshotID
	view.Status = SkillStatusReady
	view.Error = ""
	return &view
}

// SkillServedVersion is what a row described when it was last ready, kept
// while a newer install of the same skill has not replaced it in the image.
type SkillServedVersion struct {
	Version      string `json:"version,omitempty"`
	Description  string `json:"description,omitempty"`
	Instructions string `json:"instructions,omitempty"`
	BundleSHA256 string `json:"bundle_sha256,omitempty"`
	// BundleRef names the archive the served files came from. It is resolved
	// when the version is recorded, including for a row that was following the
	// catalog, because the catalog may be re-registered while the upgrade
	// runs and the served files must stay readable after it is.
	BundleRef string `json:"bundle_ref,omitempty"`
	// SnapshotID is the image generation that put this version in. The reaper
	// compares it with the live chain to tell an upgrade that never moved the
	// pointer from one whose terminal write was lost.
	SnapshotID string `json:"snapshot_id,omitempty"`
}

// Value stores the served version as one JSON column.
func (v SkillServedVersion) Value() (driver.Value, error) {
	return json.Marshal(v)
}

// Scan reads the served version back.
func (v *SkillServedVersion) Scan(value interface{}) error {
	var b []byte
	switch raw := value.(type) {
	case nil:
		return nil
	case []byte:
		b = raw
	case string:
		b = []byte(raw)
	default:
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, v)
}

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

	// PlannedName is the name handed to CreateSnapshot. It is written before
	// the provider call, which is what makes an abandoned build identifiable:
	// SnapshotID can only be recorded once the provider has answered, so a
	// process that died in between left a snapshot the ledger could not name
	// and therefore could never reclaim. Matching is by name because only
	// Docker's ID is derivable from it; Cube and E2B mint their own and echo
	// the name back in the listing.
	PlannedName string `gorm:"type:varchar(255)"`

	Trigger string `gorm:"type:varchar(16)"`
	State   string `gorm:"type:varchar(16);index"`

	SupersededAt *time.Time
	CreatedAt    time.Time
	UpdatedAt    time.Time
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (e *TenantSkillSnapshotEntity) TableName() string { return "tenant_skill_snapshots" }

// TenantSkillCatalogEntity is one workspace skill definition. It does not
// belong to a sandbox: installations onto a config's image are TenantSkillEntity
// rows that point back here.
type TenantSkillCatalogEntity struct {
	ID           string `gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64
	Name         string `gorm:"type:varchar(255);not null"`
	Version      string `gorm:"type:varchar(64)"`
	Description  string `gorm:"type:text"`
	Instructions string `gorm:"type:text"`
	// BundleRef locates the one stored zip for this definition. Install rows
	// do not own a copy: sandbox uninstall must not delete this object.
	BundleRef    string `gorm:"type:varchar(1024)"`
	BundleSHA256 string `gorm:"type:varchar(64)"`
	CreatedAt    time.Time
	UpdatedAt    time.Time
	DeletedAt    gorm.DeletedAt
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (e *TenantSkillCatalogEntity) TableName() string { return "tenant_skill_catalog" }
