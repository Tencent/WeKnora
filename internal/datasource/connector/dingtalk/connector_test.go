package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeDingTalkConfig(baseURL string, resourceIDs []string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":           "ding-test",
			"app_secret":        "secret",
			"operator_union_id": "operator-1",
			"base_url":          baseURL,
		},
		ResourceIDs: resourceIDs,
	}
}

func TestParseConfig_AcceptsConsoleAliases(t *testing.T) {
	cfg, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "ding-console",
			"client_secret": "console-secret",
			"operator_id":   "union-1",
		},
	})
	if err != nil {
		t.Fatalf("parseConfig error: %v", err)
	}
	if cfg.AppKey != "ding-console" || cfg.AppSecret != "console-secret" || cfg.OperatorUnionID != "union-1" {
		t.Fatalf("parsed config mismatch: %+v", cfg)
	}
}

func TestParseConfig_RequiresOperatorUnionID(t *testing.T) {
	_, err := parseConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_key":    "ding-test",
			"app_secret": "secret",
		},
	})
	if err == nil {
		t.Fatal("expected missing operator unionId error")
	}
}

func TestConnector_ValidateAndListResources(t *testing.T) {
	f := newFakeDingTalk(t)

	if err := NewConnector().Validate(context.Background(), makeDingTalkConfig(f.URL, nil)); err != nil {
		t.Fatalf("Validate error: %v", err)
	}

	resources, err := NewConnector().ListResources(context.Background(), makeDingTalkConfig(f.URL, nil), "")
	if err != nil {
		t.Fatalf("ListResources(root) error: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("len = %d, want 1", len(resources))
	}
	got := resources[0]
	if got.ExternalID != "ws-1:root-1" || got.Name != "Demo Space" || got.Type != "wiki_space" || !got.HasChildren {
		t.Fatalf("workspace resource mismatch: %+v", got)
	}

	children, err := NewConnector().ListResources(context.Background(), makeDingTalkConfig(f.URL, nil), "ws-1:root-1")
	if err != nil {
		t.Fatalf("ListResources(children) error: %v", err)
	}
	if len(children) != 2 {
		t.Fatalf("children len = %d, want 2", len(children))
	}
	byID := map[string]types.Resource{}
	for _, r := range children {
		byID[r.ExternalID] = r
	}
	if child := byID["ws-1:doc-1"]; child.Name != "Online Doc" || child.Type != "document" || child.ParentID != "ws-1:root-1" {
		t.Fatalf("doc child mismatch: %+v", child)
	}
	if folder := byID["ws-1:folder-1"]; folder.Name != "Folder" || folder.Type != "folder" || !folder.HasChildren {
		t.Fatalf("folder child mismatch: %+v", folder)
	}
}

func TestConnector_FetchAll_Markdown(t *testing.T) {
	f := newFakeDingTalk(t)

	items, err := NewConnector().FetchAll(
		context.Background(),
		makeDingTalkConfig(f.URL, []string{"ws-1:root-1"}),
		[]string{"ws-1:root-1"},
	)
	if err != nil {
		t.Fatalf("FetchAll error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("items len = %d, want 2", len(items))
	}
	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	doc := byID["doc-1"]
	if doc.Title != "Online Doc" || doc.ContentType != "text/markdown" || doc.FileName != "Online Doc.md" {
		t.Fatalf("doc item mismatch: %+v", doc)
	}
	if !strings.Contains(string(doc.Content), "# Online Doc\n\n## Heading\n\nParagraph text") {
		t.Fatalf("unexpected markdown:\n%s", string(doc.Content))
	}
	if doc.Metadata["channel"] != types.ChannelDingtalk || doc.Metadata["workspace_id"] != "ws-1" {
		t.Fatalf("metadata mismatch: %+v", doc.Metadata)
	}
	if doc.UpdatedAt.IsZero() {
		t.Fatal("UpdatedAt should be parsed")
	}

	nested := byID["doc-2"]
	if !strings.Contains(string(nested.Content), "Nested text") {
		t.Fatalf("nested doc markdown missing content:\n%s", string(nested.Content))
	}
}

func TestConnector_FetchIncremental_SkipsUnchangedAndDetectsDeletion(t *testing.T) {
	f := newFakeDingTalk(t)
	cfg := makeDingTalkConfig(f.URL, []string{"ws-1:root-1"})

	items, cursor, err := NewConnector().FetchIncremental(context.Background(), cfg, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental error: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("first items len = %d, want 2", len(items))
	}

	f.omitDoc2 = true
	items, _, err = NewConnector().FetchIncremental(context.Background(), cfg, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental error: %v", err)
	}
	if len(items) != 1 || !items[0].IsDeleted || items[0].ExternalID != "doc-2" {
		t.Fatalf("expected doc-2 deletion only, got %+v", items)
	}
}

type fakeDingTalk struct {
	*httptest.Server
	omitDoc2 bool
}

func newFakeDingTalk(t *testing.T) *fakeDingTalk {
	t.Helper()
	f := &fakeDingTalk{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", f.handleToken)
	mux.HandleFunc("/v2.0/wiki/workspaces", f.handleWorkspaces)
	mux.HandleFunc("/v2.0/wiki/nodes", f.handleNodes)
	mux.HandleFunc("/v2.0/wiki/nodes/", f.handleNode)
	mux.HandleFunc("/v1.0/doc/suites/documents/", f.handleBlocks)
	f.Server = httptest.NewServer(mux)
	t.Cleanup(f.Close)
	return f
}

func (f *fakeDingTalk) handleToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	var body map[string]string
	_ = json.NewDecoder(r.Body).Decode(&body)
	if body["appKey"] != "ding-test" || body["appSecret"] != "secret" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"message":"bad credentials"}`))
		return
	}
	writeJSON(w, accessTokenResponse{AccessToken: "token-1", ExpireIn: 7200})
}

func (f *fakeDingTalk) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if !f.checkAuth(w, r) {
		return
	}
	writeJSON(w, workspaceListResponse{
		Workspaces: []workspace{{
			WorkspaceID:  "ws-1",
			RootNodeID:   "root-1",
			Name:         "Demo Space",
			Description:  "Demo",
			URL:          "https://alidocs.dingtalk.com/wiki/ws-1",
			ModifiedTime: "2026-06-01T10:00:00Z",
		}},
	})
}

func (f *fakeDingTalk) handleNodes(w http.ResponseWriter, r *http.Request) {
	if !f.checkAuth(w, r) {
		return
	}
	parent := r.URL.Query().Get("parentNodeId")
	switch parent {
	case "root-1":
		nodes := []node{
			{NodeID: "doc-1", WorkspaceID: "ws-1", Name: "Online Doc", Type: "FILE", Category: "ALIDOC", URL: "https://alidocs.dingtalk.com/i/nodes/doc-1", ModifiedTime: "2026-06-01T10:00:00Z", Stats: statisticalInfo{WordCount: 12}},
			{NodeID: "folder-1", WorkspaceID: "ws-1", Name: "Folder", Type: "FOLDER", HasChildren: true, ModifiedTime: "2026-06-01T11:00:00Z"},
		}
		writeJSON(w, nodeListResponse{Nodes: nodes})
	case "folder-1":
		if f.omitDoc2 {
			writeJSON(w, nodeListResponse{})
			return
		}
		writeJSON(w, nodeListResponse{Nodes: []node{
			{NodeID: "doc-2", WorkspaceID: "ws-1", Name: "Nested Doc", Type: "FILE", Category: "ALIDOC", URL: "https://alidocs.dingtalk.com/i/nodes/doc-2", ModifiedTime: "2026-06-01T12:00:00Z"},
		}})
	default:
		writeJSON(w, nodeListResponse{})
	}
}

func (f *fakeDingTalk) handleNode(w http.ResponseWriter, r *http.Request) {
	if !f.checkAuth(w, r) {
		return
	}
	id := strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/nodes/")
	switch id {
	case "root-1":
		writeJSON(w, nodeInfoResponse{Node: node{NodeID: "root-1", WorkspaceID: "ws-1", Name: "Root", Type: "FOLDER", HasChildren: true, ModifiedTime: "2026-06-01T09:00:00Z"}})
	default:
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"message":"not found"}`))
	}
}

func (f *fakeDingTalk) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if !f.checkAuth(w, r) {
		return
	}
	switch {
	case strings.Contains(r.URL.Path, "/doc-1/blocks"):
		writeJSON(w, docBlocksResponse{Success: true, Result: struct {
			Data []docBlock `json:"data"`
		}{Data: []docBlock{
			{BlockType: "heading", Index: 1, Heading: &headingBlock{Text: "Heading", Level: 2}},
			{BlockType: "paragraph", Index: 2, Paragraph: &textBlock{Text: "Paragraph text"}},
		}}})
	case strings.Contains(r.URL.Path, "/doc-2/blocks"):
		writeJSON(w, docBlocksResponse{Success: true, Result: struct {
			Data []docBlock `json:"data"`
		}{Data: []docBlock{
			{BlockType: "paragraph", Index: 1, Paragraph: &textBlock{Text: "Nested text"}},
		}}})
	default:
		w.WriteHeader(http.StatusNotFound)
	}
}

func (f *fakeDingTalk) checkAuth(w http.ResponseWriter, r *http.Request) bool {
	if r.Header.Get("x-acs-dingtalk-access-token") != "token-1" {
		w.WriteHeader(http.StatusUnauthorized)
		return false
	}
	if r.URL.Query().Get("operatorId") != "operator-1" {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"message":"missing operatorId"}`))
		return false
	}
	return true
}

func writeJSON(w http.ResponseWriter, value interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(value)
}
