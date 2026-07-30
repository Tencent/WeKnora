package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

type fakeDingTalk struct {
	server *httptest.Server
	mux    *http.ServeMux
	calls  []string
}

func newFakeDingTalk() *fakeDingTalk {
	f := &fakeDingTalk{mux: http.NewServeMux()}
	f.server = httptest.NewServer(f.mux)
	f.handleAccessToken()
	return f
}

func (f *fakeDingTalk) Close() { f.server.Close() }

func (f *fakeDingTalk) config(resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":     "ding-app-key",
			"app_secret":  "ding-app-secret",
			"operator_id": "operator-union-id",
			"base_url":    f.server.URL,
		},
		ResourceIDs: resourceIDs,
	}
}

func (f *fakeDingTalk) handleAccessToken() {
	f.mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if r.Method != http.MethodPost {
			w.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		var body map[string]string
		_ = json.NewDecoder(r.Body).Decode(&body)
		if body["appKey"] != "ding-app-key" || body["appSecret"] != "ding-app-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "invalid app credentials"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"accessToken": "token-value",
			"expireIn":    7200,
		})
	})
}

func (f *fakeDingTalk) handleJSON(path string, status int, body interface{}) {
	f.mux.HandleFunc(path, func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "token-value" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "missing token"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_ = json.NewEncoder(w).Encode(body)
	})
}

func TestConnector_Type(t *testing.T) {
	if NewConnector().Type() != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q, want %q", NewConnector().Type(), types.ConnectorTypeDingTalk)
	}
}

func TestConnector_Validate_Success(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.handleJSON("/v2.0/wiki/workspaces", http.StatusOK, map[string]interface{}{
		"workspaces": []map[string]interface{}{},
	})

	if err := NewConnector().Validate(context.Background(), f.config(nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}
}

func TestConnector_ListResources_WorkspacesAndChildren(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.handleJSON("/v2.0/wiki/workspaces", http.StatusOK, map[string]interface{}{
		"workspaces": []map[string]interface{}{
			{
				"workspaceId":  "space-1",
				"rootNodeId":   "root-1",
				"name":         "Engineering",
				"url":          "https://alidocs.dingtalk.com/i/space-1",
				"modifiedTime": "2026-04-20T10:00:00Z",
			},
		},
	})
	f.handleJSON("/v2.0/wiki/nodes", http.StatusOK, map[string]interface{}{
		"nodes": []map[string]interface{}{
			{
				"nodeId":       "folder-1",
				"name":         "Specs",
				"type":         "FOLDER",
				"hasChildren":  true,
				"modifiedTime": "2026-04-20T11:00:00Z",
			},
			{
				"nodeId":       "doc-1",
				"name":         "Roadmap/2026",
				"type":         "FILE",
				"url":          "https://alidocs.dingtalk.com/i/doc-1",
				"modifiedTime": "2026-04-20T12:00:00Z",
			},
		},
	})

	root, err := NewConnector().ListResources(context.Background(), f.config(nil), "")
	if err != nil {
		t.Fatalf("ListResources root: %v", err)
	}
	if len(root) != 1 {
		t.Fatalf("root len = %d, want 1", len(root))
	}
	if root[0].ExternalID != "workspace:space-1:root-1" || root[0].Type != "workspace" || !root[0].HasChildren {
		t.Fatalf("unexpected root resource: %+v", root[0])
	}

	children, err := NewConnector().ListResources(context.Background(), f.config(nil), "workspace:space-1:root-1")
	if err != nil {
		t.Fatalf("ListResources children: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children len = %d, want 2", len(children))
	}
	if children[0].ExternalID != "folder:space-1:folder-1" || children[0].ParentID != "workspace:space-1:root-1" {
		t.Errorf("unexpected folder resource: %+v", children[0])
	}
	if children[1].ExternalID != "doc:space-1:doc-1" || children[1].Name != "Roadmap/2026" {
		t.Errorf("unexpected document resource: %+v", children[1])
	}
}

func TestConnector_FetchAll_Markdown(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		f.calls = append(f.calls, r.Method+" "+r.URL.Path+"?"+r.URL.RawQuery)
		if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "token-value" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("parentNodeId") == "folder-1" {
			_ = json.NewEncoder(w).Encode(map[string]interface{}{
				"nodes": []map[string]interface{}{
					{"nodeId": "doc-2", "name": "Design", "type": "FILE", "modifiedTime": "2026-04-20T13:00:00Z", "url": "https://alidocs.dingtalk.com/i/doc-2"},
				},
			})
			return
		}
		if r.URL.Query().Get("parentNodeId") != "root-1" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(map[string]string{"message": "unexpected parent"})
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]interface{}{
			"nodes": []map[string]interface{}{
				{"nodeId": "folder-1", "name": "Specs", "type": "FOLDER", "hasChildren": true},
				{"nodeId": "doc-1", "name": "Roadmap/2026", "type": "FILE", "modifiedTime": "2026-04-20T12:00:00Z", "url": "https://alidocs.dingtalk.com/i/doc-1"},
			},
		})
	})
	f.handleJSON("/v2.0/wiki/nodes/doc-1", http.StatusOK, map[string]interface{}{
		"node": map[string]interface{}{
			"nodeId":       "doc-1",
			"name":         "Roadmap/2026",
			"url":          "https://alidocs.dingtalk.com/i/doc-1",
			"modifiedTime": "2026-04-20T12:00:00Z",
			"workspaceId":  "space-1",
		},
	})
	f.handleJSON("/v2.0/wiki/nodes/doc-2", http.StatusOK, map[string]interface{}{
		"node": map[string]interface{}{
			"nodeId":       "doc-2",
			"name":         "Design",
			"url":          "https://alidocs.dingtalk.com/i/doc-2",
			"modifiedTime": "2026-04-20T13:00:00Z",
			"workspaceId":  "space-1",
		},
	})

	items, err := NewConnector().FetchAll(context.Background(), f.config([]string{"workspace:space-1:root-1"}), []string{"workspace:space-1:root-1"})
	if err != nil {
		t.Fatalf("FetchAll: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2: %+v", len(items), items)
	}

	var roadmap *types.FetchedItem
	for i := range items {
		if items[i].ExternalID == "doc:space-1:doc-1" {
			roadmap = &items[i]
		}
	}
	if roadmap == nil {
		t.Fatal("expected roadmap document")
	}
	if string(roadmap.Content) != "# Roadmap/2026\n\nSource: https://alidocs.dingtalk.com/i/doc-1\n" {
		t.Errorf("Content = %q", string(roadmap.Content))
	}
	if roadmap.ContentType != "text/markdown" {
		t.Errorf("ContentType = %q", roadmap.ContentType)
	}
	if roadmap.FileName != "Roadmap_2026.md" {
		t.Errorf("FileName = %q", roadmap.FileName)
	}
	if roadmap.Metadata["channel"] != types.ChannelDingtalk {
		t.Errorf("channel = %q", roadmap.Metadata["channel"])
	}
	if roadmap.SourceResourceID != "workspace:space-1:root-1" {
		t.Errorf("SourceResourceID = %q", roadmap.SourceResourceID)
	}
}

func TestConnector_FetchIncremental_ChangedAndDeleted(t *testing.T) {
	f := newFakeDingTalk()
	defer f.Close()

	f.handleJSON("/v2.0/wiki/nodes", http.StatusOK, map[string]interface{}{
		"nodes": []map[string]interface{}{
			{"nodeId": "doc-1", "name": "Unchanged", "type": "FILE", "modifiedTime": "2026-04-20T10:00:00Z"},
			{"nodeId": "doc-2", "name": "Changed", "type": "FILE", "modifiedTime": "2026-04-20T12:00:00Z"},
		},
	})
	f.handleJSON("/v2.0/wiki/nodes/doc-2", http.StatusOK, map[string]interface{}{
		"node": map[string]interface{}{
			"nodeId":       "doc-2",
			"name":         "Changed",
			"url":          "https://alidocs.dingtalk.com/i/doc-2",
			"modifiedTime": "2026-04-20T12:00:00Z",
			"workspaceId":  "space-1",
		},
	})

	cursor := &types.SyncCursor{ConnectorCursor: map[string]interface{}{
		"doc_times": map[string]interface{}{
			"doc:space-1:doc-1":   "2026-04-20T10:00:00Z",
			"doc:space-1:doc-2":   "2026-04-20T11:00:00Z",
			"doc:space-1:doc-old": "2026-04-20T09:00:00Z",
		},
	}}

	items, nextCursor, err := NewConnector().FetchIncremental(context.Background(), f.config([]string{"workspace:space-1:root-1"}), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental: %v", err)
	}
	if nextCursor == nil {
		t.Fatal("next cursor must not be nil")
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want changed + deleted: %+v", len(items), items)
	}

	var changed, deleted *types.FetchedItem
	for i := range items {
		if items[i].ExternalID == "doc:space-1:doc-2" {
			changed = &items[i]
		}
		if items[i].ExternalID == "doc:space-1:doc-old" {
			deleted = &items[i]
		}
	}
	if changed == nil || string(changed.Content) != "# Changed\n\nSource: https://alidocs.dingtalk.com/i/doc-2\n" {
		t.Fatalf("changed document not fetched correctly: %+v", changed)
	}
	if deleted == nil || !deleted.IsDeleted {
		t.Fatalf("deleted placeholder missing: %+v", deleted)
	}

	for _, call := range f.calls {
		if strings.Contains(call, "/v2.0/wiki/nodes/doc-1") {
			t.Fatalf("unchanged doc detail should not be fetched; calls=%v", f.calls)
		}
	}
}
