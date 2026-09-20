package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	apperrors "github.com/Tencent/WeKnora/internal/errors"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubMCPEndpointRepo struct {
	interfaces.MCPEndpointRepository
	rows map[string]*types.MCPEndpoint
}

func newStubMCPEndpointRepo() *stubMCPEndpointRepo {
	return &stubMCPEndpointRepo{rows: map[string]*types.MCPEndpoint{}}
}

func (r *stubMCPEndpointRepo) Create(_ context.Context, ep *types.MCPEndpoint) error {
	if ep.ID == "" {
		ep.ID = "ep-" + ep.Name
	}
	cp := *ep
	r.rows[ep.ID] = &cp
	return nil
}

func (r *stubMCPEndpointRepo) GetByID(_ context.Context, id string) (*types.MCPEndpoint, error) {
	ep, ok := r.rows[id]
	if !ok {
		return nil, nil
	}
	cp := *ep
	return &cp, nil
}

func (r *stubMCPEndpointRepo) GetByTokenHash(_ context.Context, hash string) (*types.MCPEndpoint, error) {
	for _, ep := range r.rows {
		if ep.TokenHash == hash {
			cp := *ep
			return &cp, nil
		}
	}
	return nil, nil
}

func (r *stubMCPEndpointRepo) Update(_ context.Context, ep *types.MCPEndpoint) error {
	cp := *ep
	r.rows[ep.ID] = &cp
	return nil
}

func (r *stubMCPEndpointRepo) Delete(_ context.Context, tenantID uint64, id string) error {
	if ep, ok := r.rows[id]; ok && ep.TenantID == tenantID {
		delete(r.rows, id)
	}
	return nil
}

func newMCPEndpointServiceForTest(repo *stubMCPEndpointRepo, agent *types.CustomAgent) *mcpEndpointService {
	return &mcpEndpointService{
		repo:         repo,
		agentService: &stubAgentForEmbed{agent: agent},
	}
}

func TestMCPEndpointCreateIssuesHashedToken(t *testing.T) {
	repo := newStubMCPEndpointRepo()
	svc := newMCPEndpointServiceForTest(repo, nil)

	ep, token, err := svc.Create(context.Background(), 7, &types.MCPEndpoint{
		Name:    "  Docs bot ",
		Enabled: true,
		Tools:   types.StringArray{"bogus", types.MCPEndpointToolAsk, types.MCPEndpointToolSearchKnowledge},
	})
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if !strings.HasPrefix(token, types.MCPEndpointTokenPrefix) {
		t.Fatalf("token %q lacks prefix", token)
	}
	if ep.TokenHash != HashMCPEndpointToken(token) || ep.TokenHash == token {
		t.Fatal("token must be stored hashed")
	}
	if ep.Name != "Docs bot" || ep.TenantID != 7 ||
		ep.RateLimitPerMinute != types.DefaultMCPEndpointRateLimitPerMinute {
		t.Fatalf("unexpected row %+v", ep)
	}
	toolNames := []string(ep.Tools)
	if len(toolNames) != 2 || toolNames[0] != types.MCPEndpointToolSearchKnowledge ||
		toolNames[1] != types.MCPEndpointToolAsk {
		t.Fatalf("tools not normalized: %v", toolNames)
	}
	if !strings.HasPrefix(token, ep.TokenHint) {
		t.Fatalf("hint %q is not a prefix of token", ep.TokenHint)
	}

	got, err := svc.Authenticate(context.Background(), ep.ID, token)
	if err != nil || got == nil || got.ID != ep.ID {
		t.Fatalf("authenticate: %v %v", got, err)
	}
	_, err = svc.Authenticate(context.Background(), "other-endpoint", token)
	if !errors.Is(err, ErrMCPEndpointTokenInvalid) {
		t.Fatalf("token must be bound to its endpoint id, got %v", err)
	}
	_, err = svc.Authenticate(context.Background(), ep.ID, "sk-not-an-endpoint-token")
	if !errors.Is(err, ErrMCPEndpointTokenInvalid) {
		t.Fatalf("foreign token prefix must be rejected, got %v", err)
	}
}

func TestMCPEndpointCreateValidation(t *testing.T) {
	svc := newMCPEndpointServiceForTest(newStubMCPEndpointRepo(), nil)
	_, _, err := svc.Create(context.Background(), 1, &types.MCPEndpoint{Name: "", Tools: types.StringArray{"ask"}})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrBadRequest {
		t.Fatalf("empty name must be a bad request, got %v", err)
	}
	_, _, err = svc.Create(context.Background(), 1, &types.MCPEndpoint{Name: "x", Tools: types.StringArray{"bogus"}})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrBadRequest {
		t.Fatalf("no valid tools must be a bad request, got %v", err)
	}
	_, _, err = svc.Create(context.Background(), 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, RateLimitPerMinute: -5,
	})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrBadRequest {
		t.Fatalf("negative rate must be a bad request, got %v", err)
	}
}

func TestMCPEndpointDefaultAgentMustBelongToTenant(t *testing.T) {
	foreign := &types.CustomAgent{ID: "agent-1", TenantID: 99}
	svc := newMCPEndpointServiceForTest(newStubMCPEndpointRepo(), foreign)
	_, _, err := svc.Create(context.Background(), 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, DefaultAgentID: "agent-1",
	})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrNotFound {
		t.Fatalf("foreign agent must be rejected as not found, got %v", err)
	}

	builtin := &types.CustomAgent{ID: types.BuiltinQuickAnswerID, TenantID: 99, IsBuiltin: true}
	svc = newMCPEndpointServiceForTest(newStubMCPEndpointRepo(), builtin)
	if _, _, err := svc.Create(context.Background(), 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, DefaultAgentID: types.BuiltinQuickAnswerID,
	}); err != nil {
		t.Fatalf("builtin agent must be accepted: %v", err)
	}
}

func TestMCPEndpointUpdateRotateDeleteAreTenantScoped(t *testing.T) {
	repo := newStubMCPEndpointRepo()
	svc := newMCPEndpointServiceForTest(repo, nil)
	ep, token, err := svc.Create(context.Background(), 1, &types.MCPEndpoint{
		Name: "x", Enabled: true, Tools: types.StringArray{"ask"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Update(context.Background(), 2, ep.ID, interfaces.MCPEndpointUpdate{})
	if !errors.Is(err, ErrMCPEndpointNotFound) {
		t.Fatalf("other tenant must not see the endpoint, got %v", err)
	}
	disabled := false
	name := "renamed"
	tools := []string{types.MCPEndpointToolSearchKnowledge, "bogus"}
	updated, err := svc.Update(context.Background(), 1, ep.ID, interfaces.MCPEndpointUpdate{
		Name: &name, Enabled: &disabled, Tools: &tools,
	})
	if err != nil {
		t.Fatalf("update: %v", err)
	}
	if updated.Name != "renamed" || updated.Enabled || len(updated.Tools) != 1 {
		t.Fatalf("update not applied: %+v", updated)
	}
	if _, err := svc.Authenticate(context.Background(), ep.ID, token); !errors.Is(err, ErrMCPEndpointDisabled) {
		t.Fatalf("disabled endpoint must reject calls, got %v", err)
	}

	empty := []string{}
	if _, err := svc.Update(context.Background(), 1, ep.ID, interfaces.MCPEndpointUpdate{Tools: &empty}); err == nil {
		t.Fatal("clearing every tool must be rejected")
	}

	rotated, newToken, err := svc.RotateToken(context.Background(), 1, ep.ID)
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if newToken == token || rotated.TokenHash == ep.TokenHash {
		t.Fatal("rotate must issue a fresh token")
	}
	enabled := true
	_, err = svc.Update(context.Background(), 1, ep.ID, interfaces.MCPEndpointUpdate{Enabled: &enabled})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Authenticate(context.Background(), ep.ID, token); !errors.Is(err, ErrMCPEndpointTokenInvalid) {
		t.Fatalf("old token must stop working after rotation, got %v", err)
	}
	if got, err := svc.Authenticate(context.Background(), ep.ID, newToken); err != nil || got.ID != ep.ID {
		t.Fatalf("new token must work: %v %v", got, err)
	}

	if err := svc.Delete(context.Background(), 2, ep.ID); !errors.Is(err, ErrMCPEndpointNotFound) {
		t.Fatalf("delete by other tenant must be not found, got %v", err)
	}
	if err := svc.Delete(context.Background(), 1, ep.ID); err != nil {
		t.Fatalf("delete: %v", err)
	}
	if _, err := svc.Get(context.Background(), 1, ep.ID); !errors.Is(err, ErrMCPEndpointNotFound) {
		t.Fatalf("deleted endpoint must be gone, got %v", err)
	}
}

type stubKBForMCPEndpoint struct {
	interfaces.KnowledgeBaseService
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBForMCPEndpoint) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	kb, ok := s.kbs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return kb, nil
}

func TestMCPEndpointKnowledgeBaseScopeIsTenantIsolated(t *testing.T) {
	svc := &mcpEndpointService{
		repo: newStubMCPEndpointRepo(),
		kbService: &stubKBForMCPEndpoint{kbs: map[string]*types.KnowledgeBase{
			"kb-own":     {ID: "kb-own", TenantID: 1, Name: "Own"},
			"kb-foreign": {ID: "kb-foreign", TenantID: 2, Name: "Foreign"},
		}},
	}
	ctx := types.WithCaller(context.Background(), types.Caller{TenantID: 1, UserID: "u1", Role: types.TenantRoleAdmin})

	if _, _, err := svc.Create(ctx, 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, KnowledgeBaseIDs: types.StringArray{"kb-own"},
	}); err != nil {
		t.Fatalf("own knowledge base must be accepted: %v", err)
	}

	_, _, err := svc.Create(ctx, 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, KnowledgeBaseIDs: types.StringArray{"kb-foreign"},
	})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrNotFound {
		t.Fatalf("another workspace's knowledge base must be rejected, got %v", err)
	}

	_, _, err = svc.Create(ctx, 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, KnowledgeBaseIDs: types.StringArray{"kb-missing"},
	})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrNotFound {
		t.Fatalf("unknown knowledge base must be rejected, got %v", err)
	}
}

func TestMCPEndpointRejectsInternalBuiltinAgent(t *testing.T) {
	hidden := &types.CustomAgent{ID: types.BuiltinWikiFixerID, TenantID: 1, IsBuiltin: true}
	svc := newMCPEndpointServiceForTest(newStubMCPEndpointRepo(), hidden)
	_, _, err := svc.Create(context.Background(), 1, &types.MCPEndpoint{
		Name: "x", Tools: types.StringArray{"ask"}, DefaultAgentID: types.BuiltinWikiFixerID,
	})
	if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrNotFound {
		t.Fatalf("internal builtin agent must be rejected, got %v", err)
	}
}

// A scoped API key must not mint an endpoint token with more authority than
// it holds: neither a wider KB scope nor tools needing capabilities it lacks.
func TestMCPEndpointScopedKeyCannotExceedItsOwnScope(t *testing.T) {
	requireForbidden := func(t *testing.T, err error) {
		t.Helper()
		if appErr, ok := apperrors.IsAppError(err); !ok || appErr.Code != apperrors.ErrForbidden {
			t.Fatalf("want forbidden, got %v", err)
		}
	}
	keyCtx := func(kbIDs []string, capabilities ...types.APIKeyCapability) context.Context {
		caps := types.StringArray{string(types.APIKeyCapabilityManageChannels)}
		for _, c := range capabilities {
			caps = append(caps, string(c))
		}
		return types.WithTenantAPIKeyScope(context.Background(), types.TenantAPIKeyScope{
			KnowledgeBaseIDs: types.StringArray(kbIDs), Capabilities: caps,
		})
	}
	search := types.StringArray{types.MCPEndpointToolSearchKnowledge}
	ingest := types.StringArray{types.MCPEndpointToolSearchKnowledge, types.MCPEndpointToolDeleteDocument}

	svc := newMCPEndpointServiceForTest(newStubMCPEndpointRepo(), nil)
	// A KB-restricted key cannot create a workspace-wide endpoint.
	_, _, err := svc.Create(keyCtx([]string{"kb-a"}, types.APIKeyCapabilityRetrieve), 7,
		&types.MCPEndpoint{Name: "wide", Tools: search})
	requireForbidden(t, err)
	// Nor tools needing a capability the key lacks.
	_, _, err = svc.Create(keyCtx(nil, types.APIKeyCapabilityRetrieve), 7,
		&types.MCPEndpoint{Name: "writer", Tools: ingest})
	requireForbidden(t, err)
	// Within its own scope it may.
	ep, _, err := svc.Create(keyCtx(nil, types.APIKeyCapabilityRetrieve), 7,
		&types.MCPEndpoint{Name: "reader", Tools: search})
	if err != nil {
		t.Fatalf("create within scope: %v", err)
	}

	// Widening an endpoint, or taking over one created by an admin, is checked
	// against the endpoint the key would end up holding.
	ingestTools := []string(ingest)
	_, err = svc.Update(keyCtx(nil, types.APIKeyCapabilityRetrieve), 7, ep.ID,
		interfaces.MCPEndpointUpdate{Tools: &ingestTools})
	requireForbidden(t, err)
	adminEP, _, err := svc.Create(context.Background(), 7, &types.MCPEndpoint{Name: "admin", Tools: ingest})
	if err != nil {
		t.Fatalf("admin create: %v", err)
	}
	_, _, err = svc.RotateToken(keyCtx(nil, types.APIKeyCapabilityRetrieve), 7, adminEP.ID)
	requireForbidden(t, err)
}
