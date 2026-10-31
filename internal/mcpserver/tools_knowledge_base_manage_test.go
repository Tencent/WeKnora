package mcpserver

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

type manageKBService struct {
	*stubKBService
	called      bool
	name        string
	description string
	config      *types.KnowledgeBaseConfig
}

func (s *manageKBService) UpdateKnowledgeBase(
	_ context.Context, id, name, description string,
	config *types.KnowledgeBaseConfig, _ *types.VLMConfig,
) (*types.KnowledgeBase, error) {
	s.called = true
	s.name, s.description, s.config = name, description, config
	kb := *s.kbs[id]
	kb.Name, kb.Description = name, description
	return &kb, nil
}

func manageRequest(args map[string]any) mcp.CallToolRequest {
	return mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
}

func TestUpdateKnowledgeBaseToolRequiresManagementCapability(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Original", Type: types.KnowledgeBaseTypeDocument}
	stub := &manageKBService{stubKBService: &stubKBService{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}}}
	srv := &Server{kbService: stub}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolAddDocument}}
	result, err := srv.handleUpdateKnowledgeBase(mcpCallContext(1, ep), manageRequest(map[string]any{
		"knowledge_base_id": kb.ID, "name": "Changed",
	}))
	if err != nil || !result.IsError || stub.called {
		t.Fatalf("ingest-only endpoint must not manage KB: result=%+v err=%v called=%v", result, err, stub.called)
	}
}

func TestUpdateKnowledgeBaseToolPreservesOmittedSettings(t *testing.T) {
	kb := &types.KnowledgeBase{
		ID: "kb-1", TenantID: 1, Name: "Original", Description: "keep",
		Type:             types.KnowledgeBaseTypeDocument,
		IndexingStrategy: types.IndexingStrategy{VectorEnabled: true, KeywordEnabled: true},
	}
	stub := &manageKBService{stubKBService: &stubKBService{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}}}
	srv := &Server{kbService: stub}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolUpdateKnowledgeBase}}
	result, err := srv.handleUpdateKnowledgeBase(mcpCallContext(1, ep), manageRequest(map[string]any{
		"knowledge_base_id": kb.ID, "wiki_enabled": true,
	}))
	if err != nil || result.IsError || !stub.called {
		t.Fatalf("update failed: result=%+v err=%v called=%v", result, err, stub.called)
	}
	if stub.name != kb.Name || stub.description != kb.Description || stub.config == nil ||
		stub.config.IndexingStrategy == nil || !stub.config.IndexingStrategy.VectorEnabled ||
		!stub.config.IndexingStrategy.KeywordEnabled || !stub.config.IndexingStrategy.WikiEnabled ||
		stub.config.IndexingStrategy.GraphEnabled {
		t.Fatalf("omitted settings changed: name=%q description=%q config=%+v", stub.name, stub.description, stub.config)
	}
}

func TestUpdateKnowledgeBaseToolNameOnlyDoesNotReplaceConfig(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1, Name: "Original", Description: "keep", Type: types.KnowledgeBaseTypeDocument}
	stub := &manageKBService{stubKBService: &stubKBService{kbs: map[string]*types.KnowledgeBase{kb.ID: kb}}}
	srv := &Server{kbService: stub}
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolUpdateKnowledgeBase}}
	result, err := srv.handleUpdateKnowledgeBase(mcpCallContext(1, ep), manageRequest(map[string]any{
		"knowledge_base_id": kb.ID, "name": "Renamed",
	}))
	if err != nil || result.IsError || !stub.called || stub.name != "Renamed" ||
		stub.description != "keep" || stub.config != nil {
		t.Fatalf("name-only update replaced config: result=%+v err=%v config=%+v", result, err, stub.config)
	}
}
