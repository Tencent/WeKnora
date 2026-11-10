package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
)

// JSONMap is a map[string]any that round-trips through Postgres JSONB
// columns via GORM. The standard library's database/sql doesn't know how
// to scan JSONB into a typed map, so we hand-roll Value() / Scan().
//
// Lives here (not in /utils) because the only callers are types whose
// schemas already declare JSONB columns — keeping the helper next to its
// users avoids a circular import in any package that already pulls in
// /types.
type JSONMap map[string]any

// Value implements driver.Valuer. Returns nil when the map is nil so
// JSONB columns store SQL NULL instead of "null".
func (m JSONMap) Value() (driver.Value, error) {
	if m == nil {
		return nil, nil
	}
	return json.Marshal(m)
}

// Scan implements sql.Scanner. Accepts both []byte (real JSONB) and
// string (some drivers under sqlmock); anything else is a programming
// error.
func (m *JSONMap) Scan(src any) error {
	if src == nil {
		*m = nil
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("JSONMap.Scan: unsupported source type")
	}
	if len(raw) == 0 {
		*m = nil
		return nil
	}
	return json.Unmarshal(raw, m)
}
