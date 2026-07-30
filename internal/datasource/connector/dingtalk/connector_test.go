package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	secutils "github.com/Tencent/WeKnora/internal/utils"
	"github.com/Tencent/WeKnora/internal/types"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// fakeDingTalk is a minimal mock of DingTalk OpenAPI endpoints.
type fakeDingTalk struct {
	server *httptest.Server
	mux    *http.ServeMux
}

func newFakeDingTalk() *fakeDingTalk {
	f := &fakeDingTalk{mux: http.NewServeMux()}
	f.server = httptest.NewServer(f.mux)
	// Default auth
	f.mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(accessTokenResponse{AccessToken: "tok-test", ExpireIn: 7200})
	})
	return f
}

func (f *fakeDingTalk) Close() { f.server.Close() }

func (f *fakeDingTalk) cfg() *Config {
	return &Config{
		AppKey:     "key",
		AppSecret:  "secret",
		OperatorID: "union-1",
		BaseURL:    f.server.URL,
	}
}

func makeDSConfig(f *fakeDingTalk, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":     f.cfg().AppKey,
			"app_secret":  f.cfg().AppSecret,
			"operator_id": f.cfg().OperatorID,
			"base_url":    f.cfg().BaseURL,
		},
		ResourceIDs: resourceIDs,
	}
}

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q", NewConnector().Type())
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspaceListResponse{
			Workspaces: []workspace{{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root1"}},
		})
	})
	if err := NewConnector().Validate(context.Background(), makeDSConfig(f, nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_Validate_Bad401(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"message":"bad"}`))
	}))
	defer srv.Close()

	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_key": "bad", "app_secret": "bad", "operator_id": "u", "base_url": srv.URL,
		},
	})
	if err == nil {
		t.Fatal("expected error for 401")
	}
}

func TestConnector_ListResources_Workspaces(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("operatorId") != "union-1" {
			t.Errorf("missing operatorId")
		}
		// Only bare list (no path suffix)
		if strings.Contains(strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/workspaces"), "/") {
			http.NotFound(w, r)
			return
		}
		_ = json.NewEncoder(w).Encode(workspaceListResponse{
			Workspaces: []workspace{
				{WorkspaceID: "ws-b", Name: "B", RootNodeID: "root-b"},
				{WorkspaceID: "ws-a", Name: "A", RootNodeID: "root-a"},
			},
		})
	})

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d resources", len(resources))
	}
	// Sorted by ExternalID
	if resources[0].ExternalID != "ws-a" || resources[1].ExternalID != "ws-b" {
		t.Errorf("order = %q, %q", resources[0].ExternalID, resources[1].ExternalID)
	}
	if resources[0].Type != "wiki_space" || !resources[0].HasChildren {
		t.Errorf("workspace resource type/has_children unexpected: %+v", resources[0])
	}
}

func TestConnector_ListResources_Children(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspace{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root1"})
	})
	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parent := r.URL.Query().Get("parentNodeId")
		if parent != "root1" {
			t.Errorf("parentNodeId = %q, want root1", parent)
		}
		_ = json.NewEncoder(w).Encode(nodeListResponse{
			Nodes: []wikiNode{
				{NodeID: "n-folder", Name: "Folder", Type: "FOLDER", WorkspaceID: "ws1", HasChildren: true},
				{NodeID: "n-doc", Name: "Doc", Type: "FILE", Category: "ALIDOC", WorkspaceID: "ws1"},
			},
		})
	})

	resources, err := NewConnector().ListResources(context.Background(), makeDSConfig(f, nil), "ws1")
	if err != nil {
		t.Fatalf("ListResources: %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("got %d", len(resources))
	}
	// Sorted by external id
	byID := map[string]types.Resource{}
	for _, r := range resources {
		byID[r.ExternalID] = r
	}
	doc := byID["ws1:n-doc"]
	if doc.Type != "file" || doc.HasChildren {
		t.Errorf("doc resource: %+v", doc)
	}
	if doc.ParentID != "ws1" {
		t.Errorf("doc ParentID = %q, want ws1", doc.ParentID)
	}
	folder := byID["ws1:n-folder"]
	if folder.Type != "doc_category" || !folder.HasChildren {
		t.Errorf("folder resource: %+v", folder)
	}
	if folder.ParentID != "ws1" {
		t.Errorf("folder ParentID = %q, want ws1", folder.ParentID)
	}
}

func TestConnector_FetchAll(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspace{WorkspaceID: "ws1", Name: "KB", RootNodeID: "root1"})
	})
	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parent := r.URL.Query().Get("parentNodeId")
		switch parent {
		case "root1":
			_ = json.NewEncoder(w).Encode(nodeListResponse{
				Nodes: []wikiNode{
					{NodeID: "doc1", Name: "Hello", Type: "FILE", Category: "ALIDOC",
						WorkspaceID: "ws1", ModifiedTime: "2024-01-01T00:00:00Z"},
					{NodeID: "folder1", Name: "Sub", Type: "FOLDER", WorkspaceID: "ws1", HasChildren: true},
				},
			})
		case "folder1":
			_ = json.NewEncoder(w).Encode(nodeListResponse{
				Nodes: []wikiNode{
					{NodeID: "doc2", Name: "Nested", Type: "FILE", Category: "ALIDOC",
						WorkspaceID: "ws1", ModifiedTime: "2024-01-02T00:00:00Z"},
				},
			})
		default:
			_ = json.NewEncoder(w).Encode(nodeListResponse{})
		}
	})
	f.mux.HandleFunc("/v1.0/doc/suites/documents/", func(w http.ResponseWriter, r *http.Request) {
		// path: /v1.0/doc/suites/documents/{id}/blocks
		_ = json.NewEncoder(w).Encode(blocksResponse{
			Result: &struct {
				Data []docBlock `json:"data"`
			}{Data: []docBlock{
				{Index: 0, BlockType: "heading", Heading: &struct {
					Level string `json:"level"`
					Text  string `json:"text"`
				}{Level: "heading-1", Text: "H"}},
				{Index: 1, BlockType: "paragraph", Paragraph: &struct {
					Text string `json:"text"`
				}{Text: "body"}},
			}},
		})
	})

	items, err := NewConnector().FetchAll(context.Background(), makeDSConfig(f, []string{"ws1"}), []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected 2 docs, got %d: %+v", len(items), items)
	}
	for _, it := range items {
		if it.ContentType != "text/markdown" {
			t.Errorf("content type = %q", it.ContentType)
		}
		if !strings.Contains(string(it.Content), "# H") {
			t.Errorf("content missing heading: %q", string(it.Content))
		}
		if it.Metadata["channel"] != types.ChannelDingtalk {
			t.Errorf("channel = %q", it.Metadata["channel"])
		}
	}
}

func TestConnector_FetchIncremental_SkipUnchanged(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspace{WorkspaceID: "ws1", RootNodeID: "root1"})
	})
	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(nodeListResponse{
			Nodes: []wikiNode{
				{NodeID: "doc1", Name: "Hello", Type: "FILE", Category: "ALIDOC",
					WorkspaceID: "ws1", ModifiedTime: "t1"},
			},
		})
	})
	// blocks should NOT be called for unchanged docs
	blockCalls := 0
	f.mux.HandleFunc("/v1.0/doc/suites/documents/", func(w http.ResponseWriter, r *http.Request) {
		blockCalls++
		_ = json.NewEncoder(w).Encode(blocksResponse{})
	})

	prev := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"node_mod_times": map[string]interface{}{
				"ws1": map[string]interface{}{"doc1": "t1"},
			},
		},
	}
	items, next, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"ws1"}), prev)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 0 {
		t.Errorf("expected 0 changed items, got %d", len(items))
	}
	if blockCalls != 0 {
		t.Errorf("expected 0 block fetches, got %d", blockCalls)
	}
	if next == nil {
		t.Fatal("expected next cursor")
	}
}

func TestConnector_FetchIncremental_DetectDeletion(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	f.mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(workspace{WorkspaceID: "ws1", RootNodeID: "root1"})
	})
	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		// Empty tree — previous doc1 is gone.
		_ = json.NewEncoder(w).Encode(nodeListResponse{Nodes: []wikiNode{}})
	})

	prev := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"node_mod_times": map[string]interface{}{
				"ws1": map[string]interface{}{"doc1": "t1"},
			},
		},
	}
	items, _, err := NewConnector().FetchIncremental(context.Background(), makeDSConfig(f, []string{"ws1"}), prev)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if len(items) != 1 || !items[0].IsDeleted || items[0].ExternalID != "doc1" {
		t.Errorf("expected deletion of doc1, got %+v", items)
	}
}

func TestConnector_ResolveResourceAncestors_EmptyForWorkspace(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()
	ids, err := NewConnector().ResolveResourceAncestors(context.Background(), makeDSConfig(f, nil), []string{"ws1"})
	if err != nil {
		t.Fatalf("err: %v", err)
	}
	if len(ids) != 0 {
		t.Errorf("expected no ancestors for workspace, got %v", ids)
	}
}
