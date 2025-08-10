package types

import (
	"database/sql/driver"
	"encoding/json"
	"time"

	"gorm.io/gorm"
)

const (
	InitDefaultTenantID uint = 1
)

// Tenant represents the tenant
type Tenant struct {
	// ID
	ID uint `yaml:"id" json:"id" gorm:"primaryKey"`
	// Name
	Name string `yaml:"name" json:"name"`
	// Description
	Description string `yaml:"description" json:"description"`
	// API key
	APIKey string `yaml:"api_key" json:"api_key"`
	// Status
	Status string `yaml:"status" json:"status" gorm:"default:'active'"`
	// Retriever engines
	RetrieverEngines RetrieverEngines `yaml:"retriever_engines" json:"retriever_engines" gorm:"type:json"`
	// Business
	Business string `yaml:"business" json:"business"`
	// Storage quota (Bytes), default is 10GB, including vector, original file, text, index, etc.
	StorageQuota int64 `yaml:"storage_quota" json:"storage_quota" gorm:"default:10737418240"`
	// Storage used (Bytes)
	StorageUsed int64 `yaml:"storage_used" json:"storage_used" gorm:"default:0"`
	// Creation time
	CreatedAt time.Time `yaml:"created_at" json:"created_at"`
	// Last updated time
	UpdatedAt time.Time `yaml:"updated_at" json:"updated_at"`
	// Deletion time
	DeletedAt gorm.DeletedAt `yaml:"deleted_at" json:"deleted_at" gorm:"index"`
}

type RetrieverEngines struct {
	Engines []RetrieverEngineParams `yaml:"engines" json:"engines" gorm:"type:json"`
}

func (t *Tenant) BeforeCreate(tx *gorm.DB) error {
	if t.RetrieverEngines.Engines == nil {
		t.RetrieverEngines.Engines = []RetrieverEngineParams{}
	}
	return nil
}

// Value implements the driver.Valuer interface, used to convert RetrieverEngines to database value
func (c RetrieverEngines) Value() (driver.Value, error) {
	return json.Marshal(c)
}

// Scan implements the sql.Scanner interface, used to convert database value to RetrieverEngines
func (c *RetrieverEngines) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	b, ok := value.([]byte)
	if !ok {
		return nil
	}
	return json.Unmarshal(b, c)
}
