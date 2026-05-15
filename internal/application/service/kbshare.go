package service

import (
	"context"
	"errors"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var (
	ErrShareNotFound         = errors.New("share not found")
	ErrSharePermissionDenied = errors.New("permission denied for this share operation")
	ErrKBNotFound            = errors.New("knowledge base not found")
	ErrNotKBOwner            = errors.New("only knowledge base owner can share")
	// ErrOrgRoleCannotShare: only editors and admins (in tenant's org role) may share KBs to that org; viewers cannot
	ErrOrgRoleCannotShare = errors.New("only editors and admins can share knowledge bases to this organization")
)

// kbShareService implements KBShareService.
//
// Plan 3 of #1303: permission checks resolve "is the *caller's tenant*
// in this org, with what role" rather than "is this user". The 3-D cap
// inside CheckTenantKBPermission encodes:
//
//   effective = min(share.Permission, tenant_org_role, tenant_role_cap)
//
// where tenant_role_cap pins tenant Viewers to OrgRoleViewer regardless
// of what the org-level grant said. That keeps the tenant RBAC promise
// — Viewer in your own tenant cannot write — even when the access is
// routed through cross-tenant sharing.
type kbShareService struct {
	shareRepo interfaces.KBShareRepository
	orgRepo   interfaces.OrganizationRepository
	kbRepo    interfaces.KnowledgeBaseRepository
	kgRepo    interfaces.KnowledgeRepository
	chunkRepo interfaces.ChunkRepository
}

// NewKBShareService creates a new knowledge base share service
func NewKBShareService(
	shareRepo interfaces.KBShareRepository,
	orgRepo interfaces.OrganizationRepository,
	kbRepo interfaces.KnowledgeBaseRepository,
	kgRepo interfaces.KnowledgeRepository,
	chunkRepo interfaces.ChunkRepository,
) interfaces.KBShareService {
	return &kbShareService{
		shareRepo: shareRepo,
		orgRepo:   orgRepo,
		kbRepo:    kbRepo,
		kgRepo:    kgRepo,
		chunkRepo: chunkRepo,
	}
}

// applyTenantRoleCap applies the third dimension of the cap: a caller
// whose own tenant role is Viewer cannot exceed OrgRoleViewer on any
// shared resource, regardless of what the org-level grant said. Higher
// roles (Contributor / Admin / Owner) pass through unchanged.
func applyTenantRoleCap(p types.OrgMemberRole, callerTenantRole types.TenantRole) types.OrgMemberRole {
	if callerTenantRole == types.TenantRoleViewer && p.HasPermission(types.OrgRoleEditor) {
		return types.OrgRoleViewer
	}
	return p
}

// ShareKnowledgeBase shares a knowledge base to an organization.
// Caller must be in a tenant that owns the KB *and* be a member of the
// target org with at least editor role.
func (s *kbShareService) ShareKnowledgeBase(ctx context.Context, kbID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	logger.Infof(ctx, "Sharing knowledge base %s to organization %s", kbID, orgID)

	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, ErrKBNotFound
	}
	if kb.TenantID != tenantID {
		return nil, ErrNotKBOwner
	}

	_, err = s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}

	// Caller's tenant must be an org member with editor+ role to share.
	tm, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrTenantNotInOrg
		}
		return nil, err
	}
	if !tm.Role.HasPermission(types.OrgRoleEditor) {
		return nil, ErrOrgRoleCannotShare
	}

	if !permission.IsValid() {
		return nil, ErrInvalidRole
	}

	share := &types.KnowledgeBaseShare{
		ID:              uuid.New().String(),
		KnowledgeBaseID: kbID,
		OrganizationID:  orgID,
		SharedByUserID:  userID,
		SourceTenantID:  tenantID,
		Permission:      permission,
		CreatedAt:       time.Now(),
		UpdatedAt:       time.Now(),
	}

	if err := s.shareRepo.Create(ctx, share); err != nil {
		if errors.Is(err, repository.ErrKBShareAlreadyExists) {
			existingShare, err := s.shareRepo.GetByKBAndOrg(ctx, kbID, orgID)
			if err != nil {
				return nil, err
			}
			existingShare.Permission = permission
			existingShare.UpdatedAt = time.Now()
			if err := s.shareRepo.Update(ctx, existingShare); err != nil {
				return nil, err
			}
			return existingShare, nil
		}
		return nil, err
	}

	logger.Infof(ctx, "Knowledge base %s shared successfully to organization %s", kbID, orgID)
	return share, nil
}

// UpdateSharePermission updates a share's permission.
// Allowed if (1) the caller is the original sharer (same user id), or
// (2) the caller's tenant is admin in the target org. The latter lets
// org admins repair shares when the original sharer leaves.
func (s *kbShareService) UpdateSharePermission(ctx context.Context, shareID string, permission types.OrgMemberRole, userID string, tenantID uint64) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return ErrShareNotFound
		}
		return err
	}

	if share.SharedByUserID != userID {
		tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, tenantID)
		if err != nil || tm.Role != types.OrgRoleAdmin {
			return ErrSharePermissionDenied
		}
	}

	if !permission.IsValid() {
		return ErrInvalidRole
	}

	share.Permission = permission
	share.UpdatedAt = time.Now()

	return s.shareRepo.Update(ctx, share)
}

// RemoveShare removes a share.
// Same authz envelope as UpdateSharePermission: original sharer or org admin.
func (s *kbShareService) RemoveShare(ctx context.Context, shareID string, userID string, tenantID uint64) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return ErrShareNotFound
		}
		return err
	}

	if share.SharedByUserID == userID {
		return s.shareRepo.Delete(ctx, shareID)
	}

	tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, tenantID)
	if err == nil && tm.Role == types.OrgRoleAdmin {
		return s.shareRepo.Delete(ctx, shareID)
	}

	return ErrSharePermissionDenied
}

// ListSharesByKnowledgeBase lists shares for a knowledge base; caller's tenant must own the KB.
func (s *kbShareService) ListSharesByKnowledgeBase(ctx context.Context, kbID string, tenantID uint64) ([]*types.KnowledgeBaseShare, error) {
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, ErrKBNotFound
	}
	if kb.TenantID != tenantID {
		return nil, ErrNotKBOwner
	}
	return s.shareRepo.ListByKnowledgeBase(ctx, kbID)
}

// ListSharesByOrganization lists all shares for an organization
func (s *kbShareService) ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.KnowledgeBaseShare, error) {
	return s.shareRepo.ListByOrganization(ctx, orgID)
}

// ListSharedKnowledgeBases lists all knowledge bases reachable from the
// caller's tenant via cross-tenant org shares. Permission per KB is
// computed via the 3-D cap.
func (s *kbShareService) ListSharedKnowledgeBases(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.SharedKnowledgeBaseInfo, error) {
	shares, err := s.shareRepo.ListSharedKBsForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	kbInfoMap := make(map[string]*types.SharedKnowledgeBaseInfo)

	for _, share := range shares {
		if share.SourceTenantID == tenantID {
			continue
		}
		if share.KnowledgeBase == nil {
			continue
		}

		kbID := share.KnowledgeBase.ID

		tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, tenantID)
		if err != nil {
			continue
		}

		// 3-D cap: share × tenant_org_role × tenant_role_cap.
		effective := types.MinOrgRole(share.Permission, tm.Role)
		effective = applyTenantRoleCap(effective, callerTenantRole)

		kb := share.KnowledgeBase
		switch kb.Type {
		case types.KnowledgeBaseTypeDocument:
			knowledgeCount, err := s.kgRepo.CountKnowledgeByKnowledgeBaseID(ctx, share.SourceTenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get knowledge count for shared KB %s: %v", kb.ID, err)
			} else {
				kb.KnowledgeCount = knowledgeCount
			}
		case types.KnowledgeBaseTypeFAQ:
			chunkCount, err := s.chunkRepo.CountChunksByKnowledgeBaseID(ctx, share.SourceTenantID, kb.ID)
			if err != nil {
				logger.Warnf(ctx, "Failed to get chunk count for shared KB %s: %v", kb.ID, err)
			} else {
				kb.ChunkCount = chunkCount
			}
		}

		info := &types.SharedKnowledgeBaseInfo{
			KnowledgeBase:  kb,
			ShareID:        share.ID,
			OrganizationID: share.OrganizationID,
			OrgName:        "",
			Permission:     effective,
			SourceTenantID: share.SourceTenantID,
			SharedAt:       share.CreatedAt,
		}

		if share.Organization != nil {
			info.OrgName = share.Organization.Name
		}

		existing, exists := kbInfoMap[kbID]
		if !exists {
			kbInfoMap[kbID] = info
		} else {
			if effective.HasPermission(existing.Permission) && effective != existing.Permission {
				kbInfoMap[kbID] = info
			}
		}
	}

	result := make([]*types.SharedKnowledgeBaseInfo, 0, len(kbInfoMap))
	for _, info := range kbInfoMap {
		result = append(result, info)
	}

	return result, nil
}

// ListSharedKnowledgeBasesInOrganization returns all knowledge bases shared to
// the given organization (including those shared from the caller's tenant) for
// list-page display when the user picks a space.
func (s *kbShareService) ListSharedKnowledgeBasesInOrganization(ctx context.Context, orgID string, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	tm, err := s.orgRepo.GetTenantMember(ctx, orgID, tenantID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrTenantNotInOrg
		}
		return nil, err
	}

	shares, err := s.shareRepo.ListByOrganization(ctx, orgID)
	if err != nil {
		return nil, err
	}

	result := make([]*types.OrganizationSharedKnowledgeBaseItem, 0, len(shares))
	for _, share := range shares {
		if share.KnowledgeBase == nil {
			continue
		}

		effective := types.MinOrgRole(share.Permission, tm.Role)
		effective = applyTenantRoleCap(effective, callerTenantRole)

		kb := share.KnowledgeBase
		switch kb.Type {
		case types.KnowledgeBaseTypeDocument:
			if count, err := s.kgRepo.CountKnowledgeByKnowledgeBaseID(ctx, share.SourceTenantID, kb.ID); err == nil {
				kb.KnowledgeCount = count
			}
		case types.KnowledgeBaseTypeFAQ:
			if count, err := s.chunkRepo.CountChunksByKnowledgeBaseID(ctx, share.SourceTenantID, kb.ID); err == nil {
				kb.ChunkCount = count
			}
		}

		orgName := ""
		if share.Organization != nil {
			orgName = share.Organization.Name
		}
		item := &types.OrganizationSharedKnowledgeBaseItem{
			SharedKnowledgeBaseInfo: types.SharedKnowledgeBaseInfo{
				KnowledgeBase:  kb,
				ShareID:        share.ID,
				OrganizationID: share.OrganizationID,
				OrgName:        orgName,
				Permission:     effective,
				SourceTenantID: share.SourceTenantID,
				SharedAt:       share.CreatedAt,
			},
			IsMine: share.SourceTenantID == tenantID,
		}
		result = append(result, item)
	}
	return result, nil
}

// ListSharedKnowledgeBaseIDsByOrganizations returns per-org direct shared KB
// IDs (batch); only orgs where the caller's tenant is a member.
func (s *kbShareService) ListSharedKnowledgeBaseIDsByOrganizations(ctx context.Context, orgIDs []string, tenantID uint64) (map[string][]string, error) {
	if len(orgIDs) == 0 {
		return make(map[string][]string), nil
	}
	members, err := s.orgRepo.ListTenantMembersByTenantForOrgs(ctx, tenantID, orgIDs)
	if err != nil {
		return nil, err
	}
	shares, err := s.shareRepo.ListByOrganizations(ctx, orgIDs)
	if err != nil {
		return nil, err
	}
	byOrg := make(map[string][]string)
	for _, share := range shares {
		if share == nil || members[share.OrganizationID] == nil {
			continue
		}
		kbID := share.KnowledgeBaseID
		if kbID == "" && share.KnowledgeBase != nil {
			kbID = share.KnowledgeBase.ID
		}
		if kbID != "" {
			byOrg[share.OrganizationID] = append(byOrg[share.OrganizationID], kbID)
		}
	}
	return byOrg, nil
}

// GetShare gets a share by ID
func (s *kbShareService) GetShare(ctx context.Context, shareID string) (*types.KnowledgeBaseShare, error) {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return nil, ErrShareNotFound
		}
		return nil, err
	}
	return share, nil
}

// GetShareByKBAndOrg gets a share by knowledge base and organization
func (s *kbShareService) GetShareByKBAndOrg(ctx context.Context, kbID string, orgID string) (*types.KnowledgeBaseShare, error) {
	share, err := s.shareRepo.GetByKBAndOrg(ctx, kbID, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return nil, ErrShareNotFound
		}
		return nil, err
	}
	return share, nil
}

// CheckTenantKBPermission resolves the caller's effective permission on a KB
// reached via org sharing. Returns (effectiveRole, isShared, err).
//
// effectiveRole is the maximum role across all shares of this KB into orgs
// where the caller's tenant is a member, capped by the 3-D rule. Empty
// when isShared is false.
func (s *kbShareService) CheckTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole) (types.OrgMemberRole, bool, error) {
	shares, err := s.shareRepo.ListByKnowledgeBase(ctx, kbID)
	if err != nil {
		return "", false, err
	}

	var highest types.OrgMemberRole
	isShared := false

	for _, share := range shares {
		tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, callerTenantID)
		if err != nil {
			continue
		}

		isShared = true

		effective := types.MinOrgRole(share.Permission, tm.Role)
		effective = applyTenantRoleCap(effective, callerTenantRole)

		if highest == "" || effective.HasPermission(highest) {
			highest = effective
		}
	}

	return highest, isShared, nil
}

// HasTenantKBPermission is a thin "do I have at least N" wrapper over
// CheckTenantKBPermission for callers that don't need the granular role.
func (s *kbShareService) HasTenantKBPermission(ctx context.Context, kbID string, callerTenantID uint64, callerTenantRole types.TenantRole, requiredRole types.OrgMemberRole) (bool, error) {
	role, isShared, err := s.CheckTenantKBPermission(ctx, kbID, callerTenantID, callerTenantRole)
	if err != nil {
		return false, err
	}
	if !isShared {
		return false, nil
	}
	return role.HasPermission(requiredRole), nil
}

// GetKBSourceTenant gets the source tenant ID for a shared knowledge base
func (s *kbShareService) GetKBSourceTenant(ctx context.Context, kbID string) (uint64, error) {
	shares, err := s.shareRepo.ListByKnowledgeBase(ctx, kbID)
	if err != nil {
		return 0, err
	}

	if len(shares) > 0 {
		return shares[0].SourceTenantID, nil
	}

	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return 0, ErrKBNotFound
	}

	return kb.TenantID, nil
}

// CountSharesByKnowledgeBaseIDs counts the number of shares for multiple knowledge bases
func (s *kbShareService) CountSharesByKnowledgeBaseIDs(ctx context.Context, kbIDs []string) (map[string]int64, error) {
	return s.shareRepo.CountSharesByKnowledgeBaseIDs(ctx, kbIDs)
}

// CountByOrganizations returns share counts per organization (for list sidebar); excludes deleted KBs
func (s *kbShareService) CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error) {
	return s.shareRepo.CountByOrganizations(ctx, orgIDs)
}
