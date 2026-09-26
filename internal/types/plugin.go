package types

import "time"

// Desired states of an installed plugin.
const (
	PluginStateEnabled  = "enabled"
	PluginStateDisabled = "disabled"
)

// InstalledPlugin is a non-builtin plugin installed on the platform. Every
// node converges to these rows: an enabled plugin's active version is loaded,
// anything else is unloaded.
type InstalledPlugin struct {
	ID string `json:"id" gorm:"type:varchar(128);primaryKey"`
	// OwnerTenantID is nil for platform plugins a system admin installed.
	OwnerTenantID *uint64 `json:"owner_tenant_id,omitempty"`
	// Source records where the package came from: {"kind":"upload"} or
	// {"kind":"url","url":"..."}.
	Source        JSON   `json:"source" gorm:"type:json"`
	ActiveVersion string `json:"active_version" gorm:"type:varchar(64)"`
	DesiredState  string `json:"desired_state" gorm:"type:varchar(16)"`
	Runtime       string `json:"runtime" gorm:"type:varchar(32)"`
	// GrantedPerms is the manifest permissions an administrator accepted.
	GrantedPerms JSON `json:"granted_perms" gorm:"type:json"`
	SystemConfig JSON `json:"system_config,omitempty" gorm:"type:json"`
	// RemoteURL is where a remote plugin is served; RemoteSecret (sealed)
	// signs the calls to it. Other runtimes leave both empty.
	RemoteURL    string    `json:"remote_url,omitempty" gorm:"type:varchar(1024)"`
	RemoteSecret string    `json:"-"                    gorm:"type:text"`
	CreatedBy    string    `json:"created_by" gorm:"type:varchar(36)"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (InstalledPlugin) TableName() string { return "plugins" }

// PluginVersion is one stored package version of an installed plugin.
type PluginVersion struct {
	PluginID string `json:"plugin_id" gorm:"type:varchar(128);primaryKey"`
	Version  string `json:"version" gorm:"type:varchar(64);primaryKey"`
	// Digest is "sha256:<hex>" of the package archive.
	Digest   string `json:"digest" gorm:"type:varchar(80)"`
	Manifest JSON   `json:"manifest" gorm:"type:json"`
	// PackageURI is where the archive is stored (FileService path).
	PackageURI string    `json:"-" gorm:"type:varchar(1024)"`
	Size       int64     `json:"size"`
	CreatedBy  string    `json:"created_by" gorm:"type:varchar(36)"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (PluginVersion) TableName() string { return "plugin_versions" }

// PluginKV is one entry of a plugin's key-value store (Host API kv),
// partitioned by plugin and tenant.
type PluginKV struct {
	PluginID  string     `json:"-"         gorm:"type:varchar(128);primaryKey"`
	TenantID  uint64     `json:"-"         gorm:"primaryKey"`
	Key       string     `json:"key"       gorm:"type:varchar(256);primaryKey"`
	Value     JSON       `json:"value"     gorm:"type:json"`
	ExpiresAt *time.Time `json:"expiresAt,omitempty"`
	UpdatedAt time.Time  `json:"updatedAt"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (PluginKV) TableName() string { return "plugin_kv" }

// PluginOAuthConnection is an authorization a plugin form field made with
// its OAuth button (x-oauth). The field holds "oauth:<ID>"; the tokens stay
// here, sealed (enc:v1), and never reach the browser.
type PluginOAuthConnection struct {
	ID       string `json:"id"       gorm:"type:varchar(36);primaryKey"`
	PluginID string `json:"pluginId" gorm:"type:varchar(128);index:idx_plugin_oauth_connections_owner"`
	// TenantID is 0 for the platform (system configuration).
	TenantID uint64 `json:"-"        gorm:"index:idx_plugin_oauth_connections_owner"`
	// Scope, Contribution and Field find the x-oauth spec again when the
	// token needs refreshing.
	Scope        string     `json:"scope"        gorm:"type:varchar(16)"`
	Contribution string     `json:"contribution" gorm:"type:varchar(160)"`
	Field        string     `json:"field"        gorm:"type:varchar(256)"`
	Token        string     `json:"-"            gorm:"type:text"`
	ExpiresAt    *time.Time `json:"expiresAt,omitempty"`
	CreatedBy    string     `json:"-"            gorm:"type:varchar(64)"`
	CreatedAt    time.Time  `json:"createdAt"`
	UpdatedAt    time.Time  `json:"updatedAt"`
}

// TableName pins the table so GORM's pluralizer cannot drift.
func (PluginOAuthConnection) TableName() string { return "plugin_oauth_connections" }
