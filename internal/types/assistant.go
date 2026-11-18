package types

import (
	"database/sql/driver"
	"encoding/json"
	"strings"
)

// AssistantAccessLevel is how far the WeKnora assistant may act on the
// caller's behalf. It only narrows what the caller's role already allows.
type AssistantAccessLevel string

const (
	// AssistantAccessReadOnly offers no operation that changes anything.
	AssistantAccessReadOnly AssistantAccessLevel = "read_only"
	// AssistantAccessConfirm asks the user before every change. It is the
	// level when neither the caller nor the workspace chose one.
	AssistantAccessConfirm AssistantAccessLevel = "confirm"
	// AssistantAccessAutoWrite runs recoverable changes without asking; a
	// destructive change still asks.
	AssistantAccessAutoWrite AssistantAccessLevel = "auto_write"
)

// MaxAssistantDisplayNameRunes bounds the workspace's assistant name.
const MaxAssistantDisplayNameRunes = 24

var assistantAccessRank = map[AssistantAccessLevel]int{
	AssistantAccessReadOnly:  0,
	AssistantAccessConfirm:   1,
	AssistantAccessAutoWrite: 2,
}

// Valid reports whether l is a known level.
func (l AssistantAccessLevel) Valid() bool {
	_, ok := assistantAccessRank[l]
	return ok
}

// AssistantConfig is the workspace's assistant setting, stored as JSONB on
// the tenant.
type AssistantConfig struct {
	// DisplayName replaces the product name in the assistant UI; empty keeps
	// the default.
	DisplayName string `json:"display_name"`
	// MaxAccessLevel is the highest level a member may pick; empty means
	// AssistantAccessConfirm.
	MaxAccessLevel AssistantAccessLevel `json:"max_access_level"`
}

// Normalize trims the name and resets an unknown ceiling to the default.
func (c *AssistantConfig) Normalize() {
	c.DisplayName = strings.TrimSpace(c.DisplayName)
	if !c.MaxAccessLevel.Valid() {
		c.MaxAccessLevel = AssistantAccessConfirm
	}
}

// EffectiveAccessLevel is the level a run gets for the requested one: the
// default when unset or unknown, never above the workspace ceiling. A nil
// config caps at the default.
func (c *AssistantConfig) EffectiveAccessLevel(requested AssistantAccessLevel) AssistantAccessLevel {
	ceiling := AssistantAccessConfirm
	if c != nil && c.MaxAccessLevel.Valid() {
		ceiling = c.MaxAccessLevel
	}
	level := requested
	if !level.Valid() {
		level = AssistantAccessConfirm
	}
	if assistantAccessRank[level] > assistantAccessRank[ceiling] {
		return ceiling
	}
	return level
}

// CapAccessLevel is the level a run of an agent whose ceiling is ceiling
// gets for the requested level. An unset or unknown ceiling is auto_write;
// an unset or unknown request gets the ceiling.
func CapAccessLevel(requested, ceiling AssistantAccessLevel) AssistantAccessLevel {
	if !ceiling.Valid() {
		ceiling = AssistantAccessAutoWrite
	}
	if !requested.Valid() || assistantAccessRank[requested] > assistantAccessRank[ceiling] {
		return ceiling
	}
	return requested
}

// Value implements driver.Valuer for JSON storage.
func (c AssistantConfig) Value() (driver.Value, error) { return json.Marshal(c) }

// Scan implements sql.Scanner for JSON storage.
func (c *AssistantConfig) Scan(value interface{}) error {
	if value == nil {
		return nil
	}
	var b []byte
	switch v := value.(type) {
	case []byte:
		b = v
	case string:
		b = []byte(v)
	default:
		return nil
	}
	if len(b) == 0 {
		return nil
	}
	return json.Unmarshal(b, c)
}
