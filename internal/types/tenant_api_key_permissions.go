package types

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// APIKeyKBPermission bounds access to one KB. These levels do not grant route
// capabilities, ownership, or sharing rights: all of those are checked separately.
type APIKeyKBPermission string

// KB permission levels are cumulative: manage includes write, which includes read.
const (
	APIKeyKBRead   APIKeyKBPermission = "read"
	APIKeyKBWrite  APIKeyKBPermission = "write"
	APIKeyKBManage APIKeyKBPermission = "manage"
)

func (p APIKeyKBPermission) rank() int {
	switch p {
	case APIKeyKBRead:
		return 1
	case APIKeyKBWrite:
		return 2
	case APIKeyKBManage:
		return 3
	default:
		return 0
	}
}

// Allows reports whether a valid required permission fits within this level.
func (p APIKeyKBPermission) Allows(required APIKeyKBPermission) bool {
	return required.rank() > 0 && p.rank() >= required.rank()
}

// APIKeyKBPermissions is nil for legacy uniform authorization. An explicit
// empty map denies every KB. Never collapse {} into nil during persistence or
// request normalization: doing so would turn no access into unrestricted access.
type APIKeyKBPermissions map[string]APIKeyKBPermission

// Value implements driver.Valuer without collapsing an empty map into SQL NULL.
func (p APIKeyKBPermissions) Value() (driver.Value, error) {
	if p == nil {
		return nil, nil
	}
	return json.Marshal(p)
}

// Scan implements sql.Scanner for both PostgreSQL and SQLite JSON storage.
func (p *APIKeyKBPermissions) Scan(value any) error {
	if value == nil {
		*p = nil
		return nil
	}
	var data []byte
	switch v := value.(type) {
	case []byte:
		data = v
	case string:
		data = []byte(v)
	default:
		return fmt.Errorf("invalid API key KB permissions storage type %T", value)
	}
	return json.Unmarshal(data, p)
}

// Clone returns an independent grant map while preserving legacy nil semantics.
func (p APIKeyKBPermissions) Clone() APIKeyKBPermissions {
	if p == nil {
		return nil
	}
	cloned := make(APIKeyKBPermissions, len(p))
	for id, permission := range p {
		cloned[id] = permission
	}
	return cloned
}

// IDs returns sorted KB IDs that allow the required permission.
func (p APIKeyKBPermissions) IDs(required APIKeyKBPermission) StringArray {
	ids := make(StringArray, 0, len(p))
	for id, permission := range p {
		if id != "" && permission.Allows(required) {
			ids = append(ids, id)
		}
	}
	sort.Strings(ids)
	return ids
}

// ValidateAPIKeyKBPermissions rejects ambiguous configurations instead of
// silently ignoring malformed grants or merging two different scope models.
func ValidateAPIKeyKBPermissions(permissions APIKeyKBPermissions, legacyIDs []string) error {
	if permissions == nil {
		return nil
	}
	if len(legacyIDs) > 0 {
		return fmt.Errorf("knowledge_base_permissions and knowledge_base_ids are mutually exclusive")
	}
	for id, permission := range permissions {
		if id == "" || strings.TrimSpace(id) != id {
			return fmt.Errorf("knowledge_base_permissions contains an invalid knowledge base ID")
		}
		if !permission.Allows(APIKeyKBRead) {
			return fmt.Errorf("knowledge_base_permissions must use read, write, or manage")
		}
	}
	return nil
}

// ForKnowledgeBasePermission projects the existing scope for one operation.
// Every existing allow-list guard and list/search filter consumes this same
// projection, including operations whose KB is resolved from a document ID/body.
func (s TenantAPIKeyScope) ForKnowledgeBasePermission(required APIKeyKBPermission) TenantAPIKeyScope {
	s.KnowledgeBasePermission = required
	return s.Normalize()
}

// AllowsKnowledgeBasePermission checks an explicit resource operation against the scope.
func (s TenantAPIKeyScope) AllowsKnowledgeBasePermission(id string, required APIKeyKBPermission) bool {
	if s.KnowledgeBasePermissions == nil {
		return s.AllowsKnowledgeBase(id)
	}
	return s.KnowledgeBasePermissions[id].Allows(required)
}
