package mcpserver

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/application/access"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
)

type stubKBService struct {
	interfaces.KnowledgeBaseService
	kbs     map[string]*types.KnowledgeBase
	lookups int
}

func (s *stubKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
	s.lookups++
	kb, ok := s.kbs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return kb, nil
}

func (s *stubKBService) ListKnowledgeBases(ctx context.Context) ([]*types.KnowledgeBase, error) {
	tenantID := types.MustTenantIDFromContext(ctx)
	out := []*types.KnowledgeBase{}
	for _, kb := range s.kbs {
		if kb.TenantID == tenantID {
			out = append(out, kb)
		}
	}
	return out, nil
}

type stubAgentService struct {
	interfaces.CustomAgentService
	agents map[string]*types.CustomAgent
}

func (s *stubAgentService) GetAgentByID(_ context.Context, id string) (*types.CustomAgent, error) {
	agent, ok := s.agents[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return agent, nil
}

func mcpCallContext(tenantID uint64, ep *types.MCPEndpoint) context.Context {
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, tenantID)
	ctx = context.WithValue(ctx, types.MCPEndpointContextKey, ep)
	ctx = types.WithTenantAPIKeyScope(ctx, types.MCPEndpointScope(ep))
	return types.WithCaller(ctx, types.Caller{TenantID: tenantID, UserID: "mcp-" + ep.ID, Role: types.TenantRoleViewer})
}

func newScopeTestServer(kbs ...*types.KnowledgeBase) *Server {
	stub := &stubKBService{kbs: map[string]*types.KnowledgeBase{}}
	for _, kb := range kbs {
		stub.kbs[kb.ID] = kb
	}
	return &Server{kbService: stub}
}

func TestAllowedKnowledgeBasesDropsForeignIDs(t *testing.T) {
	srv := newScopeTestServer(
		&types.KnowledgeBase{ID: "kb-own", TenantID: 1, Name: "Own"},
		&types.KnowledgeBase{ID: "kb-foreign", TenantID: 2, Name: "Foreign"},
	)
	ep := &types.MCPEndpoint{
		ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-own", "kb-foreign", "kb-gone"},
	}
	kbs, err := srv.allowedKnowledgeBases(mcpCallContext(1, ep), ep)
	if err != nil {
		t.Fatal(err)
	}
	if len(kbs) != 1 || kbs[0].ID != "kb-own" {
		t.Fatalf("expected only the owned knowledge base, got %v", knowledgeBaseIDs(kbs))
	}
}

func TestSelectKnowledgeBasesMatchesIDOrName(t *testing.T) {
	srv := newScopeTestServer(
		&types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Product Docs"},
		&types.KnowledgeBase{ID: "kb-2", TenantID: 1, Name: "Support"},
		&types.KnowledgeBase{ID: "kb-3", TenantID: 2, Name: "Other tenant"},
	)
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1}
	ctx := mcpCallContext(1, ep)

	all, err := srv.selectKnowledgeBases(ctx, ep, nil)
	if err != nil || len(all) != 2 {
		t.Fatalf("unrestricted endpoint must see its workspace only: %v %v", knowledgeBaseIDs(all), err)
	}
	picked, err := srv.selectKnowledgeBases(ctx, ep, []string{"product docs", "kb-2", "kb-2"})
	if err != nil {
		t.Fatal(err)
	}
	if got := knowledgeBaseIDs(picked); len(got) != 2 || got[0] != "kb-1" || got[1] != "kb-2" {
		t.Fatalf("name/id selection = %v", got)
	}
	_, err = srv.selectKnowledgeBases(ctx, ep, []string{"kb-3"})
	if err == nil || !strings.Contains(err.Error(), "outside") {
		t.Fatalf("foreign knowledge base must be rejected, got %v", err)
	}
}

type stubKBShareService struct {
	interfaces.KBShareService
	shared map[string]types.OrgMemberRole // kb id -> permission granted to any caller
}

func (s *stubKBShareService) CheckTenantKBPermission(
	_ context.Context, kbID string, _ uint64, _ types.TenantRole,
) (types.OrgMemberRole, bool, error) {
	perm, ok := s.shared[kbID]
	return perm, ok, nil
}

type stubTenantService struct {
	interfaces.TenantService
	tenants map[uint64]*types.Tenant
}

func (s *stubTenantService) GetTenantByID(_ context.Context, id uint64) (*types.Tenant, error) {
	t, ok := s.tenants[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return t, nil
}

type stubKnowledgeService struct {
	interfaces.KnowledgeService
	docs map[string]*types.Knowledge
}

func (s *stubKnowledgeService) GetKnowledgeByIDOnly(_ context.Context, id string) (*types.Knowledge, error) {
	k, ok := s.docs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return k, nil
}

func TestScopedKBContextEnablesWritesOnlyForAuthorizedKnowledgeBases(t *testing.T) {
	own := &types.KnowledgeBase{ID: "kb-own", TenantID: 1}
	foreign := &types.KnowledgeBase{ID: "kb-foreign", TenantID: 2}
	srv := newScopeTestServer(own, foreign)
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolAddDocument}}
	ctx := mcpCallContext(1, ep)

	if err := access.RequireKBWrite(ctx, own); err == nil {
		t.Fatal("no grant must exist before scopedKBContext runs")
	}
	scoped, err := srv.scopedKBContext(ctx, own, types.OrgRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if err := access.RequireKBWrite(scoped, own); err != nil {
		t.Fatalf("owned knowledge base must be writable after grant: %v", err)
	}
	if got := types.MustTenantIDFromContext(scoped); got != 1 {
		t.Fatalf("execution tenant = %d, want owner 1", got)
	}
	if _, err := srv.scopedKBContext(ctx, foreign, types.OrgRoleEditor); err == nil {
		t.Fatal("foreign knowledge base must not receive a write grant")
	}

	readOnly := &types.MCPEndpoint{
		ID: "ep2", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolSearchKnowledge},
	}
	roScoped, err := srv.scopedKBContext(mcpCallContext(1, readOnly), own, types.OrgRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if err := access.RequireKBWrite(roScoped, own); err == nil {
		t.Fatal("an endpoint without ingest tools must lack the ingest capability and be refused")
	}
}

func TestSharedKnowledgeBaseRunsUnderOwnerTenant(t *testing.T) {
	shared := &types.KnowledgeBase{ID: "kb-shared", TenantID: 2, Name: "Shared"}
	srv := newScopeTestServer(shared)
	srv.kbShareService = &stubKBShareService{shared: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleEditor}}
	srv.tenantService = &stubTenantService{tenants: map[uint64]*types.Tenant{2: {ID: 2, Name: "Owner"}}}
	srv.knowledgeService = &stubKnowledgeService{docs: map[string]*types.Knowledge{
		"doc-shared": {ID: "doc-shared", TenantID: 2, KnowledgeBaseID: "kb-shared"},
		"doc-spoof":  {ID: "doc-spoof", TenantID: 1, KnowledgeBaseID: "kb-shared"},
	}}
	ep := &types.MCPEndpoint{
		ID: "ep", TenantID: 1, KnowledgeBaseIDs: types.StringArray{"kb-shared"},
		Tools: types.StringArray{types.MCPEndpointToolUpdateDocument},
	}
	ctx := context.WithValue(mcpCallContext(1, ep), types.TenantInfoContextKey, &types.Tenant{ID: 1, Name: "Caller"})

	// The shared knowledge base is visible through the organization share.
	kbs, err := srv.allowedKnowledgeBases(ctx, ep)
	if err != nil || len(kbs) != 1 {
		t.Fatalf("shared knowledge base must be in scope: %v %v", knowledgeBaseIDs(kbs), err)
	}

	// Its document resolves even though it lives under tenant 2 ...
	k, kb, err := srv.knowledgeInScope(ctx, ep, "doc-shared")
	if err != nil || k.ID != "doc-shared" || kb.ID != "kb-shared" {
		t.Fatalf("shared document must be in scope: %v %v %v", k, kb, err)
	}
	// ... but a document whose tenant does not match its knowledge base is not.
	if _, _, err := srv.knowledgeInScope(ctx, ep, "doc-spoof"); err == nil {
		t.Fatal("document with mismatched tenant must be rejected")
	}

	// Writes run under the owner tenant with an editor grant and owner TenantInfo.
	scoped, err := srv.scopedKBContext(ctx, kb, types.OrgRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if got := types.MustTenantIDFromContext(scoped); got != 2 {
		t.Fatalf("execution tenant = %d, want owner 2", got)
	}
	if info, ok := types.TenantInfoFromContext(scoped); !ok || info.ID != 2 {
		t.Fatalf("tenant info must be swapped to the owner, got %+v", info)
	}
	if err := access.RequireKBWrite(scoped, kb); err != nil {
		t.Fatalf("editor share must allow writes: %v", err)
	}
	if caller := types.CallerFromContext(scoped); caller.TenantID != 1 {
		t.Fatalf("caller must stay the endpoint tenant, got %+v", caller)
	}

	// A viewer share must not mint a write grant.
	srv.kbShareService = &stubKBShareService{shared: map[string]types.OrgMemberRole{"kb-shared": types.OrgRoleViewer}}
	if _, err := srv.scopedKBContext(ctx, kb, types.OrgRoleEditor); err == nil {
		t.Fatal("viewer share must not allow writes")
	}
	if _, err := srv.scopedKBContext(ctx, kb, types.OrgRoleViewer); err != nil {
		t.Fatalf("viewer share must allow reads: %v", err)
	}
}

func TestResolveAskAgentUsesEndpointAgentOnly(t *testing.T) {
	agents := &stubAgentService{agents: map[string]*types.CustomAgent{
		types.BuiltinQuickAnswerID: {ID: types.BuiltinQuickAnswerID, TenantID: 1, IsBuiltin: true},
		types.BuiltinWikiFixerID:   {ID: types.BuiltinWikiFixerID, TenantID: 1, IsBuiltin: true},
		"agent-own":                {ID: "agent-own", TenantID: 1},
		"agent-foreign":            {ID: "agent-foreign", TenantID: 2},
	}}
	srv := &Server{agentService: agents}

	got, err := srv.resolveAskAgent(context.Background(), &types.MCPEndpoint{ID: "ep", TenantID: 1})
	if err != nil || got.ID != types.BuiltinQuickAnswerID {
		t.Fatalf("empty default must fall back to quick answer: %v %v", got, err)
	}
	got, err = srv.resolveAskAgent(context.Background(),
		&types.MCPEndpoint{ID: "ep", TenantID: 1, DefaultAgentID: "agent-own"})
	if err != nil || got.ID != "agent-own" {
		t.Fatalf("configured tenant agent must be used: %v %v", got, err)
	}
	for _, bad := range []string{types.BuiltinWikiFixerID, "agent-foreign", "agent-missing"} {
		_, err := srv.resolveAskAgent(context.Background(),
			&types.MCPEndpoint{ID: "ep", TenantID: 1, DefaultAgentID: bad})
		if err == nil {
			t.Fatalf("agent %q must be refused", bad)
		}
	}
}

func TestAskToolHasNoAgentParameter(t *testing.T) {
	tool := askTool()
	if _, ok := tool.InputSchema.Properties["agent_id"]; ok {
		t.Fatal("ask must not let clients pick an agent")
	}
	if tool.Annotations.ReadOnlyHint != nil && *tool.Annotations.ReadOnlyHint {
		t.Fatal("ask must not advertise itself as read-only")
	}
	add := addDocumentTool()
	if add.Annotations.DestructiveHint == nil || !*add.Annotations.DestructiveHint {
		t.Fatal("add_document must advertise a mutation")
	}
}
