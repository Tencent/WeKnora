// Package types: sandbox backend config entity.
//
// A workspace holds several named sandbox configs so different agents can run
// on different backends (e.g. a big-memory E2B account for data analysis, a
// self-hosted Cube for everything else). The credential-bearing payload lives
// in Config, reusing TenantSandboxConfig's encrypted Value/Scan hooks.
package types

import (
	"time"

	"gorm.io/gorm"
)

// SandboxConfigIDGlobalDefault marks a sandbox created on the deployment-wide
// WEKNORA_SANDBOX_* configuration rather than on a stored config row.
//
// A sentinel rather than the empty string, so NULL on sessions.sandbox_config_id
// unambiguously means "this session has no live sandbox".
const SandboxConfigIDGlobalDefault = "-"

// SandboxCordonLease bounds how long a cordon is honoured. Identity changes
// take the cordon for the duration of two provider API calls; anything older
// is a crashed handler's leftover and must not wedge the config.
const SandboxCordonLease = 2 * time.Minute

// TenantSandboxConfigEntity is one named sandbox backend configuration.
type TenantSandboxConfigEntity struct {
	ID          string `gorm:"type:varchar(36);primaryKey"`
	TenantID    uint64 `gorm:"index"`
	Name        string `gorm:"type:varchar(255);not null"`
	Description string `gorm:"type:text"`

	// SandboxType is promoted out of Config so listing and cleanup decisions
	// do not have to decrypt and unmarshal the payload.
	SandboxType string `gorm:"type:varchar(32);not null"`

	Config *TenantSandboxConfig `gorm:"type:jsonb"`

	// CordonedAt is held while identity fields are being changed. See
	// IsCordoned: it is a lease, never a permanent lock.
	CordonedAt *time.Time

	CreatedAt time.Time
	UpdatedAt time.Time
	DeletedAt gorm.DeletedAt `gorm:"index"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (e *TenantSandboxConfigEntity) TableName() string {
	return "tenant_sandbox_configs"
}

// IsCordoned reports whether sandbox resolution must be refused for this
// config right now.
func (e *TenantSandboxConfigEntity) IsCordoned(now time.Time, lease time.Duration) bool {
	if e == nil || e.CordonedAt == nil {
		return false
	}
	return now.Sub(*e.CordonedAt) < lease
}
