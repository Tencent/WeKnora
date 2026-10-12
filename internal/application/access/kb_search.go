package access

import (
	"context"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// AuthorizeKBAccess rejects multi-KB searches whose scope includes a KB
// that the original caller is not entitled to read. Exact upstream grants
// support shared KB/agent execution; other foreign KBs need an organization
// permission check for the caller via KBPermissions, applying the 3-D cap
// (share role + caller's tenant-org role + tenant Viewer cap) introduced
// in Plan 3 of #1303.
//
// Returning NotFound rather than Forbidden avoids leaking the existence
// of unauthorized KB IDs that the caller could not otherwise observe.
// Structured logs record the rejection with the offending kb_id (always
// safe — KB IDs are UUIDs without sensitive content) and the requesting
// tenant for audit.
func AuthorizeKBAccess(
	ctx context.Context,
	shares KBShareLookup,
	kbs []*types.KnowledgeBase,
) error {
	if len(kbs) == 0 {
		return nil
	}

	kbIDs := make([]string, 0, len(kbs))
	for _, kb := range kbs {
		kbIDs = append(kbIDs, kb.ID)
	}
	if err := types.AuthorizeTenantAPIKeyKnowledgeBases(ctx, kbIDs...); err != nil {
		return err
	}

	requestTenantID := types.CallerFromContext(ctx).TenantID
	permissions := NewKBPermissions(ctx, shares)

	for _, kb := range kbs {
		hasPermission, permErr := permissions.Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
		if permErr != nil {
			logger.ErrorWithFields(ctx, permErr, map[string]interface{}{
				"caller_tenant_id": requestTenantID,
				"kb_tenant_id":     kb.TenantID,
				"kb_id":            kb.ID,
				"reason":           "shared-KB permission lookup failed",
			})
			return apperrors.NewInternalServerError("failed to verify knowledge base access")
		}
		if !hasPermission {
			logger.WarnWithFields(ctx, logger.Fields{
				"caller_tenant_id": requestTenantID,
				"kb_tenant_id":     kb.TenantID,
				"kb_id":            kb.ID,
				"reason":           "tenant lacks viewer permission for foreign-tenant KB",
			}, "search scope rejected: unauthorized foreign-tenant KB")
			return apperrors.NewNotFoundError("knowledge base not found")
		}
	}
	return nil
}
