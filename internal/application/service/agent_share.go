package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/Tencent/WeKnora/internal/agent/tools"
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
	ErrAgentNotConfigured      = errors.New("agent is not fully configured (missing required chat model, or rerank model when the knowledge_search tool is enabled)")
)

// agentRequiresRerankModel returns true when the agent's configured tools
// will actually invoke the reranker at runtime. This mirrors the runtime
// check in session_agent_qa.go: only `knowledge_search` uses the reranker.
// Wiki-first agents (wiki_search / wiki_read_page / …) never call it and
// therefore don't need a rerank model configured, even when knowledge bases
// are attached.
//
// When AllowedTools is empty the runtime falls back to
// tools.DefaultAllowedTools(), which includes knowledge_search, so we treat
// that case as requiring the reranker.
func agentRequiresRerankModel(agent *types.CustomAgent) bool {
	if agent == nil {
		return false
	}
	allowed := agent.Config.AllowedTools
	if len(allowed) == 0 {
		allowed = tools.DefaultAllowedTools()
	}
	for _, t := range allowed {
		if t == tools.ToolKnowledgeSearch {
			return true
		}
	}
	return false
}

// agentShareService implements AgentShareService.
//
// Plan 3 of #1303: visibility and access checks key on the caller's
// tenant. callerTenantRole flows through every read path so the 3-D
// cap (tenant Viewer → at most OrgRoleViewer) lands consistently.
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

// ShareAgent shares an agent to an organization. Permission is forced to
// OrgRoleViewer (cross-tenant agent edit is not part of v1).
func (s *agentShareService) ShareAgent(ctx context.Context, agentID string, orgID string, userID string, tenantID uint64, permission types.OrgMemberRole) (*types.AgentShare, error) {
	logger.Infof(ctx, "Sharing agent %s to organization %s", agentID, orgID)

	agent, err := s.agentRepo.GetAgentByID(ctx, agentID, tenantID)
	if err != nil || agent == nil {
		return nil, ErrAgentNotFoundForShare
	}
	if agent.TenantID != tenantID {
		return nil, ErrNotAgentOwner
	}

	if agent.Config.ModelID == "" {
		return nil, ErrAgentNotConfigured
	}
	if agentRequiresRerankModel(agent) && agent.Config.RerankModelID == "" {
		return nil, ErrAgentNotConfigured
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
		return nil, ErrOrgRoleCannotShareAgent
	}

	// 智能体共享仅支持只读
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

// RemoveShare removes an agent share.
// Same authz envelope as KB-share remove (see kbshare.callerCanManageShare):
// original sharer, OR source-tenant Admin+, OR target-org admin.
func (s *agentShareService) RemoveShare(ctx context.Context, shareID string, userID string, tenantID uint64) error {
	share, err := s.shareRepo.GetByID(ctx, shareID)
	if err != nil {
		if errors.Is(err, repository.ErrAgentShareNotFound) {
			return ErrAgentShareNotFound
		}
		return err
	}
	// (1) Original sharer.
	if share.SharedByUserID == userID {
		return s.shareRepo.Delete(ctx, shareID)
	}
	// (2) Source-tenant Admin+ — Plan 3 ownership is tenant-level.
	if tenantID != 0 && tenantID == share.SourceTenantID {
		if types.TenantRoleFromContext(ctx).HasPermission(types.TenantRoleAdmin) {
			return s.shareRepo.Delete(ctx, shareID)
		}
	}
	// (3) Org admin in the target org (governance / sharer-left repair).
	if tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, tenantID); err == nil && tm.Role == types.OrgRoleAdmin {
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

// ListSharedAgents lists agents reachable from the caller's tenant.
func (s *agentShareService) ListSharedAgents(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.SharedAgentInfo, error) {
	shares, err := s.shareRepo.ListSharedAgentsForTenant(ctx, tenantID)
	if err != nil {
		return nil, err
	}

	agentInfoMap := make(map[string]*types.SharedAgentInfo)
	for _, share := range shares {
		if share.SourceTenantID == tenantID {
			continue
		}
		if share.Agent == nil {
			continue
		}
		tm, err := s.orgRepo.GetTenantMember(ctx, share.OrganizationID, tenantID)
		if err != nil {
			continue
		}
		effective := types.MinOrgRole(share.Permission, tm.Role)
		effective = applyTenantRoleCap(effective, callerTenantRole)
		info := &types.SharedAgentInfo{
			Agent:          share.Agent,
			ShareID:        share.ID,
			OrganizationID: share.OrganizationID,
			OrgName:        "",
			Permission:     effective,
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
		} else if effective.HasPermission(existing.Permission) && effective != existing.Permission {
			agentInfoMap[key] = info
		}
	}

	result := make([]*types.SharedAgentInfo, 0, len(agentInfoMap))
	for _, info := range agentInfoMap {
		result = append(result, info)
	}

	// Set DisabledByMe from tenant_disabled_shared_agents for current tenant
	disabledList, err := s.disabledRepo.ListByTenantID(ctx, tenantID)
	if err != nil {
		return result, nil
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

// ListSharedAgentsInOrganization returns all agents shared to the given
// organization (including those shared by the caller's tenant), for list-page
// display when a space is selected.
func (s *agentShareService) ListSharedAgentsInOrganization(ctx context.Context, orgID string, tenantID uint64, callerTenantRole types.TenantRole) ([]*types.OrganizationSharedAgentItem, error) {
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

	result := make([]*types.OrganizationSharedAgentItem, 0, len(shares))
	for _, share := range shares {
		if share.Agent == nil {
			continue
		}

		effective := types.MinOrgRole(share.Permission, tm.Role)
		effective = applyTenantRoleCap(effective, callerTenantRole)

		orgName := ""
		if share.Organization != nil {
			orgName = share.Organization.Name
		}
		info := &types.SharedAgentInfo{
			Agent:          share.Agent,
			ShareID:        share.ID,
			OrganizationID: share.OrganizationID,
			OrgName:        orgName,
			Permission:     effective,
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
			IsMine:          share.SourceTenantID == tenantID,
		}
		result = append(result, item)
	}

	disabledList, err := s.disabledRepo.ListByTenantID(ctx, tenantID)
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

// ListSharedAgentsInOrganizations returns per-org agent lists (batch); only
// orgs where the caller's tenant is a member.
func (s *agentShareService) ListSharedAgentsInOrganizations(ctx context.Context, orgIDs []string, tenantID uint64, callerTenantRole types.TenantRole) (map[string][]*types.OrganizationSharedAgentItem, error) {
	out := make(map[string][]*types.OrganizationSharedAgentItem)
	if len(orgIDs) == 0 {
		return out, nil
	}
	members, err := s.orgRepo.ListTenantMembersByTenantForOrgs(ctx, tenantID, orgIDs)
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
	if disabledList, err := s.disabledRepo.ListByTenantID(ctx, tenantID); err == nil {
		for _, d := range disabledList {
			disabledSet[fmt.Sprintf("%s_%d", d.AgentID, d.SourceTenantID)] = true
		}
	}
	for orgID, list := range byOrg {
		tm := members[orgID]
		result := make([]*types.OrganizationSharedAgentItem, 0, len(list))
		for _, share := range list {
			if share.Agent == nil {
				continue
			}
			effective := types.MinOrgRole(share.Permission, tm.Role)
			effective = applyTenantRoleCap(effective, callerTenantRole)
			orgName := ""
			if share.Organization != nil {
				orgName = share.Organization.Name
			}
			info := &types.SharedAgentInfo{
				Agent:          share.Agent,
				ShareID:        share.ID,
				OrganizationID: share.OrganizationID,
				OrgName:        orgName,
				Permission:     effective,
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
				IsMine:          share.SourceTenantID == tenantID,
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

// GetSharedAgentForTenant returns the shared agent by agentID if the caller's
// tenant has access; source tenant is resolved from the share. One share
// lookup + one agent lookup.
//
// callerTenantRole is currently only used for symmetry / future caps on
// agent execution (e.g. tenant Viewers might be banned from
// state-changing tool calls in a follow-up).
func (s *agentShareService) GetSharedAgentForTenant(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, agentID string) (*types.CustomAgent, error) {
	if agentID == "" {
		return nil, ErrAgentShareNotFound
	}
	share, err := s.shareRepo.GetShareByAgentIDForTenant(ctx, tenantID, agentID, tenantID)
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
	_ = callerTenantRole
	return agent, nil
}

// TenantCanAccessKBViaSomeSharedAgent returns true if the caller's tenant has
// at least one shared agent that can access the given KB (used when opening KB
// detail from "通过智能体可见" list without agent_id).
func (s *agentShareService) TenantCanAccessKBViaSomeSharedAgent(ctx context.Context, tenantID uint64, callerTenantRole types.TenantRole, kb *types.KnowledgeBase) (bool, error) {
	if kb == nil || kb.ID == "" {
		return false, nil
	}
	list, err := s.ListSharedAgents(ctx, tenantID, callerTenantRole)
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

// GetShareByAgentIDForTenant returns one share for the given agentID that the
// tenant can reach, excluding source_tenant_id == excludeTenantID.
func (s *agentShareService) GetShareByAgentIDForTenant(ctx context.Context, tenantID uint64, agentID string, excludeTenantID uint64) (*types.AgentShare, error) {
	return s.shareRepo.GetShareByAgentIDForTenant(ctx, tenantID, agentID, excludeTenantID)
}
