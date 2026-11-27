package types

import "context"

// KnowledgeBaseAccess is the caller's read / write / manage rights on one
// knowledge base. It is request-scoped (API-key overlay or tenant RBAC),
// not stored on the knowledge_bases row.
//
// JSON sits next to id / name / capabilities on GET /knowledge-bases:
//
//	"permission": { "read": true, "write": false, "manage": false }
//
// Mapping: read = retrieve (or chat), write = ingest, manage = manage_kbs.
type KnowledgeBaseAccess struct {
	Read   bool `json:"read"`
	Write  bool `json:"write"`
	Manage bool `json:"manage"`
}

// CallerKnowledgeBasePermission reports what the current request may do to kb.
// An API-key scope always wins over tenant RBAC, including FullAccess keys
// that would otherwise look like TenantRoleOwner.
func CallerKnowledgeBasePermission(ctx context.Context, kb *KnowledgeBase, callerTenantID uint64) KnowledgeBaseAccess {
	if kb == nil {
		return KnowledgeBaseAccess{}
	}
	if scope, ok := TenantAPIKeyScopeFromContext(ctx); ok {
		return knowledgeBaseAccessFromAPIKey(scope, kb.ID)
	}
	if callerTenantID != 0 && kb.TenantID != callerTenantID {
		return KnowledgeBaseAccess{Read: true}
	}
	return knowledgeBaseAccessFromTenantRole(ctx, kb)
}

func knowledgeBaseAccessFromAPIKey(scope TenantAPIKeyScope, kbID string) KnowledgeBaseAccess {
	return KnowledgeBaseAccess{
		Read:   scope.AllowsKnowledgeBaseCapability(kbID, APIKeyCapabilityRetrieve),
		Write:  scope.AllowsKnowledgeBaseCapability(kbID, APIKeyCapabilityIngest),
		Manage: scope.AllowsKnowledgeBaseCapability(kbID, APIKeyCapabilityManageKnowledgeBases),
	}
}

func knowledgeBaseAccessFromTenantRole(ctx context.Context, kb *KnowledgeBase) KnowledgeBaseAccess {
	role := TenantRoleFromContext(ctx)
	if role.HasPermission(TenantRoleAdmin) {
		return KnowledgeBaseAccess{Read: true, Write: true, Manage: true}
	}
	if role == TenantRoleContributor {
		userID, _ := UserIDFromContext(ctx)
		if userID != "" && kb.CreatorID == userID {
			return KnowledgeBaseAccess{Read: true, Write: true, Manage: true}
		}
		return KnowledgeBaseAccess{Read: true}
	}
	return KnowledgeBaseAccess{Read: true}
}
