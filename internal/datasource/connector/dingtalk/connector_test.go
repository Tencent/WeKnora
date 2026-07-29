package dingtalk

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
)

func makeDingTalkConfig(clientID, clientSecret, operatorID string, resourceIDs []string) *types.DataSourceConfig {
	return makeDingTalkConfigWithBaseURL(clientID, clientSecret, operatorID, "", resourceIDs)
}

func makeDingTalkConfigWithBaseURL(clientID, clientSecret, operatorID, baseURL string, resourceIDs []string) *types.DataSourceConfig {
	creds := map[string]interface{}{
		"client_id":     clientID,
		"client_secret": clientSecret,
	}
	if operatorID != "" {
		creds["operator_id"] = operatorID
	}
	if baseURL != "" {
		creds["base_url"] = baseURL
	}
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: creds,
		ResourceIDs: resourceIDs,
	}
}

func newTestConnector() *Connector {
	c := NewConnector()
	c.perDocumentDelay = 0
	return c
}

type fakeDingTalk struct {
	server        *httptest.Server
	mux           *http.ServeMux
	nodesByParent map[string][]WikiNode
	blockStatus   map[string]int
	blocksByNode  map[string][]docBlock
}

func newFakeDingTalk(t *testing.T) *fakeDingTalk {
	t.Helper()
	allowLocalDingTalkHTTPForTest(t)
	f := &fakeDingTalk{
		mux:           http.NewServeMux(),
		nodesByParent: make(map[string][]WikiNode),
		blockStatus:   make(map[string]int),
		blocksByNode:  make(map[string][]docBlock),
	}
	f.server = httptest.NewServer(f.mux)
	f.handleToken()
	f.handleWorkspaces([]WikiWorkspace{{
		WorkspaceID:  "ws-1",
		Name:         "Engineering Wiki",
		Type:         "TEAM",
		RootNodeID:   "root-1",
		URL:          "https://wiki.dingtalk.com/ws/1",
		ModifiedTime: "2026-01-15T10:00:00+08:00",
		Description:  "Team docs",
		CorpID:       "corp-1",
	}})
	f.handleNodes()
	f.handleBlocks()
	return f
}

func (f *fakeDingTalk) Close() {
	f.server.Close()
}

func (f *fakeDingTalk) URL() string {
	return f.server.URL
}

func (f *fakeDingTalk) config(resourceIDs []string) *types.DataSourceConfig {
	return makeDingTalkConfigWithBaseURL("valid-id", "valid-secret", "operator-1", f.URL(), resourceIDs)
}

func (f *fakeDingTalk) handleToken() {
	f.mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		var req accessTokenRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if req.AppKey != "valid-id" || req.AppSecret != "valid-secret" {
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(dingtalkErrorResponse{ErrCode: 401, ErrMsg: "invalid credentials"})
			return
		}
		_ = json.NewEncoder(w).Encode(accessTokenResponse{AccessToken: "test-token", ExpireIn: 7200})
	})
}

func (f *fakeDingTalk) handleWorkspaces(workspaces []WikiWorkspace) {
	f.mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if got := r.URL.Query().Get("operatorId"); got != "operator-1" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(dingtalkErrorResponse{ErrCode: 400, ErrMsg: "operatorId is required"})
			return
		}
		_ = json.NewEncoder(w).Encode(wikiWorkspacesResponse{Workspaces: workspaces})
	})
}

func (f *fakeDingTalk) handleNodes() {
	f.mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentID := r.URL.Query().Get("parentNodeId")
		w.Header().Set("Content-Type", "application/json")
		if got := r.URL.Query().Get("operatorId"); got != "operator-1" {
			w.WriteHeader(http.StatusBadRequest)
			_ = json.NewEncoder(w).Encode(dingtalkErrorResponse{ErrCode: 400, ErrMsg: "operatorId is required"})
			return
		}
		_ = json.NewEncoder(w).Encode(wikiNodesResponse{Nodes: f.nodesByParent[parentID]})
	})
}

func (f *fakeDingTalk) handleBlocks() {
	f.mux.HandleFunc("/v1.0/doc/suites/documents/", func(w http.ResponseWriter, r *http.Request) {
		nodeID := strings.TrimPrefix(r.URL.Path, "/v1.0/doc/suites/documents/")
		nodeID = strings.TrimSuffix(nodeID, "/blocks")
		if status := f.blockStatus[nodeID]; status != 0 {
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(dingtalkErrorResponse{ErrCode: status, ErrMsg: "block failure"})
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(docBlocksResponse{Blocks: f.blocksByNode[nodeID]})
	})
}

func TestConnectorType(t *testing.T) {
	c := NewConnector()
	if c.Type() != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q, want %q", c.Type(), types.ConnectorTypeDingTalk)
	}
}

func TestIsDocumentNode_ClassifiesNativeDocsAndAttachments(t *testing.T) {
	tests := []struct {
		name string
		node WikiNode
		want bool
	}{
		{
			name: "native ALIDOC",
			node: WikiNode{NodeType: "FILE", Category: "ALIDOC", Extension: "adoc"},
			want: true,
		},
		{
			name: "native adoc with compatible category",
			node: WikiNode{NodeType: "FILE", Category: "DOCUMENT", Extension: ".ADOC"},
			want: true,
		},
		{
			name: "uploaded PDF reported as DOCUMENT",
			node: WikiNode{NodeType: "FILE", Category: "DOCUMENT", Extension: "pdf"},
			want: false,
		},
		{
			name: "uploaded CSV",
			node: WikiNode{NodeType: "FILE", Category: "OTHER", Extension: "csv"},
			want: false,
		},
		{
			name: "folder",
			node: WikiNode{NodeType: "FOLDER", Category: "OTHER"},
			want: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isDocumentNode(tt.node); got != tt.want {
				t.Fatalf("isDocumentNode(%+v) = %t, want %t", tt.node, got, tt.want)
			}
		})
	}
}

func TestConnectorValidate_Success(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()

	if err := newTestConnector().Validate(context.Background(), f.config(nil)); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
}

func TestConnectorValidate_MissingClientID(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{"client_secret": "secret"},
	})
	if err == nil {
		t.Fatal("expected error for missing client_id")
	}
}

func TestConnectorValidate_MissingClientSecret(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Type:        types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{"client_id": "id"},
	})
	if err == nil {
		t.Fatal("expected error for missing client_secret")
	}
}

func TestConnectorValidate_MissingOperatorID(t *testing.T) {
	err := NewConnector().Validate(context.Background(), &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id":     "id",
			"client_secret": "secret",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing operator_id")
	}
}

func TestConnectorValidate_NilConfig(t *testing.T) {
	if err := NewConnector().Validate(context.Background(), nil); err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestConnectorResolveResourceAncestors(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "folder-1", Name: "Guides", NodeType: "FOLDER", Category: "FOLDER", HasChildren: true},
	}
	f.nodesByParent["folder-1"] = []WikiNode{
		{NodeID: "doc-1", Name: "Runbook", NodeType: "FILE", Category: "ALIDOC"},
	}

	ancestors, err := newTestConnector().ResolveResourceAncestors(context.Background(), f.config(nil), []string{"ws-1:doc-1:document"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []string{"ws-1", "ws-1:folder-1:folder"}
	if strings.Join(ancestors, ",") != strings.Join(want, ",") {
		t.Errorf("ancestors = %v, want %v", ancestors, want)
	}
}

func TestConnectorListResources(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()

	resources, err := newTestConnector().ListResources(context.Background(), f.config(nil), "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 1 {
		t.Fatalf("len(resources) = %d, want 1", len(resources))
	}
	if resources[0].ExternalID != "ws-1" || resources[0].Name != "Engineering Wiki" {
		t.Fatalf("unexpected resource: %+v", resources[0])
	}
	if resources[0].Type != dingtalkResourceSpace || !resources[0].HasChildren {
		t.Fatalf("unexpected resource type/children: %+v", resources[0])
	}
	if resources[0].Metadata["root_node_id"] != "root-1" {
		t.Fatalf("root_node_id metadata = %v, want root-1", resources[0].Metadata["root_node_id"])
	}
}

func TestConnectorListResources_LoadsWorkspaceChildren(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "folder-1", Name: "Guides", NodeType: "FOLDER", Category: "FOLDER", HasChildren: true},
		{NodeID: "doc-1", Name: "Runbook", NodeType: "FILE", Category: "ALIDOC", ModifiedTime: "2026-01-15T10:00:00+08:00"},
		{NodeID: "pdf-1", Name: "Manual.pdf", NodeType: "FILE", Category: "DOCUMENT", Extension: "pdf"},
		{NodeID: "img-1", Name: "Logo", NodeType: "FILE", Category: "IMAGE"},
	}

	resources, err := newTestConnector().ListResources(context.Background(), f.config(nil), "ws-1")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 2 {
		t.Fatalf("len(resources) = %d, want 2: %+v", len(resources), resources)
	}
	byID := map[string]types.Resource{}
	for _, r := range resources {
		byID[r.ExternalID] = r
	}
	if byID["ws-1:folder-1:folder"].Type != dingtalkResourceFolder || !byID["ws-1:folder-1:folder"].HasChildren {
		t.Fatalf("unexpected folder resource: %+v", byID["ws-1:folder-1:folder"])
	}
	if byID["ws-1:doc-1:document"].Type != dingtalkResourceDocument || byID["ws-1:doc-1:document"].ParentID != "ws-1" {
		t.Fatalf("unexpected document resource: %+v", byID["ws-1:doc-1:document"])
	}
	if _, ok := byID["ws-1:img-1:image"]; ok {
		t.Fatalf("unsupported image leaf should not be selectable: %+v", resources)
	}
	if _, ok := byID["ws-1:pdf-1:document"]; ok {
		t.Fatalf("generic DOCUMENT attachment should not be selectable as a DingTalk doc: %+v", resources)
	}
}

func TestConnectorListResources_LoadsFolderChildren(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["folder-1"] = []WikiNode{
		{NodeID: "doc-2", Name: "Nested", NodeType: "FILE", Category: "ALIDOC", Extension: "adoc"},
	}

	resources, err := newTestConnector().ListResources(context.Background(), f.config(nil), "ws-1:folder-1:folder")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}
	if len(resources) != 1 || resources[0].ExternalID != "ws-1:doc-2:document" || resources[0].ParentID != "ws-1:folder-1:folder" {
		t.Fatalf("unexpected resources: %+v", resources)
	}
}

func TestConnectorFetchAll_RecursesAndFetchesDocumentBlocks(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "folder-1", Name: "Guides", NodeType: "FOLDER", Category: "FOLDER", HasChildren: true},
		{NodeID: "doc-1", DocKey: "doc-key-1", Name: "Runbook", NodeType: "FILE", Category: "ALIDOC", URL: "https://wiki/doc-1", ModifiedTime: "2026-01-15T10:00:00+08:00", WordCount: 42},
		{NodeID: "pdf-1", Name: "Manual.pdf", NodeType: "FILE", Category: "DOCUMENT", Extension: "pdf"},
		{NodeID: "img-1", Name: "Logo", NodeType: "FILE", Category: "IMAGE"},
	}
	f.nodesByParent["folder-1"] = []WikiNode{
		{NodeID: "doc-2", Name: "Nested Plan", NodeType: "FILE", Category: "ALIDOC", Extension: "adoc", URL: "https://wiki/doc-2", ModifiedTime: "2026-01-16T10:00:00+08:00"},
	}
	f.blocksByNode["doc-key-1"] = []docBlock{{BlockType: "heading2", Text: "Deploy"}, {Text: "Restart workers"}}
	f.blocksByNode["doc-2"] = []docBlock{{Text: "Nested content"}}

	items, err := newTestConnector().FetchAll(context.Background(), f.config(nil), []string{"ws-1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want 2: %+v", len(items), items)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if got := string(byID["doc-1"].Content); !strings.Contains(got, "## Deploy") || !strings.Contains(got, "Restart workers") {
		t.Fatalf("doc-1 content = %q", got)
	}
	if got := string(byID["doc-2"].Content); !strings.Contains(got, "Nested content") {
		t.Fatalf("doc-2 content = %q", got)
	}
}

func TestConnectorFetchAll_ContentFailureReturnsErrorItem(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "doc-1", Name: "Private Doc", NodeType: "FILE", Category: "ALIDOC"},
	}
	f.blockStatus["doc-1"] = http.StatusForbidden

	items, err := newTestConnector().FetchAll(context.Background(), f.config(nil), []string{"ws-1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1", len(items))
	}
	if items[0].Metadata["failure_stage"] != "fetch_content" {
		t.Fatalf("failure_stage = %q, want fetch_content", items[0].Metadata["failure_stage"])
	}
	if string(items[0].Content) != "" {
		t.Fatalf("failure item should not include placeholder content, got %q", string(items[0].Content))
	}
}

func TestConnectorFetchAll_SyncsSingleDocumentSelection(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "doc-1", DocKey: "doc-key-1", Name: "One Doc", NodeType: "FILE", Category: "ALIDOC", ModifiedTime: "2026-01-15T10:00:00+08:00"},
		{NodeID: "doc-2", Name: "Other Doc", NodeType: "FILE", Category: "ALIDOC"},
	}
	f.blocksByNode["doc-key-1"] = []docBlock{{Text: "Only selected content"}}
	f.blocksByNode["doc-2"] = []docBlock{{Text: "Should not sync"}}

	items, err := newTestConnector().FetchAll(context.Background(), f.config(nil), []string{"ws-1:doc-1:document"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("len(items) = %d, want 1: %+v", len(items), items)
	}
	if items[0].ExternalID != "doc-1" || items[0].SourceResourceID != "ws-1:doc-1:document" {
		t.Fatalf("unexpected item: %+v", items[0])
	}
	if got := string(items[0].Content); !strings.Contains(got, "Only selected content") || strings.Contains(got, "Should not sync") {
		t.Fatalf("content = %q", got)
	}
}

func TestConnectorFetchIncremental_SkipsUnchangedAndDetectsDeleted(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "same-doc", Name: "Same", NodeType: "FILE", Category: "ALIDOC", ModifiedTime: "2026-01-15T10:00:00+08:00"},
		{NodeID: "new-doc", Name: "New", NodeType: "FILE", Category: "ALIDOC", ModifiedTime: "2026-01-16T10:00:00+08:00"},
	}
	f.blocksByNode["new-doc"] = []docBlock{{Text: "New content"}}

	prevSame := parseTime("2026-01-15T10:00:00+08:00")
	prevDeleted := time.Date(2026, 1, 14, 10, 0, 0, 0, time.UTC)
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"workspace_node_times": map[string]interface{}{
				"ws-1": map[string]interface{}{
					"same-doc":    prevSame,
					"deleted-doc": prevDeleted,
				},
			},
		},
	}

	items, _, err := newTestConnector().FetchIncremental(context.Background(), f.config([]string{"ws-1"}), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("len(items) = %d, want changed + deleted: %+v", len(items), items)
	}

	seenChanged := false
	seenDeleted := false
	for _, item := range items {
		if item.ExternalID == "new-doc" && strings.Contains(string(item.Content), "New content") {
			seenChanged = true
		}
		if item.ExternalID == "deleted-doc" && item.IsDeleted {
			seenDeleted = true
		}
	}
	if !seenChanged || !seenDeleted {
		t.Fatalf("seenChanged=%v seenDeleted=%v items=%+v", seenChanged, seenDeleted, items)
	}
}

func TestConnectorFetchIncremental_ContentFailureKeepsPreviousCursor(t *testing.T) {
	f := newFakeDingTalk(t)
	defer f.Close()
	f.nodesByParent["root-1"] = []WikiNode{
		{NodeID: "doc-1", Name: "Needs Retry", NodeType: "FILE", Category: "ALIDOC", ModifiedTime: "2026-01-16T10:00:00+08:00"},
	}
	f.blockStatus["doc-1"] = http.StatusForbidden

	prevTime := parseTime("2026-01-15T10:00:00+08:00")
	cursor := &types.SyncCursor{
		ConnectorCursor: map[string]interface{}{
			"workspace_node_times": map[string]interface{}{
				"ws-1": map[string]interface{}{"doc-1": prevTime},
			},
		},
	}

	items, next, err := newTestConnector().FetchIncremental(context.Background(), f.config([]string{"ws-1"}), cursor)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}
	if len(items) != 1 || items[0].Metadata["failure_stage"] != "fetch_content" {
		t.Fatalf("unexpected items: %+v", items)
	}
	rawTimes, ok := next.ConnectorCursor["workspace_node_times"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing workspace_node_times: %#v", next.ConnectorCursor)
	}
	wsTimes, ok := rawTimes["ws-1"].(map[string]interface{})
	if !ok {
		t.Fatalf("missing ws-1 times: %#v", rawTimes)
	}
	gotRaw, ok := wsTimes["doc-1"].(string)
	if !ok {
		t.Fatalf("doc-1 cursor = %#v, want RFC3339 string", wsTimes["doc-1"])
	}
	got := parseTime(gotRaw)
	if !got.Equal(prevTime) {
		t.Fatalf("doc-1 cursor = %v, want previous %v", got, prevTime)
	}
}

func TestRenderBlocksMarkdown_OfficialBlockShapes(t *testing.T) {
	var blocks []docBlock
	body := `[
		{
			"blockId": "h1",
			"blockType": "heading",
			"heading": {"level": 2, "elements": [{"text": {"content": "Deploy"}}]}
		},
		{
			"blockId": "p1",
			"blockType": "paragraph",
			"paragraph": {
				"elements": [
					{"text": {"content": "Read "}},
					{"link": {"href": "https://example.com/runbook", "text": "runbook"}}
				]
			}
		},
		{
			"blockId": "t1",
			"blockType": "table",
			"table": {"cells": [["Name", "Status"], ["API", "OK"]]}
		},
		{
			"blockId": "i1",
			"blockType": "image",
			"image": {"src": "https://example.com/diagram.png", "alt": "Diagram"}
		}
	]`
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}

	got := renderBlocksMarkdown("Doc", blocks)
	for _, want := range []string{
		"# Doc",
		"## Deploy",
		"Read [runbook](https://example.com/runbook)",
		"| Name | Status |",
		"| --- | --- |",
		"| API | OK |",
		"![Diagram](https://example.com/diagram.png)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q:\n%s", want, got)
		}
	}
}

func TestRenderBlocksMarkdown_LiveResponseShapes(t *testing.T) {
	var blocks []docBlock
	// Preserve the field nesting and type/level values observed from the
	// dedicated live tenant while replacing document IDs and content.
	body := `[
		{
			"id": "paragraph",
			"blockType": "paragraph",
			"paragraph": {"text": "Live paragraph"}
		},
		{
			"id": "heading-2",
			"blockType": "heading",
			"heading": {"level": "heading-2", "text": "Second level"}
		},
		{
			"id": "heading-3",
			"blockType": "heading",
			"heading": {"level": "heading-3", "text": "Third level"}
		},
		{
			"id": "unordered-list",
			"blockType": "unorderedList",
			"unorderedList": {"text": "Bullet item"}
		},
		{
			"id": "ordered-list",
			"blockType": "orderedList",
			"orderedList": {"text": "Numbered item"}
		},
		{
			"id": "table",
			"blockType": "table",
			"table": {"cells": [["Field", "Value"], ["marker", "DINGTALK_TABLE_MARKER"]]}
		}
	]`
	if err := json.Unmarshal([]byte(body), &blocks); err != nil {
		t.Fatalf("unmarshal blocks: %v", err)
	}

	got := renderBlocksMarkdown("Doc", blocks)
	for _, want := range []string{
		"Live paragraph",
		"## Second level",
		"### Third level",
		"- Bullet item",
		"1. Numbered item",
		"| Field | Value |",
		"| marker | DINGTALK_TABLE_MARKER |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("markdown missing %q for live DingTalk block shape:\n%s", want, got)
		}
	}
}

func TestConnectorFetchIncremental_NoResourceIDs(t *testing.T) {
	_, _, err := NewConnector().FetchIncremental(context.Background(), makeDingTalkConfig("id", "secret", "", nil), nil)
	if err == nil {
		t.Fatal("expected error when no resource IDs configured")
	}
}

func TestConnectorImplementsInterface(t *testing.T) {
	var _ interface {
		Type() string
		Validate(context.Context, *types.DataSourceConfig) error
		ListResources(context.Context, *types.DataSourceConfig, string) ([]types.Resource, error)
		FetchAll(context.Context, *types.DataSourceConfig, []string) ([]types.FetchedItem, error)
		FetchIncremental(context.Context, *types.DataSourceConfig, *types.SyncCursor) ([]types.FetchedItem, *types.SyncCursor, error)
		ResolveResourceAncestors(context.Context, *types.DataSourceConfig, []string) ([]string, error)
	} = NewConnector()
}
