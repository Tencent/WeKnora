package feishu

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

// ──────────────────────────────────────────────────────────────────────
// Fake Feishu API server
// ──────────────────────────────────────────────────────────────────────

// fakeFeishu builds an httptest.Server that emulates the relevant Feishu APIs.
// It returns the server and a Config pointing at it.
func fakeFeishu(nodes []wikiNode) (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	// --- auth ---
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{
			apiResponse:       apiResponse{Code: 0},
			TenantAccessToken: "fake-token",
			Expire:            7200,
		})
	})

	// --- wiki spaces ---
	mux.HandleFunc("/open-apis/wiki/v2/spaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, wikiSpaceListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Items     []wikiSpace `json:"items"`
				HasMore   bool        `json:"has_more"`
				PageToken string      `json:"page_token"`
			}{
				Items: []wikiSpace{
					{SpaceID: "space1", Name: "Test Space", Description: "desc", Visibility: "public"},
				},
			},
		})
	})

	// --- wiki nodes (top-level only for simplicity) ---
	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, wikiNodeListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Items     []wikiNode `json:"items"`
				HasMore   bool       `json:"has_more"`
				PageToken string     `json:"page_token"`
			}{
				Items: nodes,
			},
		})
	})

	// --- export task: create ---
	mux.HandleFunc("/open-apis/drive/v1/export_tasks", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			writeJSON(w, exportTaskCreateResponse{
				apiResponse: apiResponse{Code: 0},
				Data: struct {
					Ticket string `json:"ticket"`
				}{Ticket: "ticket-123"},
			})
			return
		}
		// GET /open-apis/drive/v1/export_tasks/ticket-123
		writeJSON(w, exportTaskStatusResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Result struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				} `json:"result"`
			}{
				Result: struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				}{
					FileToken: "ft-abc",
					FileSize:  100,
					JobStatus: 0, // success
					FileName:  "exported.docx",
				},
			},
		})
	})

	// --- export task: status polling (pattern match with ticket) ---
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/ticket-123", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, exportTaskStatusResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Result struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				} `json:"result"`
			}{
				Result: struct {
					FileToken   string `json:"file_token"`
					FileSize    int64  `json:"file_size"`
					JobStatus   int    `json:"job_status"`
					JobErrorMsg string `json:"job_error_msg"`
					FileName    string `json:"file_name"`
				}{
					FileToken: "ft-abc",
					FileSize:  100,
					JobStatus: 0,
					FileName:  "exported.docx",
				},
			},
		})
	})

	// --- export file download ---
	mux.HandleFunc("/open-apis/drive/v1/export_tasks/file/ft-abc/download", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/octet-stream")
		w.Write([]byte("fake-docx-content"))
	})

	// --- drive file download (for "file" type nodes) ---
	mux.HandleFunc("/open-apis/drive/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			w.Write([]byte("fake-pdf-binary"))
			return
		}
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(mux)
	cfg := &Config{
		AppID:     "test-app-id",
		AppSecret: "test-app-secret",
		BaseURL:   ts.URL,
	}
	return ts, cfg
}

func fakeFeishuWithChildFailure(topNodes []wikiNode, failingParentToken string) (*httptest.Server, *Config) {
	return fakeFeishuHierarchy(topNodes, nil, failingParentToken)
}

func fakeFeishuHierarchy(topNodes []wikiNode, childNodes map[string][]wikiNode, failingParentToken string) (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{
			apiResponse:       apiResponse{Code: 0},
			TenantAccessToken: "fake-token",
			Expire:            7200,
		})
	})

	mux.HandleFunc("/open-apis/wiki/v2/spaces/space1/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentToken := r.URL.Query().Get("parent_node_token")
		if parentToken != "" && parentToken == failingParentToken {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"code":1663,"msg":"internal error"}`))
			return
		}
		nodes := topNodes
		if parentToken != "" {
			nodes = childNodes[parentToken]
		}
		writeJSON(w, wikiNodeListResponse{
			apiResponse: apiResponse{Code: 0},
			Data: struct {
				Items     []wikiNode `json:"items"`
				HasMore   bool       `json:"has_more"`
				PageToken string     `json:"page_token"`
			}{
				Items: nodes,
			},
		})
	})

	mux.HandleFunc("/open-apis/drive/v1/files/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/download") {
			w.Header().Set("Content-Type", "application/octet-stream")
			_, _ = w.Write([]byte("fake-file-content"))
			return
		}
		http.NotFound(w, r)
	})

	ts := httptest.NewServer(mux)
	cfg := &Config{
		AppID:     "test-app-id",
		AppSecret: "test-app-secret",
		BaseURL:   ts.URL,
	}
	return ts, cfg
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

func makeConfig(cfg *Config, resourceIDs []string) *types.DataSourceConfig {
	creds := map[string]interface{}{
		"app_id":     cfg.AppID,
		"app_secret": cfg.AppSecret,
		"base_url":   cfg.BaseURL,
	}
	return &types.DataSourceConfig{
		Type:        types.ConnectorTypeFeishu,
		Credentials: creds,
		ResourceIDs: resourceIDs,
	}
}

// ──────────────────────────────────────────────────────────────────────
// Helper function tests
// ──────────────────────────────────────────────────────────────────────

func TestIsSupportedDocType(t *testing.T) {
	tests := []struct {
		objType  string
		expected bool
	}{
		{"docx", true},
		{"doc", true},
		{"sheet", true},
		{"bitable", true},
		{"file", true},
		{"mindnote", false},
		{"slides", false},
		{"unknown", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(tt.objType, func(t *testing.T) {
			got := isSupportedDocType(tt.objType)
			if got != tt.expected {
				t.Errorf("isSupportedDocType(%q) = %v, want %v", tt.objType, got, tt.expected)
			}
		})
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"hello", "hello"},
		{"", "untitled"},
		{"a/b\\c:d*e", "a_b_c_d_e"},
		{"normal file.docx", "normal file.docx"},
		{strings.Repeat("a", 300), strings.Repeat("a", 200)},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := sanitizeFileName(tt.input)
			if got != tt.expected {
				t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestSanitizeFileName_TruncatesAtRuneBoundary(t *testing.T) {
	// Each 测 is 3 bytes; raw byte truncation at 200 would split a rune and
	// produce invalid UTF-8 that downstream filename validation rejects.
	long := strings.Repeat("测试", 100)
	got := sanitizeFileName(long)
	if !utf8.ValidString(got) {
		t.Fatalf("sanitizeFileName produced invalid UTF-8: %q", got)
	}
	if len(got) > 200 {
		t.Errorf("len = %d, want ≤ 200", len(got))
	}
	if len(got) == 0 {
		t.Error("result is empty")
	}
}

func TestParseFeishuTimestamp(t *testing.T) {
	ts := parseFeishuTimestamp("1711468800") // 2024-03-27 00:00:00 UTC
	if ts.IsZero() {
		t.Fatal("expected non-zero time")
	}
	if ts.Unix() != 1711468800 {
		t.Errorf("unexpected unix = %d", ts.Unix())
	}

	if !parseFeishuTimestamp("").IsZero() {
		t.Error("expected zero time for empty string")
	}
	if !parseFeishuTimestamp("invalid").IsZero() {
		t.Error("expected zero time for invalid string")
	}
}

func TestParseFeishuConfig(t *testing.T) {
	t.Run("valid", func(t *testing.T) {
		cfg, err := parseFeishuConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_id":     "id1",
				"app_secret": "sec1",
				"base_url":   "https://custom.example.com",
			},
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if cfg.AppID != "id1" || cfg.AppSecret != "sec1" || cfg.BaseURL != "https://custom.example.com" {
			t.Errorf("unexpected config: %+v", cfg)
		}
	})

	t.Run("nil config", func(t *testing.T) {
		_, err := parseFeishuConfig(nil)
		if err == nil {
			t.Fatal("expected error for nil config")
		}
	})

	t.Run("missing credentials", func(t *testing.T) {
		_, err := parseFeishuConfig(&types.DataSourceConfig{
			Credentials: map[string]interface{}{
				"app_id": "id1",
				// missing app_secret
			},
		})
		if err == nil {
			t.Fatal("expected error for missing app_secret")
		}
	})
}

// ──────────────────────────────────────────────────────────────────────
// Connector interface tests
// ──────────────────────────────────────────────────────────────────────

func TestConnectorType(t *testing.T) {
	c := NewConnector()
	if c.Type() != types.ConnectorTypeFeishu {
		t.Errorf("Type() = %q, want %q", c.Type(), types.ConnectorTypeFeishu)
	}
}

func TestConnectorValidate(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	c := NewConnector()
	err := c.Validate(context.Background(), makeConfig(cfg, nil))
	if err != nil {
		t.Fatalf("Validate() error: %v", err)
	}
}

func TestConnectorValidate_BadCredentials(t *testing.T) {
	c := NewConnector()
	err := c.Validate(context.Background(), &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"app_id":     "bad",
			"app_secret": "bad",
			"base_url":   "http://127.0.0.1:1", // will fail to connect
		},
	})
	if err == nil {
		t.Fatal("expected error for bad credentials")
	}
}

func TestConnectorListResources(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	c := NewConnector()
	resources, err := c.ListResources(context.Background(), makeConfig(cfg, nil))
	if err != nil {
		t.Fatalf("ListResources() error: %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("expected 1 resource, got %d", len(resources))
	}
	if resources[0].ExternalID != "space1" {
		t.Errorf("ExternalID = %q, want %q", resources[0].ExternalID, "space1")
	}
	if resources[0].Name != "Test Space" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Test Space")
	}
	if resources[0].Type != "wiki_space" {
		t.Errorf("Type = %q, want %q", resources[0].Type, "wiki_space")
	}
}

// ──────────────────────────────────────────────────────────────────────
// FetchAll: test all supported doc types
// ──────────────────────────────────────────────────────────────────────

func TestFetchAll_DocxNode(t *testing.T) {
	nodes := []wikiNode{{
		NodeToken:    "nt1",
		ObjToken:     "obj-docx-1",
		ObjType:      "docx",
		Title:        "My Document",
		NodeEditTime: "1711468800",
	}}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if item.ExternalID != "nt1" {
		t.Errorf("ExternalID = %q, want %q", item.ExternalID, "nt1")
	}
	if item.Title != "My Document" {
		t.Errorf("Title = %q", item.Title)
	}
	if string(item.Content) != "fake-docx-content" {
		t.Errorf("Content = %q, want %q", string(item.Content), "fake-docx-content")
	}
	if item.FileName != "exported.docx" {
		t.Errorf("FileName = %q, want %q", item.FileName, "exported.docx")
	}
	if item.Metadata["obj_type"] != "docx" {
		t.Errorf("Metadata[obj_type] = %q", item.Metadata["obj_type"])
	}
	if item.Metadata["channel"] != types.ChannelFeishu {
		t.Errorf("Metadata[channel] = %q", item.Metadata["channel"])
	}
}

func TestFetchAll_SheetNode(t *testing.T) {
	nodes := []wikiNode{{
		NodeToken:    "nt-sheet",
		ObjToken:     "obj-sheet-1",
		ObjType:      "sheet",
		Title:        "Sales Report",
		NodeEditTime: "1711468800",
	}}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Metadata["obj_type"] != "sheet" {
		t.Errorf("obj_type = %q, want sheet", items[0].Metadata["obj_type"])
	}
}

func TestFetchAll_BitableNode(t *testing.T) {
	nodes := []wikiNode{{
		NodeToken:    "nt-bitable",
		ObjToken:     "obj-bitable-1",
		ObjType:      "bitable",
		Title:        "Project Tracker",
		NodeEditTime: "1711468800",
	}}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}
	if items[0].Metadata["obj_type"] != "bitable" {
		t.Errorf("obj_type = %q, want bitable", items[0].Metadata["obj_type"])
	}
}

func TestFetchAll_FileNode(t *testing.T) {
	nodes := []wikiNode{{
		NodeToken:    "nt-file",
		ObjToken:     "obj-file-1",
		ObjType:      "file",
		Title:        "manual.pdf",
		NodeEditTime: "1711468800",
	}}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	if len(items) != 1 {
		t.Fatalf("expected 1 item, got %d", len(items))
	}

	item := items[0]
	if string(item.Content) != "fake-pdf-binary" {
		t.Errorf("Content = %q, want %q", string(item.Content), "fake-pdf-binary")
	}
	if item.FileName != "manual.pdf" {
		t.Errorf("FileName = %q, want %q", item.FileName, "manual.pdf")
	}
	if item.Metadata["obj_type"] != "file" {
		t.Errorf("obj_type = %q, want file", item.Metadata["obj_type"])
	}
}

func TestFetchAll_SkipsMindnoteAndSlides(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt-mn", ObjToken: "obj-mn", ObjType: "mindnote", Title: "Brain Map"},
		{NodeToken: "nt-sl", ObjToken: "obj-sl", ObjType: "slides", Title: "Presentation"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	// Both should be skipped (nil returned by fetchNodeContent)
	if len(items) != 0 {
		t.Errorf("expected 0 items (mindnote+slides skipped), got %d", len(items))
	}
}

func TestFetchAll_MixedTypes(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc", NodeEditTime: "1711468800"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "sheet", Title: "Sheet", NodeEditTime: "1711468800"},
		{NodeToken: "nt3", ObjToken: "obj3", ObjType: "file", Title: "report.pdf", NodeEditTime: "1711468800"},
		{NodeToken: "nt4", ObjToken: "obj4", ObjType: "mindnote", Title: "Mind", NodeEditTime: "1711468800"},
		{NodeToken: "nt5", ObjToken: "obj5", ObjType: "slides", Title: "Slides", NodeEditTime: "1711468800"},
		{NodeToken: "nt6", ObjToken: "obj6", ObjType: "bitable", Title: "Table", NodeEditTime: "1711468800"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	items, err := c.FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll() error: %v", err)
	}

	// docx + sheet + file + bitable = 4 items; mindnote + slides = skipped
	if len(items) != 4 {
		t.Errorf("expected 4 items, got %d", len(items))
		for i, it := range items {
			t.Logf("  item[%d]: %s (obj_type=%s)", i, it.Title, it.Metadata["obj_type"])
		}
	}
}

func TestFetchAll_ChildNodeListErrorReturnsPartialItems(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt-parent", ObjToken: "obj-parent", ObjType: "file", Title: "Parent.pdf", NodeEditTime: "100", HasChild: true},
		{NodeToken: "nt-peer", ObjToken: "obj-peer", ObjType: "file", Title: "Peer.pdf", NodeEditTime: "200"},
	}
	ts, cfg := fakeFeishuWithChildFailure(nodes, "nt-parent")
	defer ts.Close()

	items, err := NewConnector().FetchAll(context.Background(), makeConfig(cfg, []string{"space1"}), []string{"space1"})
	if err != nil {
		t.Fatalf("FetchAll must not abort when one child listing fails: %v", err)
	}

	if len(items) != 3 {
		t.Fatalf("want 3 items (2 fetched + 1 failure placeholder), got %d: %+v", len(items), items)
	}

	var parent, peer, placeholder *types.FetchedItem
	for i := range items {
		switch {
		case items[i].ExternalID == "nt-parent" && len(items[i].Content) > 0:
			parent = &items[i]
		case items[i].ExternalID == "nt-peer" && len(items[i].Content) > 0:
			peer = &items[i]
		case items[i].Metadata["error"] != "":
			placeholder = &items[i]
		}
	}

	if parent == nil || peer == nil {
		t.Fatalf("expected both successfully listed nodes to be fetched, parent=%v peer=%v", parent != nil, peer != nil)
	}
	if placeholder == nil {
		t.Fatal("expected a failure placeholder for the child listing error")
	}
	if placeholder.ExternalID != "nt-parent" || placeholder.Title != "Parent.pdf" {
		t.Errorf("placeholder identity wrong: %+v", placeholder)
	}
	if placeholder.Metadata["channel"] != types.ChannelFeishu {
		t.Errorf("placeholder channel = %q", placeholder.Metadata["channel"])
	}
	if placeholder.Metadata["node_token"] != "nt-parent" || placeholder.Metadata["space_id"] != "space1" {
		t.Errorf("placeholder missing traceability metadata: %+v", placeholder.Metadata)
	}
	if !strings.Contains(placeholder.Metadata["error"], "list children of nt-parent") {
		t.Errorf("placeholder error = %q", placeholder.Metadata["error"])
	}
}

// ──────────────────────────────────────────────────────────────────────
// FetchIncremental tests
// ──────────────────────────────────────────────────────────────────────

func TestFetchIncremental_FirstSync(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc1", NodeEditTime: "100"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "file", Title: "file.pdf", NodeEditTime: "200"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	dsConfig := makeConfig(cfg, []string{"space1"})

	// First sync with no cursor → all items should be fetched
	items, cursor, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error: %v", err)
	}

	if len(items) != 2 {
		t.Fatalf("expected 2 items on first sync, got %d", len(items))
	}
	if cursor == nil {
		t.Fatal("expected non-nil cursor")
	}
	if cursor.LastSyncTime.IsZero() {
		t.Error("cursor.LastSyncTime should not be zero")
	}
}

func TestFetchIncremental_NoChanges(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc1", NodeEditTime: "100"},
	}
	ts, cfg := fakeFeishu(nodes)
	defer ts.Close()

	c := NewConnector()
	dsConfig := makeConfig(cfg, []string{"space1"})

	// First sync
	_, cursor1, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}

	// Second sync with same edit times → should return 0 changed items
	items, _, err := c.FetchIncremental(context.Background(), dsConfig, cursor1)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}

	if len(items) != 0 {
		t.Errorf("expected 0 items (no changes), got %d", len(items))
	}
}

func TestFetchIncremental_DetectsDeleted(t *testing.T) {
	// First sync: 2 nodes
	allNodes := []wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc1", NodeEditTime: "100"},
		{NodeToken: "nt2", ObjToken: "obj2", ObjType: "docx", Title: "Doc2", NodeEditTime: "200"},
	}
	ts, cfg := fakeFeishu(allNodes)

	c := NewConnector()
	dsConfig := makeConfig(cfg, []string{"space1"})

	_, cursor1, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	ts.Close()

	// Second sync: only 1 node remains (nt2 was deleted)
	ts2, cfg2 := fakeFeishu([]wikiNode{
		{NodeToken: "nt1", ObjToken: "obj1", ObjType: "docx", Title: "Doc1", NodeEditTime: "100"},
	})
	defer ts2.Close()
	dsConfig2 := makeConfig(cfg2, []string{"space1"})

	items, _, err := c.FetchIncremental(context.Background(), dsConfig2, cursor1)
	if err != nil {
		t.Fatalf("second sync error: %v", err)
	}

	// Should have 1 deleted item for nt2
	var deletedCount int
	for _, item := range items {
		if item.IsDeleted {
			deletedCount++
			if item.ExternalID != "nt2" {
				t.Errorf("expected deleted ExternalID=nt2, got %q", item.ExternalID)
			}
		}
	}
	if deletedCount != 1 {
		t.Errorf("expected 1 deleted item, got %d", deletedCount)
	}
}

func TestFetchIncremental_NoResourceIDs(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	c := NewConnector()
	dsConfig := makeConfig(cfg, nil) // no resource IDs
	dsConfig.ResourceIDs = nil

	_, _, err := c.FetchIncremental(context.Background(), dsConfig, nil)
	if err == nil {
		t.Fatal("expected error for empty resource IDs")
	}
	if !strings.Contains(err.Error(), "no resource IDs") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestFetchIncremental_ChildNodeListErrorReturnsPartialItemsAndCursor(t *testing.T) {
	nodes := []wikiNode{
		{NodeToken: "nt-parent", ObjToken: "obj-parent", ObjType: "file", Title: "Parent.pdf", NodeEditTime: "100", HasChild: true},
		{NodeToken: "nt-peer", ObjToken: "obj-peer", ObjType: "file", Title: "Peer.pdf", NodeEditTime: "200"},
	}
	ts, cfg := fakeFeishuWithChildFailure(nodes, "nt-parent")
	defer ts.Close()

	items, cursor, err := NewConnector().FetchIncremental(context.Background(), makeConfig(cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("FetchIncremental must not abort when one child listing fails: %v", err)
	}
	if cursor == nil {
		t.Fatal("expected cursor to be returned after partial sync")
	}
	if len(items) != 3 {
		t.Fatalf("want 3 items (2 fetched + 1 failure placeholder), got %d: %+v", len(items), items)
	}

	var placeholder *types.FetchedItem
	for i := range items {
		if items[i].Metadata["error"] != "" {
			placeholder = &items[i]
			break
		}
	}
	if placeholder == nil {
		t.Fatal("expected a failure placeholder for the child listing error")
	}
	if placeholder.Metadata["node_token"] != "nt-parent" {
		t.Errorf("placeholder node_token = %q", placeholder.Metadata["node_token"])
	}
}

func TestFetchIncremental_ChildNodeListErrorDoesNotDeletePreviouslySeenChildren(t *testing.T) {
	firstNodes := []wikiNode{
		{NodeToken: "nt-parent", ObjToken: "obj-parent", ObjType: "file", Title: "Parent.pdf", NodeEditTime: "100", HasChild: true},
	}
	firstChildren := map[string][]wikiNode{
		"nt-parent": {
			{NodeToken: "nt-child", ObjToken: "obj-child", ObjType: "file", Title: "Child.pdf", NodeEditTime: "150"},
		},
	}
	ts, cfg := fakeFeishuHierarchy(firstNodes, firstChildren, "")

	firstItems, cursor, err := NewConnector().FetchIncremental(context.Background(), makeConfig(cfg, []string{"space1"}), nil)
	if err != nil {
		t.Fatalf("first sync error: %v", err)
	}
	if len(firstItems) != 2 {
		t.Fatalf("want 2 first-sync items, got %d", len(firstItems))
	}
	ts.Close()

	secondNodes := []wikiNode{
		{NodeToken: "nt-parent", ObjToken: "obj-parent", ObjType: "file", Title: "Parent.pdf", NodeEditTime: "100", HasChild: true},
	}
	ts2, cfg2 := fakeFeishuHierarchy(secondNodes, nil, "nt-parent")
	defer ts2.Close()

	items, nextCursor, err := NewConnector().FetchIncremental(context.Background(), makeConfig(cfg2, []string{"space1"}), cursor)
	if err != nil {
		t.Fatalf("partial second sync error: %v", err)
	}
	for _, item := range items {
		if item.IsDeleted {
			t.Fatalf("partial child listing must not mark previously seen children as deleted: %+v", item)
		}
	}

	cursorBytes, _ := json.Marshal(nextCursor.ConnectorCursor)
	var restored feishuCursor
	if err := json.Unmarshal(cursorBytes, &restored); err != nil {
		t.Fatalf("restore cursor: %v", err)
	}
	if restored.SpaceNodeTimes["space1"]["nt-child"] != "150" {
		t.Fatalf("partial sync should preserve previous child cursor entry, got %+v", restored.SpaceNodeTimes["space1"])
	}
}

// ──────────────────────────────────────────────────────────────────────
// Client tests
// ──────────────────────────────────────────────────────────────────────

func TestClientPing(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	client := NewClient(cfg)
	if err := client.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() error: %v", err)
	}
}

func TestClientTokenCaching(t *testing.T) {
	callCount := 0
	mux := http.NewServeMux()
	mux.HandleFunc("/open-apis/auth/v3/tenant_access_token/internal", func(w http.ResponseWriter, r *http.Request) {
		callCount++
		writeJSON(w, tokenResponse{
			apiResponse:       apiResponse{Code: 0},
			TenantAccessToken: fmt.Sprintf("token-%d", callCount),
			Expire:            7200,
		})
	})
	ts := httptest.NewServer(mux)
	defer ts.Close()

	client := NewClient(&Config{AppID: "a", AppSecret: "b", BaseURL: ts.URL})

	// First call: fetches token
	t1, _ := client.getTenantAccessToken(context.Background())
	// Second call: should use cache
	t2, _ := client.getTenantAccessToken(context.Background())

	if t1 != t2 {
		t.Errorf("expected cached token, got different tokens: %q vs %q", t1, t2)
	}
	if callCount != 1 {
		t.Errorf("expected 1 auth call, got %d", callCount)
	}
}

func TestClientExportAndDownload(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	client := NewClient(cfg)
	data, fileName, err := client.ExportAndDownload(context.Background(), "obj-token-1", "docx")
	if err != nil {
		t.Fatalf("ExportAndDownload() error: %v", err)
	}

	if string(data) != "fake-docx-content" {
		t.Errorf("data = %q, want %q", string(data), "fake-docx-content")
	}
	if fileName != "exported.docx" {
		t.Errorf("fileName = %q, want %q", fileName, "exported.docx")
	}
}

func TestClientExportAndDownload_UnsupportedType(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	client := NewClient(cfg)
	_, _, err := client.ExportAndDownload(context.Background(), "obj-token-1", "mindnote")
	if err == nil {
		t.Fatal("expected error for unsupported type")
	}
	if !strings.Contains(err.Error(), "unsupported") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestClientDownloadDriveFile(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	client := NewClient(cfg)
	data, err := client.DownloadDriveFile(context.Background(), "file-token-abc")
	if err != nil {
		t.Fatalf("DownloadDriveFile() error: %v", err)
	}

	if string(data) != "fake-pdf-binary" {
		t.Errorf("data = %q, want %q", string(data), "fake-pdf-binary")
	}
}

func TestClientListWikiSpaces(t *testing.T) {
	ts, cfg := fakeFeishu(nil)
	defer ts.Close()

	client := NewClient(cfg)
	spaces, err := client.ListWikiSpaces(context.Background())
	if err != nil {
		t.Fatalf("ListWikiSpaces() error: %v", err)
	}
	if len(spaces) != 1 {
		t.Fatalf("expected 1 space, got %d", len(spaces))
	}
	if spaces[0].SpaceID != "space1" {
		t.Errorf("SpaceID = %q", spaces[0].SpaceID)
	}
}

// ──────────────────────────────────────────────────────────────────────
// Type mapping tests
// ──────────────────────────────────────────────────────────────────────

func TestObjTypeToExportMappings(t *testing.T) {
	// Verify all exportable types have valid mappings
	exportable := []string{"docx", "doc", "sheet", "bitable"}
	for _, ot := range exportable {
		if _, ok := objTypeToExportFileExtension[ot]; !ok {
			t.Errorf("objTypeToExportFileExtension missing %q", ot)
		}
		if _, ok := objTypeToExportType[ot]; !ok {
			t.Errorf("objTypeToExportType missing %q", ot)
		}
	}

	// Verify non-exportable types do NOT have mappings
	nonExportable := []string{"file", "mindnote", "slides"}
	for _, ot := range nonExportable {
		if _, ok := objTypeToExportFileExtension[ot]; ok {
			t.Errorf("objTypeToExportFileExtension should NOT contain %q", ot)
		}
	}
}

func TestExportFileExtToSuffix(t *testing.T) {
	if exportFileExtToSuffix[ExportTypeDocx] != ".docx" {
		t.Errorf("docx suffix = %q", exportFileExtToSuffix[ExportTypeDocx])
	}
	if exportFileExtToSuffix[ExportTypeXlsx] != ".xlsx" {
		t.Errorf("xlsx suffix = %q", exportFileExtToSuffix[ExportTypeXlsx])
	}
	if exportFileExtToSuffix[ExportTypePDF] != ".pdf" {
		t.Errorf("pdf suffix = %q", exportFileExtToSuffix[ExportTypePDF])
	}
}

// ──────────────────────────────────────────────────────────────────────
// Feishu cursor serialization
// ──────────────────────────────────────────────────────────────────────

func TestFeishuCursorRoundTrip(t *testing.T) {
	original := feishuCursor{
		LastSyncTime: time.Date(2026, 3, 26, 12, 0, 0, 0, time.UTC),
		SpaceNodeTimes: map[string]map[string]string{
			"space1": {
				"nt1": "100",
				"nt2": "200",
			},
		},
	}

	// Serialize to map[string]interface{} (as stored in SyncCursor.ConnectorCursor)
	data, err := json.Marshal(original)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var cursorMap map[string]interface{}
	if err := json.Unmarshal(data, &cursorMap); err != nil {
		t.Fatalf("unmarshal to map error: %v", err)
	}

	// Deserialize back
	data2, _ := json.Marshal(cursorMap)
	var restored feishuCursor
	if err := json.Unmarshal(data2, &restored); err != nil {
		t.Fatalf("restore error: %v", err)
	}

	if restored.SpaceNodeTimes["space1"]["nt1"] != "100" {
		t.Errorf("restored nt1 = %q, want 100", restored.SpaceNodeTimes["space1"]["nt1"])
	}
	if restored.SpaceNodeTimes["space1"]["nt2"] != "200" {
		t.Errorf("restored nt2 = %q, want 200", restored.SpaceNodeTimes["space1"]["nt2"])
	}
}
