package session

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/service"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

type resolveAgentShareStub struct {
	agent *types.CustomAgent
	err   error
}

func (s *resolveAgentShareStub) GetSharedAgentForTenant(
	_ context.Context,
	_ uint64,
	_ types.TenantRole,
	_ string,
	_ ...uint64,
) (*types.CustomAgent, error) {
	return s.agent, s.err
}

func (s *resolveAgentShareStub) ShareAgent(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) RemoveShare(context.Context, string, string, uint64) error {
	panic("not implemented")
}
func (s *resolveAgentShareStub) ListSharesByAgent(context.Context, string, uint64) ([]*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) ListSharesByOrganization(context.Context, string) ([]*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) ListSharedAgents(context.Context, uint64, types.TenantRole) ([]*types.SharedAgentInfo, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) ListSharedAgentsInOrganization(context.Context, string, uint64, types.TenantRole) ([]*types.OrganizationSharedAgentItem, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) ListSharedAgentsInOrganizations(context.Context, []string, uint64, types.TenantRole) (map[string][]*types.OrganizationSharedAgentItem, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) SetSharedAgentDisabledByMe(context.Context, uint64, string, uint64, bool) error {
	panic("not implemented")
}
func (s *resolveAgentShareStub) TenantCanAccessKBViaSomeSharedAgent(context.Context, uint64, types.TenantRole, *types.KnowledgeBase) (bool, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) GetShare(context.Context, string) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) GetShareByAgentAndOrg(context.Context, string, string) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) GetShareByAgentIDForTenant(context.Context, uint64, string, uint64) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *resolveAgentShareStub) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}

type resolveOwnAgentStub struct {
	agent *types.CustomAgent
	err   error
}

func (s *resolveOwnAgentStub) GetAgentByID(context.Context, string) (*types.CustomAgent, error) {
	return s.agent, s.err
}

func (s *resolveOwnAgentStub) CreateAgent(context.Context, *types.CustomAgent) (*types.CustomAgent, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) GetAgentByIDAndTenant(context.Context, string, uint64) (*types.CustomAgent, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) ListAgents(context.Context) ([]*types.CustomAgent, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) UpdateAgent(
	context.Context, *types.CustomAgent, *string,
) (*types.CustomAgent, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) DeleteAgent(context.Context, string) error {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) CopyAgent(context.Context, string) (*types.CustomAgent, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) GetSuggestedQuestions(context.Context, string, []string, []string, []types.TagScope, int) ([]types.SuggestedQuestion, error) {
	panic("not implemented")
}
func (s *resolveOwnAgentStub) GetKnowledgeSuggestedQuestions(context.Context, string, []string, []string, []types.TagScope, int) ([]types.SuggestedQuestion, error) {
	panic("not implemented")
}

func newResolveAgentTestContext(tenantID uint64) (*gin.Context, context.Context) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(http.MethodPost, "/", nil)
	c.Set(types.UserIDContextKey.String(), "user-1")
	c.Set(types.TenantIDContextKey.String(), tenantID)
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, tenantID)
	c.Request = c.Request.WithContext(ctx)
	return c, ctx
}

func TestResolveAgent_RejectsInvalidSharedSelectorWithoutLocalFallback(t *testing.T) {
	c, ctx := newResolveAgentTestContext(7)
	localAgent := &types.CustomAgent{ID: "builtin-smart-reasoning", TenantID: 7, Name: "Local"}
	h := &Handler{
		agentShareService:  &resolveAgentShareStub{},
		customAgentService: &resolveOwnAgentStub{agent: localAgent},
	}

	agent, effectiveTenantID, sharedReadOnly := h.resolveAgent(ctx, c, "builtin-smart-reasoning", 84)

	require.Nil(t, agent)
	require.Equal(t, uint64(0), effectiveTenantID)
	require.False(t, sharedReadOnly)
}

func TestResolveAgent_UsesSharedAgentWhenSourceSelectorMatches(t *testing.T) {
	c, ctx := newResolveAgentTestContext(7)
	sharedAgent := &types.CustomAgent{ID: "builtin-smart-reasoning", TenantID: 84, Name: "Shared"}
	h := &Handler{
		agentShareService:  &resolveAgentShareStub{agent: sharedAgent},
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{ID: "builtin-smart-reasoning", TenantID: 7}},
	}

	agent, effectiveTenantID, sharedReadOnly := h.resolveAgent(ctx, c, "builtin-smart-reasoning", 84)

	require.Equal(t, sharedAgent, agent)
	require.Equal(t, uint64(84), effectiveTenantID)
	require.True(t, sharedReadOnly)
}

func TestResolveAgent_FallsBackToLocalAgentWithoutSourceSelector(t *testing.T) {
	c, ctx := newResolveAgentTestContext(7)
	localAgent := &types.CustomAgent{ID: "builtin-smart-reasoning", TenantID: 7, Name: "Local"}
	h := &Handler{
		agentShareService:  &resolveAgentShareStub{},
		customAgentService: &resolveOwnAgentStub{agent: localAgent},
	}

	agent, effectiveTenantID, sharedReadOnly := h.resolveAgent(ctx, c, "builtin-smart-reasoning", 0)

	require.Equal(t, localAgent, agent)
	require.Equal(t, uint64(0), effectiveTenantID)
	require.False(t, sharedReadOnly)
}

// Built-in IDs exist in every workspace. Without a source selector a share of
// the same ID must not replace the caller's own agent.
func TestResolveAgent_PrefersOwnAgentOverSameIDShareWithoutSourceSelector(t *testing.T) {
	c, ctx := newResolveAgentTestContext(7)
	localAgent := &types.CustomAgent{ID: "builtin-quick-answer", TenantID: 7, Name: "Local"}
	h := &Handler{
		agentShareService: &resolveAgentShareStub{
			agent: &types.CustomAgent{ID: "builtin-quick-answer", TenantID: 84, Name: "Shared"},
		},
		customAgentService: &resolveOwnAgentStub{agent: localAgent},
	}

	agent, effectiveTenantID, sharedReadOnly := h.resolveAgent(ctx, c, "builtin-quick-answer", 0)

	require.Equal(t, localAgent, agent)
	require.Equal(t, uint64(0), effectiveTenantID)
	require.False(t, sharedReadOnly)
}

// A shared agent without the source selector (e.g. a restored selection) still
// resolves when the caller has no agent of that ID.
func TestResolveAgent_UsesShareWhenNoOwnAgentMatches(t *testing.T) {
	c, ctx := newResolveAgentTestContext(7)
	sharedAgent := &types.CustomAgent{ID: "3f0c2d9e-shared", TenantID: 84, Name: "Shared"}
	h := &Handler{
		agentShareService:  &resolveAgentShareStub{agent: sharedAgent},
		customAgentService: &resolveOwnAgentStub{err: errors.New("agent not found")},
	}

	agent, effectiveTenantID, sharedReadOnly := h.resolveAgent(ctx, c, sharedAgent.ID, 0)

	require.Equal(t, sharedAgent, agent)
	require.Equal(t, uint64(84), effectiveTenantID)
	require.True(t, sharedReadOnly)
}

func TestTerminalProvisionPin_UsesSharedAgentSandboxConfig(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodGet,
		"/?agent_id=builtin-smart-reasoning&agent_source_tenant_id=84",
		nil,
	)
	c.Set(types.UserIDContextKey.String(), "user-1")
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
	c.Request = c.Request.WithContext(ctx)

	sharedAgent := &types.CustomAgent{
		ID:       "builtin-smart-reasoning",
		TenantID: 84,
		Config:   types.CustomAgentConfig{SandboxConfigID: "shared-cfg"},
	}
	h := &Handler{
		agentShareService: &resolveAgentShareStub{agent: sharedAgent},
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{
			ID:       "builtin-smart-reasoning",
			TenantID: 7,
			Config:   types.CustomAgentConfig{SandboxConfigID: "local-cfg"},
		}},
	}

	require.Equal(t, service.SandboxPin{}, h.terminalProvisionPin(ctx, c, false),
		"lookup-only connects must not resolve an agent config")
	// The lending workspace travels with the config id: sandbox configs are
	// keyed by (tenant, id), so "shared-cfg" does not exist in workspace 7.
	require.Equal(t,
		service.SandboxPin{ConfigID: "shared-cfg", TenantID: 84},
		h.terminalProvisionPin(ctx, c, true))
}

// An own agent's config already lives in the caller's workspace, so the pin
// carries no owner and SandboxPin.TenantOr falls back to the request tenant.
func TestTerminalProvisionPin_OwnAgentCarriesNoWorkspace(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Request = httptest.NewRequest(
		http.MethodGet, "/?agent_id=builtin-smart-reasoning", nil)
	c.Set(types.UserIDContextKey.String(), "user-1")
	c.Set(types.TenantIDContextKey.String(), uint64(7))
	ctx := context.WithValue(c.Request.Context(), types.TenantIDContextKey, uint64(7))
	c.Request = c.Request.WithContext(ctx)

	h := &Handler{
		customAgentService: &resolveOwnAgentStub{agent: &types.CustomAgent{
			ID:       "builtin-smart-reasoning",
			TenantID: 7,
			Config:   types.CustomAgentConfig{SandboxConfigID: "local-cfg"},
		}},
	}

	pin := h.terminalProvisionPin(ctx, c, true)
	require.Equal(t, service.SandboxPin{ConfigID: "local-cfg"}, pin)
	require.Equal(t, uint64(7), pin.TenantOr(7))
}
