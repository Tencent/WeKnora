package types

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"
)

// SystemSetting is a platform-wide (NOT workspace-scoped) tunable that
// SystemAdmins can edit at runtime via the management UI without
// restarting the service. Persisted in the system_settings table
// (migration 000053).
//
// The 3-tier resolver in service.SystemSettingService reads in priority
// order: DB row > os.Getenv(EnvName) > built-in default. The Service
// owns the registry of legal keys + their default values + their ENV
// names; this type is just the on-disk shape.
//
// Value is stored as JSONB so the same column can hold ints / strings /
// booleans / arrays. ValueType ("int" | "string" | "bool") tells callers
// (and the AsXxx helpers below) how to decode the raw bytes. Booleans
// roundtrip as `true`/`false`, ints as `42`, strings as `"foo"`.
type SystemSetting struct {
	ID    uint64 `gorm:"primaryKey"      json:"id"`
	Key   string `gorm:"type:varchar(128);uniqueIndex;not null" json:"key"`
	Value JSON   `gorm:"type:jsonb;not null"                    json:"value"`
	// ValueType is one of "int", "string", "bool". Service layer rejects
	// updates whose payload type does not match; UI uses it to pick
	// InputNumber vs Input vs Switch.
	ValueType string `gorm:"type:varchar(16);not null"  json:"value_type"`
	// Category groups settings in the management UI ("limits", "agent",
	// "auth", ...). Free-form string so adding a new category is a
	// data-only change.
	Category    string `gorm:"type:varchar(32);not null"  json:"category"`
	Description string `gorm:"type:text;not null;default:''" json:"description"`
	// IsSecret reserves UI affordances for P3 (mask + reveal-with-confirm).
	// In P1 every row is is_secret=false; service Update accepts the
	// column but does not yet enforce special handling.
	IsSecret bool `gorm:"not null;default:false"  json:"is_secret"`
	// RequiresRestart reserves UI affordances for P3 (banner "this
	// change won't take effect until the next restart"). In P1 the
	// only seeded key is per-request, so always false.
	RequiresRestart bool      `gorm:"not null;default:false"  json:"requires_restart"`
	LastModifiedBy  string    `gorm:"type:varchar(36);not null;default:''" json:"last_modified_by"`
	CreatedAt       time.Time `json:"created_at"`
	UpdatedAt       time.Time `json:"updated_at"`

	// Enum is populated by the service layer (NOT persisted) from the
	// in-code registry. Empty/nil means "free-form input"; non-empty
	// means the UI should render a select with these options. Tagged
	// `gorm:"-"` so GORM never tries to read/write the column.
	Enum []string `gorm:"-" json:"enum,omitempty"`

	// LastModifiedByName is a display label resolved from LastModifiedBy
	// (the user's UUID) at handler time — username when available,
	// otherwise email. Empty for virtual rows that were never persisted
	// (LastModifiedBy=""). Not stored: derived per request so renaming
	// a user reflects on the next reload without a backfill job.
	LastModifiedByName string `gorm:"-" json:"last_modified_by_name,omitempty"`
}

// TableName pins the schema to migration 000053 — GORM's default
// pluralisation would yield "system_settings" anyway, but spelling it
// out shields against future renames.
func (SystemSetting) TableName() string {
	return "system_settings"
}

// AsInt decodes the raw JSON value as an int64. Returns an error if
// ValueType is not "int" or the JSON does not parse as a number.
//
// Accepts both `42` (number literal) and `"42"` (quoted string) so
// hand-edited DB rows are tolerated. The service-layer Update path
// always writes a number literal.
func (s *SystemSetting) AsInt() (int64, error) {
	if s.ValueType != "int" {
		return 0, fmt.Errorf("system_setting %q: value_type=%q, not int", s.Key, s.ValueType)
	}
	if len(s.Value) == 0 {
		return 0, fmt.Errorf("system_setting %q: empty value", s.Key)
	}
	// Try number first (canonical form).
	var n int64
	if err := json.Unmarshal(s.Value, &n); err == nil {
		return n, nil
	}
	// Fall back to quoted string ("42") — tolerate hand-edited rows.
	var raw string
	if err := json.Unmarshal(s.Value, &raw); err == nil {
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v, nil
		}
	}
	return 0, fmt.Errorf("system_setting %q: cannot parse %s as int", s.Key, string(s.Value))
}

// AsString decodes the raw JSON value as a string. Requires ValueType
// to be "string"; the JSON must be a JSON string ("foo"), not a number
// or object.
func (s *SystemSetting) AsString() (string, error) {
	if s.ValueType != "string" {
		return "", fmt.Errorf("system_setting %q: value_type=%q, not string", s.Key, s.ValueType)
	}
	if len(s.Value) == 0 {
		return "", nil
	}
	var v string
	if err := json.Unmarshal(s.Value, &v); err != nil {
		return "", fmt.Errorf("system_setting %q: %w", s.Key, err)
	}
	return v, nil
}

// AsBool decodes the raw JSON value as a bool. Requires ValueType to
// be "bool"; the JSON must be `true` or `false`.
func (s *SystemSetting) AsBool() (bool, error) {
	if s.ValueType != "bool" {
		return false, fmt.Errorf("system_setting %q: value_type=%q, not bool", s.Key, s.ValueType)
	}
	if len(s.Value) == 0 {
		return false, errors.New("empty value")
	}
	var v bool
	if err := json.Unmarshal(s.Value, &v); err != nil {
		return false, fmt.Errorf("system_setting %q: %w", s.Key, err)
	}
	return v, nil
}

// AsStringList decodes the raw JSON value as []string. Requires
// ValueType to be "string_list"; the JSON must be a JSON array of
// strings, e.g. `["example.com", "*.foo.bar", "10.0.0.0/8"]`.
//
// Returns an empty (non-nil) slice for an empty list, so callers can
// treat the absence of items uniformly and still iterate without nil checks.
func (s *SystemSetting) AsStringList() ([]string, error) {
	if s.ValueType != "string_list" {
		return nil, fmt.Errorf("system_setting %q: value_type=%q, not string_list", s.Key, s.ValueType)
	}
	if len(s.Value) == 0 {
		return []string{}, nil
	}
	var v []string
	if err := json.Unmarshal(s.Value, &v); err != nil {
		return nil, fmt.Errorf("system_setting %q: %w", s.Key, err)
	}
	if v == nil {
		v = []string{}
	}
	return v, nil
}
