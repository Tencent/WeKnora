package middleware

import (
	"context"
	"errors"
	"net/http/httptest"
	"testing"

	apprepo "github.com/Tencent/WeKnora/internal/application/repository"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/require"
)

// stubKBLookup is a tiny KBLookup stand-in for tests; satisfies the
// KBLookup interface (a single method) without dragging in the full
// KnowledgeBaseService surface.
type stubKBLookup struct {
	kbs    map[string]*types.KnowledgeBase
	getErr error
}

func (s *stubKBLookup) GetKnowledgeBaseByID(_ context.Context, id string) (*types.KnowledgeBase, error) {
	if s.getErr != nil {
		return nil, s.getErr
	}
	if kb, ok := s.kbs[id]; ok {
		return kb, nil
	}
	return nil, apprepo.ErrKnowledgeBaseNotFound
}

// stubKBShareForGuard implements just the methods the guard touches —
// CheckTenantKBPermission and GetKBSourceTenant. The other methods on
// the interface panic so any unintended new dependency surfaces
// immediately.
type stubKBShareForGuard struct {
	permission map[string]types.OrgMemberRole
	shared     map[string]bool
	source     map[string]uint64
}

func (s *stubKBShareForGuard) CheckTenantKBPermission(_ context.Context, kbID string, _ uint64, _ types.TenantRole) (types.OrgMemberRole, bool, error) {
	if s.shared[kbID] {
		return s.permission[kbID], true, nil
	}
	return "", false, nil
}

func (s *stubKBShareForGuard) GetKBSourceTenant(_ context.Context, kbID string) (uint64, error) {
	if v, ok := s.source[kbID]; ok {
		return v, nil
	}
	return 0, errors.New("not found")
}

func (s *stubKBShareForGuard) ShareKnowledgeBase(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) UpdateSharePermission(context.Context, string, types.OrgMemberRole, string, uint64) error {
	panic("not implemented")
}
func (s *stubKBShareForGuard) RemoveShare(context.Context, string, string, uint64) error {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharesByKnowledgeBase(context.Context, string, uint64) ([]*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharesByOrganization(context.Context, string) ([]*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBases(context.Context, uint64, types.TenantRole) ([]*types.SharedKnowledgeBaseInfo, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBasesInOrganization(context.Context, string, uint64, types.TenantRole) ([]*types.OrganizationSharedKnowledgeBaseItem, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) ListSharedKnowledgeBaseIDsByOrganizations(context.Context, []string, uint64) (map[string][]string, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) GetShare(context.Context, string) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) GetShareByKBAndOrg(context.Context, string, string) (*types.KnowledgeBaseShare, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) HasTenantKBPermission(context.Context, string, uint64, types.TenantRole, types.OrgMemberRole) (bool, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) CountSharesByKnowledgeBaseIDs(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}
func (s *stubKBShareForGuard) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}

// stubAgentShareForGuard implements just the two methods the guard
// touches: GetSharedAgentForTenant (when ?agent_id=X is supplied) and
// TenantCanAccessKBViaSomeSharedAgent (the any-shared-agent fallback).
// Every other method panics so unintended new dependencies surface
// immediately.
type stubAgentShareForGuard struct {
	// agents indexed by agent id; nil entry means GetSharedAgentForTenant
	// returns nil + nil (i.e. caller has no access to that agent id).
	agents map[string]*types.CustomAgent
	// kbsViaSomeAgent[kb.ID] -> true means the any-agent fallback grants
	// access to that KB.
	kbsViaSomeAgent map[string]bool
}

func (s *stubAgentShareForGuard) GetSharedAgentForTenant(_ context.Context, _ uint64, _ types.TenantRole, agentID string) (*types.CustomAgent, error) {
	return s.agents[agentID], nil
}

func (s *stubAgentShareForGuard) TenantCanAccessKBViaSomeSharedAgent(_ context.Context, _ uint64, _ types.TenantRole, kb *types.KnowledgeBase) (bool, error) {
	return s.kbsViaSomeAgent[kb.ID], nil
}

func (s *stubAgentShareForGuard) ShareAgent(context.Context, string, string, string, uint64, types.OrgMemberRole) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) RemoveShare(context.Context, string, string, uint64) error {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) ListSharesByAgent(context.Context, string) ([]*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) ListSharesByOrganization(context.Context, string) ([]*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) ListSharedAgents(context.Context, uint64, types.TenantRole) ([]*types.SharedAgentInfo, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) ListSharedAgentsInOrganization(context.Context, string, uint64, types.TenantRole) ([]*types.OrganizationSharedAgentItem, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) ListSharedAgentsInOrganizations(context.Context, []string, uint64, types.TenantRole) (map[string][]*types.OrganizationSharedAgentItem, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) SetSharedAgentDisabledByMe(context.Context, uint64, string, uint64, bool) error {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) GetShare(context.Context, string) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) GetShareByAgentAndOrg(context.Context, string, string) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) GetShareByAgentIDForTenant(context.Context, uint64, string, uint64) (*types.AgentShare, error) {
	panic("not implemented")
}
func (s *stubAgentShareForGuard) CountByOrganizations(context.Context, []string) (map[string]int64, error) {
	panic("not implemented")
}

// guardOpts collects optional knobs for runGuard. Keeps the call site
// readable when most tests only care about a couple of dimensions.
type guardOpts struct {
	agentID    string                  // ?agent_id query param
	agentShare *stubAgentShareForGuard // nil means "no agent-share service"
}

// runGuard fires a single request through the guard and returns the
// gin recorder + the kb access (if any) the guard stashed. Defaults
// to EnableRBAC=true; the EnableRBAC=false fail-open path has its own
// dedicated tests further below.
func runGuard(
	t *testing.T,
	tenantID uint64,
	kbID string,
	requiredPerm types.OrgMemberRole,
	kb *types.KnowledgeBase,
	share *stubKBShareForGuard,
	opts guardOpts,
) (*httptest.ResponseRecorder, *gin.Context) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: kbID}}

	url := "/"
	if opts.agentID != "" {
		url = "/?agent_id=" + opts.agentID
	}
	req := httptest.NewRequest("GET", url, nil)
	ctx := context.WithValue(req.Context(), types.TenantIDContextKey, tenantID)
	c.Request = req.WithContext(ctx)

	kbsvc := &stubKBLookup{kbs: map[string]*types.KnowledgeBase{}}
	if kb != nil {
		kbsvc.kbs[kbID] = kb
	}

	// Convert the package-local concrete stub types to typed interface
	// nils when they aren't supplied — otherwise the interface wraps a
	// nil pointer and `iface != nil` evaluates true on the guard side
	// (classic Go typed-nil trap).
	var shareSvc interfaces.KBShareService
	if share != nil {
		shareSvc = share
	}
	var agentSvc interfaces.AgentShareService
	if opts.agentShare != nil {
		agentSvc = opts.agentShare
	}

	guard := RequireKBAccess(
		KBIDFromParam("id"),
		requiredPerm,
		kbsvc,
		shareSvc,
		agentSvc,
		cfgRBAC(true),
	)
	guard(c)
	return rec, c
}

func TestRequireKBAccess_OwnKB(t *testing.T) {
	rec, c := runGuard(t, 100, "kb-1",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-1", TenantID: 100},
		nil,
		guardOpts{},
	)
	require.False(t, c.IsAborted(), "should pass through")
	require.Equal(t, 200, rec.Code) // gin's default; nothing wrote a status
	access, ok := KBAccessFromContext(c)
	require.True(t, ok)
	require.Equal(t, uint64(100), access.EffectiveTenantID)
	require.Equal(t, types.OrgRoleAdmin, access.Permission, "own KB grants admin")
	// The request context's tenant should still be the caller's own.
	got, ok := types.TenantIDFromContext(c.Request.Context())
	require.True(t, ok)
	require.Equal(t, uint64(100), got)
}

func TestRequireKBAccess_NotFound_Aborts(t *testing.T) {
	_, c := runGuard(t, 100, "kb-missing", types.OrgRoleViewer, nil, nil, guardOpts{})
	require.True(t, c.IsAborted(), "missing KB must abort")
	require.NotEmpty(t, c.Errors)
	_, ok := KBAccessFromContext(c)
	require.False(t, ok, "no access should be stashed on failure")
}

func TestRequireKBAccess_SharedKB_RewritesTenantContext(t *testing.T) {
	share := &stubKBShareForGuard{
		permission: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleEditor},
		shared:     map[string]bool{"kb-shared": true},
		source:     map[string]uint64{"kb-shared": 200},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleEditor,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		share,
		guardOpts{},
	)
	require.False(t, c.IsAborted())
	access, ok := KBAccessFromContext(c)
	require.True(t, ok)
	require.Equal(t, uint64(200), access.EffectiveTenantID)
	got, _ := types.TenantIDFromContext(c.Request.Context())
	require.Equal(t, uint64(200), got, "guard must rewrite context to source tenant")
}

func TestRequireKBAccess_SharedKB_PermissionBelowMin_Aborts(t *testing.T) {
	share := &stubKBShareForGuard{
		permission: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleViewer},
		shared:     map[string]bool{"kb-shared": true},
		source:     map[string]uint64{"kb-shared": 200},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleEditor, // require Editor
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		share,
		guardOpts{},
	)
	require.True(t, c.IsAborted(), "Viewer share must reject when Editor required")
}

func TestRequireKBAccess_NoTenant_Aborts(t *testing.T) {
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "kb-x"}}
	c.Request = httptest.NewRequest("GET", "/", nil) // no tenant in context
	guard := RequireKBAccess(
		KBIDFromParam("id"),
		types.OrgRoleViewer,
		&stubKBLookup{},
		nil,
		nil,
		cfgRBAC(true),
	)
	guard(c)
	require.True(t, c.IsAborted())
}

// ---------- Agent-share fallback ----------

func TestRequireKBAccess_AgentShare_AnyAgent_ViewerOnly(t *testing.T) {
	// No org-share entry; caller has at least one shared agent that can
	// access this KB. Required permission is Viewer, so the agent-share
	// branch activates and grants read access at the source tenant.
	agent := &stubAgentShareForGuard{
		kbsViaSomeAgent: map[string]bool{"kb-shared": true},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent},
	)
	require.False(t, c.IsAborted())
	access, ok := KBAccessFromContext(c)
	require.True(t, ok)
	require.Equal(t, uint64(200), access.EffectiveTenantID)
	require.Equal(t, types.OrgRoleViewer, access.Permission, "agent share grants viewer only")
}

func TestRequireKBAccess_AgentShare_EditorRequired_Aborts(t *testing.T) {
	// Required permission is Editor → agent-share fallback MUST NOT
	// activate. Regression test for the implicit-security-fix in this
	// PR: old TagHandler.effectiveCtxForKB granted any agent-share
	// access to write routes, which leaked tag CRUD.
	agent := &stubAgentShareForGuard{
		kbsViaSomeAgent: map[string]bool{"kb-shared": true},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleEditor,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent},
	)
	require.True(t, c.IsAborted(), "agent share must NOT satisfy Editor requirement")
}

func TestRequireKBAccess_AgentShare_SpecificAgent_ModeAll(t *testing.T) {
	// ?agent_id=A and A has KBSelectionMode=all on the source tenant.
	// Guard should accept regardless of which KB.
	agent := &stubAgentShareForGuard{
		agents: map[string]*types.CustomAgent{
			"agent-A": {
				ID:       "agent-A",
				TenantID: 200,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "all",
				},
			},
		},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent, agentID: "agent-A"},
	)
	require.False(t, c.IsAborted())
	access, _ := KBAccessFromContext(c)
	require.Equal(t, types.OrgRoleViewer, access.Permission)
}

func TestRequireKBAccess_AgentShare_SpecificAgent_ModeSelected_Match(t *testing.T) {
	agent := &stubAgentShareForGuard{
		agents: map[string]*types.CustomAgent{
			"agent-A": {
				ID:       "agent-A",
				TenantID: 200,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "selected",
					KnowledgeBases:  []string{"kb-other", "kb-shared"},
				},
			},
		},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent, agentID: "agent-A"},
	)
	require.False(t, c.IsAborted())
}

func TestRequireKBAccess_AgentShare_SpecificAgent_ModeSelected_Miss(t *testing.T) {
	// ?agent_id=A but A's selected list does NOT include this KB. Even
	// though SOME OTHER shared agent (B) would have granted access via
	// the any-agent fallback, the explicit agent_id pins the resolution
	// to A. This is the divergence the review flagged.
	agent := &stubAgentShareForGuard{
		agents: map[string]*types.CustomAgent{
			"agent-A": {
				ID:       "agent-A",
				TenantID: 200,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "selected",
					KnowledgeBases:  []string{"kb-other"},
				},
			},
		},
		// any-agent fallback would have said yes — but agent_id=A pins us.
		kbsViaSomeAgent: map[string]bool{"kb-shared": true},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent, agentID: "agent-A"},
	)
	require.True(t, c.IsAborted(), "agent_id=A must NOT fall back to any-agent")
}

func TestRequireKBAccess_AgentShare_SpecificAgent_ModeNone(t *testing.T) {
	agent := &stubAgentShareForGuard{
		agents: map[string]*types.CustomAgent{
			"agent-A": {
				ID:       "agent-A",
				TenantID: 200,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "none",
				},
			},
		},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent, agentID: "agent-A"},
	)
	require.True(t, c.IsAborted(), "agent in mode=none must not grant access")
}

func TestRequireKBAccess_AgentShare_SpecificAgent_TenantMismatch(t *testing.T) {
	// Agent belongs to tenant 999 but KB belongs to tenant 200 → reject
	// (this is the kb.TenantID != agent.TenantID guard in the handler;
	// preserves cross-tenant isolation when shares get reshuffled).
	agent := &stubAgentShareForGuard{
		agents: map[string]*types.CustomAgent{
			"agent-A": {
				ID:       "agent-A",
				TenantID: 999,
				Config: types.CustomAgentConfig{
					KBSelectionMode: "all",
				},
			},
		},
	}
	_, c := runGuard(t, 100, "kb-shared",
		types.OrgRoleViewer,
		&types.KnowledgeBase{ID: "kb-shared", TenantID: 200},
		nil,
		guardOpts{agentShare: agent, agentID: "agent-A"},
	)
	require.True(t, c.IsAborted())
}

// ---------- EnableRBAC=false rollout window ----------

func TestRequireKBAccess_Forbidden_FailOpenWhenRBACDisabled(t *testing.T) {
	// Same scenario as PermissionBelowMin (which aborts when enforcing),
	// but with EnableRBAC=false the guard logs and passes through.
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "kb-shared"}}
	req := httptest.NewRequest("GET", "/", nil)
	c.Request = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(100)))

	share := &stubKBShareForGuard{
		permission: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleViewer},
		shared:     map[string]bool{"kb-shared": true},
		source:     map[string]uint64{"kb-shared": 200},
	}
	kbsvc := &stubKBLookup{kbs: map[string]*types.KnowledgeBase{
		"kb-shared": {ID: "kb-shared", TenantID: 200},
	}}

	guard := RequireKBAccess(
		KBIDFromParam("id"),
		types.OrgRoleEditor, // would-deny
		kbsvc, share, nil,
		cfgRBAC(false), // enforcement off
	)
	guard(c)
	require.False(t, c.IsAborted(), "guard must pass through when EnableRBAC is off")
	_ = rec
}

func TestRequireKBAccess_NotFound_FiresEvenWhenRBACDisabled(t *testing.T) {
	// Not-found is not an authorisation event; the client asked for a
	// resource that genuinely isn't there. We surface 404 regardless of
	// the rollout flag (matches the comment in RequireKBAccess).
	gin.SetMode(gin.TestMode)
	rec := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(rec)
	c.Params = gin.Params{{Key: "id", Value: "kb-missing"}}
	req := httptest.NewRequest("GET", "/", nil)
	c.Request = req.WithContext(context.WithValue(req.Context(), types.TenantIDContextKey, uint64(100)))

	guard := RequireKBAccess(
		KBIDFromParam("id"),
		types.OrgRoleViewer,
		&stubKBLookup{kbs: map[string]*types.KnowledgeBase{}},
		nil, nil,
		cfgRBAC(false),
	)
	guard(c)
	require.True(t, c.IsAborted(), "404 still fires with enforcement off")
	_ = rec
}
