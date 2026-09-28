package types

import "time"

// ModelCatalogConfig is the singleton platform catalog override. Version is
// an optimistic lock; History contains previous console overlays, never the
// deployment file (which can contain secrets).
type ModelCatalogConfig struct {
	ID        uint      `gorm:"primaryKey" json:"-"`
	Version   uint64    `json:"version"`
	Overlay   JSON      `gorm:"type:jsonb" json:"overlay"`
	History   JSON      `gorm:"type:jsonb" json:"-"`
	UpdatedBy string    `json:"updated_by"`
	UpdatedAt time.Time `json:"updated_at"`
}
