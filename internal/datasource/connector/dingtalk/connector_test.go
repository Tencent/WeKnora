package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

// ──────────────────────────────────────────────────────────────────────
// Fake DingTalk API server
// ──────────────────────────────────────────────────────────────────────

// fakeState holds the mutable knowledge base tree served by the fake server,
// so tests can mutate it between syncs (incremental sync scenarios).
type fakeState struct {
	mu sync.Mutex

	workspaces []workspace
	// tree maps parentNodeID -> direct children
	tree map[string][]wikiNode

	// syncExport makes the export submit response carry the downloadUrl
	// directly (no polling round-trip).
	syncExport bool
}

func (s *fakeState) findNode(nodeID string) (wikiNode, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, children := range s.tree {
		for _, n := range children {
			if n.NodeID == nodeID {
				return n, true
			}
		}
	}
	return wikiNode{}, false
}

// fakeDingTalk builds an httptest.Server emulating the DingTalk APIs used by
// the connector, and a Config pointing at it.
func fakeDingTalk(state *fakeState) (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	// --- new-style auth ---
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{AccessToken: "fake-token", ExpireIn: 7200})
	})

	// --- legacy APIs for operator auto-resolution ---
	mux.HandleFunc("/gettoken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, legacyTokenResponse{ErrCode: 0, AccessToken: "fake-legacy-token"})
	})
	mux.HandleFunc("/topapi/user/listadmin", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"errcode": 0,
			"result":  []map[string]interface{}{{"userid": "admin-1", "sys_level": 1}},
		})
	})
	mux.HandleFunc("/topapi/v2/user/get", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"errcode": 0,
			"result":  map[string]interface{}{"unionid": "resolved-union-id", "name": "Admin"},
		})
	})

	// --- workspaces ---
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		state.mu.Lock()
		defer state.mu.Unlock()
		writeJSON(w, workspaceListResponse{Workspaces: state.workspaces})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/workspaces/")
		state.mu.Lock()
		defer state.mu.Unlock()
		for _, ws := range state.workspaces {
			if ws.WorkspaceID == id {
				writeJSON(w, workspaceDetailResponse{Workspace: ws})
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, apiError{Code: "NotFound", Message: "workspace not found"})
	})

	// --- nodes ---
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentID := r.URL.Query().Get("parentNodeId")
		state.mu.Lock()
		defer state.mu.Unlock()
		writeJSON(w, nodeListResponse{Nodes: state.tree[parentID]})
	})
	mux.HandleFunc("/v2.0/wiki/nodes/", func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/nodes/")
		if node, ok := state.findNode(id); ok {
			writeJSON(w, nodeDetailResponse{Node: node})
			return
		}
		w.WriteHeader(http.StatusNotFound)
		writeJSON(w, apiError{Code: "NotFound", Message: "node not found"})
	})

	// --- export flow ---
	var server *httptest.Server
	mux.HandleFunc("/v2.0/doc/me/export/submit", func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			DentryUUID string `json:"dentryUuid"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		resp := exportSubmitResponse{TaskID: "task-" + body.DentryUUID}
		state.mu.Lock()
		if state.syncExport {
			resp.DownloadURL = server.URL + "/download/" + body.DentryUUID
		}
		state.mu.Unlock()
		writeJSON(w, resp)
	})
	mux.HandleFunc("/v2.0/doc/me/export/task/query", func(w http.ResponseWriter, r *http.Request) {
		taskID := r.URL.Query().Get("taskId")
		nodeID := strings.TrimPrefix(taskID, "task-")
		writeJSON(w, exportQueryResponse{
			Status:      "SUCCESS",
			DownloadURL: server.URL + "/download/" + nodeID,
		})
	})
	mux.HandleFunc("/download/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := strings.TrimPrefix(r.URL.Path, "/download/")
		fmt.Fprintf(w, "# markdown of %s", nodeID)
	})

	server = httptest.NewServer(mux)
	return server, &Config{
		AppKey:        "fake-key",
		AppSecret:     "fake-secret",
		OperatorID:    "op-union-id",
		BaseURL:       server.URL,
		LegacyBaseURL: server.URL,
	}
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

// defaultState builds the standard test knowledge base:
//
//	ws1 (root: root1)
//	├── folder1/          (FOLDER)
//	│   └── doc2.adoc     (FILE, ALIDOC, adoc)
//	├── doc1.adoc         (FILE, ALIDOC, adoc)
//	└── sheet1.axls       (FILE, ALIDOC, axls — not exportable)
func defaultState() *fakeState {
	return &fakeState{
		workspaces: []workspace{{
			WorkspaceID: "ws1",
			Name:        "测试知识库",
			RootNodeID:  "root1",
			URL:         "https://alidocs.dingtalk.com/i/spaces/ws1",
		}},
		tree: map[string][]wikiNode{
			"root1": {
				{NodeID: "folder1", WorkspaceID: "ws1", Name: "学习资料", Type: nodeTypeFolder,
					Category: "OTHER", HasChildren: true, ModifiedTimestamp: 1000},
				{NodeID: "doc1", WorkspaceID: "ws1", Name: "管理制度.adoc", Type: nodeTypeFile,
					Category: categoryAlidoc, Extension: extensionAdoc, ModifiedTimestamp: 2000,
					URL: "https://alidocs.dingtalk.com/i/nodes/doc1"},
				{NodeID: "sheet1", WorkspaceID: "ws1", Name: "统计表.axls", Type: nodeTypeFile,
					Category: categoryAlidoc, Extension: "axls", ModifiedTimestamp: 3000},
			},
			"folder1": {
				{NodeID: "doc2", WorkspaceID: "ws1", Name: "核心业务.adoc", Type: nodeTypeFile,
					Category: categoryAlidoc, Extension: extensionAdoc, ModifiedTimestamp: 4000,
					URL: "https://alidocs.dingtalk.com/i/nodes/doc2"},
			},
		},
		syncExport: true,
	}
}

func configFor(cfg *Config) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"app_key":         cfg.AppKey,
			"app_secret":      cfg.AppSecret,
			"operator_id":     cfg.OperatorID,
			"base_url":        cfg.BaseURL,
			"legacy_base_url": cfg.LegacyBaseURL,
		},
	}
}

// ──────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────

func TestConnectorType(t *testing.T) {
	if got := NewConnector().Type(); got != types.ConnectorTypeDingTalk {
		t.Fatalf("Type() = %q, want %q", got, types.ConnectorTypeDingTalk)
	}
}

func TestValidate_MissingCredentials(t *testing.T) {
	connector := NewConnector()
	err := connector.Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{"app_key": "only-key"},
	})
	if err == nil || !strings.Contains(err.Error(), "app_key and app_secret") {
		t.Fatalf("expected missing-credential error, got %v", err)
	}
}

func TestValidate_Success(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	if err := NewConnector().Validate(context.Background(), configFor(cfg)); err != nil {
		t.Fatalf("Validate failed: %v", err)
	}
}

func TestListResources_Root(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	resources, err := NewConnector().ListResources(context.Background(), configFor(cfg), "")
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("expected 1 workspace, got %d", len(resources))
	}
	ws := resources[0]
	if ws.ExternalID != "ws1" || ws.Type != "workspace" || !ws.HasChildren {
		t.Fatalf("unexpected workspace resource: %+v", ws)
	}
}

func TestListResources_WorkspaceLevel(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	resources, err := NewConnector().ListResources(context.Background(), configFor(cfg), "ws1")
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resources) != 3 {
		t.Fatalf("expected 3 top-level nodes, got %d", len(resources))
	}

	byID := map[string]types.Resource{}
	for _, r := range resources {
		byID[r.ExternalID] = r
	}
	folder, ok := byID["ws1:folder1"]
	if !ok || folder.Type != "folder" || !folder.HasChildren || folder.ParentID != "ws1" {
		t.Fatalf("unexpected folder resource: %+v", folder)
	}
	doc, ok := byID["ws1:doc1"]
	if !ok || doc.Type != "document" || doc.HasChildren {
		t.Fatalf("unexpected document resource: %+v", doc)
	}
}

func TestListResources_NodeChildren(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	resources, err := NewConnector().ListResources(context.Background(), configFor(cfg), "ws1:folder1")
	if err != nil {
		t.Fatalf("ListResources failed: %v", err)
	}
	if len(resources) != 1 || resources[0].ExternalID != "ws1:doc2" {
		t.Fatalf("unexpected children: %+v", resources)
	}
	if resources[0].ParentID != "ws1:folder1" {
		t.Fatalf("unexpected parent ID: %q", resources[0].ParentID)
	}
}

func TestFetchAll_Workspace(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	items, err := NewConnector().FetchAll(context.Background(), configFor(cfg), []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll failed: %v", err)
	}
	// folder1 and sheet1 are skipped; doc1 and doc2 are exported.
	if len(items) != 2 {
		t.Fatalf("expected 2 items, got %d: %+v", len(items), items)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	doc1 := byID["doc1"]
	if doc1.Title != "管理制度" || doc1.FileName != "管理制度.md" || doc1.ContentType != "text/markdown" {
		t.Fatalf("unexpected doc1 item: %+v", doc1)
	}
	if string(doc1.Content) != "# markdown of doc1" {
		t.Fatalf("unexpected doc1 content: %q", doc1.Content)
	}
	if doc1.Metadata["channel"] != types.ChannelDingtalk {
		t.Fatalf("unexpected doc1 metadata: %+v", doc1.Metadata)
	}
	if _, ok := byID["doc2"]; !ok {
		t.Fatalf("nested doc2 missing from items: %+v", items)
	}
}

func TestFetchAll_NodeSelection(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	items, err := NewConnector().FetchAll(context.Background(), configFor(cfg), []string{"ws1:doc1"})
	if err != nil {
		t.Fatalf("FetchAll failed: %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "doc1" {
		t.Fatalf("expected only doc1, got %+v", items)
	}
	if items[0].SourceResourceID != "ws1:doc1" {
		t.Fatalf("unexpected source resource: %q", items[0].SourceResourceID)
	}
}

func TestFetchIncremental(t *testing.T) {
	state := defaultState()
	server, cfg := fakeDingTalk(state)
	defer server.Close()

	connector := NewConnector()
	dsConfig := configFor(cfg)
	dsConfig.ResourceIDs = []string{"ws1"}

	// First sync: everything is new.
	items, cursor, err := connector.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first FetchIncremental failed: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("first sync: expected 2 items, got %d", len(items))
	}
	if cursor == nil || cursor.ConnectorCursor == nil {
		t.Fatalf("first sync: missing cursor")
	}

	// Second sync with no changes: nothing to fetch.
	items, cursor2, err := connector.FetchIncremental(context.Background(), dsConfig, cursor)
	if err != nil {
		t.Fatalf("second FetchIncremental failed: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("second sync: expected 0 items, got %d: %+v", len(items), items)
	}

	// Mutate: doc1 edited, doc2 deleted.
	state.mu.Lock()
	state.tree["root1"][1].ModifiedTimestamp = 9999
	state.tree["folder1"] = nil
	state.mu.Unlock()

	items, _, err = connector.FetchIncremental(context.Background(), dsConfig, cursor2)
	if err != nil {
		t.Fatalf("third FetchIncremental failed: %v", err)
	}

	var changed, deleted []string
	for _, item := range items {
		if item.IsDeleted {
			deleted = append(deleted, item.ExternalID)
		} else {
			changed = append(changed, item.ExternalID)
		}
	}
	if len(changed) != 1 || changed[0] != "doc1" {
		t.Fatalf("expected doc1 changed, got %v", changed)
	}
	if len(deleted) != 1 || deleted[0] != "doc2" {
		t.Fatalf("expected doc2 deleted, got %v", deleted)
	}
}

func TestOperatorAutoResolve(t *testing.T) {
	server, cfg := fakeDingTalk(defaultState())
	defer server.Close()

	cfg.OperatorID = "" // force auto-resolution via legacy contact API
	client := NewClient(cfg)

	operatorID, err := client.GetOperatorID(context.Background())
	if err != nil {
		t.Fatalf("GetOperatorID failed: %v", err)
	}
	if operatorID != "resolved-union-id" {
		t.Fatalf("operatorID = %q, want resolved-union-id", operatorID)
	}
}

func TestExportPolling(t *testing.T) {
	state := defaultState()
	state.syncExport = false // submit returns only taskId; client must poll
	server, cfg := fakeDingTalk(state)
	defer server.Close()

	oldInterval := exportPollInterval
	exportPollInterval = 10 * time.Millisecond
	defer func() { exportPollInterval = oldInterval }()

	data, err := NewClient(cfg).ExportMarkdown(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("ExportMarkdown failed: %v", err)
	}
	if string(data) != "# markdown of doc1" {
		t.Fatalf("unexpected content: %q", data)
	}
}

func TestResourceIDHelpers(t *testing.T) {
	id := makeNodeResourceID("ws1", "node9")
	if id != "ws1:node9" {
		t.Fatalf("makeNodeResourceID = %q", id)
	}
	ws, node := parseNodeResourceID(id)
	if ws != "ws1" || node != "node9" {
		t.Fatalf("parseNodeResourceID = (%q, %q)", ws, node)
	}
	ws, node = parseNodeResourceID("ws-only")
	if ws != "ws-only" || node != "" {
		t.Fatalf("parseNodeResourceID(ws-only) = (%q, %q)", ws, node)
	}
}

func TestIsExportableNode(t *testing.T) {
	cases := []struct {
		node wikiNode
		want bool
	}{
		{wikiNode{Type: nodeTypeFile, Category: categoryAlidoc, Extension: extensionAdoc}, true},
		{wikiNode{Type: nodeTypeFolder, Category: "OTHER"}, false},
		{wikiNode{Type: nodeTypeFile, Category: categoryAlidoc, Extension: "axls"}, false},
		{wikiNode{Type: nodeTypeFile, Category: "FILE", Extension: "pdf"}, false},
	}
	for i, tc := range cases {
		if got := isExportableNode(tc.node); got != tc.want {
			t.Errorf("case %d: isExportableNode(%+v) = %v, want %v", i, tc.node, got, tc.want)
		}
	}
}

func TestSanitizeFileName(t *testing.T) {
	if got := sanitizeFileName(`a/b\c:d*e?f"g<h>i|j`); got != "a_b_c_d_e_f_g_h_i_j" {
		t.Fatalf("sanitizeFileName = %q", got)
	}
	if got := sanitizeFileName(""); got != "untitled" {
		t.Fatalf("sanitizeFileName(empty) = %q", got)
	}
	long := strings.Repeat("汉", 100) // 300 bytes
	got := sanitizeFileName(long)
	if len(got) > 200 || !strings.HasPrefix(got, "汉") {
		t.Fatalf("sanitizeFileName(long) len=%d", len(got))
	}
	for _, r := range got {
		if r != '汉' {
			t.Fatalf("sanitizeFileName produced invalid rune %q", r)
		}
	}
}
