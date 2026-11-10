package types

import "time"

// Resource type constants for UserResourceFavorite. Extensible — adding a
// new favoritable resource is just a new constant string + a frontend hook,
// no schema change required.
const (
	ResourceTypeKB    = "kb"
	ResourceTypeAgent = "agent"
)

// UserResourceFavorite is a per-(user, tenant) star on a single resource.
// See migration 000047 for the schema rationale (composite key, no FK,
// tenant-scoped on purpose).
type UserResourceFavorite struct {
	UserID       string    `json:"user_id"       gorm:"type:varchar(36);primaryKey"`
	TenantID     uint64    `json:"tenant_id"     gorm:"primaryKey"`
	ResourceType string    `json:"resource_type" gorm:"type:varchar(16);primaryKey"`
	ResourceID   string    `json:"resource_id"   gorm:"type:varchar(64);primaryKey"`
	CreatedAt    time.Time `json:"created_at"    gorm:"autoCreateTime"`
}

// TableName pins the table to the migration's exact name so GORM's
// pluraliser doesn't drift if we ever rename the struct.
func (UserResourceFavorite) TableName() string {
	return "user_resource_favorites"
}

// IsValidFavoriteResourceType returns true for resource types this product
// currently supports favoriting. The handler validates against this list
// to keep clients from inserting arbitrary strings (which would balloon
// the table and break the frontend's segmented view).
func IsValidFavoriteResourceType(t string) bool {
	switch t {
	case ResourceTypeKB, ResourceTypeAgent:
		return true
	default:
		return false
	}
}
