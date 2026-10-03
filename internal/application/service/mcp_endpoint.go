package service

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"strings"

	"github.com/Tencent/WeKnora/internal/application/access"
	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

var (
	// ErrMCPEndpointNotFound is returned when an endpoint does not exist or
	// belongs to another workspace; the two cases are deliberately
	// indistinguishable to callers.
	ErrMCPEndpointNotFound = errors.New("mcp endpoint not found")
	// ErrMCPEndpointDisabled is returned when an otherwise valid token targets
	// an endpoint that has been switched off.
	ErrMCPEndpointDisabled = errors.New("mcp endpoint disabled")
	// ErrMCPEndpointTokenInvalid is returned when the bearer token does not
	// resolve to the addressed endpoint.
	ErrMCPEndpointTokenInvalid = errors.New("mcp endpoint token invalid")
)

const (
	mcpEndpointTokenBytes    = 32
	mcpEndpointTokenHintLen  = 12
	mcpEndpointMaxNameLen    = 255
	mcpEndpointMaxRatePerMin = 6000
)

type mcpEndpointService struct {
	repo           interfaces.MCPEndpointRepository
	kbService      interfaces.KnowledgeBaseService
	kbShareService interfaces.KBShareService
	agentService   interfaces.CustomAgentService
}

// NewMCPEndpointService creates the workspace MCP endpoint service.
func NewMCPEndpointService(
	repo interfaces.MCPEndpointRepository,
	kbService interfaces.KnowledgeBaseService,
	kbShareService interfaces.KBShareService,
	agentService interfaces.CustomAgentService,
) interfaces.MCPEndpointService {
	return &mcpEndpointService{
		repo:           repo,
		kbService:      kbService,
		kbShareService: kbShareService,
		agentService:   agentService,
	}
}

func generateMCPEndpointToken() (string, error) {
	buf := make([]byte, mcpEndpointTokenBytes)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return types.MCPEndpointTokenPrefix + base64.RawURLEncoding.EncodeToString(buf), nil
}

// HashMCPEndpointToken returns the storage digest of a plaintext token.
func HashMCPEndpointToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func mcpEndpointTokenHint(token string) string {
	if len(token) <= mcpEndpointTokenHintLen {
		return token
	}
	return token[:mcpEndpointTokenHintLen]
}

func (s *mcpEndpointService) Create(
	ctx context.Context, tenantID uint64, ep *types.MCPEndpoint,
) (*types.MCPEndpoint, string, error) {
	if ep == nil {
		return nil, "", apperrors.NewBadRequestError("endpoint is required")
	}
	name := strings.TrimSpace(ep.Name)
	if name == "" {
		return nil, "", apperrors.NewBadRequestError("name is required")
	}
	if len(name) > mcpEndpointMaxNameLen {
		return nil, "", apperrors.NewBadRequestError("name is too long")
	}
	tools := types.NormalizeMCPEndpointTools(ep.Tools)
	if len(tools) == 0 {
		return nil, "", apperrors.NewBadRequestError("at least one tool must be enabled")
	}
	kbIDs, err := s.validateKnowledgeBases(ctx, tenantID, ep.KnowledgeBaseIDs)
	if err != nil {
		return nil, "", err
	}
	agentID, err := s.validateAgent(ctx, tenantID, ep.DefaultAgentID)
	if err != nil {
		return nil, "", err
	}
	rate, err := normalizeMCPEndpointRate(ep.RateLimitPerMinute)
	if err != nil {
		return nil, "", err
	}
	token, err := generateMCPEndpointToken()
	if err != nil {
		return nil, "", err
	}
	row := &types.MCPEndpoint{
		TenantID:           tenantID,
		Name:               name,
		Description:        strings.TrimSpace(ep.Description),
		Enabled:            ep.Enabled,
		TokenHash:          HashMCPEndpointToken(token),
		TokenHint:          mcpEndpointTokenHint(token),
		KnowledgeBaseIDs:   types.StringArray(kbIDs),
		Tools:              types.StringArray(tools),
		DefaultAgentID:     agentID,
		RateLimitPerMinute: rate,
	}
	if err := requireKeyCoversEndpoint(ctx, row); err != nil {
		return nil, "", err
	}
	if err := s.repo.Create(ctx, row); err != nil {
		return nil, "", err
	}
	return row, token, nil
}

func (s *mcpEndpointService) Get(ctx context.Context, tenantID uint64, id string) (*types.MCPEndpoint, error) {
	return s.getOwned(ctx, tenantID, id)
}

func (s *mcpEndpointService) List(ctx context.Context, tenantID uint64) ([]*types.MCPEndpoint, error) {
	return s.repo.ListByTenant(ctx, tenantID)
}

func (s *mcpEndpointService) Update(
	ctx context.Context, tenantID uint64, id string, upd interfaces.MCPEndpointUpdate,
) (*types.MCPEndpoint, error) {
	ep, err := s.getOwned(ctx, tenantID, id)
	if err != nil {
		return nil, err
	}
	if upd.Name != nil {
		name := strings.TrimSpace(*upd.Name)
		if name == "" {
			return nil, apperrors.NewBadRequestError("name is required")
		}
		if len(name) > mcpEndpointMaxNameLen {
			return nil, apperrors.NewBadRequestError("name is too long")
		}
		ep.Name = name
	}
	if upd.Description != nil {
		ep.Description = strings.TrimSpace(*upd.Description)
	}
	if upd.Enabled != nil {
		ep.Enabled = *upd.Enabled
	}
	if upd.Tools != nil {
		tools := types.NormalizeMCPEndpointTools(*upd.Tools)
		if len(tools) == 0 {
			return nil, apperrors.NewBadRequestError("at least one tool must be enabled")
		}
		ep.Tools = types.StringArray(tools)
	}
	if upd.KnowledgeBaseIDs != nil {
		kbIDs, err := s.validateKnowledgeBases(ctx, tenantID, *upd.KnowledgeBaseIDs)
		if err != nil {
			return nil, err
		}
		ep.KnowledgeBaseIDs = types.StringArray(kbIDs)
	}
	if upd.DefaultAgentID != nil {
		agentID, err := s.validateAgent(ctx, tenantID, *upd.DefaultAgentID)
		if err != nil {
			return nil, err
		}
		ep.DefaultAgentID = agentID
	}
	if upd.RateLimitPerMinute != nil {
		rate, err := normalizeMCPEndpointRate(*upd.RateLimitPerMinute)
		if err != nil {
			return nil, err
		}
		ep.RateLimitPerMinute = rate
	}
	if err := requireKeyCoversEndpoint(ctx, ep); err != nil {
		return nil, err
	}
	if err := s.repo.Update(ctx, ep); err != nil {
		return nil, err
	}
	return ep, nil
}

func (s *mcpEndpointService) Delete(ctx context.Context, tenantID uint64, id string) error {
	if _, err := s.getOwned(ctx, tenantID, id); err != nil {
		return err
	}
	return s.repo.Delete(ctx, tenantID, id)
}

func (s *mcpEndpointService) RotateToken(
	ctx context.Context, tenantID uint64, id string,
) (*types.MCPEndpoint, string, error) {
	ep, err := s.getOwned(ctx, tenantID, id)
	if err != nil {
		return nil, "", err
	}
	if err := requireKeyCoversEndpoint(ctx, ep); err != nil {
		return nil, "", err
	}
	token, err := generateMCPEndpointToken()
	if err != nil {
		return nil, "", err
	}
	ep.TokenHash = HashMCPEndpointToken(token)
	ep.TokenHint = mcpEndpointTokenHint(token)
	if err := s.repo.Update(ctx, ep); err != nil {
		return nil, "", err
	}
	return ep, token, nil
}

// requireKeyCoversEndpoint keeps a scoped API key from minting an endpoint
// token with more authority than the key holds. The token is a new credential
// whose scope comes from the endpoint, so the endpoint's KBs must lie inside
// the key's allow-list (an empty list means the whole workspace) and its tools
// may need only capabilities the key has. Rotating counts too: it hands out
// the existing endpoint's authority.
func requireKeyCoversEndpoint(ctx context.Context, ep *types.MCPEndpoint) error {
	scope, ok := types.TenantAPIKeyScopeFromContext(ctx)
	if !ok || scope.FullAccess {
		return nil
	}
	if len(scope.KnowledgeBaseIDs) > 0 {
		if len(ep.KnowledgeBaseIDs) == 0 {
			return apperrors.NewForbiddenError("this API key may only create endpoints limited to its knowledge bases")
		}
		allowed := make(map[string]bool, len(scope.KnowledgeBaseIDs))
		for _, id := range scope.KnowledgeBaseIDs {
			allowed[id] = true
		}
		for _, id := range ep.KnowledgeBaseIDs {
			if !allowed[id] {
				return apperrors.NewForbiddenError("knowledge base is outside this API key's scope: " + id)
			}
		}
	}
	for _, capability := range types.MCPEndpointCapabilitiesForTools(ep.Tools) {
		if !scope.HasCapability(types.APIKeyCapability(capability)) {
			return apperrors.NewForbiddenError(
				"this API key lacks the " + capability + " capability the endpoint's tools need")
		}
	}
	return nil
}

func (s *mcpEndpointService) Authenticate(
	ctx context.Context, endpointID, token string,
) (*types.MCPEndpoint, error) {
	endpointID = strings.TrimSpace(endpointID)
	token = strings.TrimSpace(token)
	if endpointID == "" || token == "" || !strings.HasPrefix(token, types.MCPEndpointTokenPrefix) {
		return nil, ErrMCPEndpointTokenInvalid
	}
	ep, err := s.repo.GetByTokenHash(ctx, HashMCPEndpointToken(token))
	if err != nil {
		return nil, err
	}
	// Compare the addressed endpoint id in constant time so a token for one
	// endpoint cannot be probed against another by timing.
	if ep == nil || subtle.ConstantTimeCompare([]byte(ep.ID), []byte(endpointID)) != 1 {
		return nil, ErrMCPEndpointTokenInvalid
	}
	if !ep.Enabled {
		return nil, ErrMCPEndpointDisabled
	}
	return ep, nil
}

func (s *mcpEndpointService) getOwned(ctx context.Context, tenantID uint64, id string) (*types.MCPEndpoint, error) {
	ep, err := s.repo.GetByID(ctx, strings.TrimSpace(id))
	if err != nil {
		return nil, err
	}
	if ep == nil || ep.TenantID != tenantID {
		return nil, ErrMCPEndpointNotFound
	}
	return ep, nil
}

// validateKnowledgeBases confirms every ID names a knowledge base the
// workspace owns or has been granted through an organization share, and
// returns the deduplicated list. The lookup is tenant-agnostic on purpose so
// that shared knowledge bases resolve; authorization is then decided by
// access.ResolveKB for the calling principal, never inferred from the ID.
func (s *mcpEndpointService) validateKnowledgeBases(
	ctx context.Context, tenantID uint64, ids []string,
) ([]string, error) {
	caller := types.CallerFromContext(ctx)
	if caller.TenantID == 0 {
		caller.TenantID = tenantID
	}
	if caller.TenantID != tenantID {
		return nil, apperrors.NewForbiddenError("caller does not belong to this workspace")
	}
	out := make([]string, 0, len(ids))
	seen := map[string]struct{}{}
	for _, raw := range ids {
		id := strings.TrimSpace(raw)
		if id == "" {
			continue
		}
		if _, dup := seen[id]; dup {
			continue
		}
		kb, err := s.kbService.GetKnowledgeBaseByIDOnly(ctx, id)
		if err != nil || kb == nil {
			return nil, apperrors.NewNotFoundError("knowledge base not found: " + id)
		}
		if _, err := access.ResolveKB(ctx, access.KBRequest{Caller: caller}, kb,
			types.OrgRoleViewer, s.kbShareService, nil); err != nil {
			return nil, apperrors.NewNotFoundError("knowledge base not found: " + id)
		}
		seen[id] = struct{}{}
		out = append(out, id)
	}
	return out, nil
}

// validateAgent accepts a tenant-owned agent or a user-facing builtin. The
// same rule is applied again at call time by the MCP server.
func (s *mcpEndpointService) validateAgent(ctx context.Context, tenantID uint64, agentID string) (string, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return "", nil
	}
	agent, err := s.agentService.GetAgentByID(ctx, agentID)
	if err != nil || agent == nil || !types.MCPEndpointAgentAllowed(agent, tenantID) {
		return "", apperrors.NewNotFoundError("agent not found")
	}
	return agentID, nil
}

func normalizeMCPEndpointRate(rate int) (int, error) {
	if rate == 0 {
		return types.DefaultMCPEndpointRateLimitPerMinute, nil
	}
	if rate < 0 || rate > mcpEndpointMaxRatePerMin {
		return 0, apperrors.NewBadRequestError("rate_limit_per_minute is out of range")
	}
	return rate, nil
}
