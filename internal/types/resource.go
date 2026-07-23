package types

import (
	"strings"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

// Stable resource reference and lifecycle constants.
const (
	ResourceScheme              = "resource://"
	ResourceHandleLength        = 22
	ResourceStateActive         = "active"
	ResourceStateDeleted        = "deleted"
	ResourceLifecyclePersistent = "persistent"
	ResourceLifecycleTemporary  = "temporary"
)

// StoredResource is the stable application identity of one stored object. PhysicalPath
// is deliberately internal: API responses, persisted rich text and LLM prompts
// use resource://<handle> instead.
type StoredResource struct {
	ID               string         `json:"id" gorm:"type:varchar(36);primaryKey"`
	Handle           string         `json:"handle" gorm:"type:varchar(22);not null;uniqueIndex"`
	TenantID         uint64         `json:"tenant_id" gorm:"not null;index"`
	StorageBackendID string         `json:"storage_backend_id,omitempty" gorm:"type:varchar(36);index"`
	Provider         string         `json:"provider" gorm:"type:varchar(32);not null"`
	PhysicalPath     string         `json:"-" gorm:"type:text;not null"`
	LocationHash     string         `json:"-" gorm:"type:varchar(64);not null"`
	Kind             string         `json:"kind" gorm:"type:varchar(32);not null;default:'file'"`
	MimeType         string         `json:"mime_type,omitempty" gorm:"type:varchar(255);not null;default:''"`
	OriginalName     string         `json:"original_name,omitempty" gorm:"type:varchar(1024);not null;default:''"`
	Size             int64          `json:"size" gorm:"not null;default:0"`
	ContentHash      string         `json:"content_hash,omitempty" gorm:"type:varchar(64);not null;default:''"`
	Lifecycle        string         `json:"lifecycle" gorm:"type:varchar(16);not null;default:'persistent'"`
	ExpiresAt        *time.Time     `json:"expires_at,omitempty"`
	State            string         `json:"state" gorm:"type:varchar(16);not null;default:'active'"`
	CreatedAt        time.Time      `json:"created_at"`
	UpdatedAt        time.Time      `json:"updated_at"`
	DeletedAt        gorm.DeletedAt `json:"deleted_at" gorm:"index"`
}

// TableName returns the resource registry table name.
func (StoredResource) TableName() string { return "resources" }

// BeforeCreate fills resource defaults before persistence.
func (r *StoredResource) BeforeCreate(_ *gorm.DB) error {
	if r.ID == "" {
		r.ID = uuid.NewString()
	}
	if r.Kind == "" {
		r.Kind = "file"
	}
	if r.Lifecycle == "" {
		r.Lifecycle = ResourceLifecyclePersistent
	}
	if r.State == "" {
		r.State = ResourceStateActive
	}
	return nil
}

// ResourceBinding connects a resource to a domain object that owns or uses it.
type ResourceBinding struct {
	ID         string    `json:"id" gorm:"type:varchar(36);primaryKey"`
	ResourceID string    `json:"resource_id" gorm:"type:varchar(36);not null;index"`
	TenantID   uint64    `json:"tenant_id" gorm:"not null;index"`
	OwnerType  string    `json:"owner_type" gorm:"type:varchar(32);not null"`
	OwnerID    string    `json:"owner_id" gorm:"type:varchar(64);not null"`
	Relation   string    `json:"relation" gorm:"type:varchar(32);not null;default:'attachment'"`
	CreatedAt  time.Time `json:"created_at"`
}

// TableName returns the resource binding table name.
func (ResourceBinding) TableName() string { return "resource_bindings" }

// BeforeCreate assigns an ID before persistence.
func (b *ResourceBinding) BeforeCreate(_ *gorm.DB) error {
	if b.ID == "" {
		b.ID = uuid.NewString()
	}
	return nil
}

// ResourceAccessGrant is a revocable, expiring read capability.
type ResourceAccessGrant struct {
	ID          string     `json:"id" gorm:"type:varchar(36);primaryKey"`
	TokenHash   string     `json:"-" gorm:"type:varchar(64);not null;uniqueIndex"`
	ResourceID  string     `json:"resource_id" gorm:"type:varchar(36);not null;index"`
	AccessScope string     `json:"access_scope" gorm:"type:varchar(16);not null;default:'read'"`
	ExpiresAt   time.Time  `json:"expires_at" gorm:"not null;index"`
	RevokedAt   *time.Time `json:"revoked_at,omitempty"`
	CreatedAt   time.Time  `json:"created_at"`
}

// TableName returns the resource access grant table name.
func (ResourceAccessGrant) TableName() string { return "resource_access_grants" }

// BeforeCreate assigns an ID before persistence.
func (g *ResourceAccessGrant) BeforeCreate(_ *gorm.DB) error {
	if g.ID == "" {
		g.ID = uuid.NewString()
	}
	return nil
}

// BuildResourcePath creates a stable application resource reference.
func BuildResourcePath(handle string) string {
	return ResourceScheme + strings.TrimSpace(handle)
}

// ParseResourcePath validates and extracts a canonical resource handle.
func ParseResourcePath(value string) (string, bool) {
	value = strings.TrimSpace(value)
	if !strings.HasPrefix(value, ResourceScheme) {
		return "", false
	}
	handle := strings.TrimPrefix(value, ResourceScheme)
	if len(handle) != ResourceHandleLength {
		return "", false
	}
	for _, char := range handle {
		if (char < 'a' || char > 'z') && (char < 'A' || char > 'Z') &&
			(char < '0' || char > '9') && char != '_' && char != '-' {
			return "", false
		}
	}
	return handle, true
}
