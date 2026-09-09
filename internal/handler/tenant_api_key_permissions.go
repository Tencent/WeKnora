package handler

import (
	"context"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// validateAPIKeyKBGrants authorizes delegation to a machine credential. The
// management route already requires workspace Owner. Shared-agent visibility
// alone is deliberately insufficient: only a direct workspace share can be
// delegated, and the live sharing check will run again on each resource access.
func validateAPIKeyKBGrants(
	ctx context.Context, tenantID uint64, grants types.APIKeyKBPermissions,
	lookup func(context.Context, string) (*types.KnowledgeBase, error), shares access.KBShareLookup,
) *errors.AppError {
	permissions := access.NewKBSharePermissions(ctx, shares, tenantID, types.TenantRoleOwner)
	for _, id := range grants.IDs(types.APIKeyKBRead) {
		kb, err := lookup(ctx, id)
		if err != nil || kb == nil {
			return errors.NewValidationError("knowledge_base_permissions contains an unknown knowledge base")
		}
		if kb.TenantID == tenantID {
			continue
		}
		required := types.OrgRoleViewer
		if grants[id].Allows(types.APIKeyKBWrite) {
			required = types.OrgRoleEditor
		}
		allowed, err := permissions.Check(id, required)
		if err != nil {
			return errors.NewServiceUnavailableError("cannot verify shared knowledge base permissions")
		}
		if !allowed {
			return errors.NewForbiddenError(
				"knowledge_base_permissions exceeds this workspace's shared knowledge base access",
			)
		}
	}
	return nil
}

func validateTenantAPIKeyRequest(
	ctx context.Context,
	kbService interfaces.KnowledgeBaseService,
	tenantID uint64,
	req tenantAPIKeyCreateRequest,
	shares access.KBShareLookup,
) *errors.AppError {
	if strings.TrimSpace(req.Name) == "" {
		return errors.NewValidationError("name is required")
	}
	if req.FullAccess {
		return nil
	}
	caps := types.NormalizeAPIKeyCapabilities(types.StringArray(req.Capabilities))
	if len(caps) == 0 {
		return errors.NewValidationError("capabilities are required for scoped API keys")
	}
	for _, cap := range req.Capabilities {
		if strings.TrimSpace(cap) == "" {
			continue
		}
		if types.NormalizeAPIKeyCapability(types.APIKeyCapability(cap)) == "" {
			return errors.NewValidationError("capabilities contains an unknown capability")
		}
	}
	if err := types.ValidateAPIKeyKBPermissions(req.KnowledgeBasePermissions, req.KnowledgeBaseIDs); err != nil {
		return errors.NewValidationError(err.Error())
	}
	if req.KnowledgeBasePermissions != nil {
		if len(req.KnowledgeBasePermissions) == 0 {
			return nil
		}
		return validateAPIKeyKBGrants(
			ctx,
			tenantID,
			req.KnowledgeBasePermissions,
			kbService.GetKnowledgeBaseByID,
			shares,
		)
	}
	return validateTenantAPIKeyKnowledgeBaseIDs(ctx, kbService, tenantID, req.KnowledgeBaseIDs)
}

// validateTenantAPIKeyKnowledgeBaseIDs 校验白名单中的知识库真实存在且属于目标租户。
// 入参是请求上下文、知识库服务、租户 ID 和待授权 ID；成功无返回值，失败返回可直接响应的应用错误。
func validateTenantAPIKeyKnowledgeBaseIDs(
	ctx context.Context,
	kbService interfaces.KnowledgeBaseService,
	tenantID uint64,
	knowledgeBaseIDs []string,
) *errors.AppError {
	if len(knowledgeBaseIDs) == 0 {
		return nil
	}
	return validateTenantAPIKeyKnowledgeBaseIDsWithLookup(
		ctx, tenantID, knowledgeBaseIDs, kbService.GetKnowledgeBaseByID,
	)
}

// validateTenantAPIKeyKnowledgeBaseIDsWithLookup 将归属校验与大型知识库服务接口解耦，便于覆盖边界测试。
// lookup 输入知识库 ID 并返回真实知识库；函数输出 nil 或可直接响应的校验错误。
func validateTenantAPIKeyKnowledgeBaseIDsWithLookup(
	ctx context.Context,
	tenantID uint64,
	knowledgeBaseIDs []string,
	lookup func(context.Context, string) (*types.KnowledgeBase, error),
) *errors.AppError {
	for _, kbID := range knowledgeBaseIDs {
		kbID = strings.TrimSpace(kbID)
		if kbID == "" {
			continue
		}
		kb, err := lookup(ctx, kbID)
		if err != nil || kb == nil {
			return errors.NewValidationError("knowledge_base_ids contains an unknown knowledge base")
		}
		if kb.TenantID != tenantID {
			return errors.NewForbiddenError("knowledge_base_ids contains a knowledge base outside this workspace")
		}
	}
	return nil
}
