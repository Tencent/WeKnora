package dingtalk

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
	secutils "github.com/Tencent/WeKnora/internal/utils"
)

func TestMain(m *testing.M) {
	os.Setenv("SSRF_WHITELIST", "127.0.0.1,localhost")
	secutils.ResetSSRFWhitelistForTest()
	os.Exit(m.Run())
}

// ──────────────────────────────────────────────────────────────────────
// Fake DingTalk API server
// ──────────────────────────────────────────────────────────────────────

func fakeDingTalk() (*httptest.Server, *Config) {
	mux := http.NewServeMux()

	// --- auth ---
	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{
			AccessToken: "fake-token",
			ExpireIn:    7200,
		})
	})

	// --- list workspaces ---
	mux.HandleFunc("/v2.0/wiki/workspaces", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, workspaceListResponse{
			Workspaces: []workspace{
				{WorkspaceID: "ws1", Name: "Test Workspace", RootNodeID: "root1", Type: "TEAM", Description: "desc"},
			},
		})
	})

	// --- get workspace (nested {"workspace": {...}} structure matching real DingTalk API) ---
	mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"workspace": map[string]interface{}{
				"workspaceId": "ws1",
				"rootNodeId":  "root1",
				"name":        "Test Workspace",
			},
		})
	})

	// --- list nodes ---
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentID := r.URL.Query().Get("parentNodeId")
		var nodes []node
		switch parentID {
		case "root1":
			nodes = []node{
				{NodeID: "folder1", Name: "Folder 1", Type: "FOLDER", Category: "", HasChildren: true, ParentID: "root1", ModifiedTime: "2024-01-01T00:00Z"},
				{NodeID: "doc1", Name: "Doc 1", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ParentID: "root1", ModifiedTime: "2024-01-02T00:00Z"},
			}
		case "folder1":
			nodes = []node{
				{NodeID: "doc2", Name: "Doc 2", Type: "FILE", Category: "ALIDOC", Extension: "adoc", ParentID: "folder1", ModifiedTime: "2024-01-03T00:00Z"},
			}
		}
		writeJSON(w, nodeListResponse{Nodes: nodes})
	})

	// --- get node ---
	mux.HandleFunc("/v2.0/wiki/nodes/doc1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nodeDetailResponse{
			Node: node{NodeID: "doc1", Name: "Doc 1", Type: "FILE", Category: "ALIDOC", ParentID: "root1", ModifiedTime: "2024-01-02T00:00Z"},
		})
	})
	mux.HandleFunc("/v2.0/wiki/nodes/doc2", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, nodeDetailResponse{
			Node: node{NodeID: "doc2", Name: "Doc 2", Type: "FILE", Category: "ALIDOC", ParentID: "folder1", ModifiedTime: "2024-01-03T00:00Z"},
		})
	})

	// --- document blocks ---
	mux.HandleFunc("/v1.0/doc/suites/documents/doc1/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, blocksResponse{
			Success: true,
			Result: blocksResult{
				Data: []block{
					{BlockType: "heading", Heading: &blockHeading{Level: flexibleInt(1), Text: "Title"}},
					{BlockType: "paragraph", Paragraph: &blockParagraph{Text: "Hello world"}},
				},
			},
		})
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc2/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, blocksResponse{
			Success: true,
			Result: blocksResult{
				Data: []block{
					{BlockType: "paragraph", Paragraph: &blockParagraph{Text: "Second doc content"}},
				},
			},
		})
	})

	server := httptest.NewServer(mux)
	config := &Config{
		ClientID:     "test-client-id",
		ClientSecret: "test-client-secret",
		OperatorID:   "test-operator-id",
		BaseURL:      server.URL,
	}
	return server, config
}

func writeJSON(w http.ResponseWriter, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ──────────────────────────────────────────────────────────────────────
// Tests
// ──────────────────────────────────────────────────────────────────────

func TestType(t *testing.T) {
	c := Connector{}
	if got := c.Type(); got != types.ConnectorTypeDingTalk {
		t.Errorf("Type() = %q, want %q", got, types.ConnectorTypeDingTalk)
	}
}

func TestValidate(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	if err := conn.Validate(context.Background(), dsConfig); err != nil {
		t.Errorf("Validate() error = %v", err)
	}
}

func TestValidate_MissingCredentials(t *testing.T) {
	conn := Connector{}

	tests := []struct {
		name string
		cfg  *types.DataSourceConfig
	}{
		{"nil config", nil},
		{"missing client_id", &types.DataSourceConfig{Credentials: map[string]interface{}{"client_secret": "s", "operator_id": "o"}}},
		{"missing client_secret", &types.DataSourceConfig{Credentials: map[string]interface{}{"client_id": "c", "operator_id": "o"}}},
		{"missing operator_id", &types.DataSourceConfig{Credentials: map[string]interface{}{"client_id": "c", "client_secret": "s"}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := conn.Validate(context.Background(), tt.cfg); err == nil {
				t.Error("Validate() expected error, got nil")
			}
		})
	}
}

func TestListResources_Workspaces(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	resources, err := conn.ListResources(context.Background(), dsConfig, "")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("ListResources() got %d workspaces, want 1", len(resources))
	}
	if resources[0].ExternalID != "ws1" {
		t.Errorf("ExternalID = %q, want %q", resources[0].ExternalID, "ws1")
	}
	if resources[0].Name != "Test Workspace" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Test Workspace")
	}
	if !resources[0].HasChildren {
		t.Error("HasChildren = false, want true")
	}
}

func TestListResources_Nodes(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	// List nodes under workspace (parentID = workspaceId)
	resources, err := conn.ListResources(context.Background(), dsConfig, "ws1")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}

	if len(resources) != 2 {
		t.Fatalf("ListResources() got %d nodes, want 2", len(resources))
	}

	// First should be the folder
	if resources[0].Name != "Folder 1" {
		t.Errorf("resources[0].Name = %q, want %q", resources[0].Name, "Folder 1")
	}
	if !resources[0].HasChildren {
		t.Error("Folder HasChildren = false, want true")
	}

	// Second should be the doc
	if resources[1].Name != "Doc 1" {
		t.Errorf("resources[1].Name = %q, want %q", resources[1].Name, "Doc 1")
	}
}

func TestListResources_ChildNodes(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	// List children of a folder (parentID = workspaceId:nodeId)
	resources, err := conn.ListResources(context.Background(), dsConfig, "ws1:folder1")
	if err != nil {
		t.Fatalf("ListResources() error = %v", err)
	}

	if len(resources) != 1 {
		t.Fatalf("ListResources() got %d children, want 1", len(resources))
	}
	if resources[0].Name != "Doc 2" {
		t.Errorf("Name = %q, want %q", resources[0].Name, "Doc 2")
	}
}

func TestFetchAll(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	items, err := conn.FetchAll(context.Background(), dsConfig, []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}

	// Should fetch doc1 and doc2 (folder1 is skipped)
	if len(items) != 2 {
		t.Fatalf("FetchAll() got %d items, want 2", len(items))
	}

	// Verify items are markdown content
	for _, item := range items {
		if item.ContentType != "text/markdown; charset=utf-8" {
			t.Errorf("ContentType = %q, want text/markdown; charset=utf-8", item.ContentType)
		}
		if !strings.HasSuffix(item.FileName, ".md") {
			t.Errorf("FileName = %q, want .md suffix", item.FileName)
		}
	}
}

func TestBlocksToMarkdown(t *testing.T) {
	tests := []struct {
		name   string
		blocks []block
		want   string
	}{
		{
			name: "heading",
			blocks: []block{
				{BlockType: "heading", Heading: &blockHeading{Level: flexibleInt(1), Text: "Title"}},
			},
			want: "# Title",
		},
		{
			name: "paragraph",
			blocks: []block{
				{BlockType: "paragraph", Paragraph: &blockParagraph{Text: "Hello world"}},
			},
			want: "Hello world",
		},
		{
			name: "code block",
			blocks: []block{
				{BlockType: "codeBlock", CodeBlock: &blockCode{Language: "go", Text: "fmt.Println()"}},
			},
			want: "```go\nfmt.Println()\n```",
		},
		{
			name: "unordered list",
			blocks: []block{
				{BlockType: "list", List: &blockList{Style: "unordered", Items: []string{"a", "b"}}},
			},
			want: "- a\n- b",
		},
		{
			name: "ordered list",
			blocks: []block{
				{BlockType: "list", List: &blockList{Style: "ordered", Items: []string{"first", "second"}}},
			},
			want: "1. first\n2. second",
		},
		{
			name: "divider",
			blocks: []block{
				{BlockType: "divider", Divider: &blockDivider{}},
			},
			want: "---",
		},
		{
			name: "image",
			blocks: []block{
				{BlockType: "image", Image: &blockImage{URL: "https://example.com/img.png", AltText: "pic"}},
			},
			want: "![pic](https://example.com/img.png)",
		},
		{
			name: "table",
			blocks: []block{
				{BlockType: "table", Table: &blockTable{
					RowSize: 2, ColSize: 2,
					Cells: [][]string{
						{"H1", "H2"},
						{"A", "B"},
					},
				}},
			},
			want: "H1 | H2\n--- | ---\nA | B\n",
		},
		{
			name: "quote",
			blocks: []block{
				{BlockType: "quote", Quote: &blockQuote{Text: "quoted text"}},
			},
			want: "> quoted text\n",
		},
		{
			name: "multiple blocks",
			blocks: []block{
				{BlockType: "heading", Heading: &blockHeading{Level: flexibleInt(2), Text: "Section"}},
				{BlockType: "paragraph", Paragraph: &blockParagraph{Text: "Content"}},
			},
			want: "## Section\nContent",
		},
		{
			name: "heading with string level from API (regression)",
			blocks: []block{
				{BlockType: "heading", Heading: &blockHeading{Level: flexibleInt(3), Text: "Sub"}},
			},
			want: "### Sub",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := blocksToMarkdown(tt.blocks)
			if got != tt.want {
				t.Errorf("blocksToMarkdown() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSanitizeFileName(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.txt", "normal.txt"},
		{"", "untitled"},
		{"a/b\\c:d*e?f", "a_b_c_d_e_f"},
		{strings.Repeat("长", 100), strings.Repeat("长", 66)}, // truncate at UTF-8 rune boundary
	}

	for _, tt := range tests {
		got := sanitizeFileName(tt.input)
		if got != tt.want {
			t.Errorf("sanitizeFileName(%q) = %q, want %q", tt.input, got, tt.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("sanitizeFileName(%q) produced invalid UTF-8", tt.input)
		}
	}
}

func TestRedactToken(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"short", "***"},
		{"123456789012", "123456...9012"},
	}
	for _, tt := range tests {
		got := redactToken(tt.input)
		if got != tt.want {
			t.Errorf("redactToken(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		input  string
		maxLen int
		want   string
	}{
		{"short", 10, "short"},
		{"hello world", 5, "hello..."},
		// Chinese characters: each rune is 3 bytes in UTF-8.
		// "你好世界" = 12 bytes. Truncating to 8 bytes should fall back to
		// 6 bytes ("你好") which is a valid rune boundary.
		{"你好世界", 8, "你好..."},
		// Truncating to exactly a rune boundary (6 bytes = 2 Chinese chars)
		{"你好世界", 6, "你好..."},
	}
	for _, tt := range tests {
		got := truncate(tt.input, tt.maxLen)
		if got != tt.want {
			t.Errorf("truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
		}
		if !utf8.ValidString(got) {
			t.Errorf("truncate(%q, %d) produced invalid UTF-8: %q", tt.input, tt.maxLen, got)
		}
	}
}

func TestParseDingtalkTime(t *testing.T) {
	tests := []struct {
		input string
		want  time.Time
	}{
		{"", time.Time{}},
		{"2024-01-01T00:00Z", time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)},
		{"2024-01-01T12:30", time.Date(2024, 1, 1, 12, 30, 0, 0, time.UTC)},
	}

	for _, tt := range tests {
		got := parseDingtalkTime(tt.input)
		if !got.Equal(tt.want) {
			t.Errorf("parseDingtalkTime(%q) = %v, want %v", tt.input, got, tt.want)
		}
	}
}

func TestFetchIncremental_FirstSync(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
		ResourceIDs: []string{"ws1"},
	}

	// First sync — no previous cursor
	items, cursor, err := conn.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("FetchIncremental() error = %v", err)
	}

	// On first sync, all items should be fetched
	if len(items) != 2 {
		t.Fatalf("FetchIncremental() got %d items, want 2", len(items))
	}
	if cursor == nil {
		t.Fatal("FetchIncremental() cursor is nil")
	}
}

func TestFetchIncremental_NoChanges(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
		ResourceIDs: []string{"ws1"},
	}

	// First sync to get a cursor
	_, cursor, err := conn.FetchIncremental(context.Background(), dsConfig, nil)
	if err != nil {
		t.Fatalf("First FetchIncremental() error = %v", err)
	}

	// Second sync with the same cursor — should detect no changes
	items, _, err := conn.FetchIncremental(context.Background(), dsConfig, cursor)
	if err != nil {
		t.Fatalf("Second FetchIncremental() error = %v", err)
	}

	if len(items) != 0 {
		t.Fatalf("FetchIncremental() got %d items on no-change sync, want 0", len(items))
	}
}

func TestParseDingtalkConfig(t *testing.T) {
	tests := []struct {
		name    string
		config  *types.DataSourceConfig
		wantErr bool
	}{
		{
			name: "valid config",
			config: &types.DataSourceConfig{
				Credentials: map[string]interface{}{
					"client_id":     "id",
					"client_secret": "secret",
					"operator_id":   "op",
				},
			},
			wantErr: false,
		},
		{
			name:    "nil config",
			config:  nil,
			wantErr: true,
		},
		{
			name: "missing client_id",
			config: &types.DataSourceConfig{
				Credentials: map[string]interface{}{
					"client_secret": "secret",
					"operator_id":   "op",
				},
			},
			wantErr: true,
		},
		{
			name: "missing operator_id",
			config: &types.DataSourceConfig{
				Credentials: map[string]interface{}{
					"client_id":     "id",
					"client_secret": "secret",
				},
			},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg, err := parseDingtalkConfig(tt.config)
			if (err != nil) != tt.wantErr {
				t.Errorf("parseDingtalkConfig() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil && cfg == nil {
				t.Error("parseDingtalkConfig() returned nil config without error")
			}
		})
	}
}

func TestGetBaseURL(t *testing.T) {
	tests := []struct {
		baseURL string
		want    string
	}{
		{"", DefaultBaseURL},
		{"https://custom.api.com", "https://custom.api.com"},
		{"  https://custom.api.com  ", "https://custom.api.com"}, // whitespace trimming
		{"https://custom.api.com/", "https://custom.api.com"},    // trailing slash removal
		{"https://custom.api.com//", "https://custom.api.com"},   // multiple trailing slashes
		{"custom.api.com", "https://custom.api.com"},             // auto-prefix https://
		{"http://internal.local", "http://internal.local"},       // http:// preserved
	}

	for _, tt := range tests {
		cfg := &Config{BaseURL: tt.baseURL}
		if got := cfg.GetBaseURL(); got != tt.want {
			t.Errorf("GetBaseURL(%q) = %q, want %q", tt.baseURL, got, tt.want)
		}
	}
}

// Test that the resource ID encoding/decoding round-trips correctly.
func TestResourceIDRoundTrip(t *testing.T) {
	tests := []struct {
		workspaceID string
		nodeID      string
	}{
		{"ws1", "node1"},
		{"ws1", ""},
	}

	for _, tt := range tests {
		encoded := makeDingtalkNodeResourceID(tt.workspaceID, tt.nodeID)
		ws, node := parseDingtalkResourceID(encoded)
		if ws != tt.workspaceID || node != tt.nodeID {
			t.Errorf("roundtrip(%q, %q) = (%q, %q), want (%q, %q)",
				tt.workspaceID, tt.nodeID, ws, node, tt.workspaceID, tt.nodeID)
		}
	}
}

// Test that table markdown is generated correctly.
func TestWriteTableMarkdown(t *testing.T) {
	var sb strings.Builder
	tb := &blockTable{
		RowSize: 3, ColSize: 2,
		Cells: [][]string{
			{"Name", "Age"},
			{"Alice", "30"},
			{"Bob", "25"},
		},
	}
	writeTableMarkdown(&sb, tb)
	want := "Name | Age\n--- | ---\nAlice | 30\nBob | 25\n"
	if got := sb.String(); got != want {
		t.Errorf("writeTableMarkdown() = %q, want %q", got, want)
	}
}

// Test that Config properly handles the base_url with SSRF validation.
func TestConfigBaseURLSSRF(t *testing.T) {
	config := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "id",
			"client_secret": "secret",
			"operator_id":   "op",
			"base_url":      "http://127.0.0.1:1234", // SSRF whitelisted
		},
	}

	cfg, err := parseDingtalkConfig(config)
	if err != nil {
		t.Fatalf("parseDingtalkConfig() error = %v", err)
	}
	if cfg.GetBaseURL() != "http://127.0.0.1:1234" {
		t.Errorf("GetBaseURL() = %q, want %q", cfg.GetBaseURL(), "http://127.0.0.1:1234")
	}
}

// Ensure FetchedItem FileName is sanitized and has .md extension.
func TestFetchAll_FileNameSanitization(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	items, err := conn.FetchAll(context.Background(), dsConfig, []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}

	for _, item := range items {
		if !utf8.ValidString(item.FileName) {
			t.Errorf("FileName %q is not valid UTF-8", item.FileName)
		}
		if !strings.HasSuffix(item.FileName, ".md") {
			t.Errorf("FileName %q should have .md extension", item.FileName)
		}
	}
}

// Test that unsupported categories are skipped.
func TestFetchAll_SkipsNonALIDOC(t *testing.T) {
	mux := http.NewServeMux()

	mux.HandleFunc("/v1.0/oauth2/accessToken", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, tokenResponse{AccessToken: "fake-token", ExpireIn: 7200})
	})
	mux.HandleFunc("/v2.0/wiki/workspaces/ws1", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, map[string]interface{}{
			"workspace": map[string]interface{}{"workspaceId": "ws1", "rootNodeId": "root1", "name": "WS"},
		})
	})
	mux.HandleFunc("/v2.0/wiki/nodes", func(w http.ResponseWriter, r *http.Request) {
		parentID := r.URL.Query().Get("parentNodeId")
		if parentID == "root1" {
			writeJSON(w, nodeListResponse{
				Nodes: []node{
					{NodeID: "doc_alidoc", Name: "ALIDOC Doc", Type: "FILE", Category: "ALIDOC", ModifiedTime: "2024-01-01T00:00Z"},
					{NodeID: "doc_local", Name: "Local Doc", Type: "FILE", Category: "DOCUMENT", ModifiedTime: "2024-01-01T00:00Z"},
					{NodeID: "img1", Name: "Image", Type: "FILE", Category: "IMAGE", ModifiedTime: "2024-01-01T00:00Z"},
				},
			})
		} else {
			writeJSON(w, nodeListResponse{Nodes: nil})
		}
	})
	mux.HandleFunc("/v1.0/doc/suites/documents/doc_alidoc/blocks", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, blocksResponse{Success: true, Result: blocksResult{Data: []block{
			{BlockType: "paragraph", Paragraph: &blockParagraph{Text: "content"}},
		}}})
	})

	server := httptest.NewServer(mux)
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "id",
			"client_secret": "secret",
			"operator_id":   "op",
			"base_url":      server.URL,
		},
	}

	items, err := conn.FetchAll(context.Background(), dsConfig, []string{"ws1"})
	if err != nil {
		t.Fatalf("FetchAll() error = %v", err)
	}

	// Only ALIDOC should be fetched, DOCUMENT and IMAGE should be skipped
	if len(items) != 1 {
		t.Fatalf("FetchAll() got %d items, want 1 (only ALIDOC)", len(items))
	}
	if items[0].ExternalID != "doc_alidoc" {
		t.Errorf("ExternalID = %q, want %q", items[0].ExternalID, "doc_alidoc")
	}
}

func TestResolveResourceAncestors(t *testing.T) {
	server, config := fakeDingTalk()
	defer server.Close()

	conn := Connector{}
	dsConfig := &types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     config.ClientID,
			"client_secret": config.ClientSecret,
			"operator_id":   config.OperatorID,
			"base_url":      config.BaseURL,
		},
	}

	// Resolve ancestors for ws1:doc1 (doc1's parent is root1, which is a top-level node in ws1)
	ancestors, err := conn.ResolveResourceAncestors(context.Background(), dsConfig, []string{"ws1:doc1"})
	if err != nil {
		t.Fatalf("ResolveResourceAncestors() error = %v", err)
	}

	// Should include at least the workspace ID
	found := false
	for _, a := range ancestors {
		if a == "ws1" {
			found = true
		}
	}
	if !found {
		t.Errorf("ResolveResourceAncestors() = %v, want to include ws1", ancestors)
	}
}

func TestConnectorInterface(t *testing.T) {
	// Compile-time check via var _ datasource.Connector = (*Connector)(nil)
	// This test is a runtime sanity check that the connector is usable.
	var c Connector
	_ = fmt.Sprintf("%T", c)
}

func TestFlexibleInt(t *testing.T) {
	tests := []struct {
		name    string
		json    string
		want    int
		wantErr bool
	}{
		{"integer", `1`, 1, false},
		{"string", `"1"`, 1, false},
		{"string level 3", `"3"`, 3, false},
		{"heading style", `"heading-1"`, 1, false},
		{"heading style level 3", `"heading-3"`, 3, false},
		{"null", `null`, 0, false},
		{"invalid string", `"abc"`, 0, true},
		{"float", `1.5`, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var f flexibleInt
			err := json.Unmarshal([]byte(tt.json), &f)
			if (err != nil) != tt.wantErr {
				t.Errorf("UnmarshalJSON(%s) error = %v, wantErr %v", tt.json, err, tt.wantErr)
			}
			if err == nil && f.Int() != tt.want {
				t.Errorf("UnmarshalJSON(%s) = %d, want %d", tt.json, f.Int(), tt.want)
			}
		})
	}
}

// TestBlocksResponseWithStringLevel verifies that the real DingTalk API response
// shape (where heading.level is a string) can be parsed successfully.
func TestBlocksResponseWithStringLevel(t *testing.T) {
	// This is the actual shape DingTalk returns — note "level": "1" (string, not int)
	raw := `{
		"success": true,
		"result": {
			"data": [
				{
					"blockType": "heading",
					"id": "abc",
					"index": 0,
					"heading": {"level": "1", "text": "Title"}
				},
				{
					"blockType": "paragraph",
					"id": "def",
					"index": 1,
					"paragraph": {"text": "Content"}
				}
			]
		}
	}`

	var resp blocksResponse
	if err := json.Unmarshal([]byte(raw), &resp); err != nil {
		t.Fatalf("Unmarshal blocks response with string level: %v", err)
	}
	if len(resp.Result.Data) != 2 {
		t.Fatalf("got %d blocks, want 2", len(resp.Result.Data))
	}
	if resp.Result.Data[0].Heading == nil {
		t.Fatal("first block heading is nil")
	}
	if resp.Result.Data[0].Heading.Level.Int() != 1 {
		t.Errorf("heading level = %d, want 1", resp.Result.Data[0].Heading.Level.Int())
	}
	if resp.Result.Data[0].Heading.Text != "Title" {
		t.Errorf("heading text = %q, want %q", resp.Result.Data[0].Heading.Text, "Title")
	}
}
