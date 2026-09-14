package service

import (
	"context"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

// Service reads accept exact upstream grants. Otherwise cross-tenant reads
// require a user and organization permission resolved for the original caller.
func kbReadPermissions(ctx context.Context, shares access.KBShareLookup) *access.KBPermissions {
	if types.CallerFromContext(ctx).UserID == "" {
		shares = nil
	}
	return access.NewKBPermissions(ctx, shares)
}

func resolveKBReadTenant(ctx context.Context, kb *types.KnowledgeBase, shares access.KBShareLookup) (uint64, error) {
	if kb != nil {
		allowed, err := kbReadPermissions(ctx, shares).Check(kb.ID, kb.TenantID, types.OrgRoleViewer)
		if err == nil && allowed {
			return kb.TenantID, nil
		}
	}
	return 0, apperrors.NewForbiddenError("无权访问该知识库")
}

func requireKBWrite(ctx context.Context, kb *types.KnowledgeBase) (context.Context, error) {
	if err := access.RequireKBWrite(ctx, kb); err != nil {
		return ctx, apperrors.NewForbiddenError("无权修改该知识库")
	}
	return types.WithExecutionTenant(ctx, kb.TenantID), nil
}

func (s *knowledgeService) writableFAQKnowledgeBase(
	ctx context.Context,
	kbID string,
) (*types.KnowledgeBase, context.Context, error) {
	kb, err := s.validateFAQKnowledgeBase(ctx, kbID)
	if err != nil {
		return nil, ctx, err
	}
	ctx, err = requireKBWrite(ctx, kb)
	if err == nil {
		ctx, err = withKBWriteTenantInfo(ctx, kb, s.tenantRepo)
	}
	return kb, ctx, err
}

// A shared KB mutation must also resolve models/index backends against its
// owner. The authenticated caller remains unchanged when TenantInfo changes.
func withKBWriteTenantInfo(
	ctx context.Context,
	kb *types.KnowledgeBase,
	tenants interfaces.TenantRepository,
) (context.Context, error) {
	if tenant, ok := types.TenantInfoFromContext(ctx); ok && tenant != nil && tenant.ID == kb.TenantID {
		return ctx, nil
	}
	if tenants == nil {
		return ctx, apperrors.NewServiceUnavailableError("无法获取知识库所属空间")
	}
	tenant, err := tenants.GetTenantByID(ctx, kb.TenantID)
	if err != nil {
		return ctx, err
	}
	if tenant == nil || tenant.ID != kb.TenantID {
		return ctx, apperrors.NewNotFoundError("知识库所属空间不存在")
	}
	return context.WithValue(ctx, types.TenantInfoContextKey, tenant), nil
}
