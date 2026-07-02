package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

type fakeDingTalk struct {
	server *httptest.Server
	mux    *http.ServeMux
}

func newFakeDingTalk() *fakeDingTalk {
	f := &fakeDingTalk{mux: http.NewServeMux()}
	f.server = httptest.NewServer(f.mux)

	// Mock accessToken endpoint
	f.mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "mock_access_token_123",
			"expireIn":    7200,
		})
	})

	// Mock user detail endpoint to get unionid
	f.mux.HandleFunc("/topapi/v2/user/get", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"errcode": 0,
			"errmsg":  "ok",
			"result": map[string]interface{}{
				"unionid": "mock_union_id_789",
			},
		})
	})

	// Mock workspace detail endpoint
	f.mux.HandleFunc("/v2.0/wiki/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workspace": map[string]interface{}{
				"workspaceId": "test_workspace_id",
				"name":        "Test Workspace",
				"description": "Test Description",
				"url":         "https://example.com/workspace",
				"rootNodeId":  "test_root_node_id",
				"createTime":  "2026-06-30T15:14Z",
				"updateTime":  "2026-06-30T15:14Z",
			},
		})
	})

	return f
}

func (f *fakeDingTalk) Close() { f.server.Close() }

func (f *fakeDingTalk) cfg() *Config {
	return &Config{
		ClientID:     "test_client_id",
		ClientSecret: "test_client_secret",
		UserID:       "test_user_id",
	}
}

func TestClient_Ping_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	c := newClient(f.cfg())
	c.baseURL = f.server.URL

	if err := c.Ping(context.Background()); err != nil {
		t.Fatalf("Ping error: %v", err)
	}
}

func TestClient_ListWorkspaces_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"workspaces": []map[string]interface{}{
				{
					"workspaceId": "ws_123",
					"name":        "Test Workspace",
					"description": "Test Desc",
					"url":         "https://example.com/ws_123",
				},
			},
		})
	})

	c := newClient(f.cfg())
	c.baseURL = f.server.URL

	workspaces, err := c.ListWorkspaces(context.Background())
	if err != nil {
		t.Fatalf("ListWorkspaces error: %v", err)
	}

	if len(workspaces) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(workspaces))
	}

	if workspaces[0].WorkspaceID != "ws_123" || workspaces[0].Name != "Test Workspace" {
		t.Errorf("unexpected workspace: %+v", workspaces[0])
	}
}

func TestClient_ListNodes_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": []map[string]interface{}{
				{
					"nodeId":     "node_456",
					"parentId":   "",
					"name":       "Test Doc",
					"type":       "file",
					"url":        "https://example.com/doc_456",
					"dentryUuid": "uuid_789",
				},
			},
		})
	})

	c := newClient(f.cfg())
	c.baseURL = f.server.URL

	nodes, err := c.ListNodes(context.Background(), "ws_123", "")
	if err != nil {
		t.Fatalf("ListNodes error: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("expected 1 node, got %d", len(nodes))
	}

	if nodes[0].NodeID != "node_456" || nodes[0].Name != "Test Doc" {
		t.Errorf("unexpected node: %+v", nodes[0])
	}
}

func TestClient_GetDocumentContent_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	registerMCPOnlineDoc(f, "node_123", "# Hello via MCP")

	c := newClient(f.cfg())
	c.baseURL = f.server.URL

	content, err := c.GetDocumentContent(context.Background(), "node_123")
	if err != nil {
		t.Fatalf("GetDocumentContent error: %v", err)
	}
	if string(content) != "# Hello via MCP" {
		t.Errorf("expected %q, got %q", "# Hello via MCP", string(content))
	}
}

func TestClient_DownloadFileMCP_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	registerMCPFileDownload(f, "node_456", "binary content")

	c := newClient(f.cfg())
	c.baseURL = f.server.URL

	content, err := c.DownloadFileMCP(context.Background(), "node_456")
	if err != nil {
		t.Fatalf("DownloadFileMCP error: %v", err)
	}
	if string(content) != "binary content" {
		t.Errorf("expected %q, got %q", "binary content", string(content))
	}
}

// registerMCPOnlineDoc mocks the MCP /mcp/doc endpoint for get_document_content.
// The handler returns the markdown content wrapped in the MCP tools/call response.
func registerMCPOnlineDoc(f *fakeDingTalk, nodeID, markdown string) {
	f.mux.HandleFunc("/mcp/doc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		var req struct {
			Params struct {
				Name string `json:"name"`
			} `json:"params"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)

		// The MCP gateway returns tool output as JSON inside content[0].text.
		toolOutput, _ := json.Marshal(map[string]interface{}{
			"success":  true,
			"markdown": markdown,
		})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": string(toolOutput)}},
				"isError": false,
			},
		})
	})
}

// registerMCPFileDownload mocks the MCP /mcp/doc endpoint for download_file,
// returning a signed URL that the test server then serves.
func registerMCPFileDownload(f *fakeDingTalk, nodeID, content string) {
	blobPath := "/mcp-blob/" + nodeID
	f.mux.HandleFunc("/mcp/doc", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		toolOutput, _ := json.Marshal(map[string]interface{}{
			"success":     true,
			"resourceUrl": f.server.URL + blobPath,
			"headers":     map[string]string{},
		})
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"jsonrpc": "2.0", "id": 1,
			"result": map[string]interface{}{
				"content": []map[string]interface{}{{"type": "text", "text": string(toolOutput)}},
				"isError": false,
			},
		})
	})
	f.mux.HandleFunc(blobPath, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(content))
	})
}

// registerMCPDocRead is a convenience alias that wires up get_document_content.
// Used by connector tests where the node extension is not an online-doc type.
func registerMCPDocRead(f *fakeDingTalk, nodeID, content string) {
	registerMCPOnlineDoc(f, nodeID, content)
}
