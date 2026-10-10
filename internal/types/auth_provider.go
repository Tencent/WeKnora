package types

import "time"

// ---------------------------------------------------------------------------
// Platform authentication providers (SystemAdmin → 用户管理 / 通用OIDC / LDAP)
//
// A platform auth provider is a deployment-wide login source managed from the
// Settings → 系统管理 → 用户管理 screen. Two kinds are supported:
//
//   - oidc: an external OpenID Connect provider. Historically configured with
//     ~14 OIDC_AUTH_* environment variables; the DB row now takes precedence
//     and the environment is only consulted as a fallback default so existing
//     deployments keep working without an operator touching anything.
//   - ldap: a directory (Active Directory / OpenLDAP) queried for bind
//     authentication when the local password does not apply.
//
// One row per kind. The non-secret settings live as JSON in Config; the
// credential (OIDC client_secret / LDAP bind password) lives in Secret and is
// never serialised to a client — admin read endpoints report only `has_secret`.
// ---------------------------------------------------------------------------

// PlatformAuthProviderKind identifies a platform-level auth provider.
type PlatformAuthProviderKind string

const (
	// AuthProviderKindOIDC is the generic OpenID Connect provider.
	AuthProviderKindOIDC PlatformAuthProviderKind = "oidc"
	// AuthProviderKindLDAP is the LDAP / Active Directory directory binding.
	AuthProviderKindLDAP PlatformAuthProviderKind = "ldap"
)

// IsValid reports whether k is a known provider kind.
func (k PlatformAuthProviderKind) IsValid() bool {
	return k == AuthProviderKindOIDC || k == AuthProviderKindLDAP
}

// PlatformAuthProviderConfigSource describes where a resolved provider
// configuration came from, so the UI can label a row that is still being
// served from the legacy environment rather than from the database.
type PlatformAuthProviderConfigSource string

const (
	// AuthProviderSourceDatabase means the operator saved this config in 用户管理.
	AuthProviderSourceDatabase PlatformAuthProviderConfigSource = "database"
	// AuthProviderSourceEnvironment means no DB row exists and the deployment
	// environment variables are still driving the provider.
	AuthProviderSourceEnvironment PlatformAuthProviderConfigSource = "environment"
	// AuthProviderSourceDefault means neither a DB row nor environment config
	// exists; the built-in defaults are in effect (provider disabled).
	AuthProviderSourceDefault PlatformAuthProviderConfigSource = "default"
)

// AccountAuthSource records how a local account authenticates. Stored on
// users.auth_source so the 用户管理 list can distinguish accounts created by
// the local password flow from ones auto-provisioned by OIDC or LDAP.
type AccountAuthSource string

const (
	AccountAuthSourceLocal AccountAuthSource = "local"
	AccountAuthSourceOIDC  AccountAuthSource = "oidc"
	AccountAuthSourceLDAP  AccountAuthSource = "ldap"
)

// PlatformAuthProvider is the persisted row (table platform_auth_providers).
type PlatformAuthProvider struct {
	ID      uint64                   `json:"id"        gorm:"primaryKey"`
	Kind    PlatformAuthProviderKind `json:"kind"      gorm:"type:varchar(16);uniqueIndex;not null"`
	Enabled bool                     `json:"enabled"   gorm:"not null;default:false"`
	// Config holds the kind-specific settings as JSON. Column type is jsonb on
	// Postgres and TEXT on SQLite (types.JSON implements both Valuer/Scanner).
	Config JSON `json:"config" gorm:"type:jsonb;not null;default:'{}'"`
	// Secret is the write-only credential (AES-256-GCM wrapped when
	// SYSTEM_AES_KEY is present). Never returned by an API.
	Secret    string    `json:"-"          gorm:"type:text;not null;default:''"`
	UpdatedBy string    `json:"updated_by" gorm:"type:varchar(36);not null;default:''"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName pins the table name so a future rename of the struct cannot
// silently orphan an existing deployment's rows.
func (PlatformAuthProvider) TableName() string { return "platform_auth_providers" }

// ---------------------------------------------------------------------------
// OIDC
// ---------------------------------------------------------------------------

// OIDCProviderSettings is the JSON shape persisted in Config for the OIDC row.
type OIDCProviderSettings struct {
	IssuerURL             string   `json:"issuer_url,omitempty"`
	DiscoveryURL          string   `json:"discovery_url,omitempty"`
	ProviderDisplayName   string   `json:"provider_display_name,omitempty"`
	ClientID              string   `json:"client_id,omitempty"`
	AuthorizationEndpoint string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint         string   `json:"token_endpoint,omitempty"`
	UserInfoEndpoint      string   `json:"user_info_endpoint,omitempty"`
	JwksURI               string   `json:"jwks_uri,omitempty"`
	Scopes                []string `json:"scopes,omitempty"`
	// UsernameClaim / EmailClaim map IdP claims onto the local account fields.
	// They mirror the legacy OIDC_USER_INFO_MAPPING_* environment variables.
	UsernameClaim string `json:"username_claim,omitempty"`
	EmailClaim    string `json:"email_claim,omitempty"`
}

// ResolvedOIDCConfig is the effective OIDC configuration after merging the DB
// row over the environment defaults. It deliberately lives in `types` (rather
// than reusing config.OIDCAuthConfig) so the service interface does not have to
// import internal/config.
type ResolvedOIDCConfig struct {
	Enabled               bool                             `json:"enabled"`
	IssuerURL             string                           `json:"issuer_url"`
	DiscoveryURL          string                           `json:"discovery_url"`
	ProviderDisplayName   string                           `json:"provider_display_name"`
	ClientID              string                           `json:"client_id"`
	ClientSecret          string                           `json:"-"`
	AuthorizationEndpoint string                           `json:"authorization_endpoint"`
	TokenEndpoint         string                           `json:"token_endpoint"`
	UserInfoEndpoint      string                           `json:"user_info_endpoint"`
	JwksURI               string                           `json:"jwks_uri"`
	Scopes                []string                         `json:"scopes"`
	UsernameClaim         string                           `json:"username_claim"`
	EmailClaim            string                           `json:"email_claim"`
	Source                PlatformAuthProviderConfigSource `json:"source"`
}

// OIDCProviderView is the admin-facing projection of the OIDC provider. It is
// OIDCProviderSettings plus metadata, with the client secret replaced by a
// boolean so the credential never leaves the server.
type OIDCProviderView struct {
	Success               bool                             `json:"success"`
	Enabled               bool                             `json:"enabled"`
	IssuerURL             string                           `json:"issuer_url"`
	DiscoveryURL          string                           `json:"discovery_url"`
	ProviderDisplayName   string                           `json:"provider_display_name"`
	ClientID              string                           `json:"client_id"`
	HasSecret             bool                             `json:"has_secret"`
	AuthorizationEndpoint string                           `json:"authorization_endpoint"`
	TokenEndpoint         string                           `json:"token_endpoint"`
	UserInfoEndpoint      string                           `json:"user_info_endpoint"`
	JwksURI               string                           `json:"jwks_uri"`
	Scopes                []string                         `json:"scopes"`
	UsernameClaim         string                           `json:"username_claim"`
	EmailClaim            string                           `json:"email_claim"`
	Source                PlatformAuthProviderConfigSource `json:"source"`
	UpdatedAt             *time.Time                       `json:"updated_at,omitempty"`
	UpdatedBy             string                           `json:"updated_by,omitempty"`
	UpdatedByName         string                           `json:"updated_by_name,omitempty"`
	// EnvOverrides lists the legacy environment variables that are currently
	// set on this deployment, so an operator who edits the DB row knows which
	// values will still act as defaults for fields left blank.
	EnvOverrides map[string]string `json:"env_overrides,omitempty"`
}

// OIDCProviderUpdateRequest is the write payload for the OIDC tab.
//
// ClientSecret is write-only: omit it (nil) or send an empty string to keep
// the stored secret untouched. Set ClearSecret to explicitly wipe it.
type OIDCProviderUpdateRequest struct {
	Enabled               bool     `json:"enabled"`
	IssuerURL             string   `json:"issuer_url"`
	DiscoveryURL          string   `json:"discovery_url"`
	ProviderDisplayName   string   `json:"provider_display_name"`
	ClientID              string   `json:"client_id"`
	ClientSecret          *string  `json:"client_secret"`
	AuthorizationEndpoint string   `json:"authorization_endpoint"`
	TokenEndpoint         string   `json:"token_endpoint"`
	UserInfoEndpoint      string   `json:"user_info_endpoint"`
	JwksURI               string   `json:"jwks_uri"`
	Scopes                []string `json:"scopes"`
	UsernameClaim         string   `json:"username_claim"`
	EmailClaim            string   `json:"email_claim"`
	ClearSecret           bool     `json:"clear_secret"`
}

// OIDCTestResponse reports the outcome of an OIDC discovery probe launched
// from the 通用OIDC tab's "测试连接" button.
type OIDCTestResponse struct {
	Success                  bool     `json:"success"`
	Message                  string   `json:"message"`
	DiscoveryOK              bool     `json:"discovery_ok"`
	Issuer                   string   `json:"issuer,omitempty"`
	AuthorizationEndpoint    string   `json:"authorization_endpoint,omitempty"`
	TokenEndpoint            string   `json:"token_endpoint,omitempty"`
	UserInfoEndpoint         string   `json:"user_info_endpoint,omitempty"`
	JwksURI                  string   `json:"jwks_uri,omitempty"`
	TokenEndpointAuthMethods []string `json:"token_endpoint_auth_methods,omitempty"`
	Warnings                 []string `json:"warnings,omitempty"`
}

// ---------------------------------------------------------------------------
// LDAP
// ---------------------------------------------------------------------------

// LDAPProviderSettings is the JSON shape persisted in Config for the LDAP row.
type LDAPProviderSettings struct {
	Host     string `json:"host,omitempty"`
	Port     int    `json:"port,omitempty"`
	UseTLS   bool   `json:"use_tls,omitempty"`
	StartTLS bool   `json:"start_tls,omitempty"`
	// SkipTLSVerify is only honoured for ldaps:// / StartTLS and exists for
	// lab directories with self-signed certificates.
	SkipTLSVerify   bool   `json:"skip_tls_verify,omitempty"`
	BindDN          string `json:"bind_dn,omitempty"`
	BaseDN          string `json:"base_dn,omitempty"`
	UserSearchBase  string `json:"user_search_base,omitempty"`
	UserFilter      string `json:"user_filter,omitempty"`
	UserNameAttr    string `json:"user_name_attr,omitempty"`
	UserEmailAttr   string `json:"user_email_attr,omitempty"`
	UserDisplayAttr string `json:"user_display_attr,omitempty"`
	// AutoCreateUser enables just-in-time provisioning of a local account the
	// first time a directory user logs in.
	AutoCreateUser bool `json:"auto_create_user,omitempty"`
	// DefaultTenantMode decides the tenancy of an auto-provisioned account.
	// Empty means "follow auth.default_tenant_mode".
	DefaultTenantMode string `json:"default_tenant_mode,omitempty"`
	// TimeoutSeconds bounds every directory call. Zero uses the built-in default.
	TimeoutSeconds int `json:"timeout_seconds,omitempty"`
}

// ResolvedLDAPConfig is the effective LDAP configuration.
type ResolvedLDAPConfig struct {
	LDAPProviderSettings
	// BindPassword is the resolved credential. Never serialised.
	BindPassword string                           `json:"-"`
	Source       PlatformAuthProviderConfigSource `json:"source"`
}

// LDAPProviderView is the admin-facing projection of the LDAP provider.
type LDAPProviderView struct {
	Success           bool                             `json:"success"`
	Enabled           bool                             `json:"enabled"`
	Host              string                           `json:"host"`
	Port              int                              `json:"port"`
	UseTLS            bool                             `json:"use_tls"`
	StartTLS          bool                             `json:"start_tls"`
	SkipTLSVerify     bool                             `json:"skip_tls_verify"`
	BindDN            string                           `json:"bind_dn"`
	HasBindPassword   bool                             `json:"has_bind_password"`
	BaseDN            string                           `json:"base_dn"`
	UserSearchBase    string                           `json:"user_search_base"`
	UserFilter        string                           `json:"user_filter"`
	UserNameAttr      string                           `json:"user_name_attr"`
	UserEmailAttr     string                           `json:"user_email_attr"`
	UserDisplayAttr   string                           `json:"user_display_attr"`
	AutoCreateUser    bool                             `json:"auto_create_user"`
	DefaultTenantMode string                           `json:"default_tenant_mode"`
	TimeoutSeconds    int                              `json:"timeout_seconds"`
	Source            PlatformAuthProviderConfigSource `json:"source"`
	UpdatedAt         *time.Time                       `json:"updated_at,omitempty"`
	UpdatedBy         string                           `json:"updated_by,omitempty"`
	UpdatedByName     string                           `json:"updated_by_name,omitempty"`
}

// LDAPProviderUpdateRequest is the write payload for the LDAP tab.
//
// BindPassword is write-only: omit it (nil) or send "" to keep the stored
// password. Set ClearBindPassword to explicitly wipe it.
type LDAPProviderUpdateRequest struct {
	Enabled           bool    `json:"enabled"`
	Host              string  `json:"host"`
	Port              int     `json:"port"`
	UseTLS            bool    `json:"use_tls"`
	StartTLS          bool    `json:"start_tls"`
	SkipTLSVerify     bool    `json:"skip_tls_verify"`
	BindDN            string  `json:"bind_dn"`
	BindPassword      *string `json:"bind_password"`
	BaseDN            string  `json:"base_dn"`
	UserSearchBase    string  `json:"user_search_base"`
	UserFilter        string  `json:"user_filter"`
	UserNameAttr      string  `json:"user_name_attr"`
	UserEmailAttr     string  `json:"user_email_attr"`
	UserDisplayAttr   string  `json:"user_display_attr"`
	AutoCreateUser    bool    `json:"auto_create_user"`
	DefaultTenantMode string  `json:"default_tenant_mode"`
	TimeoutSeconds    int     `json:"timeout_seconds"`
	ClearBindPassword bool    `json:"clear_bind_password"`
}

// LDAPTestRequest probes the directory. Both fields are optional: with no
// credentials the endpoint only verifies connectivity and the service bind.
type LDAPTestRequest struct {
	// Username is the value matched against UserFilter (usually an email or a
	// sAMAccountName). Empty skips the search + user-bind phase.
	Username string `json:"username"`
	// Password is the user's directory password. Never logged.
	Password string `json:"password"`
}

// LDAPTestResponse reports the outcome of a connection probe.
type LDAPTestResponse struct {
	Success   bool   `json:"success"`
	Message   string `json:"message"`
	ServiceOK bool   `json:"service_ok"`
	UserFound bool   `json:"user_found"`
	UserDN    string `json:"user_dn,omitempty"`
	// DetectedSubjectAttribute is the attribute that actually carries the
	// stable subject id for the matched entry (entryUUID on OpenLDAP,
	// objectGUID on AD). Ported from Octop, which surfaces this so operators
	// do not have to guess the correct subject_attribute.
	DetectedSubjectAttribute string            `json:"detected_subject_attribute,omitempty"`
	Attributes               map[string]string `json:"attributes,omitempty"`
	Warnings                 []string          `json:"warnings,omitempty"`
}

// LDAPUserInfo is the directory entry resolved during an LDAP login and used
// to find (or just-in-time provision) the matching local account.
type LDAPUserInfo struct {
	// DN is the distinguished name of the matched entry.
	DN string
	// SubjectID is the stable, immutable identifier of the entry
	// (entryUUID / objectGUID). Empty when the directory does not expose one.
	SubjectID string
	// Username, Email and DisplayName come from the configured attribute
	// mapping, falling back to sensible defaults when blank.
	Username    string
	Email       string
	DisplayName string
	// Attributes carries the raw string values of the mapped attributes so the
	// 测试连接 panel can show operators exactly what was read.
	Attributes map[string]string
}

// ---------------------------------------------------------------------------
// Local user management
// ---------------------------------------------------------------------------

// ManagedUserInfo is the row shape for the 用户管理 → 本地用户 table. It is a
// slimmer projection than UserInfo: the preferences blob is replaced by a
// derived auth_source label, and the owning workspace name is resolved for
// display.
type ManagedUserInfo struct {
	ID                  string            `json:"id"`
	Username            string            `json:"username"`
	Email               string            `json:"email"`
	Avatar              string            `json:"avatar"`
	TenantID            uint64            `json:"tenant_id"`
	TenantName          string            `json:"tenant_name,omitempty"`
	IsActive            bool              `json:"is_active"`
	IsSystemAdmin       bool              `json:"is_system_admin"`
	CanAccessAllTenants bool              `json:"can_access_all_tenants"`
	AuthSource          AccountAuthSource `json:"auth_source"`
	HasLocalPassword    bool              `json:"has_local_password"`
	CreatedAt           time.Time         `json:"created_at"`
	UpdatedAt           time.Time         `json:"updated_at"`
}

// ListManagedUsersResponse is the paged payload for GET /system/admin/users.
type ListManagedUsersResponse struct {
	Total int64              `json:"total"`
	Users []*ManagedUserInfo `json:"users"`
	// Stats summarises the whole table (not just the current page) so the
	// header counters stay correct while paging.
	Stats ManagedUserStats `json:"stats"`
}

// ManagedUserStats powers the small counter row at the top of the tab.
type ManagedUserStats struct {
	Total     int64 `json:"total"`
	Active    int64 `json:"active"`
	Disabled  int64 `json:"disabled"`
	Admins    int64 `json:"admins"`
	LDAPUsers int64 `json:"ldap_users"`
	OIDCUsers int64 `json:"oidc_users"`
}
