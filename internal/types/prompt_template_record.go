package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"time"
)

// PromptTemplateCategory enumerates the user-facing template groups whose rows
// live in the prompt_templates table. The set is intentionally a closed list
// so the DB CHECK constraint and Go validation stay in sync.
const (
	PromptTemplateCategorySystemPrompt      = "system_prompt"
	PromptTemplateCategoryAgentSystemPrompt = "agent_system_prompt"
	PromptTemplateCategoryContextTemplate   = "context_template"
	PromptTemplateCategoryRewrite           = "rewrite"
	PromptTemplateCategoryFallback          = "fallback"
)

// AllPromptTemplateCategories lists every accepted category, in the order they
// are presented to users. Iteration order is deterministic, which matters for
// seeding from YAML and for tests.
var AllPromptTemplateCategories = []string{
	PromptTemplateCategorySystemPrompt,
	PromptTemplateCategoryAgentSystemPrompt,
	PromptTemplateCategoryContextTemplate,
	PromptTemplateCategoryRewrite,
	PromptTemplateCategoryFallback,
}

// IsValidPromptTemplateCategory reports whether s is one of the accepted
// category constants. Used by API validation and seeding.
func IsValidPromptTemplateCategory(s string) bool {
	for _, c := range AllPromptTemplateCategories {
		if c == s {
			return true
		}
	}
	return false
}

// PromptTemplateI18nMap holds locale → {name, description}.
// Stored as JSON in the i18n column; structurally identical to the
// `map[string]PromptTemplateI18n` used in config.PromptTemplate so values
// round-trip without translation logic.
type PromptTemplateI18nMap map[string]PromptTemplateI18nEntry

// PromptTemplateI18nEntry is a single locale's localised display strings.
// Mirrors config.PromptTemplateI18n — declared here to keep this file free of
// import cycles (the types package can't depend on internal/config).
type PromptTemplateI18nEntry struct {
	Name        string `json:"name,omitempty"        yaml:"name,omitempty"`
	Description string `json:"description,omitempty" yaml:"description,omitempty"`
}

// Value implements driver.Valuer for GORM.
func (m PromptTemplateI18nMap) Value() (driver.Value, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

// Scan implements sql.Scanner for GORM.
func (m *PromptTemplateI18nMap) Scan(value interface{}) error {
	if value == nil {
		*m = nil
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return errors.New("PromptTemplateI18nMap: unsupported scan source")
	}
	if len(b) == 0 {
		*m = nil
		return nil
	}
	return json.Unmarshal(b, m)
}

// PromptTemplateRecord is the GORM model for the prompt_templates table.
// It maps 1:1 to a single row; conversion to/from config.PromptTemplate
// (the in-memory shape used by the rest of the codebase) lives in
// internal/config to avoid import cycles.
type PromptTemplateRecord struct {
	// Category and ID together form the composite primary key.
	// Category MUST be one of AllPromptTemplateCategories — DB-side CHECK
	// rejects everything else.
	Category string `json:"category" gorm:"primaryKey;type:varchar(64);not null"`
	ID       string `json:"id"       gorm:"primaryKey;type:varchar(64);not null"`

	Name        string `json:"name"        gorm:"type:varchar(255);not null;default:''"`
	Description string `json:"description" gorm:"type:text;not null;default:''"`
	Content     string `json:"content"     gorm:"type:text;not null"`
	// UserPrompt is the optional "user-side" prompt used by paired
	// system+user templates such as rewrite. Empty for single-prompt rows.
	UserPrompt string `json:"user_prompt" gorm:"column:user_prompt;type:text;not null;default:''"`

	HasKB        bool `json:"has_kb"         gorm:"column:has_kb;not null;default:false"`
	HasWebSearch bool `json:"has_web_search" gorm:"column:has_web_search;not null;default:false"`
	IsDefault    bool `json:"is_default"     gorm:"column:is_default;not null;default:false"`

	// Mode disambiguates rows inside the same category — currently used by
	// fallback to separate fixed responses (mode="") from model-driven
	// fallback prompts (mode="model"). Empty by default.
	Mode string `json:"mode" gorm:"type:varchar(32);not null;default:''"`

	// I18n stores localised name/description. The English Name/Description
	// columns above are the fallback when no locale-specific entry matches.
	I18n PromptTemplateI18nMap `json:"i18n,omitempty" gorm:"type:jsonb;not null;default:'{}'"`

	// Version is bumped by every Upsert; mainly for audit and future
	// optimistic-locking. Not exposed to writers.
	Version int `json:"version" gorm:"not null;default:1"`

	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// TableName overrides GORM's snake-cased pluralisation just to be explicit;
// keeps the table name decoupled from the struct name.
func (PromptTemplateRecord) TableName() string {
	return "prompt_templates"
}
