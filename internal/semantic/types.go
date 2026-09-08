// Package semantic implements the semantic modeling module:
// database connection management, visual Cube.js model authoring with
// publish/rollback, data-group based access control, and the underlying
// Cube REST client. The module is self-contained by design — everything
// lives in this package so it can track upstream WeKnora with minimal
// conflict surface. See README.md in this package.
package semantic

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/Tencent/WeKnora/internal/semantic/dbinspector"
	"github.com/Tencent/WeKnora/internal/types"
)

// Connection types with guided support (test + schema browsing).
const (
	ConnTypeMySQL      = "mysql"
	ConnTypePostgres   = "postgres"
	ConnTypeClickHouse = "clickhouse"
	ConnTypeSQLServer  = "sqlserver"
)

// GuidedTypes lists connection types with full guided support (test
// connection, schema browsing, draft generation). Every other type accepted
// by the Cube driver matrix works in passthrough mode.
var GuidedTypes = []string{ConnTypeMySQL, ConnTypePostgres, ConnTypeClickHouse, ConnTypeSQLServer}

// IsGuidedType reports whether the connection type has a dbinspector adapter.
func IsGuidedType(t string) bool {
	for _, g := range GuidedTypes {
		if g == t {
			return true
		}
	}
	return false
}

// Semantic model lifecycle states.
const (
	ModelStatusDraft         = "draft"
	ModelStatusPublished     = "published"
	ModelStatusPublishFailed = "publish_failed"
)

// Model kinds: cube is the regular semantic model; view aggregates members
// from one or more cubes (authored via the YAML source mode in v1).
const (
	ModelKindCube = "cube"
	ModelKindView = "view"
)

// CubeConnection stores one business database connection. Credentials are
// AES-256-GCM encrypted in ConfigEncrypted (same scheme as data_sources).
type CubeConnection struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Name is the slug used as the Cube dataSource identifier.
	Name string `json:"name" gorm:"type:varchar(64)"`
	// Title is the display name shown in the UI.
	Title string `json:"title" gorm:"type:varchar(255)"`
	// Description helps modelers pick the right connection.
	Description string `json:"description" gorm:"type:text"`
	// Type is a Cube driver name, e.g. mysql/postgres/clickhouse/sqlserver or
	// any other Cube-supported driver in passthrough mode.
	Type string `json:"type" gorm:"type:varchar(50)"`
	// ConfigEncrypted holds the AES-256-GCM encrypted JSON connection config.
	ConfigEncrypted string `json:"-" gorm:"type:text"`
	// Status reflects the last test result: active | error.
	Status string `json:"status" gorm:"type:varchar(32);default:'active'"`
	// LastTestResult carries latency/rowcount samples from the latest test.
	LastTestResult types.JSON `json:"last_test_result" gorm:"type:jsonb"`
	// CreatedBy is the user ID of the creator.
	CreatedBy string         `json:"created_by" gorm:"type:varchar(64)"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the table name for CubeConnection.
func (CubeConnection) TableName() string { return "semantic_connections" }

// BeforeCreate hook to generate UUID.
func (c *CubeConnection) BeforeCreate(_ *gorm.DB) error {
	if c.ID == "" {
		c.ID = uuid.NewString()
	}
	return nil
}

// ConnectionConfig aliases dbinspector's definition so the whole module
// shares one struct (semantic imports dbinspector; the reverse import does
// not exist, so no cycle).
type ConnectionConfig = dbinspector.ConnectionConfig

// SemanticModel is one Cube model (cube or view) authored in the studio.
// The YAML text is the canonical definition; the form is a structured view
// over it so advanced SQL survives round-trips via inline extra fields.
//
//nolint:revive // intentional: SemanticModel is clear in context of semantic package
type SemanticModel struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Name is the cube/view slug in Cube.
	Name string `json:"name" gorm:"type:varchar(64)"`
	// Title is shown to end users and used by the LLM to pick members.
	Title string `json:"title" gorm:"type:varchar(255)"`
	// Description tells the LLM when to use this model and what it means.
	Description string `json:"description" gorm:"type:text"`
	// ConnectionID references the CubeConnection used as dataSource.
	ConnectionID string `json:"connection_id" gorm:"type:varchar(36);index"`
	// Kind is cube or view.
	Kind string `json:"kind" gorm:"type:varchar(16);default:'cube'"`
	// DraftYAML is the canonical editable definition.
	DraftYAML string `json:"draft_yaml" gorm:"type:text"`
	// PublishedYAML is the snapshot of the last successful publish.
	PublishedYAML string `json:"published_yaml,omitempty" gorm:"type:text"`
	// Status is draft | published | publish_failed.
	Status string `json:"status" gorm:"type:varchar(32);default:'draft'"`
	// LastError explains publish_failed.
	LastError string `json:"last_error" gorm:"type:text"`
	// AllowedGroups lists data group slugs allowed to query this model.
	AllowedGroups types.JSON `json:"allowed_groups" gorm:"type:jsonb"`
	// Version increments on every successful publish.
	Version int `json:"version"`
	// PublishedAt is when the current version went live.
	PublishedAt *time.Time `json:"published_at"`
	// CreatedBy is the user ID of the creator.
	CreatedBy string         `json:"created_by" gorm:"type:varchar(64)"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the table name for SemanticModel.
func (SemanticModel) TableName() string { return "semantic_models" }

// BeforeCreate hook to generate UUID.
func (m *SemanticModel) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

// SemanticModelVersion is a publish-time snapshot enabling one-click rollback.
//
//nolint:revive // intentional: SemanticModelVersion is clear in context of semantic package
type SemanticModelVersion struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	ModelID  string `json:"model_id" gorm:"type:varchar(36);index:idx_semver_model_version,unique"`
	// Version matches the SemanticModel.Version at publish time.
	Version       int        `json:"version" gorm:"index:idx_semver_model_version,unique"`
	YAML          string     `json:"yaml" gorm:"type:text"`
	AllowedGroups types.JSON `json:"allowed_groups" gorm:"type:jsonb"`
	// Note documents what changed in this version.
	Note        string    `json:"note" gorm:"type:varchar(255)"`
	PublishedBy string    `json:"published_by" gorm:"type:varchar(64)"`
	PublishedAt time.Time `json:"published_at"`
}

// TableName specifies the table name for SemanticModelVersion.
func (SemanticModelVersion) TableName() string { return "semantic_model_versions" }

// BeforeCreate hook to generate UUID.
func (v *SemanticModelVersion) BeforeCreate(_ *gorm.DB) error {
	if v.ID == "" {
		v.ID = uuid.NewString()
	}
	return nil
}

// DataGroup is a named access group (e.g. sales/purchase/stock) referenced
// by model accessPolicy and resolved into the JWT securityContext.
type DataGroup struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// Name is the slug used in accessPolicy and JWT groups.
	Name        string         `json:"name" gorm:"type:varchar(64)"`
	Title       string         `json:"title" gorm:"type:varchar(255)"`
	Description string         `json:"description" gorm:"type:text"`
	CreatedBy   string         `json:"created_by" gorm:"type:varchar(64)"`
	CreatedAt   time.Time      `json:"created_at"`
	UpdatedAt   time.Time      `json:"updated_at"`
	DeletedAt   gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName specifies the table name for DataGroup.
func (DataGroup) TableName() string { return "semantic_data_groups" }

// BeforeCreate hook to generate UUID.
func (g *DataGroup) BeforeCreate(_ *gorm.DB) error {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	return nil
}

// DataGroupMember links a WeKnora user to a data group.
type DataGroupMember struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	GroupID  string `json:"group_id" gorm:"type:varchar(36);index:idx_semgrp_unique,unique"`
	// UserID is the WeKnora user UUID.
	UserID    string    `json:"user_id" gorm:"type:varchar(64);index:idx_semgrp_unique,unique"`
	CreatedAt time.Time `json:"created_at"`
}

// TableName specifies the table name for DataGroupMember.
func (DataGroupMember) TableName() string { return "semantic_data_group_members" }

// BeforeCreate hook to generate UUID.
func (m *DataGroupMember) BeforeCreate(_ *gorm.DB) error {
	if m.ID == "" {
		m.ID = uuid.NewString()
	}
	return nil
}

// AuditAction constants for the module audit log.
const (
	AuditConnectionCreate = "connection.create"
	AuditConnectionUpdate = "connection.update"
	AuditConnectionDelete = "connection.delete"
	AuditConnectionTest   = "connection.test"
	AuditModelCreate      = "model.create"
	AuditModelUpdate      = "model.update"
	AuditModelDelete      = "model.delete"
	AuditModelPublish     = "model.publish"
	AuditModelUnpublish   = "model.unpublish"
	AuditModelRollback    = "model.rollback"
	AuditGroupCreate      = "group.create"
	AuditGroupUpdate      = "group.update"
	AuditGroupDelete      = "group.delete"
	AuditGroupMembers     = "group.members"
)

// AuditLog records who did what inside the module (self-contained table,
// separate from the platform audit log to avoid upstream coupling).
type AuditLog struct {
	ID       string `json:"id" gorm:"type:varchar(36);primaryKey"`
	TenantID uint64 `json:"tenant_id" gorm:"index"`
	// UserID is the acting WeKnora user.
	UserID string `json:"user_id" gorm:"type:varchar(64)"`
	// Action is one of the Audit* constants.
	Action string `json:"action" gorm:"type:varchar(64)"`
	// Target identifies the affected resource, e.g. "model:sn_list".
	Target string `json:"target" gorm:"type:varchar(128)"`
	// Detail carries structured context (never credentials).
	Detail    types.JSON `json:"detail" gorm:"type:jsonb"`
	CreatedAt time.Time  `json:"created_at"`
}

// TableName specifies the table name for AuditLog.
func (AuditLog) TableName() string { return "semantic_audit_logs" }

// BeforeCreate hook to generate UUID and timestamp.
func (a *AuditLog) BeforeCreate(_ *gorm.DB) error {
	if a.ID == "" {
		a.ID = uuid.NewString()
	}
	if a.CreatedAt.IsZero() {
		a.CreatedAt = time.Now().UTC()
	}
	return nil
}

// StringList is a helper to marshal/unmarshal slug lists stored in jsonb.
func StringList(raw types.JSON) []string {
	if len(raw) == 0 {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal(raw, &out); err != nil {
		return []string{}
	}
	return out
}

// StringListJSON marshals a slug list into jsonb storage.
func StringListJSON(list []string) types.JSON {
	if list == nil {
		list = []string{}
	}
	b, err := json.Marshal(list)
	if err != nil {
		return types.JSON("[]")
	}
	return types.JSON(b)
}
