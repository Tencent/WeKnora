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
	// ErrOrgRoleCannotShare: only editors and admins in the org can share KBs to that org; viewers cannot
	ErrOrgRoleCannotShare = errors.New("only editors and admins can share knowledge bases to this organization")
)

// kbShareService implements KBShareService interface
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

// ShareKnowledgeBase shares a knowledge base to an organization
func (s *kbShareService) ShareKnowledgeBase(ctx context.Context, kbID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	logger.Infof(ctx, "Sharing knowledge base %s to organization %s", kbID, orgID)

	// Verify knowledge base exists and user is the owner (same tenant)
	kb, err := s.kbRepo.GetKnowledgeBaseByID(ctx, kbID)
	if err != nil {
		return nil, ErrKBNotFound
	}

	// Check if user's tenant owns the knowledge base
	if kb.TenantID != tenantID {
		return nil, ErrNotKBOwner
	}

	// Verify organization exists
	_, err = s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}

	// Check if user is a member of the organization and has at least editor role (viewers cannot share KBs to the org)
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrUserNotInOrg
		}
		return nil, err
	}
	if !member.Role.HasPermission(types.OrgRoleEditor) {
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
			// Update existing share
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

// UpdateSharePermission updates the permission of a share.
// Allowed if: (1) current user is the sharer, or (2) current user is admin of the target organization.
func (s *kbShareService) UpdateSharePermission(ctx context.Context, shareID string, permission types.OrgMemberRole, userID string) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return ErrShareNotFound
		}
		return err
	}

	// Sharer can always update; org admin can also update (e.g. when sharer left)
	if share.SharedByUserID != userID {
		member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
		if err != nil || member.Role != types.OrgRoleAdmin {
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
// Allowed if: (1) current user is the sharer, or (2) current user is admin of the target organization.
// Org admins can unlink any KB shared to their org (e.g. content governance, sharer left).
func (s *kbShareService) RemoveShare(ctx context.Context, shareID string, userID string) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrKBShareNotFound) {
			return ErrShareNotFound
		}
		return err
	}

	// Sharer can always remove their own share
	if share.SharedByUserID == userID {
		return s.shareRepo.Delete(ctx, shareID)
	}

	// Org admin can remove any share targeting their organization
	member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
	if err == nil && member.Role == types.OrgRoleAdmin {
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

// ListSharedKnowledgeBases lists all knowledge bases shared to the user through organizations
// It filters out knowledge bases that belong to the user's own tenant
// It deduplicates knowledge bases that are shared to multiple organizations the user belongs to
func (s *kbShareService) ListSharedKnowledgeBases(ctx context.Context, userID string, currentTenantID uint64) ([]*types.SharedKnowledgeBaseInfo, error) {
	shares, err := s.shareRepo.ListSharedKBsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	// Use a map to deduplicate by knowledge base ID, keeping the one with highest permission
	kbInfoMap := make(map[string]*types.SharedKnowledgeBaseInfo)

	for _, share := range shares {
		// Skip knowledge bases that belong to the user's own tenant
		// (user already has full ownership of these)
		if share.SourceTenantID == currentTenantID {
			continue
		}

		// Skip if knowledge base is nil
		if share.KnowledgeBase == nil {
			continue
		}

		kbID := share.KnowledgeBase.ID

		// Get user's role in the organization
		member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
		if err != nil {
			continue // Skip if user is not a member anymore
		}

		// Effective permission is the lower of share permission and user's org role
		effectivePermission := share.Permission
		if !member.Role.HasPermission(share.Permission) {
			effectivePermission = member.Role
		}

		kb := share.KnowledgeBase
		// Calculate knowledge/chunk count based on type
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
			Permission:     effectivePermission,
			SourceTenantID: share.SourceTenantID,
			SharedAt:       share.CreatedAt,
		}

		if share.Organization != nil {
			info.OrgName = share.Organization.Name
		}

		// Check if we already have this knowledge base
		existing, exists := kbInfoMap[kbID]
		if !exists {
			// First time seeing this KB, add it
			kbInfoMap[kbID] = info
		} else {
			// KB already exists, keep the one with higher permission
			// Permission hierarchy: admin(3) > editor(2) > viewer(1)
			// If current permission is higher than existing, replace
			// This handles the case where a user belongs to multiple orgs with different permissions
			if effectivePermission.HasPermission(existing.Permission) && effectivePermission != existing.Permission {
				// Current permission is higher, replace with higher permission
				kbInfoMap[kbID] = info
			}
			// If existing permission is higher or equal, keep existing (no change needed)
		}
	}

	// Convert map to slice
	result := make([]*types.SharedKnowledgeBaseInfo, 0, len(kbInfoMap))
	for _, info := range kbInfoMap {
		result = append(result, info)
	}

	return result, nil
}

// ListSharedKnowledgeBasesInOrganization returns all knowledge bases shared to the given organization (including those shared by the current tenant), for list-page display when a space is selected.
func (s *kbShareService) ListSharedKnowledgeBasesInOrganization(ctx context.Context, orgID string, userID string, currentTenantID uint64) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrUserNotInOrg
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

		effectivePermission := share.Permission
		if !member.Role.HasPermission(share.Permission) {
			effectivePermission = member.Role
		}

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
				Permission:     effectivePermission,
				SourceTenantID: share.SourceTenantID,
				SharedAt:       share.CreatedAt,
			},
			IsMine: share.SourceTenantID == currentTenantID,
		}
		result = append(result, item)
	}
	return result, nil
}

// ListSharedKnowledgeBaseIDsByOrganizations returns per-org direct shared KB IDs (batch); only orgs where user is member.
func (s *kbShareService) ListSharedKnowledgeBaseIDsByOrganizations(ctx context.Context, orgIDs []string, userID string) (map[string][]string, error) {
	if len(orgIDs) == 0 {
		return make(map[string][]string), nil
	}
	members, err := s.orgRepo.ListMembersByUserForOrgs(ctx, userID, orgIDs)
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

// CheckUserKBPermission checks a user's permission for a knowledge base
// Returns: permission level, isShared, error
func (s *kbShareService) CheckUserKBPermission(ctx context.Context, kbID string, userID string) (types.OrgMemberRole, bool, error) {
	// Get all shares for this knowledge base
	shares, err := s.shareRepo.ListByKnowledgeBase(ctx, kbID)
	if err != nil {
		return "", false, err
	}

	var highestPermission types.OrgMemberRole
	isShared := false

	for _, share := range shares {
		// Check if user is a member of the organization
		member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
		if err != nil {
			continue // User is not a member of this org
		}

		isShared = true

		// Effective permission is the lower of share permission and user's org role
		effectivePermission := share.Permission
		if !member.Role.HasPermission(share.Permission) {
			effectivePermission = member.Role
		}

		// Keep the highest permission
		if highestPermission == "" || effectivePermission.HasPermission(highestPermission) {
			highestPermission = effectivePermission
		}
	}

	return highestPermission, isShared, nil
}

// HasKBPermission checks if a user has at least the required permission level for a knowledge base
func (s *kbShareService) HasKBPermission(ctx context.Context, kbID string, userID string, requiredRole types.OrgMemberRole) (bool, error) {
	permission, isShared, err := s.CheckUserKBPermission(ctx, kbID, userID)
	if err != nil {
		return false, err
	}

	if !isShared {
		return false, nil
	}

	return permission.HasPermission(requiredRole), nil
}

// GetKBSourceTenant gets the source tenant ID for a shared knowledge base
func (s *kbShareService) GetKBSourceTenant(ctx context.Context, kbID string) (uint64, error) {
	// First check if there are any shares for this KB
	shares, err := s.shareRepo.ListByKnowledgeBase(ctx, kbID)
	if err != nil {
		return 0, err
	}

	if len(shares) > 0 {
		return shares[0].SourceTenantID, nil
	}

	// If not shared, get the tenant from the knowledge base itself
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
