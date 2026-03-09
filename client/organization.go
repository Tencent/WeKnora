package client

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"time"
)

// Organization represents a collaboration organization
type Organization struct {
	ID                     string     `json:"id"`
	Name                   string     `json:"name"`
	Description            string     `json:"description"`
	Avatar                 string     `json:"avatar,omitempty"`
	OwnerID                string     `json:"owner_id"`
	InviteCode             string     `json:"invite_code,omitempty"`
	InviteCodeExpiresAt    *time.Time `json:"invite_code_expires_at,omitempty"`
	InviteCodeValidityDays int        `json:"invite_code_validity_days"`
	RequireApproval        bool       `json:"require_approval"`
	Searchable             bool       `json:"searchable"`
	MemberLimit            int        `json:"member_limit"`
	CreatedAt              time.Time  `json:"created_at"`
	UpdatedAt              time.Time  `json:"updated_at"`
}

// OrganizationResponse represents an organization in API responses (with counts)
type OrganizationResponse struct {
	ID                      string     `json:"id"`
	Name                    string     `json:"name"`
	Description             string     `json:"description"`
	Avatar                  string     `json:"avatar,omitempty"`
	OwnerID                 string     `json:"owner_id"`
	InviteCode              string     `json:"invite_code,omitempty"`
	InviteCodeExpiresAt     *time.Time `json:"invite_code_expires_at,omitempty"`
	InviteCodeValidityDays  int        `json:"invite_code_validity_days"`
	RequireApproval         bool       `json:"require_approval"`
	Searchable              bool       `json:"searchable"`
	MemberLimit             int        `json:"member_limit"`
	MemberCount             int        `json:"member_count"`
	ShareCount              int        `json:"share_count"`
	AgentShareCount         int        `json:"agent_share_count"`
	PendingJoinRequestCount int        `json:"pending_join_request_count"`
	IsOwner                 bool       `json:"is_owner"`
	MyRole                  string     `json:"my_role,omitempty"`
	HasPendingUpgrade       bool       `json:"has_pending_upgrade"`
	CreatedAt               time.Time  `json:"created_at"`
	UpdatedAt               time.Time  `json:"updated_at"`
}

// CreateOrganizationRequest represents a request to create an organization
type CreateOrganizationRequest struct {
	Name                   string `json:"name"`
	Description            string `json:"description,omitempty"`
	Avatar                 string `json:"avatar,omitempty"`
	InviteCodeValidityDays *int   `json:"invite_code_validity_days,omitempty"`
	MemberLimit            *int   `json:"member_limit,omitempty"`
}

// UpdateOrganizationRequest represents a request to update an organization
type UpdateOrganizationRequest struct {
	Name                   *string `json:"name,omitempty"`
	Description            *string `json:"description,omitempty"`
	Avatar                 *string `json:"avatar,omitempty"`
	RequireApproval        *bool   `json:"require_approval,omitempty"`
	Searchable             *bool   `json:"searchable,omitempty"`
	InviteCodeValidityDays *int    `json:"invite_code_validity_days,omitempty"`
	MemberLimit            *int    `json:"member_limit,omitempty"`
}

// OrganizationMemberResponse represents a member in API responses
type OrganizationMemberResponse struct {
	ID       string    `json:"id"`
	UserID   string    `json:"user_id"`
	Username string    `json:"username"`
	Email    string    `json:"email"`
	Avatar   string    `json:"avatar"`
	Role     string    `json:"role"`
	TenantID uint64    `json:"tenant_id"`
	JoinedAt time.Time `json:"joined_at"`
}

// KnowledgeBaseShareResponse represents a KB share record in API responses
type KnowledgeBaseShareResponse struct {
	ID                string    `json:"id"`
	KnowledgeBaseID   string    `json:"knowledge_base_id"`
	KnowledgeBaseName string    `json:"knowledge_base_name"`
	OrganizationID    string    `json:"organization_id"`
	OrganizationName  string    `json:"organization_name"`
	SharedByUserID    string    `json:"shared_by_user_id"`
	SharedByUsername  string    `json:"shared_by_username"`
	SourceTenantID    uint64    `json:"source_tenant_id"`
	Permission        string    `json:"permission"`
	MyRoleInOrg       string    `json:"my_role_in_org"`
	MyPermission      string    `json:"my_permission"`
	CreatedAt         time.Time `json:"created_at"`
}

// AgentShareResponse represents an agent share record in API responses
type AgentShareResponse struct {
	ID               string    `json:"id"`
	AgentID          string    `json:"agent_id"`
	AgentName        string    `json:"agent_name"`
	OrganizationID   string    `json:"organization_id"`
	OrganizationName string    `json:"organization_name"`
	SharedByUserID   string    `json:"shared_by_user_id"`
	SharedByUsername string    `json:"shared_by_username"`
	SourceTenantID   uint64    `json:"source_tenant_id"`
	Permission       string    `json:"permission"`
	CreatedAt        time.Time `json:"created_at"`
}

// JoinRequestResponse represents a join request in API responses
type JoinRequestResponse struct {
	ID            string     `json:"id"`
	UserID        string     `json:"user_id"`
	Username      string     `json:"username"`
	Email         string     `json:"email"`
	Message       string     `json:"message"`
	RequestType   string     `json:"request_type"`
	PrevRole      string     `json:"prev_role"`
	RequestedRole string     `json:"requested_role"`
	Status        string     `json:"status"`
	CreatedAt     time.Time  `json:"created_at"`
	ReviewedAt    *time.Time `json:"reviewed_at,omitempty"`
}

// SharedKnowledgeBaseInfo represents a shared knowledge base
type SharedKnowledgeBaseInfo struct {
	ShareID        string    `json:"share_id"`
	OrganizationID string    `json:"organization_id"`
	OrgName        string    `json:"org_name"`
	Permission     string    `json:"permission"`
	SourceTenantID uint64    `json:"source_tenant_id"`
	SharedAt       time.Time `json:"shared_at"`
}

// SharedAgentInfo represents a shared agent
type SharedAgentInfo struct {
	ShareID        string    `json:"share_id"`
	OrganizationID string    `json:"organization_id"`
	OrgName        string    `json:"org_name"`
	Permission     string    `json:"permission"`
	SourceTenantID uint64    `json:"source_tenant_id"`
	SharedAt       time.Time `json:"shared_at"`
}

// --- Organization CRUD ---

// CreateOrganization creates a new organization
func (c *Client) CreateOrganization(ctx context.Context, req *CreateOrganizationRequest) (*OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations", req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListMyOrganizations lists organizations the current user belongs to
func (c *Client) ListMyOrganizations(ctx context.Context) ([]OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/organizations", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Organizations []OrganizationResponse `json:"organizations"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Organizations, nil
}

// GetOrganization gets an organization by ID
func (c *Client) GetOrganization(ctx context.Context, orgID string) (*OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s", orgID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// UpdateOrganization updates an organization
func (c *Client) UpdateOrganization(ctx context.Context, orgID string, req *UpdateOrganizationRequest) (*OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/organizations/%s", orgID), req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// DeleteOrganization deletes an organization
func (c *Client) DeleteOrganization(ctx context.Context, orgID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/organizations/%s", orgID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Organization membership ---

// JoinOrganizationByInviteCode joins an organization using an invite code
func (c *Client) JoinOrganizationByInviteCode(ctx context.Context, inviteCode string) error {
	req := map[string]string{"invite_code": inviteCode}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// SubmitJoinRequest submits a join request for organizations that require approval
func (c *Client) SubmitJoinRequest(ctx context.Context, inviteCode, message, role string) error {
	req := map[string]string{
		"invite_code": inviteCode,
		"message":     message,
		"role":        role,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join-request", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// SearchOrganizations searches for discoverable organizations
func (c *Client) SearchOrganizations(ctx context.Context, keyword string, page, pageSize int) ([]OrganizationResponse, error) {
	q := url.Values{}
	if keyword != "" {
		q.Set("keyword", keyword)
	}
	if page > 0 {
		q.Set("page", fmt.Sprintf("%d", page))
	}
	if pageSize > 0 {
		q.Set("page_size", fmt.Sprintf("%d", pageSize))
	}
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/organizations/search", nil, q)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Organizations []OrganizationResponse `json:"organizations"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Organizations, nil
}

// JoinByOrganizationID joins a searchable organization by its ID
func (c *Client) JoinByOrganizationID(ctx context.Context, orgID, message, role string) error {
	req := map[string]string{
		"organization_id": orgID,
		"message":         message,
		"role":            role,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, "/api/v1/organizations/join-by-id", req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// PreviewOrganizationByInviteCode previews an organization before joining
func (c *Client) PreviewOrganizationByInviteCode(ctx context.Context, code string) (*OrganizationResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/preview/%s", code), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                  `json:"success"`
		Data    *OrganizationResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// LeaveOrganization leaves an organization
func (c *Client) LeaveOrganization(ctx context.Context, orgID string) error {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/leave", orgID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RequestRoleUpgrade requests a role upgrade in an organization
func (c *Client) RequestRoleUpgrade(ctx context.Context, orgID, requestedRole, message string) error {
	req := map[string]string{
		"requested_role": requestedRole,
		"message":        message,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/request-upgrade", orgID), req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// GenerateInviteCode generates a new invite code for an organization
func (c *Client) GenerateInviteCode(ctx context.Context, orgID string) (string, error) {
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/invite-code", orgID), nil, nil)
	if err != nil {
		return "", err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			InviteCode string `json:"invite_code"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return "", err
	}
	return result.Data.InviteCode, nil
}

// SearchUsersForInvite searches users to invite into an organization (admin only)
func (c *Client) SearchUsersForInvite(ctx context.Context, orgID, keyword string) ([]UserInfo, error) {
	q := url.Values{}
	if keyword != "" {
		q.Set("keyword", keyword)
	}
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s/search-users", orgID), nil, q)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool       `json:"success"`
		Data    []UserInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// InviteMember directly invites a user to an organization (admin only)
func (c *Client) InviteMember(ctx context.Context, orgID, userID, role string) error {
	req := map[string]string{
		"user_id": userID,
		"role":    role,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/organizations/%s/invite", orgID), req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// ListMembers lists members of an organization
func (c *Client) ListOrgMembers(ctx context.Context, orgID string) ([]OrganizationMemberResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s/members", orgID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Members []OrganizationMemberResponse `json:"members"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Members, nil
}

// UpdateMemberRole updates a member's role in an organization
func (c *Client) UpdateMemberRole(ctx context.Context, orgID, userID, role string) error {
	req := map[string]string{"role": role}
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/organizations/%s/members/%s", orgID, userID), req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RemoveMember removes a member from an organization
func (c *Client) RemoveMember(ctx context.Context, orgID, userID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/organizations/%s/members/%s", orgID, userID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Join request management ---

// ListJoinRequests lists pending join requests (admin only)
func (c *Client) ListJoinRequests(ctx context.Context, orgID string) ([]JoinRequestResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s/join-requests", orgID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Requests []JoinRequestResponse `json:"requests"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Requests, nil
}

// ReviewJoinRequest reviews a join request (approve/reject)
func (c *Client) ReviewJoinRequest(ctx context.Context, orgID, requestID string, approved bool, message, role string) error {
	req := map[string]any{
		"approved": approved,
		"message":  message,
		"role":     role,
	}
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/organizations/%s/join-requests/%s/review", orgID, requestID), req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Knowledge base sharing ---

// ShareKnowledgeBase shares a knowledge base with an organization
func (c *Client) ShareKnowledgeBase(ctx context.Context, kbID, orgID, permission string) (*KnowledgeBaseShareResponse, error) {
	req := map[string]string{
		"organization_id": orgID,
		"permission":      permission,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/knowledge-bases/%s/shares", kbID), req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                        `json:"success"`
		Data    *KnowledgeBaseShareResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListKBShares lists shares of a knowledge base
func (c *Client) ListKBShares(ctx context.Context, kbID string) ([]KnowledgeBaseShareResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/knowledge-bases/%s/shares", kbID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []KnowledgeBaseShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// UpdateSharePermission updates a KB share's permission
func (c *Client) UpdateSharePermission(ctx context.Context, kbID, shareID, permission string) error {
	req := map[string]string{"permission": permission}
	resp, err := c.doRequest(ctx, http.MethodPut, fmt.Sprintf("/api/v1/knowledge-bases/%s/shares/%s", kbID, shareID), req, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// RemoveKBShare removes a KB share
func (c *Client) RemoveKBShare(ctx context.Context, kbID, shareID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/knowledge-bases/%s/shares/%s", kbID, shareID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Agent sharing ---

// ShareAgent shares an agent with an organization
func (c *Client) ShareAgent(ctx context.Context, agentID, orgID, permission string) (*AgentShareResponse, error) {
	req := map[string]string{
		"organization_id": orgID,
		"permission":      permission,
	}
	resp, err := c.doRequest(ctx, http.MethodPost, fmt.Sprintf("/api/v1/agents/%s/shares", agentID), req, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                `json:"success"`
		Data    *AgentShareResponse `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListAgentShares lists shares of an agent
func (c *Client) ListAgentShares(ctx context.Context, agentID string) ([]AgentShareResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/agents/%s/shares", agentID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []AgentShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// RemoveAgentShare removes an agent share
func (c *Client) RemoveAgentShare(ctx context.Context, agentID, shareID string) error {
	resp, err := c.doRequest(ctx, http.MethodDelete, fmt.Sprintf("/api/v1/agents/%s/shares/%s", agentID, shareID), nil, nil)
	if err != nil {
		return err
	}
	return parseResponse(resp, nil)
}

// --- Organization shared resources ---

// ListOrgShares lists knowledge bases shared to an organization
func (c *Client) ListOrgShares(ctx context.Context, orgID string) ([]KnowledgeBaseShareResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s/shares", orgID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []KnowledgeBaseShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// ListOrgAgentShares lists agents shared to an organization
func (c *Client) ListOrgAgentShares(ctx context.Context, orgID string) ([]AgentShareResponse, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, fmt.Sprintf("/api/v1/organizations/%s/agent-shares", orgID), nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool `json:"success"`
		Data    struct {
			Shares []AgentShareResponse `json:"shares"`
		} `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data.Shares, nil
}

// ListSharedKnowledgeBases lists all knowledge bases shared to the current user
func (c *Client) ListSharedKnowledgeBases(ctx context.Context) ([]SharedKnowledgeBaseInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/shared-knowledge-bases", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool                      `json:"success"`
		Data    []SharedKnowledgeBaseInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}

// ListSharedAgents lists all agents shared to the current user
func (c *Client) ListSharedAgents(ctx context.Context) ([]SharedAgentInfo, error) {
	resp, err := c.doRequest(ctx, http.MethodGet, "/api/v1/shared-agents", nil, nil)
	if err != nil {
		return nil, err
	}
	var result struct {
		Success bool              `json:"success"`
		Data    []SharedAgentInfo `json:"data"`
	}
	if err := parseResponse(resp, &result); err != nil {
		return nil, err
	}
	return result.Data, nil
}
