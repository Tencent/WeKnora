package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeDSConfig(f *fakeDingTalk, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id":     f.cfg().ClientID,
			"client_secret": f.cfg().ClientSecret,
			"user_id":       f.cfg().UserID,
			"base_url":      f.server.URL,
		},
		ResourceIDs: resourceIDs,
	}
}

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeDingTalk)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	if err := NewConnector().Validate(context.Background(), makeDSConfig(f, nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_ListResources_Success(t *testing.T) {
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

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "")
	if err != nil {
		t.Fatalf("ListResources error: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}

	if resources[0].ExternalID != "ws_123" || resources[0].Name != "Test Workspace" {
		t.Errorf("unexpected resource: %+v", resources[0])
	}
}

func TestConnector_FetchAll_UploadedFile(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": []map[string]interface{}{
				{
					"nodeId":    "node_456",
					"name":      "Report",
					"type":      "file",
					"extension": "pdf",
					"url":       "https://example.com/doc_456",
				},
			},
		})
	})

	registerMCPFileDownload(f, "node_456", "PDF binary content")

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"ws_123"}), []string{"ws_123"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if string(items[0].Content) != "PDF binary content" {
		t.Errorf("unexpected content: %q", string(items[0].Content))
	}
	if items[0].FileName != "Report.pdf" {
		t.Errorf("expected file name %q, got %q", "Report.pdf", items[0].FileName)
	}
}

func TestConnector_FetchAll_OnlineDoc(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": []map[string]interface{}{
				{
					"nodeId":    "node_doc",
					"name":      "新人手册",
					"type":      "file",
					"extension": "adoc",
				},
			},
		})
	})

	registerMCPOnlineDoc(f, "node_doc", "# 新人手册")

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"ws_123"}), []string{"ws_123"})
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if string(items[0].Content) != "# 新人手册" {
		t.Errorf("expected MCP markdown, got %q", string(items[0].Content))
	}
	if items[0].FileName != "新人手册.md" {
		t.Errorf("expected file name %q, got %q", "新人手册.md", items[0].FileName)
	}
}

func TestConnector_FetchIncremental_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": []map[string]interface{}{
				{
					"nodeId":       "node_456",
					"parentId":     "",
					"name":         "Test Doc",
					"type":         "file",
					"extension":    "md",
					"url":          "https://example.com/doc_456",
					"dentryUuid":   "uuid_789",
					"modifiedTime": time.Now().UTC().Format(time.RFC3339),
				},
			},
		})
	})

	registerMCPFileDownload(f, "node_456", "# Updated Hello DingTalk")

	// Perform first sync with nil cursor
	items, nextCursor, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"ws_123"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental (first sync) error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	// Perform second sync with the returned cursor (no updates should be fetched)
	items2, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"ws_123"}), nextCursor)
	if err != nil {
		t.Fatalf("FetchIncremental (second sync) error: %v", err)
	}

	if len(items2) != 0 {
		t.Errorf("expected 0 items on second sync, got %d", len(items2))
	}
}
