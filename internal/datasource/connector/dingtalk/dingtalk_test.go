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
	if got := resourceIDs(children); strings.Join(got, ",") != "ws1:doc1,ws1:file1,ws1:folder1,ws1:sheet1" {
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

func TestFetchAllRecursivelyFetchesSupportedResourcesAndSkipsUnsupportedNodes(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := NewConnector()
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}
	if len(items) != 3 {
		t.Fatalf("expected two online docs and one uploaded file, got %d: %#v", len(items), items)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
		if item.Metadata["channel"] != types.ChannelDingtalk {
			t.Fatalf("missing dingtalk channel metadata: %#v", item.Metadata)
		}
	}

	if byID["ws1:doc1"].ContentType != "text/markdown" ||
		!strings.HasSuffix(byID["ws1:doc1"].FileName, ".md") ||
		byID["ws1:doc1"].Metadata["fetcher"] != "block" ||
		byID["ws1:doc1"].Metadata["fidelity"] != "best_effort" {
		t.Fatalf("unexpected online doc item: %#v", byID["ws1:doc1"])
	}
	if got := string(byID["ws1:doc1"].Content); !strings.Contains(got, "docx:doc1:v1") ||
		!strings.Contains(got, "![架构图](https://example.com/arch.png)") {
		t.Fatalf("doc1 markdown content = %q", got)
	}
	if got := string(byID["ws1:doc2"].Content); !strings.Contains(got, "docx:doc2:v1") {
		t.Fatalf("doc2 markdown content = %q", got)
	}
	if byID["ws1:file1"].ContentType != "application/octet-stream" ||
		byID["ws1:file1"].FileName != "Design.pdf" ||
		string(byID["ws1:file1"].Content) != "fake-pdf-content" ||
		byID["ws1:file1"].Metadata["fetcher"] != "file" ||
		byID["ws1:file1"].Metadata["category"] != "PDF" ||
		byID["ws1:file1"].Metadata["dentry_id"] != "798001" ||
		byID["ws1:file1"].Metadata["dentry_uuid"] != "file1" {
		t.Fatalf("unexpected uploaded file item: %#v", byID["ws1:file1"])
	}
	if _, ok := byID["ws1:sheet1"]; ok {
		t.Fatalf("unsupported sheet node should be skipped")
	}
	if got := atomic.LoadInt32(&server.blockCalls); got != 2 {
		t.Fatalf("online docs should use block fallback by default, got %d block calls", got)
	}
	if got := atomic.LoadInt32(&server.downloadInfoCalls); got != 1 {
		t.Fatalf("uploaded files should use download info API, got %d calls", got)
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
	if len(items) != 3 || seen["ws1:doc1"] != 1 || seen["ws1:doc2"] != 1 || seen["ws1:file1"] != 1 {
		t.Fatalf("expected unique docs from overlapping selections, got items=%#v counts=%#v", items, seen)
	}
}

func TestFetchAllCanUseConfiguredExporterBeforeOpenAPIFallback(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	connector := &Connector{
		contentFetchers: []dingtalkContentFetcher{
			testExportFetcher{},
			fileFetcher{},
			blockFetcher{},
		},
	}
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	if byID["ws1:doc1"].Metadata["fetcher"] != "export" ||
		string(byID["ws1:doc1"].Content) != "exported:Architecture" {
		t.Fatalf("expected configured export fetcher to handle online doc, got %#v", byID["ws1:doc1"])
	}
	if byID["ws1:file1"].Metadata["fetcher"] != "file" {
		t.Fatalf("expected uploaded file to fall back to file fetcher, got %#v", byID["ws1:file1"])
	}
	if got := atomic.LoadInt32(&server.blockCalls); got != 0 {
		t.Fatalf("configured export fetcher should run before block API, got %d block calls", got)
	}
	if got := atomic.LoadInt32(&server.downloadInfoCalls); got != 1 {
		t.Fatalf("uploaded file should still use download info API, got %d calls", got)
	}
}

func TestParseExportFinishEventAcceptsHTTPAndStreamPayloads(t *testing.T) {
	tests := map[string]struct {
		payload    string
		taskID     string
		dentryUUID string
	}{
		"http": {
			payload: `{
			"EventType": "dingdoc_export_finish",
			"EventTime": 1663143335567,
			"eventId": "evt-http",
			"biz_data": {
				"eventId": "evt-http-inner",
				"extension": "adoc",
				"format": "markdown",
				"url": "https://example.com/export/doc.md",
				"success": true,
				"dentryUuid": "doc-http",
				"name": "HTTP 文档",
				"taskId": "task-http"
			}
		}`,
			taskID:     "task-http",
			dentryUUID: "doc-http",
		},
		"http-string": {
			payload: `{
			"EventType": "dingdoc_export_finish",
			"EventTime": 1663143335567,
			"eventId": "evt-http-string",
			"biz_data": "{\"extension\":\"adoc\",\"format\":\"markdown\",\"url\":\"https://example.com/export/string.md\",\"success\":true,\"dentryUuid\":\"doc-http-string\",\"name\":\"HTTP String 文档\",\"taskId\":\"task-http-string\"}"
		}`,
			taskID:     "task-http-string",
			dentryUUID: "doc-http-string",
		},
		"stream": {
			payload: `{
			"eventType": "dingdoc_export_finish",
			"eventId": "evt-stream",
			"eventBornTime": 1683533823336,
			"data": {
				"bizData": {
					"extension": "adoc",
					"format": "markdown",
					"url": "https://example.com/export/stream.md",
					"success": true,
					"dentryUuid": "doc-stream",
					"name": "Stream 文档",
					"taskId": "task-stream"
				}
			}
		}`,
			taskID:     "task-stream",
			dentryUUID: "doc-stream",
		},
		"stream-data": {
			payload: `{
			"eventType": "doc_content_export_result",
			"eventId": "evt-stream-data",
			"eventBornTime": 1683533823336,
			"data": {
				"extension": "adoc",
				"format": "markdown",
				"url": "https://example.com/export/stream-data.md",
				"success": true,
				"dentryUuid": "doc-stream-data",
				"name": "Stream Data 文档",
				"taskId": "task-stream-data"
			}
		}`,
			taskID:     "task-stream-data",
			dentryUUID: "doc-stream-data",
		},
	}

	for name, tt := range tests {
		t.Run(name, func(t *testing.T) {
			event, err := ParseExportFinishEvent([]byte(tt.payload))
			if err != nil {
				t.Fatalf("parse export finish event: %v", err)
			}
			if !isExportFinishEventType(event.EventType) {
				t.Fatalf("unexpected event type: %#v", event)
			}
			if event.TaskID != tt.taskID || event.DentryUUID != tt.dentryUUID {
				t.Fatalf("unexpected task identity: %#v", event)
			}
			if event.Format != "markdown" || event.Extension != "adoc" || !event.Success {
				t.Fatalf("unexpected export result: %#v", event)
			}
			if event.URL == "" || event.Name == "" {
				t.Fatalf("missing url or name: %#v", event)
			}
		})
	}
}

func TestClientSubmitMarkdownExportStartsOfficialExportTask(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	cfg := &Config{
		AppKey:          "ding-client",
		AppSecret:       "ding-secret",
		OperatorUnionID: "union-001",
		BaseURL:         server.URL,
	}
	taskID, err := newClient(cfg).SubmitMarkdownExport(context.Background(), "doc1")
	if err != nil {
		t.Fatalf("submit markdown export: %v", err)
	}
	if taskID != "task-doc1" {
		t.Fatalf("task id = %q, want task-doc1", taskID)
	}
	if got := atomic.LoadInt32(&server.exportCalls); got != 1 {
		t.Fatalf("expected one export call, got %d", got)
	}
}

func TestDingTalkStatusErrorSuggestsEnterpriseVerification(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusForbidden}
	body := []byte(`{
		"requestid": "019F4176-E640-7924-A7F6-52E3A0BFAAD8",
		"code": "orgAuthLevelNotEnough",
		"message": "auth level of org is not enough"
	}`)

	err := dingtalkStatusError(resp, body)
	if err == nil {
		t.Fatal("expected status error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "enterprise verification") ||
		!strings.Contains(msg, "uploaded file download") {
		t.Fatalf("missing enterprise verification suggestion: %s", msg)
	}
}

func TestDingTalkStatusErrorSuggestsGrayScopeFallbackForAIAssistantScope(t *testing.T) {
	resp := &http.Response{StatusCode: http.StatusForbidden}
	body := []byte(`{
		"requestid": "019F419C-8737-7A5D-B6B8-4DB544A5BC90",
		"code": "Forbidden.AccessDenied.AccessTokenPermissionDenied",
		"message": "应用尚未开通所需的权限：[Document.AIAssistant.Read]"
	}`)

	err := dingtalkStatusError(resp, body)
	if err == nil {
		t.Fatal("expected status error")
	}
	msg := err.Error()
	if !strings.Contains(msg, "Document.AIAssistant.Read") ||
		!strings.Contains(msg, "gray/allowlisted") ||
		!strings.Contains(msg, "uploaded file download") ||
		!strings.Contains(msg, "block reading") {
		t.Fatalf("missing gray scope fallback suggestion: %s", msg)
	}
}

func TestFetchAllCanSubmitOfficialExportTasksWhenEnabled(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()

	cfg := newTestConfig(server.URL)
	cfg.Settings = map[string]interface{}{
		"online_doc_fetcher": "export",
	}

	items, err := NewConnector().FetchAll(context.Background(), cfg, []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	for _, id := range []string{"ws1:doc1", "ws1:doc2"} {
		item := byID[id]
		if item.Metadata["fetcher"] != "export" ||
			item.Metadata["export_status"] != "pending" ||
			item.Metadata["export_task_id"] == "" ||
			item.Metadata["dentry_uuid"] == "" {
			t.Fatalf("expected pending export item for %s, got %#v", id, item)
		}
		if len(item.Content) != 0 || item.FileName == "" {
			t.Fatalf("pending export item should carry filename but no content, got %#v", item)
		}
	}
	if byID["ws1:file1"].Metadata["fetcher"] != "file" {
		t.Fatalf("uploaded file should still use file fetcher, got %#v", byID["ws1:file1"])
	}
	if got := atomic.LoadInt32(&server.blockCalls); got != 0 {
		t.Fatalf("export mode should not call block fallback, got %d block calls", got)
	}
	if got := atomic.LoadInt32(&server.exportCalls); got != 2 {
		t.Fatalf("expected two export calls, got %d", got)
	}
}

func TestFetchAllFallsBackToBlocksWhenOfficialExportScopeIsUnavailable(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()
	atomic.StoreInt32(&server.exportPermissionDenied, 1)

	cfg := newTestConfig(server.URL)
	cfg.Settings = map[string]interface{}{
		"online_doc_fetcher": "export",
	}

	items, err := NewConnector().FetchAll(context.Background(), cfg, []string{"ws1:doc1"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected one item, got %#v", items)
	}
	item := items[0]
	if item.Metadata["fetcher"] != "block" {
		t.Fatalf("expected block fallback, got %#v", item)
	}
	if !strings.Contains(string(item.Content), "docx:doc1:v1") {
		t.Fatalf("expected block content after fallback, got %q", string(item.Content))
	}
	if got := atomic.LoadInt32(&server.exportCalls); got != 1 {
		t.Fatalf("expected one export attempt, got %d", got)
	}
	if got := atomic.LoadInt32(&server.blockCalls); got != 1 {
		t.Fatalf("expected one block fallback call, got %d", got)
	}
}

func TestMarkdownFileNameNormalizesDocumentExtensions(t *testing.T) {
	tests := map[string]string{
		"WeKnora测试.adoc": "WeKnora测试.md",
		"guide.asciidoc": "guide.md",
		"already.docx":   "already.md",
		"notes.md":       "notes.md",
		"   ":            "untitled.md",
	}

	for title, want := range tests {
		if got := markdownFileName(title); got != want {
			t.Fatalf("markdownFileName(%q) = %q, want %q", title, got, want)
		}
	}
}

func TestRenderBlocksMarkdownPreservesNestedTextAndImages(t *testing.T) {
	var resp blockListResponse
	if err := json.Unmarshal([]byte(`{
		"result": {
			"data": [
				{
					"blockType": "paragraph",
					"paragraph": {
						"elements": [
							{"textRun": {"content": "第一段"}},
							{"text": "，包含复杂文本"}
						]
					}
				},
				{
					"blockType": "callout",
					"children": [
						{
							"blockType": "paragraph",
							"paragraph": {"richTextElements": [{"content": "高亮块里的文字"}]}
						}
					]
				},
				{
					"blockType": "image",
					"image": {
						"alt": "架构图",
						"url": "https://example.com/arch.png"
					}
				}
			]
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal block response: %v", err)
	}

	got := renderBlocksMarkdown(resp.blocks())
	for _, want := range []string{
		"第一段，包含复杂文本",
		"> 高亮块里的文字",
		"![架构图](https://example.com/arch.png)",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, got)
		}
	}
}

func TestRenderBlocksMarkdownPreservesOfficialInlineChildren(t *testing.T) {
	var resp blockListResponse
	if err := json.Unmarshal([]byte(`{
		"result": {
			"data": [
				{
					"blockType": "paragraph",
					"paragraph": {},
					"children": [
						{"text": "图片前"},
						{"elementType": "image", "properties": {"src": "https://example.com/pic.jpg"}},
						{
							"elementType": "link",
							"properties": {"href": "https://www.dingtalk.com"},
							"children": [{"text": "钉钉官网"}]
						}
					]
				},
				{
					"blockType": "heading",
					"heading": {"level": 2},
					"children": [{"text": "二级标题"}]
				}
			]
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal block response: %v", err)
	}

	got := renderBlocksMarkdown(resp.blocks())
	for _, want := range []string{
		"图片前![image](https://example.com/pic.jpg)[钉钉官网](https://www.dingtalk.com)",
		"## 二级标题",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, got)
		}
	}
}

func TestRenderBlocksMarkdownPreservesOfficialListsAndTables(t *testing.T) {
	var resp blockListResponse
	if err := json.Unmarshal([]byte(`{
		"result": {
			"data": [
				{
					"blockType": "unorderedList",
					"unorderedList": {"list": {"level": 0}},
					"children": [{"text": "无序列表项"}]
				},
				{
					"blockType": "orderedList",
					"orderedList": {"list": {"level": 0}},
					"children": [{"text": "有序列表项"}]
				},
				{
					"blockType": "table",
					"table": {
						"cells": [
							["功能", "状态"],
							["图片", "已同步"]
						]
					}
				}
			]
		}
	}`), &resp); err != nil {
		t.Fatalf("unmarshal block response: %v", err)
	}

	got := renderBlocksMarkdown(resp.blocks())
	for _, want := range []string{
		"- 无序列表项",
		"1. 有序列表项",
		"| 功能 | 状态 |",
		"| 图片 | 已同步 |",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("rendered markdown missing %q:\n%s", want, got)
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
	if len(items) != 3 {
		t.Fatalf("expected initial fetch to return two docs and one file, got %d", len(items))
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
	if got := string(items[0].Content); !strings.Contains(got, "docx:doc1:v2") {
		t.Fatalf("expected updated markdown content, got %q", got)
	}
}

func TestFetchAllReturnsErrorItemForFailedBlockQuery(t *testing.T) {
	server := newFakeDingTalkServer(t)
	defer server.Close()
	atomic.StoreInt32(&server.doc1BlockFailed, 1)

	connector := &Connector{contentFetchers: []dingtalkContentFetcher{fileFetcher{}, blockFetcher{}}}
	items, err := connector.FetchAll(context.Background(), newTestConfig(server.URL), []string{"ws1:root"})
	if err != nil {
		t.Fatalf("fetch all: %v", err)
	}

	byID := map[string]types.FetchedItem{}
	for _, item := range items {
		byID[item.ExternalID] = item
	}
	doc := byID["ws1:doc1"]
	if doc.Metadata["error"] == "" || !strings.Contains(doc.Metadata["error"], "blocks") {
		t.Fatalf("expected block query error metadata, got %#v", doc)
	}
	if len(doc.Content) != 0 {
		t.Fatalf("failed block query item should not carry normal content, got %q", string(doc.Content))
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
	userDetailCalls        int32
	blockCalls             int32
	downloadInfoCalls      int32
	exportCalls            int32
	doc2Deleted            int32
	doc1Revision           int32
	doc1BlockFailed        int32
	exportPermissionDenied int32
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
	mux.HandleFunc("/v2.0/doc/dentries/", fake.handleQueryDentryID)
	mux.HandleFunc("/v1.0/doc/", fake.handleDocExport)
	mux.HandleFunc("/v1.0/doc/suites/documents/", fake.handleBlocks)
	mux.HandleFunc("/v1.0/storage/spaces/", fake.handleStorageDownloadInfo)
	mux.HandleFunc("/download/file1", fake.handleFileDownload)

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
			nodeJSON("file1", "Design.pdf", "FILE", "PDF", "root", false, "2026-06-30T10:02:30Z"),
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
	case "file1":
		node = nodeJSON("file1", "Design.pdf", "FILE", "PDF", "root", false, "2026-06-30T10:02:30Z")
	default:
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]interface{}{"node": node})
}

func (s *fakeDingTalkServer) handleBlocks(w http.ResponseWriter, r *http.Request) {
	atomic.AddInt32(&s.blockCalls, 1)
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	docKey := strings.TrimPrefix(r.URL.Path, "/v1.0/doc/suites/documents/")
	docKey = strings.TrimSuffix(docKey, "/blocks")
	if docKey == "doc1" && atomic.LoadInt32(&s.doc1BlockFailed) == 1 {
		writeJSON(w, map[string]interface{}{
			"success": false,
			"code":    "DocBlocksQueryFailed",
			"message": "blocks failed for test",
		})
		return
	}
	switch docKey {
	case "doc1":
		text := "docx:doc1:v1"
		if atomic.LoadInt32(&s.doc1Revision) == 1 {
			text = "docx:doc1:v2"
		}
		writeJSON(w, map[string]interface{}{
			"result": map[string]interface{}{
				"data": []map[string]interface{}{
					{
						"blockType": "paragraph",
						"paragraph": map[string]interface{}{
							"elements": []map[string]interface{}{
								{"textRun": map[string]interface{}{"content": text}},
							},
						},
					},
					{
						"blockType": "image",
						"image": map[string]interface{}{
							"alt": "架构图",
							"url": "https://example.com/arch.png",
						},
					},
				},
			},
		})
	case "doc2":
		writeJSON(w, map[string]interface{}{
			"blocks": []map[string]interface{}{
				{
					"blockType": "paragraph",
					"paragraph": map[string]interface{}{
						"richTextElements": []map[string]interface{}{
							{"content": "docx:doc2:v1"},
						},
					},
				},
			},
		})
	default:
		http.NotFound(w, r)
	}
}

func (s *fakeDingTalkServer) handleQueryDentryID(w http.ResponseWriter, r *http.Request) {
	if err := assertOperatorID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodGet {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/v2.0/doc/dentries/file1/queryDentryId" {
		http.NotFound(w, r)
		return
	}
	writeJSON(w, map[string]interface{}{
		"spaceId":  "854001",
		"dentryId": "798001",
	})
}

func (s *fakeDingTalkServer) handleDocExport(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if got := r.Header.Get("x-acs-dingtalk-access-token"); got != "tenant-token" {
		http.Error(w, "missing access token", http.StatusUnauthorized)
		return
	}
	if got := r.URL.Query().Get("targetFormat"); got != "markdown" {
		http.Error(w, "unexpected targetFormat "+got, http.StatusBadRequest)
		return
	}
	path := strings.TrimPrefix(r.URL.Path, "/v1.0/doc/")
	dentryUUID := strings.TrimSuffix(path, "/export")
	if dentryUUID == "" || strings.Contains(dentryUUID, "/") {
		http.NotFound(w, r)
		return
	}
	atomic.AddInt32(&s.exportCalls, 1)
	if atomic.LoadInt32(&s.exportPermissionDenied) == 1 {
		w.WriteHeader(http.StatusForbidden)
		writeJSON(w, map[string]interface{}{
			"code":      "Forbidden.AccessDenied.AccessTokenPermissionDenied",
			"requestid": "019F419C-8737-7A5D-B6B8-4DB544A5BC90",
			"message":   "应用尚未开通所需的权限：[Document.AIAssistant.Read]",
		})
		return
	}
	writeJSON(w, map[string]interface{}{
		"taskId": "task-" + dentryUUID,
	})
}

func (s *fakeDingTalkServer) handleStorageDownloadInfo(w http.ResponseWriter, r *http.Request) {
	if err := assertUnionID(r.URL.Query()); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Path != "/v1.0/storage/spaces/854001/dentries/798001/downloadInfos/query" {
		http.NotFound(w, r)
		return
	}
	atomic.AddInt32(&s.downloadInfoCalls, 1)
	writeJSON(w, map[string]interface{}{
		"downloadInfo": map[string]interface{}{
			"resourceUrl": s.URL + "/download/file1",
			"headers": map[string]interface{}{
				"x-test-download": "ok",
			},
		},
	})
}

func (s *fakeDingTalkServer) handleFileDownload(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("x-test-download") != "ok" {
		http.Error(w, "missing download header", http.StatusBadRequest)
		return
	}
	_, _ = w.Write([]byte("fake-pdf-content"))
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
		"workspaceId":  "ws1",
		"modifiedTime": modified,
	}
}

func assertOperatorID(query url.Values) error {
	if got := query.Get("operatorId"); got != "union-001" {
		return fmt.Errorf("unexpected operatorId %s", got)
	}
	return nil
}

func assertUnionID(query url.Values) error {
	if got := query.Get("unionId"); got != "union-001" {
		return fmt.Errorf("unexpected unionId %s", got)
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}

type testExportFetcher struct{}

func (testExportFetcher) Fetch(
	_ context.Context,
	_ *client,
	doc wikiNode,
	workspaceID string,
	selectedResourceID string,
) (*types.FetchedItem, string, bool, error) {
	if !doc.isOnlineDocument() {
		return nil, "", false, nil
	}
	content := []byte("exported:" + doc.displayName())
	hash := contentHash(content)
	return &types.FetchedItem{
		ExternalID:       makeResourceID(workspaceID, doc.NodeID),
		Title:            doc.displayName(),
		Content:          content,
		ContentType:      "text/markdown",
		FileName:         markdownFileName(doc.displayName()),
		SourceResourceID: selectedResourceID,
		Metadata: map[string]string{
			"channel":      types.ChannelDingtalk,
			"workspace_id": workspaceID,
			"node_id":      doc.NodeID,
			"fetcher":      "export",
			"content_hash": hash,
		},
	}, hash, true, nil
}
