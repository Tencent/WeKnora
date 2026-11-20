package types

import (
	"database/sql/driver"
	"encoding/json"
	"errors"
	"strings"
)

// KnowledgeBasePermissionMap is kb_id -> enabled KB-scoped capabilities
// (retrieve, ingest, manage_kbs). An empty map means every allow-listed
// knowledge base inherits the key's global capabilities.
type KnowledgeBasePermissionMap map[string]StringArray

func (m KnowledgeBasePermissionMap) Value() (driver.Value, error) {
	if m == nil {
		return []byte("{}"), nil
	}
	return json.Marshal(m)
}

func (m *KnowledgeBasePermissionMap) Scan(src any) error {
	if src == nil {
		*m = KnowledgeBasePermissionMap{}
		return nil
	}
	var raw []byte
	switch v := src.(type) {
	case []byte:
		raw = v
	case string:
		raw = []byte(v)
	default:
		return errors.New("KnowledgeBasePermissionMap.Scan: unsupported source type")
	}
	if len(raw) == 0 {
		*m = KnowledgeBasePermissionMap{}
		return nil
	}
	var out KnowledgeBasePermissionMap
	if err := json.Unmarshal(raw, &out); err != nil {
		return err
	}
	if out == nil {
		out = KnowledgeBasePermissionMap{}
	}
	*m = out
	return nil
}

func isKBPermissionCapability(c APIKeyCapability) bool {
	switch c {
	case APIKeyCapabilityRetrieve, APIKeyCapabilityIngest, APIKeyCapabilityManageKnowledgeBases:
		return true
	default:
		return false
	}
}

func storedKBPermissionCapability(c APIKeyCapability) APIKeyCapability {
	c = NormalizeAPIKeyCapability(c)
	if c == APIKeyCapabilityChat {
		return APIKeyCapabilityRetrieve
	}
	return c
}

func knowledgeBasePermissionCeiling(capabilities StringArray, c APIKeyCapability) bool {
	c = storedKBPermissionCapability(c)
	norm := NormalizeAPIKeyCapabilities(capabilities)
	has := func(want APIKeyCapability) bool {
		for _, item := range norm {
			if item == string(want) {
				return true
			}
		}
		return false
	}
	if c == APIKeyCapabilityRetrieve {
		return has(APIKeyCapabilityRetrieve) || has(APIKeyCapabilityChat)
	}
	return has(c)
}

func grantsContainCapability(grants StringArray, c APIKeyCapability) bool {
	c = storedKBPermissionCapability(c)
	if c == "" || !isKBPermissionCapability(c) {
		return false
	}
	for _, item := range NormalizeAPIKeyCapabilities(grants) {
		if item == string(c) {
			return true
		}
	}
	return false
}

func normalizeKnowledgeBasePermissionMap(
	ids StringArray,
	perms KnowledgeBasePermissionMap,
	capabilities StringArray,
) KnowledgeBasePermissionMap {
	if len(perms) == 0 {
		return KnowledgeBasePermissionMap{}
	}
	allowed := map[string]struct{}{}
	for _, id := range normalizeIDArray(ids) {
		allowed[id] = struct{}{}
	}
	out := KnowledgeBasePermissionMap{}
	for kbID, grants := range perms {
		kbID = strings.TrimSpace(kbID)
		if kbID == "" {
			continue
		}
		if _, ok := allowed[kbID]; !ok {
			continue
		}
		filtered := StringArray{}
		seen := map[string]struct{}{}
		for _, item := range grants {
			cap := storedKBPermissionCapability(APIKeyCapability(item))
			if !isKBPermissionCapability(cap) {
				continue
			}
			if !knowledgeBasePermissionCeiling(capabilities, cap) {
				continue
			}
			s := string(cap)
			if _, dup := seen[s]; dup {
				continue
			}
			seen[s] = struct{}{}
			filtered = append(filtered, s)
		}
		if len(filtered) == 0 {
			continue
		}
		out[kbID] = filtered
	}
	return out
}

// NormalizeKnowledgeBasePermissions intersects per-KB grants with the allow-list
// and the key's global capability ceiling. Empty input stays empty (inherit).
// An explicit per-KB entry that normalizes to no capabilities is dropped from
// the allow-list (same as unchecking 可读 and clearing the row).
func NormalizeKnowledgeBasePermissions(
	ids StringArray,
	perms KnowledgeBasePermissionMap,
	capabilities StringArray,
	fullAccess bool,
) (StringArray, KnowledgeBasePermissionMap) {
	if fullAccess {
		return nil, KnowledgeBasePermissionMap{}
	}
	normalizedIDs := normalizeIDArray(ids)
	if len(perms) == 0 {
		return normalizedIDs, KnowledgeBasePermissionMap{}
	}
	trimmed := KnowledgeBasePermissionMap{}
	for kbID, grants := range perms {
		kbID = strings.TrimSpace(kbID)
		if kbID == "" {
			continue
		}
		trimmed[kbID] = grants
	}
	normalizedPerms := normalizeKnowledgeBasePermissionMap(normalizedIDs, trimmed, capabilities)
	kept := StringArray{}
	for _, id := range normalizedIDs {
		if _, hadEntry := trimmed[id]; !hadEntry {
			kept = append(kept, id)
			continue
		}
		if _, ok := normalizedPerms[id]; ok {
			kept = append(kept, id)
		}
	}
	return kept, normalizeKnowledgeBasePermissionMap(kept, normalizedPerms, capabilities)
}

// KnowledgeBasePermissionsExceedCeiling reports whether any stored grant is
// above the key's global capabilities (used to 400 on create/update).
func KnowledgeBasePermissionsExceedCeiling(perms KnowledgeBasePermissionMap, capabilities StringArray) bool {
	if len(perms) == 0 {
		return false
	}
	for _, grants := range perms {
		for _, item := range grants {
			cap := storedKBPermissionCapability(APIKeyCapability(item))
			if cap == "" {
				continue
			}
			if !isKBPermissionCapability(cap) {
				return true
			}
			if !knowledgeBasePermissionCeiling(capabilities, cap) {
				return true
			}
		}
	}
	return false
}
