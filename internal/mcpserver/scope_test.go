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
	kbs map[string]*types.KnowledgeBase
}

func (s *stubKBService) GetKnowledgeBaseByIDOnly(_ context.Context, id string) (*types.KnowledgeBase, error) {
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

func TestGrantContextEnablesWritesOnlyForOwnedKnowledgeBases(t *testing.T) {
	own := &types.KnowledgeBase{ID: "kb-own", TenantID: 1}
	foreign := &types.KnowledgeBase{ID: "kb-foreign", TenantID: 2}
	srv := newScopeTestServer(own, foreign)
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolAddDocument}}
	ctx := mcpCallContext(1, ep)

	if err := access.RequireKBWrite(ctx, own); err == nil {
		t.Fatal("no grant must exist before grantContext runs")
	}
	granted, err := srv.grantContext(ctx, []*types.KnowledgeBase{own}, types.OrgRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if err := access.RequireKBWrite(granted, own); err != nil {
		t.Fatalf("owned knowledge base must be writable after grant: %v", err)
	}
	if _, err := srv.grantContext(ctx, []*types.KnowledgeBase{foreign}, types.OrgRoleEditor); err == nil {
		t.Fatal("foreign knowledge base must not receive a write grant")
	}

	readOnly := &types.MCPEndpoint{
		ID: "ep2", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolSearchKnowledge},
	}
	roCtx := mcpCallContext(1, readOnly)
	roGranted, err := srv.grantContext(roCtx, []*types.KnowledgeBase{own}, types.OrgRoleEditor)
	if err != nil {
		t.Fatal(err)
	}
	if err := access.RequireKBWrite(roGranted, own); err == nil {
		t.Fatal("an endpoint without ingest tools must lack the ingest capability and be refused")
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
