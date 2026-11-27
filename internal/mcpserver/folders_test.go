package mcpserver

import (
	"context"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/mark3labs/mcp-go/mcp"
	"github.com/stretchr/testify/require"
)

func TestRequestFolder(t *testing.T) {
	for _, tc := range []struct {
		value   any
		want    string
		invalid bool
	}{
		{"", "", false},
		{"ERP/技术设计/库存", "ERP/技术设计/库存", false},
		{"../private", "", true},
		{"/root/private", "", true},
		{"a/../b", "", true},
		{"C:\\private", "", true},
		{"a\x00b", "", true},
		{true, "", true},
	} {
		req := mcp.CallToolRequest{}
		req.Params.Arguments = map[string]any{"folder_path": tc.value}
		got, err := requestFolder(req)
		if tc.invalid {
			require.Error(t, err)
		} else {
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		}
	}
}

type folderListService struct {
	interfaces.KnowledgeService
	filter types.KnowledgeListFilter
}

func (s *folderListService) ListPagedKnowledgeByKnowledgeBaseID(
	_ context.Context,
	_ string,
	_ *types.Pagination,
	f types.KnowledgeListFilter,
) (*types.PageResult, error) {
	s.filter = f
	return &types.PageResult{Data: []*types.Knowledge{}, Total: 0}, nil
}

func TestListFolderPresence(t *testing.T) {
	for _, tc := range []struct {
		args  map[string]any
		scope types.KnowledgeFolderScope
	}{
		{map[string]any{}, types.FolderScopeAny},
		{map[string]any{"folder_path": ""}, types.FolderScopeExact},
		{map[string]any{"folder_path": "ERP", "folder_recursive": true}, types.FolderScopeSubtree},
	} {
		srv := newScopeTestServer(&types.KnowledgeBase{ID: "kb", TenantID: 1})
		service := &folderListService{}
		srv.knowledgeService = service
		ep := &types.MCPEndpoint{
			ID:               "ep",
			TenantID:         1,
			KnowledgeBaseIDs: types.StringArray{"kb"},
			Tools:            types.StringArray{types.MCPEndpointToolListDocuments},
		}
		tc.args["knowledge_base_id"] = "kb"
		req := mcp.CallToolRequest{}
		req.Params.Arguments = tc.args
		result, err := srv.handleListDocuments(mcpCallContext(1, ep), req)
		require.NoError(t, err)
		require.False(t, result.IsError)
		require.Equal(t, tc.scope, service.filter.FolderScope)
	}
}

type folderCreateService struct {
	interfaces.KnowledgeService
	folder string
	mode   string
	calls  int
}

func (s *folderCreateService) CreateKnowledgeFromManual(
	_ context.Context,
	kb string,
	_ *types.ManualKnowledgePayload,
	_ string,
	opts ...types.KnowledgeCreationOptions,
) (*types.Knowledge, error) {
	s.calls++
	s.mode = "manual"
	s.folder = opts[0].FolderPath
	return &types.Knowledge{ID: "doc", KnowledgeBaseID: kb, FolderPath: s.folder}, nil
}

func (s *folderCreateService) CreateKnowledgeFromURL(
	_ context.Context,
	kb, _, _, _ string,
	_ *bool,
	_ string,
	_ []string,
	_ string,
	_ *types.KnowledgeProcessOverrides,
	opts ...types.KnowledgeCreationOptions,
) (*types.Knowledge, error) {
	s.calls++
	s.mode = "url"
	s.folder = opts[0].FolderPath
	return &types.Knowledge{ID: "doc", KnowledgeBaseID: kb, FolderPath: s.folder}, nil
}

func TestAddDocumentFolderOptions(t *testing.T) {
	for _, url := range []string{"", "https://example.com/page", "https://example.com/report.pdf"} {
		for _, folder := range []string{"", "ERP/技术设计", "../private"} {
			srv := newScopeTestServer(&types.KnowledgeBase{ID: "kb", TenantID: 1})
			service := &folderCreateService{}
			srv.knowledgeService = service
			ep := &types.MCPEndpoint{
				ID:               "ep",
				TenantID:         1,
				KnowledgeBaseIDs: types.StringArray{"kb"},
				Tools:            types.StringArray{types.MCPEndpointToolAddDocument},
			}
			args := map[string]any{"knowledge_base_id": "kb", "title": "title", "content": "body", "url": url}
			if folder != "" {
				args["folder_path"] = folder
			}
			req := mcp.CallToolRequest{}
			req.Params.Arguments = args
			result, err := srv.handleAddDocument(mcpCallContext(1, ep), req)
			require.NoError(t, err)
			if folder == "../private" {
				require.True(t, result.IsError)
				require.Zero(t, service.calls)
				continue
			}
			require.False(t, result.IsError)
			require.Equal(t, folder, service.folder)
			require.Equal(t, 1, service.calls)
		}
	}
}
