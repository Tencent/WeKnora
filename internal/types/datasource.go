package types

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/Tencent/WeKnora/internal/utils"
	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Data source types and constants
const (
	// Connector types
	ConnectorTypeFeishu      = "feishu"
	ConnectorTypeNotion      = "notion"
	ConnectorTypeConfluence  = "confluence"
	ConnectorTypeYuque       = "yuque"
	ConnectorTypeGitHub      = "github"
	ConnectorTypeGoogleDrive = "google_drive"
	ConnectorTypeOneDrive    = "onedrive"
	ConnectorTypeDingTalk    = "dingtalk"
	ConnectorTypeWebCrawler  = "web_crawler"
	ConnectorTypeSlack       = "slack"
	ConnectorTypeIMAP        = "imap"
	ConnectorTypeRSS         = "rss"

	// Sync modes
	SyncModeIncremental = "incremental"
	SyncModeFull        = "full"

	// Data source status
	DataSourceStatusActive                  = "active"
	DataSourceStatusPaused                  = "paused"
	DataSourceStatusError                   = "error"
	DataSourceStatusReauthorizationRequired = "reauthorization_required"
	DataSourceStatusDeleted                 = "deleted"

	// Sync log status
	SyncLogStatusRunning  = "running"
	SyncLogStatusSuccess  = "success"
	SyncLogStatusPartial  = "partial"
	SyncLogStatusFailed   = "failed"
	SyncLogStatusCanceled = "canceled"

	// Conflict resolution strategies
	ConflictStrategyOverwrite = "overwrite"
	ConflictStrategySkip      = "skip"
)

// DataSource represents a configured external data source for synchronization
type DataSource struct {
	// Unique identifier
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`

	// Workspace ID for multi-workspace isolation
	TenantID uint64 `json:"tenant_id" gorm:"index"`

	// Target knowledge base ID
	KnowledgeBaseID string `json:"knowledge_base_id" gorm:"index"`

	// User-friendly name
	Name string `json:"name"`

	// Connector type (feishu, notion, confluence, etc.)
	Type string `json:"type" gorm:"type:varchar(50);index"`

	// Encrypted configuration (API credentials, tokens, etc.)
	// Stored as JSON with AES-256-GCM encryption
	Config JSON `json:"config" gorm:"type:jsonb"`

	// Cron expression for scheduled syncs (e.g., "0 */6 * * *" = every 6 hours)
	SyncSchedule string `json:"sync_schedule"`

	// Sync mode: "incremental" (recommended) or "full"
	SyncMode string `json:"sync_mode" gorm:"type:varchar(20);default:'incremental'"`

	// Current status: active, paused, error
	Status string `json:"status" gorm:"type:varchar(32);default:'active'"`

	// Conflict resolution strategy: overwrite or skip
	ConflictStrategy string `json:"conflict_strategy" gorm:"type:varchar(32);default:'overwrite'"`

	// Whether to sync deletions from source
	SyncDeletions bool `json:"sync_deletions" gorm:"default:true"`

	// ConnectionVersion is incremented whenever an OAuth connection is replaced.
	// Sync tasks capture it and must not commit results from an older connection.
	ConnectionVersion uint64 `json:"connection_version" gorm:"not null;default:1"`

	// Last successful sync timestamp
	LastSyncAt *time.Time `json:"last_sync_at"`

	// Cursor or state for incremental sync (connector-specific)
	LastSyncCursor JSON `json:"last_sync_cursor" gorm:"type:jsonb"`

	// Summary of last sync result
	LastSyncResult JSON `json:"last_sync_result" gorm:"type:jsonb"`

	// Error message if status is "error"
	ErrorMessage string `json:"error_message"`

	// Number of days to keep sync logs (default: 30)
	SyncLogRetentionDays int `json:"sync_log_retention_days" gorm:"default:30"`

	// Creation timestamp
	CreatedAt time.Time `json:"created_at"`

	// Last update timestamp
	UpdatedAt time.Time `json:"updated_at"`

	// Soft delete timestamp
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`

	// Total items synced (not stored in DB, calculated on query)
	TotalItemsSynced int64 `json:"total_items_synced" gorm:"-"`

	// Latest sync log (not stored in DB, populated on query)
	LatestSyncLog *SyncLog `json:"latest_sync_log" gorm:"-"`
}

// TableName specifies the table name for DataSource
func (d *DataSource) TableName() string {
	return "data_sources"
}

// BeforeCreate hook to generate UUID
func (d *DataSource) BeforeCreate(tx *gorm.DB) error {
	if d.ID == "" {
		d.ID = uuid.New().String()
	}
	if d.ConnectionVersion == 0 {
		d.ConnectionVersion = 1
	}
	return nil
}

// DataSourceOAuthToken stores the delegated OAuth grant owned by a data source.
// Secrets fail closed: unlike legacy connector credentials, plaintext fallback
// is forbidden because refresh tokens can provide long-lived drive access.
type DataSourceOAuthToken struct {
	ID                 string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64    `json:"tenant_id" gorm:"not null;uniqueIndex:idx_ds_oauth_token_tenant_source"`
	DataSourceID       string    `json:"data_source_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_ds_oauth_token_tenant_source;index"`
	Provider           string    `json:"provider" gorm:"type:varchar(32);not null"`
	AccessToken        string    `json:"-" gorm:"type:text;not null"`
	RefreshToken       string    `json:"-" gorm:"type:text;not null"`
	TokenType          string    `json:"token_type" gorm:"type:varchar(32)"`
	Scopes             string    `json:"scopes" gorm:"type:text"`
	ExpiresAt          time.Time `json:"expires_at"`
	ProviderAccountID  string    `json:"provider_account_id" gorm:"type:varchar(255);not null"`
	ProviderTenantID   string    `json:"provider_tenant_id" gorm:"type:varchar(255)"`
	AuthorizedDriveID  string    `json:"authorized_drive_id" gorm:"type:varchar(255);not null"`
	AccountDisplayName string    `json:"account_display_name" gorm:"type:varchar(255)"`
	AuthorizedByUserID string    `json:"authorized_by_user_id" gorm:"type:varchar(255);not null"`
	ConnectionVersion  uint64    `json:"connection_version" gorm:"not null"`
	CreatedAt          time.Time `json:"created_at"`
	UpdatedAt          time.Time `json:"updated_at"`
}

func (DataSourceOAuthToken) TableName() string { return "data_source_oauth_tokens" }

func (t *DataSourceOAuthToken) BeforeCreate(_ *gorm.DB) error {
	if t.ID == "" {
		t.ID = uuid.NewString()
	}
	return t.encryptSecrets()
}

func (t *DataSourceOAuthToken) BeforeSave(_ *gorm.DB) error { return t.encryptSecrets() }

func (t *DataSourceOAuthToken) AfterFind(_ *gorm.DB) error {
	var err error
	if t.AccessToken, err = utils.DecryptStoredSecret(t.AccessToken); err != nil {
		return fmt.Errorf("decrypt data source oauth access token: %w", err)
	}
	if t.RefreshToken, err = utils.DecryptStoredSecret(t.RefreshToken); err != nil {
		return fmt.Errorf("decrypt data source oauth refresh token: %w", err)
	}
	return nil
}

func (t *DataSourceOAuthToken) encryptSecrets() error {
	key := utils.GetAESKey()
	if key == nil {
		return fmt.Errorf("data source oauth requires a 32-byte SYSTEM_AES_KEY")
	}
	var err error
	if t.AccessToken, err = utils.EncryptAESGCM(t.AccessToken, key); err != nil {
		return fmt.Errorf("encrypt data source oauth access token: %w", err)
	}
	if t.RefreshToken, err = utils.EncryptAESGCM(t.RefreshToken, key); err != nil {
		return fmt.Errorf("encrypt data source oauth refresh token: %w", err)
	}
	return nil
}

// DataSourceItem is the durable projection used to map a drive-level delta
// stream onto selected folders/files without relying on mutable paths.
type DataSourceItem struct {
	ID                 string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID           uint64     `json:"tenant_id" gorm:"not null;index:idx_ds_item_scope,priority:1"`
	DataSourceID       string     `json:"data_source_id" gorm:"type:varchar(36);not null;uniqueIndex:idx_ds_item_identity,priority:1;index:idx_ds_item_scope,priority:2"`
	ConnectionVersion  uint64     `json:"connection_version" gorm:"not null;uniqueIndex:idx_ds_item_identity,priority:2;index:idx_ds_item_scope,priority:3"`
	DriveID            string     `json:"drive_id" gorm:"type:varchar(255);not null;uniqueIndex:idx_ds_item_identity,priority:3"`
	ItemID             string     `json:"item_id" gorm:"type:varchar(255);not null;uniqueIndex:idx_ds_item_identity,priority:4"`
	ParentItemID       string     `json:"parent_item_id" gorm:"type:varchar(255);index"`
	ItemType           string     `json:"item_type" gorm:"type:varchar(16);not null"`
	SelectedRootID     string     `json:"selected_root_id" gorm:"type:text;index"`
	ExternalID         string     `json:"external_id" gorm:"type:text;index"`
	LastModifiedAt     time.Time  `json:"last_modified_at"`
	LastSeenGeneration string     `json:"last_seen_generation" gorm:"type:varchar(36);index"`
	Ingested           bool       `json:"ingested" gorm:"not null;default:false"`
	DeletedAt          *time.Time `json:"deleted_at" gorm:"index"`
	CreatedAt          time.Time  `json:"created_at"`
	UpdatedAt          time.Time  `json:"updated_at"`
}

func (DataSourceItem) TableName() string { return "data_source_items" }

func (i *DataSourceItem) BeforeCreate(_ *gorm.DB) error {
	if i.ID == "" {
		i.ID = uuid.NewString()
	}
	return nil
}

// SyncLog records the execution of a sync task
type SyncLog struct {
	// Unique identifier
	ID string `json:"id" gorm:"type:varchar(36);primaryKey"`

	// Reference to the data source
	DataSourceID string `json:"data_source_id" gorm:"index"`

	// Workspace ID
	TenantID uint64 `json:"tenant_id" gorm:"index"`

	// Sync status: running, success, partial, failed, canceled
	Status string `json:"status" gorm:"type:varchar(32);index"`

	// Sync start time
	StartedAt time.Time `json:"started_at"`

	// Sync completion time
	FinishedAt *time.Time `json:"finished_at"`

	// Total items fetched from source
	ItemsTotal int `json:"items_total"`

	// New items created in knowledge base
	ItemsCreated int `json:"items_created"`

	// Existing items updated
	ItemsUpdated int `json:"items_updated"`

	// Items deleted from knowledge base
	ItemsDeleted int `json:"items_deleted"`

	// Items skipped (no changes detected)
	ItemsSkipped int `json:"items_skipped"`

	// Items that failed to sync
	ItemsFailed int `json:"items_failed"`

	// Error details if status is "failed"
	ErrorMessage string `json:"error_message"`

	// Detailed sync result (JSON-encoded)
	Result JSON `json:"result" gorm:"type:jsonb"`

	// Creation timestamp (usually same as StartedAt)
	CreatedAt time.Time `json:"created_at"`

	// Last update timestamp
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName specifies the table name for SyncLog
func (s *SyncLog) TableName() string {
	return "sync_logs"
}

// BeforeCreate hook to generate UUID
func (s *SyncLog) BeforeCreate(tx *gorm.DB) error {
	if s.ID == "" {
		s.ID = uuid.New().String()
	}
	if s.StartedAt.IsZero() {
		s.StartedAt = time.Now().UTC()
	}
	return nil
}

// DataSourceConfig represents the unencrypted configuration structure
// Each connector type will have its own specific fields.
//
// Credential management lives in the dedicated /credentials subresource
// (see internal/handler/datasource_credentials.go). Secret values are never
// included in API responses — handlers serialize via dto.NewDataSourceResponse
// which strips the Credentials map by construction.
type DataSourceConfig struct {
	// Common fields applicable to most connectors
	Type string `json:"type"`

	// OAuth/API credentials (varies by connector)
	Credentials map[string]interface{} `json:"credentials"`

	// Selected resource IDs to sync (e.g., folder IDs, space IDs)
	ResourceIDs []string `json:"resource_ids"`

	// Connector-specific configuration
	Settings map[string]interface{} `json:"settings"`

	// Runtime is injected by DataSourceService and is never persisted or
	// returned by the API. OAuth connectors use it to obtain fresh tokens.
	Runtime *DataSourceRuntime `json:"-"`
}

type DataSourceRuntime struct {
	DataSourceID       string
	TenantID           uint64
	ConnectionVersion  uint64
	AccessToken        func(context.Context) (string, error)
	RefreshAccessToken func(context.Context) (string, error)
}

// HasCredentials reports whether the credentials map carries any value at
// all. Used by the Update path and by the credential subresource to decide
// whether to run live-connector validation.
func (d DataSourceConfig) HasCredentials() bool {
	return len(d.Credentials) > 0
}

// HasConfiguredCredentials reports whether user-facing secret credentials are
// stored. RSS feed URLs are non-secret configuration (settings); only
// auth_headers count as credentials for that connector.
func (d DataSourceConfig) HasConfiguredCredentials(connectorType string) bool {
	if len(d.Credentials) == 0 {
		return false
	}
	switch connectorType {
	case ConnectorTypeRSS:
		raw, ok := d.Credentials["auth_headers"]
		if !ok {
			return false
		}
		s, ok := raw.(string)
		return ok && strings.TrimSpace(s) != ""
	default:
		return len(d.Credentials) > 0
	}
}

// StripNonSecretCredentials removes non-secret values mistakenly stored in the
// credentials map before persistence.
func (d *DataSourceConfig) StripNonSecretCredentials(connectorType string) {
	if d == nil || d.Credentials == nil {
		return
	}
	switch connectorType {
	case ConnectorTypeRSS:
		delete(d.Credentials, "feed_urls")
		if len(d.Credentials) == 0 {
			d.Credentials = nil
		}
	}
}

// Resource represents a syncable resource (document, folder, space) from external system
type Resource struct {
	// Unique identifier in the external system
	ExternalID string `json:"external_id"`

	// Display name
	Name string `json:"name"`

	// Resource type (document, folder, space, page, etc.)
	Type string `json:"type"`

	// Optional description
	Description string `json:"description"`

	// URL to access in external system
	URL string `json:"url"`

	// Last modified time in external system
	ModifiedAt time.Time `json:"modified_at"`

	// For hierarchical resources (parent ID if applicable)
	ParentID string `json:"parent_id,omitempty"`

	// Whether this resource has children that can be expanded
	HasChildren bool `json:"has_children,omitempty"`

	// Additional metadata
	Metadata map[string]interface{} `json:"metadata,omitempty"`
}

// FetchedItem represents a single document/content item fetched from external source
type FetchedItem struct {
	// Unique ID in the external system
	ExternalID string `json:"external_id"`

	// Title of the content
	Title string `json:"title"`

	// Content in bytes (Markdown format preferred)
	Content []byte `json:"content"`

	// MIME type (text/markdown, text/html, application/pdf, etc.)
	ContentType string `json:"content_type"`

	// Suggested file name
	FileName string `json:"file_name"`

	// Original URL in external system
	URL string `json:"url"`

	// When last modified in external system
	UpdatedAt time.Time `json:"updated_at"`

	// Additional metadata to preserve
	Metadata map[string]string `json:"metadata"`

	// Whether the item was deleted in the source
	IsDeleted bool `json:"is_deleted"`

	// Source resource ID (e.g., folder ID this document belongs to)
	SourceResourceID string `json:"source_resource_id"`
}

// SyncCursor represents the position/state for incremental sync
// Connector-specific structure allows flexibility
type SyncCursor struct {
	// Timestamp of last sync
	LastSyncTime time.Time `json:"last_sync_time"`

	// Connector-specific cursor (e.g., pagination token, offset, etc.)
	ConnectorCursor map[string]interface{} `json:"connector_cursor"`

	// Hash of the last full sync to detect schema changes
	LastSchemaHash string `json:"last_schema_hash"`
}

// SyncResult summarizes the outcome of a sync operation
type SyncResult struct {
	// Total items processed
	Total int `json:"total"`

	// Items created
	Created int `json:"created"`

	// Items updated
	Updated int `json:"updated"`

	// Items deleted
	Deleted int `json:"deleted"`

	// Items skipped (no changes)
	Skipped int `json:"skipped"`

	// Items that failed
	Failed int `json:"failed"`

	// Detailed error messages
	Errors []string `json:"errors,omitempty"`

	// Updated cursor for next incremental sync
	NextCursor *SyncCursor `json:"next_cursor,omitempty"`
}

type FetchWarning struct {
	Code       string `json:"code"`
	ExternalID string `json:"external_id,omitempty"`
	Message    string `json:"message"`
}

type FetchResult struct {
	Items      []FetchedItem  `json:"items"`
	NextCursor *SyncCursor    `json:"-"`
	Warnings   []FetchWarning `json:"warnings,omitempty"`
}

// DataSourceSyncPayload represents the asynq task payload for data source sync
type DataSourceSyncPayload struct {
	TracingContext

	// Data source ID to sync
	DataSourceID string `json:"data_source_id"`

	// Workspace ID
	TenantID uint64 `json:"tenant_id"`

	ConnectionVersion uint64 `json:"connection_version"`

	// Sync log ID (for tracking)
	SyncLogID string `json:"sync_log_id"`

	// Force full sync even if incremental mode is configured
	ForceFull bool `json:"force_full"`

	// Maximum number of items to fetch (0 = unlimited)
	MaxItems int `json:"max_items,omitempty"`
}

// ToJSON converts a DataSourceConfig to the JSON blob stored in
// DataSource.Config.
//
// When SYSTEM_AES_KEY is configured, every string value inside
// Credentials is AES-256-GCM encrypted before serialization. Non-string
// values (numbers, bools, nested objects) pass through untouched. This is
// the only write path through which credentials reach the DB (the GORM
// JSON type itself is a byte passthrough), so encrypting here is
// sufficient to keep DataSource.Config at rest fully encrypted.
//
// Encryption operates on a shallow copy of Credentials to avoid mutating
// the caller's in-memory map (subsequent reads would otherwise see
// ciphertext).
func (d *DataSourceConfig) ToJSON() (JSON, error) {
	if d == nil {
		return nil, nil
	}
	out := *d
	if key := utils.GetAESKey(); key != nil && len(out.Credentials) > 0 {
		encCreds := make(map[string]interface{}, len(out.Credentials))
		for k, v := range out.Credentials {
			if s, ok := v.(string); ok && s != "" {
				if enc, err := utils.EncryptAESGCM(s, key); err == nil {
					encCreds[k] = enc
					continue
				}
			}
			encCreds[k] = v
		}
		out.Credentials = encCreds
	}
	bytes, err := json.Marshal(&out)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ToJSON converts a value to JSON type
func (r *SyncCursor) ToJSON() (JSON, error) {
	if r == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ToJSON converts a value to JSON type
func (r *SyncResult) ToJSON() (JSON, error) {
	if r == nil {
		return nil, nil
	}
	bytes, err := json.Marshal(r)
	if err != nil {
		return nil, err
	}
	return JSON(bytes), nil
}

// ParseConfig deserializes DataSource.Config and decrypts any encrypted
// Credentials entries.
//
// DecryptStoredSecret transparently handles three cases per credential:
//   - empty string: untouched
//   - legacy plaintext (no enc:v1: prefix): returned as-is, so historical
//     rows continue to work without a migration step
//   - enc:v1: encrypted: decrypted with SYSTEM_AES_KEY; missing/rotated
//     key surfaces as an error so we fail loudly rather than handing
//     ciphertext to the upstream connector as the credential
func (d *DataSource) ParseConfig() (*DataSourceConfig, error) {
	if len(d.Config) == 0 {
		return nil, nil
	}
	var config DataSourceConfig
	if err := json.Unmarshal(d.Config, &config); err != nil {
		return nil, err
	}
	for k, v := range config.Credentials {
		s, ok := v.(string)
		if !ok || s == "" {
			continue
		}
		if plain, ok := utils.DecryptStoredSecretLenient(s); ok {
			config.Credentials[k] = plain
			continue
		}
		// Same rationale as the other Scan paths: don't fail the load —
		// blank the field so the row stays visible. ParseConfig callers
		// then see an empty credential string; HasCredentials() returns
		// false; the UI surfaces "credential not configured" and the
		// user can re-enter without losing the rest of the data source.
		log.Printf(
			"[crypto] datasource credential %q: decrypt failed (SYSTEM_AES_KEY missing/rotated?), treating as unconfigured",
			k,
		)
		config.Credentials[k] = ""
	}
	return &config, nil
}

// ParseSyncCursor parses the cursor JSON
func (d *DataSource) ParseSyncCursor() (*SyncCursor, error) {
	if len(d.LastSyncCursor) == 0 {
		return nil, nil
	}
	var cursor SyncCursor
	if err := json.Unmarshal(d.LastSyncCursor, &cursor); err != nil {
		return nil, err
	}
	return &cursor, nil
}

// ParseSyncResult parses the result JSON
func (d *DataSource) ParseSyncResult() (*SyncResult, error) {
	if len(d.LastSyncResult) == 0 {
		return nil, nil
	}
	var result SyncResult
	if err := json.Unmarshal(d.LastSyncResult, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ParseSyncLogResult parses the result JSON from sync log
func (s *SyncLog) ParseResult() (*SyncResult, error) {
	if len(s.Result) == 0 {
		return nil, nil
	}
	var result SyncResult
	if err := json.Unmarshal(s.Result, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
