package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sort"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseConfigAcceptsOfficialNamesAndAliases(t *testing.T) {
	t.Run("official console names", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"client_id":        "ding-client",
				"client_secret":    "ding-secret",
				"operator_user_id": "manager001",
				"base_url":         "https://example.test/",
			},
		})
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if cfg.AppKey != "ding-client" || cfg.AppSecret != "ding-secret" {
			t.Fatalf("unexpected app credentials: %#v", cfg)
		}
		if cfg.OperatorUserID != "manager001" {
			t.Fatalf("unexpected operator user id: %q", cfg.OperatorUserID)
		}
		if got := cfg.GetBaseURL(); got != "https://example.test" {
			t.Fatalf("normalized base url = %q", got)
		}
	})

	t.Run("legacy aliases and direct union id", func(t *testing.T) {
		cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_key":           "app-key",
				"app_secret":        "app-secret",
				"operator_union_id": "union-001",
			},
		})
		if err != nil {
			t.Fatalf("parse config: %v", err)
		}
		if cfg.AppKey != "app-key" || cfg.AppSecret != "app-secret" {
			t.Fatalf("unexpected app credentials: %#v", cfg)
		}
		if cfg.OperatorUnionID != "union-001" {
			t.Fatalf("unexpected union id: %q", cfg.OperatorUnionID)
		}
	})
}

func TestConnectorValidateAndListResources(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	cfg := newTestConfig(server.URL)
	ctx := context.Background()

	if err := connector.Validate(ctx, cfg); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if got := atomic.LoadInt32(&server.userDetailCalls); got != 1 {
		t.Fatalf("expected user detail to resolve union id once, got %d calls", got)
	}

	spaces, err := connector.ListResources(ctx, cfg, "")
	if err != nil {
		t.Fatalf("list root resources: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("expected one workspace, got %d: %#v", len(spaces), spaces)
	}
	if spaces[0].ExternalID != "ws1:root" || spaces[0].Type != "workspace" || !spaces[0].HasChildren {
		t.Fatalf("unexpected workspace resource: %#v", spaces[0])
	}

	children, err := connector.ListResources(ctx, cfg, "ws1:root")
	if err != nil {
		t.Fatalf("list child resources: %v", err)
	}
	if got := resourceIDs(children); strings.Join(got, ",") != "ws1:doc1,ws1:folder1,ws1:sheet1" {
		t.Fatalf("unexpected child resources: %v", got)
	}
}

func TestResolveResourceAncestorsForNestedSelection(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	ancestors, err := connector.ResolveResourceAncestors(
		context.Background(),
		newTestConfig(server.URL),
		[]string{"ws1:doc2"},
	)
	if err != nil {
		t.Fatalf("resolve ancestors: %v", err)
	}
	sort.Strings(ancestors)
	if got := strings.Join(ancestors, ","); got != "ws1:folder1,ws1:root" {
		t.Fatalf("unexpected ancestors: %v", ancestors)
	}
}

func TestFetchAllRecursivelyRendersMarkdownAndSkipsUnsupportedNodes(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected two DingTalk docs, got %d: %#v", len(items), items)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
		if item.ContentType != "text/markdown" {
			t.Fatalf("content type for %s = %q", item.ExternalID, item.ContentType)
		}
		if !strings.HasSuffix(item.FileName, ".md") {
			t.Fatalf("expected markdown filename for %s, got %q", item.ExternalID, item.FileName)
		}
		if item.Metadata["channel"] != types.ChannelDingtalk {
			t.Fatalf("missing dingtalk channel metadata: %#v", item.Metadata)
		}
	}

	if got := string(byID["ws1:doc1"].Content); !strings.Contains(got, "# Architecture") ||
		!strings.Contains(got, "Hello DingTalk") {
		t.Fatalf("doc1 markdown not rendered as expected:\n%s", got)
	}
	if got := string(byID["ws1:doc2"].Content); !strings.Contains(got, "Nested document") {
		t.Fatalf("doc2 markdown not rendered as expected:\n%s", got)
	}
	if _, ok := byID["ws1:sheet1"]; ok {
		t.Fatalf("unsupported sheet node should be skipped")
	}
}

func TestFetchAllDeduplicatesOverlappingSelections(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root", "ws1:doc1"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}

	seen := map[string]int{}
	for _, item := range items {
		seen[item.ExternalID]++
	}
	if len(items) != 2 || seen["ws1:doc1"] != 1 || seen["ws1:doc2"] != 1 {
		t.Fatalf("expected unique docs from overlapping selections, got items=%#v counts=%#v", items, seen)
	}
}

func TestMarkdownFileNameNormalizesDocumentExtensions(t *testing.T) {
	tests := map[string]string{
		"WeKnora测试.adoc":    "WeKnora测试.md",
		"guide.asciidoc":    "guide.md",
		"already.md":        "already.md",
		"notes.markdown":    "notes.md",
		"bad/name:doc.adoc": "bad_name_doc.md",
		"   ":               "untitled.md",
	}

	for title, want := range tests {
		if got := markdownFileName(title); got != want {
			t.Fatalf("markdownFileName(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestFetchIncrementalSkipsUnchangedAndDetectsDeletion(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	cfg := newTestConfig(server.URL)
	cfg.ResourceIDs = []string{"ws1:root"}
	ctx := context.Background()

	items, cursor, err := connector.FetchIncremental(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("initial incremental fetch: %v", err)
	}
	if len(items) != 2 {
		t.Fatalf("expected initial fetch to return two docs, got %d", len(items))
	}
	if cursor == nil || cursor.ConnectorCursor == nil {
		t.Fatalf("expected connector cursor")
	}

	items, cursor, err = connector.FetchIncremental(ctx, cfg, cursor)
	if err != nil {
		t.Fatalf("unchanged incremental fetch: %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("expected unchanged fetch to skip all docs, got %#v", items)
	}

	atomic.StoreInt32(&server.doc2Deleted, 1)
	items, _, err = connector.FetchIncremental(ctx, cfg, cursor)
	if err != nil {
		t.Fatalf("delete incremental fetch: %v", err)
	}
	if len(items) != 1 || !items[0].IsDeleted || items[0].ExternalID != "ws1:doc2" {
		t.Fatalf("expected doc2 deletion placeholder, got %#v", items)
	}
}

func TestFetchIncrementalDetectsContentChangeWithUnchangedModifiedTime(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	cfg := newTestConfig(server.URL)
	cfg.ResourceIDs = []string{"ws1:root"}
	ctx := context.Background()

	_, cursor, err := connector.FetchIncremental(ctx, cfg, nil)
	if err != nil {
		t.Fatalf("initial incremental fetch: %v", err)
	}

	atomic.StoreInt32(&server.doc1Revision, 1)
	items, _, err := connector.FetchIncremental(ctx, cfg, cursor)
	if err != nil {
		t.Fatalf("content-changed incremental fetch: %v", err)
	}
	if len(items) != 1 || items[0].ExternalID != "ws1:doc1" {
		t.Fatalf("expected only doc1 to be resynced, got %#v", items)
	}
	if !strings.Contains(string(items[0].Content), "Hello DingTalk updated") {
		t.Fatalf("expected updated content, got:\n%s", string(items[0].Content))
	}
}

func TestFetchAllReturnsErrorItemForEmptyRenderedContent(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()
	atomic.StoreInt32(&server.doc1Empty, 1)

	connector := NewConnector()
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	doc := byID["ws1:doc1"]
	if doc.Metadata["error"] == "" || !strings.Contains(doc.Metadata["error"], "empty") {
		t.Fatalf("expected empty-content error metadata, got %#v", doc)
	}
	if len(doc.Content) != 0 {
		t.Fatalf("empty-content item should not carry normal content, got %q", string(doc.Content))
	}
}

func TestClientDoRejectsBusinessErrorResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"success": false,
			"code":    "InvalidParameter",
			"message": "bad operator id",
		})
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	var out map[string]interface{}
	err = newClient(&Config{}).do(req, &out)
	if err == nil {
		t.Fatalf("expected DingTalk business error")
	}
	if !strings.Contains(err.Error(), "InvalidParameter") || !strings.Contains(err.Error(), "bad operator id") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestClientDoRetriesTransientStatusResponses(t *testing.T) {
	for _, status := range []int{http.StatusTooManyRequests, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			var attempts int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if atomic.AddInt32(&attempts, 1) == 1 {
					w.Header().Set("x-acs-request-id", "req-retry")
					w.WriteHeader(status)
					_, _ = w.Write([]byte(`{"message":"temporary error"}`))
					return
				}
				writeJSON(w, map[string]string{"ok": "true"})
			}))
			defer server.Close()

			req, err := http.NewRequest(http.MethodGet, server.URL, nil)
			if err != nil {
				t.Fatalf("new request: %v", err)
			}

			var out map[string]string
			if err := newClient(&Config{}).do(req, &out); err != nil {
				t.Fatalf("expected retry success, got %v", err)
			}
			if got := atomic.LoadInt32(&attempts); got != 2 {
				t.Fatalf("expected two attempts, got %d", got)
			}
			if out["ok"] != "true" {
				t.Fatalf("unexpected decoded response: %#v", out)
			}
		})
	}
}

func TestClientDoDoesNotRetryBusinessErrorAndIncludesRequestID(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&attempts, 1)
		writeJSON(w, map[string]interface{}{
			"success":    false,
			"code":       "99991672",
			"message":    "Access denied. One of the following scopes is required: [Wiki.Node.Read]",
			"request_id": "req-business",
		})
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	var out map[string]interface{}
	err = newClient(&Config{}).do(req, &out)
	if err == nil {
		t.Fatalf("expected DingTalk business error")
	}
	if got := atomic.LoadInt32(&attempts); got != 1 {
		t.Fatalf("business errors should not be retried, got %d attempts", got)
	}
	for _, want := range []string{"99991672", "Wiki.Node.Read", "req-business"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestClientDoAddsPermissionSuggestionForScopeErrors(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"success": false,
			"code":    "99991672",
			"message": "Access denied. One of the following scopes is required: [Wiki.Workspace.Read]",
		})
	}))
	defer server.Close()

	req, err := http.NewRequest(http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}

	var out map[string]interface{}
	err = newClient(&Config{}).do(req, &out)
	if err == nil {
		t.Fatalf("expected DingTalk business error")
	}
	for _, want := range []string{"99991672", "Wiki.Workspace.Read", "check DingTalk app API permissions"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q: %v", want, err)
		}
	}
}

func TestRenderBlocksMarkdown(t *testing.T) {
	md := renderBlocksMarkdown([]docBlock{
		{Type: "HEADING", Heading: blockText{Text: "Title", Level: 2}},
		{Type: "PARAGRAPH", Paragraph: blockText{Text: "A paragraph"}},
		{Type: "BULLET", Bullet: blockText{Text: "First"}},
		{Type: "ORDERED", Ordered: blockText{Text: "Second"}},
		{Type: "BLOCKQUOTE", Blockquote: blockText{Text: "Quote"}},
	})

	for _, want := range []string{"## Title", "A paragraph", "- First", "1. Second", "> Quote"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q:\n%s", want, md)
		}
	}
}

func TestBlockListResponseSupportsResultData(t *testing.T) {
	var resp blockListResponse
	if err := json.Unmarshal([]byte(`{
		"success": true,
		"result": {
			"data": [
				{"blockType": "paragraph", "paragraph": {"text": "Real DingTalk content"}}
			]
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	blocks, _, _ := resp.items()
	if len(blocks) != 1 {
		t.Fatalf("expected one result.data block, got %d", len(blocks))
	}
	if got := renderBlocksMarkdown(blocks); !strings.Contains(got, "Real DingTalk content") {
		t.Fatalf("markdown did not include result.data content:\n%s", got)
	}
}

func newTestConfig(baseURL string) *types.DataSourceConfig {
	return &types.DataSourceConfig{
		Type: types.ConnectorTypeDingTalk,
		Credentials: map[string]interface{}{
			"client_id":        "ding-client",
			"client_secret":    "ding-secret",
			"operator_user_id": "manager001",
			"base_url":         baseURL,
		},
	}
}

func resourceIDs(resources []types.Resource) []string {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.ExternalID)
	}
	sort.Strings(ids)
	return ids
}

type fakeDingTalkServer struct {
	*httptest.Server
	userDetailCalls int32
	doc2Deleted     int32
	doc1Revision    int32
	doc1Empty       int32
}

func newFakeDingTalkServer(t *testing.T) *fakeDingTalkServer {
	t.Helper()

	fake := &fakeDingTalkServer{}
	mux := http.NewServeMux()
	mux.HandleFunc("/v1.0/oauth2/accessToken", fake.handleAccessToken)
	mux.HandleFunc("/topapi/v2/user/get", fake.handleUserDetail)
	mux.HandleFunc("/v2.0/wiki/workspaces", fake.handleWorkspaces)
	mux.HandleFunc("/v2.0/wiki/nodes", fake.handleNodes)
	mux.HandleFunc("/v2.0/wiki/nodes/", fake.handleNodeDetail)
	mux.HandleFunc("/v1.0/doc/suites/documents/", fake.handleBlocks)

	fake.Server = httptest.NewServer(mux)
	return fake
}

func (s *fakeDingTalkServer) handleAccessToken(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	writeJSON(w, map[string]interface{}{
		"accessToken": "tenant-token",
		"expireIn":    7200,
	})
}

func (s *fakeDingTalkServer) handleUserDetail(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if err := r.ParseForm(); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	atomic.AddInt32(&s.userDetailCalls, 1)
	if got := r.Form.Get("userid"); got != "manager001" {
		http.Error(w, "unexpected userid "+got, http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"errcode": 0,
		"errmsg":  "ok",
		"result": map[string]interface{}{
			"unionid": "union-001",
		},
	})
}

func (s *fakeDingTalkServer) handleWorkspaces(w http.ResponseWriter, r *http.Request) {
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	writeJSON(w, map[string]interface{}{
		"workspaces": []map[string]interface{}{
			{
				"workspaceId":  "ws1",
				"rootNodeId":   "root",
				"name":         "Engineering",
				"description":  "Engineering docs",
				"url":          "https://dingtalk.example/wiki/ws1",
				"modifiedTime": "2026-06-30T10:00:00Z",
			},
		},
	})
}

func (s *fakeDingTalkServer) handleNodes(w http.ResponseWriter, r *http.Request) {
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	parentID := r.URL.Query().Get("parentNodeId")
	nodes := []map[string]interface{}{}
	switch parentID {
	case "root":
		nodes = append(nodes,
			nodeJSON("doc1", "Architecture", "FILE", "ALIDOC", "root", false, "2026-06-30T10:00:00Z"),
			nodeJSON("folder1", "Guides", "FOLDER", "", "root", true, "2026-06-30T10:01:00Z"),
			nodeJSON("sheet1", "Budget", "FILE", "AXLS", "root", false, "2026-06-30T10:02:00Z"),
		)
	case "folder1":
		if atomic.LoadInt32(&s.doc2Deleted) == 0 {
			nodes = append(nodes, nodeJSON("doc2", "Nested", "FILE", "ALIDOC", "folder1", false, "2026-06-30T10:03:00Z"))
		}
	}
	writeJSON(w, map[string]interface{}{"nodes": nodes})
}

func (s *fakeDingTalkServer) handleNodeDetail(w http.ResponseWriter, r *http.Request) {
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	nodeID := strings.TrimPrefix(r.URL.Path, "/v2.0/wiki/nodes/")
	var node map[string]interface{}
	switch nodeID {
	case "root":
		node = nodeJSON("root", "Engineering", "FOLDER", "", "", true, "2026-06-30T10:00:00Z")
	case "folder1":
		node = nodeJSON("folder1", "Guides", "FOLDER", "", "root", true, "2026-06-30T10:01:00Z")
	case "doc1":
		node = nodeJSON("doc1", "Architecture", "FILE", "ALIDOC", "root", false, "2026-06-30T10:00:00Z")
	case "doc2":
		node = nodeJSON("doc2", "Nested", "FILE", "ALIDOC", "folder1", false, "2026-06-30T10:03:00Z")
	default:
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]interface{}{"node": node})
}

func (s *fakeDingTalkServer) handleBlocks(w http.ResponseWriter, r *http.Request) {
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	docKey := strings.TrimPrefix(r.URL.Path, "/v1.0/doc/suites/documents/")
	docKey = strings.TrimSuffix(docKey, "/blocks")

	var blocks []map[string]interface{}
	switch docKey {
	case "doc1":
		if atomic.LoadInt32(&s.doc1Empty) == 1 {
			writeJSON(w, map[string]interface{}{"blocks": blocks})
			return
		}
		text := "Hello DingTalk"
		if atomic.LoadInt32(&s.doc1Revision) == 1 {
			text = "Hello DingTalk updated"
		}
		blocks = []map[string]interface{}{
			{"type": "HEADING", "heading": map[string]interface{}{"level": 1, "text": "Architecture"}},
			{"type": "PARAGRAPH", "paragraph": map[string]interface{}{"text": text}},
			{"type": "BULLET", "bullet": map[string]interface{}{"text": "A bullet"}},
		}
	case "doc2":
		blocks = []map[string]interface{}{
			{"type": "PARAGRAPH", "paragraph": map[string]interface{}{"text": "Nested document"}},
		}
	default:
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]interface{}{"blocks": blocks})
}

func nodeJSON(id, name, nodeType, category, parent string, hasChildren bool, modified string) map[string]interface{} {
	return map[string]interface{}{
		"nodeId":       id,
		"name":         name,
		"title":        name,
		"type":         nodeType,
		"category":     category,
		"docKey":       id,
		"parentNodeId": parent,
		"hasChildren":  hasChildren,
		"url":          "https://dingtalk.example/wiki/" + id,
		"modifiedTime": modified,
	}
}

func assertOperatorID(query url.Values) error {
	if got := query.Get("operatorId"); got != "union-001" {
		return fmt.Errorf("unexpected operatorId %s", got)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
