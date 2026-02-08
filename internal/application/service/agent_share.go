package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/google/uuid"
)

var (
	ErrAgentShareNotFound      = errors.New("agent share not found")
	ErrAgentSharePermission    = errors.New("permission denied for this share operation")
	ErrAgentNotFoundForShare   = errors.New("agent not found")
	ErrNotAgentOwner           = errors.New("only agent owner can share")
	ErrOrgRoleCannotShareAgent = errors.New("only editors and admins can share agents to this organization")
	ErrAgentNotConfigured      = errors.New("agent is not fully configured (missing required chat model or rerank model when using knowledge bases)")
)

// agentShareService implements AgentShareService interface
type agentShareService struct {
	shareRepo    interfaces.AgentShareRepository
	disabledRepo interfaces.TenantDisabledSharedAgentRepository
	orgRepo      interfaces.OrganizationRepository
	agentRepo    interfaces.CustomAgentRepository
	userRepo     interfaces.UserRepository
}

// NewAgentShareService creates a new agent share service
func NewAgentShareService(
	shareRepo interfaces.AgentShareRepository,
	disabledRepo interfaces.TenantDisabledSharedAgentRepository,
	orgRepo interfaces.OrganizationRepository,
	agentRepo interfaces.CustomAgentRepository,
	userRepo interfaces.UserRepository,
) interfaces.AgentShareService {
	return &agentShareService{
		shareRepo:    shareRepo,
		disabledRepo: disabledRepo,
		orgRepo:      orgRepo,
		agentRepo:    agentRepo,
		userRepo:     userRepo,
	}
}

// ShareAgent shares an agent to an organization
func (s *agentShareService) ShareAgent(ctx context.Context, agentID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.AgentShare, error) {
	logger.Infof(ctx, "Sharing agent %s to organization %s", agentID, orgID)

	agent, err := s.agentRepo.GetAgentByID(ctx, agentID, tenantID)
	if err != nil || agent == nil {
		return nil, ErrAgentNotFoundForShare
	}
	if agent.TenantID != tenantID {
		return nil, ErrNotAgentOwner
	}

	// Require agent to be fully configured before sharing (same rules as for conversation)
	if agent.Config.ModelID == "" {
		return nil, ErrAgentNotConfigured
	}
	usesKB := agent.Config.KBSelectionMode != "none" || len(agent.Config.KnowledgeBases) > 0
	if usesKB && agent.Config.RerankModelID == "" {
		return nil, ErrAgentNotConfigured
	}

	_, err = s.orgRepo.GetByID(ctx, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrOrganizationNotFound) {
			return nil, ErrOrgNotFound
		}
		return nil, err
	}

	member, err := s.orgRepo.GetMember(ctx, orgID, userID)
	if err != nil {
		if errors.Is(err, repository.ErrOrgMemberNotFound) {
			return nil, ErrUserNotInOrg
		}
		return nil, err
	}
	if !member.Role.HasPermission(types.OrgRoleEditor) {
		return nil, ErrOrgRoleCannotShareAgent
	}

	// 智能体共享仅支持只读，不支持可编辑
	permission = types.OrgRoleViewer

	share := &types.AgentShare{
		ID:             uuid.New().String(),
		AgentID:        agentID,
		OrganizationID: orgID,
		SharedByUserID: userID,
		SourceTenantID: tenantID,
		Permission:     permission,
		CreatedAt:      time.Now(),
		UpdatedAt:      time.Now(),
	}

	if err := s.shareRepo.Create(ctx, share); err != nil {
		if errors.Is(err, repository.ErrAgentShareAlreadyExists) {
			existing, err := s.shareRepo.GetByAgentAndOrg(ctx, agentID, orgID)
			if err != nil {
				return nil, err
			}
			existing.Permission = types.OrgRoleViewer
			existing.UpdatedAt = time.Now()
			if err := s.shareRepo.Update(ctx, existing); err != nil {
				return nil, err
			}
			return existing, nil
		}
		return nil, err
	}

	logger.Infof(ctx, "Agent %s shared successfully to organization %s", agentID, orgID)
	return share, nil
}

// RemoveShare removes an agent share
func (s *agentShareService) RemoveShare(ctx context.Context, shareID string, userID string) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentShareNotFound) {
			return ErrAgentShareNotFound
		}
		return err
	}
	if share.SharedByUserID == userID {
		return s.shareRepo.Delete(ctx, shareID)
	}
	member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
	if err == nil && member.Role == types.OrgRoleAdmin {
		return s.shareRepo.Delete(ctx, shareID)
	}
	return ErrAgentSharePermission
}

// ListSharesByAgent lists all shares for an agent
func (s *agentShareService) ListSharesByAgent(ctx context.Context, agentID string) ([]*types.AgentShare, error) {
	return s.shareRepo.ListByAgent(ctx, agentID)
}

// ListSharesByOrganization lists all agent shares for an organization
func (s *agentShareService) ListSharesByOrganization(ctx context.Context, orgID string) ([]*types.AgentShare, error) {
	return s.shareRepo.ListByOrganization(ctx, orgID)
}

// ListSharedAgents lists agents shared to the user through organizations, deduplicated by agent ID (keep highest permission)
func (s *agentShareService) ListSharedAgents(ctx context.Context, userID string, currentTenantID uint64) ([]*types.SharedAgentInfo, error) {
	shares, err := s.shareRepo.ListSharedAgentsForUser(ctx, userID)
	if err != nil {
		return nil, err
	}

	agentInfoMap := make(map[string]*types.SharedAgentInfo)
	for _, share := range shares {
		if share.SourceTenantID == currentTenantID {
			continue
		}
		if share.Agent == nil {
			continue
		}
		member, err := s.orgRepo.GetMember(ctx, share.OrganizationID, userID)
		if err != nil {
			continue
		}
		effectivePermission := share.Permission
		if !member.Role.HasPermission(share.Permission) {
			effectivePermission = member.Role
		}
		info := &types.SharedAgentInfo{
			Agent:          share.Agent,
			ShareID:        share.ID,
			OrganizationID: share.OrganizationID,
			OrgName:        "",
			Permission:     effectivePermission,
			SourceTenantID: share.SourceTenantID,
			SharedAt:       share.CreatedAt,
			SharedByUserID: share.SharedByUserID,
		}
		if share.Organization != nil {
			info.OrgName = share.Organization.Name
		}
		if share.SharedByUserID != "" {
			if u, err := s.userRepo.GetUserByID(ctx, share.SharedByUserID); err == nil && u != nil {
				info.SharedByUsername = u.Username
			}
		}
		key := fmt.Sprintf("%s_%d", share.AgentID, share.SourceTenantID)
		existing, exists := agentInfoMap[key]
		if !exists {
			agentInfoMap[key] = info
		} else if effectivePermission.HasPermission(existing.Permission) && effectivePermission != existing.Permission {
			agentInfoMap[key] = info
		}
	}

	result := make([]*types.SharedAgentInfo, 0, len(agentInfoMap))
	for _, info := range agentInfoMap {
		result = append(result, info)
	}

	// Set DisabledByMe from tenant_disabled_shared_agents for current tenant
	disabledList, err := s.disabledRepo.ListByTenantID(ctx, currentTenantID)
	if err != nil {
		return result, nil // non-fatal: return list without DisabledByMe
	}
	disabledSet := make(map[string]bool)
	for _, d := range disabledList {
		disabledSet[fmt.Sprintf("%s_%d", d.AgentID, d.SourceTenantID)] = true
	}
	for _, info := range result {
		if info.Agent != nil {
			key := fmt.Sprintf("%s_%d", info.Agent.ID, info.SourceTenantID)
			info.DisabledByMe = disabledSet[key]
		}
	}
	return result, nil
}

// ListSharedAgentsInOrganization returns all agents shared to the given organization (including those shared by the current tenant), for list-page display when a space is selected.
func (s *agentShareService) ListSharedAgentsInOrganization(ctx context.Context, orgID string, userID string, currentTenantID uint64) ([]*types.OrganizationSharedAgentItem, error) {
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

	result := make([]*types.OrganizationSharedAgentItem, 0, len(shares))
	for _, share := range shares {
		if share.Agent == nil {
			continue
		}

		effectivePermission := share.Permission
		if !member.Role.HasPermission(share.Permission) {
			effectivePermission = member.Role
		}

		orgName := ""
		if share.Organization != nil {
			orgName = share.Organization.Name
		}
		info := &types.SharedAgentInfo{
			Agent:          share.Agent,
			ShareID:        share.ID,
			OrganizationID: share.OrganizationID,
			OrgName:        orgName,
			Permission:     effectivePermission,
			SourceTenantID: share.SourceTenantID,
			SharedAt:       share.CreatedAt,
			SharedByUserID: share.SharedByUserID,
		}
		if share.SharedByUserID != "" {
			if u, err := s.userRepo.GetUserByID(ctx, share.SharedByUserID); err == nil && u != nil {
				info.SharedByUsername = u.Username
			}
		}

		item := &types.OrganizationSharedAgentItem{
			SharedAgentInfo: *info,
			IsMine:          share.SourceTenantID == currentTenantID,
		}
		result = append(result, item)
	}

	// Set DisabledByMe for entries shared by others (mine entries stay false)
	disabledList, err := s.disabledRepo.ListByTenantID(ctx, currentTenantID)
	if err == nil {
		disabledSet := make(map[string]bool)
		for _, d := range disabledList {
			disabledSet[fmt.Sprintf("%s_%d", d.AgentID, d.SourceTenantID)] = true
		}
		for _, item := range result {
			if item.Agent != nil && !item.IsMine {
				key := fmt.Sprintf("%s_%d", item.Agent.ID, item.SourceTenantID)
				item.DisabledByMe = disabledSet[key]
			}
		}
	}
	return result, nil
}

// ListSharedAgentsInOrganizations returns per-org agent lists (batch); only orgs where user is member.
func (s *agentShareService) ListSharedAgentsInOrganizations(ctx context.Context, orgIDs []string, userID string, currentTenantID uint64) (map[string][]*types.OrganizationSharedAgentItem, error) {
	out := make(map[string][]*types.OrganizationSharedAgentItem)
	if len(orgIDs) == 0 {
		return out, nil
	}
	members, err := s.orgRepo.ListMembersByUserForOrgs(ctx, userID, orgIDs)
	if err != nil {
		return nil, err
	}
	shares, err := s.shareRepo.ListByOrganizations(ctx, orgIDs)
	if err != nil {
		return nil, err
	}
	byOrg := make(map[string][]*types.AgentShare)
	for _, share := range shares {
		if share != nil && members[share.OrganizationID] != nil {
			byOrg[share.OrganizationID] = append(byOrg[share.OrganizationID], share)
		}
	}
	disabledSet := make(map[string]bool)
	if disabledList, err := s.disabledRepo.ListByTenantID(ctx, currentTenantID); err == nil {
		for _, d := range disabledList {
			disabledSet[fmt.Sprintf("%s_%d", d.AgentID, d.SourceTenantID)] = true
		}
	}
	for orgID, list := range byOrg {
		member := members[orgID]
		result := make([]*types.OrganizationSharedAgentItem, 0, len(list))
		for _, share := range list {
			if share.Agent == nil {
				continue
			}
			effectivePermission := share.Permission
			if !member.Role.HasPermission(share.Permission) {
				effectivePermission = member.Role
			}
			orgName := ""
			if share.Organization != nil {
				orgName = share.Organization.Name
			}
			info := &types.SharedAgentInfo{
				Agent:          share.Agent,
				ShareID:        share.ID,
				OrganizationID: share.OrganizationID,
				OrgName:        orgName,
				Permission:     effectivePermission,
				SourceTenantID: share.SourceTenantID,
				SharedAt:       share.CreatedAt,
				SharedByUserID: share.SharedByUserID,
			}
			if share.SharedByUserID != "" {
				if u, err := s.userRepo.GetUserByID(ctx, share.SharedByUserID); err == nil && u != nil {
					info.SharedByUsername = u.Username
				}
			}
			item := &types.OrganizationSharedAgentItem{
				SharedAgentInfo: *info,
				IsMine:          share.SourceTenantID == currentTenantID,
			}
			if item.Agent != nil && !item.IsMine {
				item.DisabledByMe = disabledSet[fmt.Sprintf("%s_%d", item.Agent.ID, item.SourceTenantID)]
			}
			result = append(result, item)
		}
		out[orgID] = result
	}
	return out, nil
}

// CountByOrganizations returns share counts per organization (for list sidebar); excludes deleted agents
func (s *agentShareService) CountByOrganizations(ctx context.Context, orgIDs []string) (map[string]int64, error) {
	return s.shareRepo.CountByOrganizations(ctx, orgIDs)
}

// SetSharedAgentDisabledByMe adds or removes (tenantID, agentID, sourceTenantID) from tenant_disabled_shared_agents.
func (s *agentShareService) SetSharedAgentDisabledByMe(ctx context.Context, tenantID uint64, agentID string, sourceTenantID uint64, disabled bool) error {
	if disabled {
		return s.disabledRepo.Add(ctx, tenantID, agentID, sourceTenantID)
	}
	return s.disabledRepo.Remove(ctx, tenantID, agentID, sourceTenantID)
}

// GetSharedAgentForUser returns the shared agent by agentID if the user has access; source tenant is resolved from the user's share. One share lookup + one agent lookup.
func (s *agentShareService) GetSharedAgentForUser(ctx context.Context, userID string, currentTenantID uint64, agentID string) (*types.CustomAgent, error) {
	if agentID == "" {
		return nil, ErrAgentShareNotFound
	}
	share, err := s.shareRepo.GetShareByAgentIDForUser(ctx, userID, agentID, currentTenantID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentShareNotFound) {
			return nil, ErrAgentSharePermission
		}
		return nil, err
	}
	agent, err := s.agentRepo.GetAgentByID(ctx, agentID, share.SourceTenantID)
	if err != nil {
		if errors.Is(err, repository.ErrCustomAgentNotFound) {
			return nil, ErrAgentNotFoundForShare
		}
		return nil, err
	}
	return agent, nil
}

// UserCanAccessKBViaSomeSharedAgent returns true if the user has at least one shared agent that can access the given KB (used when opening KB detail from space list without agent_id).
func (s *agentShareService) UserCanAccessKBViaSomeSharedAgent(ctx context.Context, userID string, currentTenantID uint64, kb *types.KnowledgeBase) (bool, error) {
	if kb == nil || kb.ID == "" {
		return false, nil
	}
	list, err := s.ListSharedAgents(ctx, userID, currentTenantID)
	if err != nil || len(list) == 0 {
		return false, err
	}
	for _, info := range list {
		if info.Agent == nil {
			continue
		}
		agent := info.Agent
		if agent.TenantID != kb.TenantID {
			continue
		}
		mode := agent.Config.KBSelectionMode
		if mode == "none" {
			continue
		}
		if mode == "all" {
			return true, nil
		}
		if mode == "selected" {
			for _, id := range agent.Config.KnowledgeBases {
				if id == kb.ID {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

// GetShare gets an agent share by ID
func (s *agentShareService) GetShare(ctx context.Context, shareID string) (*types.AgentShare, error) {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentShareNotFound) {
			return nil, ErrAgentShareNotFound
		}
		return nil, err
	}
	return share, nil
}

// GetShareByAgentAndOrg gets an agent share by agent ID and organization ID
func (s *agentShareService) GetShareByAgentAndOrg(ctx context.Context, agentID string, orgID string) (*types.AgentShare, error) {
	share, err := s.shareRepo.GetByAgentAndOrg(ctx, agentID, orgID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentShareNotFound) {
			return nil, ErrAgentShareNotFound
		}
		return nil, err
	}
	return share, nil
}

// GetShareByAgentIDForUser returns one share for the given agentID that the user can access (user in org), excluding source_tenant_id == excludeTenantID.
func (s *agentShareService) GetShareByAgentIDForUser(ctx context.Context, userID, agentID string, excludeTenantID uint64) (*types.AgentShare, error) {
	return s.shareRepo.GetShareByAgentIDForUser(ctx, userID, agentID, excludeTenantID)
}
