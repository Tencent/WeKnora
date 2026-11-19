package mcpserver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/mark3labs/mcp-go/mcp"
)

// folderMoveService records MoveKnowledgeToFolder calls over the stub
// knowledge service so the update_document folder test can assert the move.
type folderMoveService struct {
	stubKnowledgeService
	mu    sync.Mutex
	moved []folderMoveCall
}

type folderMoveCall struct {
	kbID string
	ids  []string
	path string
}

func (s *folderMoveService) MoveKnowledgeToFolder(
	_ context.Context, kbID string, ids []string, folderPath string,
) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.moved = append(s.moved, folderMoveCall{kbID: kbID, ids: ids, path: folderPath})
	return int64(len(ids)), nil
}

// UpdateManualKnowledge returns the stored row so the handler's content
// update succeeds against the stub without a full service implementation.
func (s *folderMoveService) UpdateManualKnowledge(
	_ context.Context, id string, _ *types.ManualKnowledgePayload,
) (*types.Knowledge, error) {
	k, ok := s.docs[id]
	if !ok {
		return nil, errors.New("not found")
	}
	return k, nil
}

// TestUpdateDocumentMovesToFolder pins the #5610 remainder: update_document
// accepts an optional folder_path and re-files the document when present,
// while omitting it leaves the filing untouched.
func TestUpdateDocumentMovesToFolder(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	doc := &types.Knowledge{
		ID: "doc-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Type: types.KnowledgeTypeManual, FileName: "doc-1.md",
	}
	svc := &folderMoveService{stubKnowledgeService: stubKnowledgeService{
		docs: map[string]*types.Knowledge{"doc-1": doc},
	}}
	srv := newScopeTestServer(kb)
	srv.knowledgeService = svc
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{
		types.MCPEndpointToolAddDocument,
	}}
	ctx := mcpCallContext(1, ep)

	req := mcp.CallToolRequest{}
	req.Params.Name = "update_document"
	req.Params.Arguments = map[string]any{
		"knowledge_id": "doc-1",
		"content":      "revised body",
		"folder_path":  "分类/子分类",
	}
	result, err := srv.handleUpdateDocument(ctx, req)
	if err != nil {
		t.Fatalf("update_document: %v", err)
	}
	if result == nil || result.IsError {
		t.Fatalf("update_document returned error result: %v", result)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.moved) != 1 {
		t.Fatalf("folder move not called once, got %d", len(svc.moved))
	}
	call := svc.moved[0]
	if call.kbID != "kb-1" || len(call.ids) != 1 || call.ids[0] != "doc-1" {
		t.Fatalf("move targeted wrong rows: %+v", call)
	}
	if call.path != "分类/子分类" {
		t.Fatalf("move path = %q", call.path)
	}
}

// TestUpdateDocumentWithoutFolderLeavesFilingAlone pins that an omitted
// folder_path never triggers a move call.
func TestUpdateDocumentWithoutFolderLeavesFilingAlone(t *testing.T) {
	kb := &types.KnowledgeBase{ID: "kb-1", TenantID: 1}
	doc := &types.Knowledge{
		ID: "doc-1", TenantID: 1, KnowledgeBaseID: "kb-1",
		Type: types.KnowledgeTypeManual, FileName: "doc-1.md",
	}
	svc := &folderMoveService{stubKnowledgeService: stubKnowledgeService{
		docs: map[string]*types.Knowledge{"doc-1": doc},
	}}
	srv := newScopeTestServer(kb)
	srv.knowledgeService = svc
	ep := &types.MCPEndpoint{ID: "ep", TenantID: 1, Tools: types.StringArray{types.MCPEndpointToolAddDocument}}
	ctx := mcpCallContext(1, ep)
	req := mcp.CallToolRequest{}
	req.Params.Name = "update_document"
	req.Params.Arguments = map[string]any{
		"knowledge_id": "doc-1",
		"content":      "revised body",
	}
	if _, err := srv.handleUpdateDocument(ctx, req); err != nil {
		t.Fatalf("update_document: %v", err)
	}
	svc.mu.Lock()
	defer svc.mu.Unlock()
	if len(svc.moved) != 0 {
		t.Fatalf("unexpected move calls: %+v", svc.moved)
	}
}
