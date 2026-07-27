package dingtalk

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/Tencent/WeKnora/internal/types"
)

func TestParseDingTalkConfig_Valid(t *testing.T) {
	cfg, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "dingabc123",
			"client_secret": "secret456",
			"operator_id":   "user789",
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg.ClientID != "dingabc123" {
		t.Errorf("ClientID = %q, want %q", cfg.ClientID, "dingabc123")
	}
	if cfg.ClientSecret != "secret456" {
		t.Errorf("ClientSecret = %q, want %q", cfg.ClientSecret, "secret456")
	}
	if cfg.OperatorID != "user789" {
		t.Errorf("OperatorID = %q, want %q", cfg.OperatorID, "user789")
	}
}

func TestParseDingTalkConfig_MissingOperatorID(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "dingabc123",
			"client_secret": "secret456",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing operator_id")
	}
}

func TestParseDingTalkConfig_MissingClientID(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_secret": "secret456",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing client_id")
	}
}

func TestParseDingTalkConfig_MissingClientSecret(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id": "dingabc123",
		},
	})
	if err == nil {
		t.Fatal("expected error for missing client_secret")
	}
}

func TestParseDingTalkConfig_WhitespaceClientID(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "   ",
			"client_secret": "secret456",
			"operator_id":   "user789",
		},
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only client_id")
	}
}

func TestParseDingTalkConfig_WhitespaceOperatorID(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "dingabc123",
			"client_secret": "secret456",
			"operator_id":   "   ",
		},
	})
	if err == nil {
		t.Fatal("expected error for whitespace-only operator_id")
	}
}

func TestParseDingTalkConfig_NilConfig(t *testing.T) {
	_, err := parseDingTalkConfig(nil)
	if err == nil {
		t.Fatal("expected error for nil config")
	}
}

func TestParseDingTalkConfig_InvalidCredentials(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "dingabc123",
			"client_secret": "secret456",
			"operator_id":   "user789",
			"bad_value":     make(chan struct{}),
		},
	})
	if err == nil {
		t.Fatal("expected error for credentials that cannot be marshaled")
	}
}

func TestConfig_GetBaseURL_ReturnsDefault(t *testing.T) {
	cfg := &Config{
		ClientID:     "test",
		ClientSecret: "test",
	}
	if got := cfg.GetBaseURL(); got != DefaultBaseURL {
		t.Errorf("GetBaseURL() = %q, want %q", got, DefaultBaseURL)
	}
}

func TestConfig_GetBaseURL_UsesExplicitBaseURL(t *testing.T) {
	cfg := &Config{
		ClientID:     "test",
		ClientSecret: "test",
		BaseURL:      "https://example.test/",
	}
	if got := cfg.GetBaseURL(); got != "https://example.test" {
		t.Errorf("GetBaseURL() = %q, want %q", got, "https://example.test")
	}
}

func TestConfig_GetBaseURL_AddsScheme(t *testing.T) {
	cfg := &Config{BaseURL: "api.dingtalk.com/"}
	if got := cfg.GetBaseURL(); got != "https://api.dingtalk.com" {
		t.Errorf("GetBaseURL() = %q, want https://api.dingtalk.com", got)
	}
}

func TestParseDingTalkConfig_RejectsUnsafeBaseURL(t *testing.T) {
	_, err := parseDingTalkConfig(&types.DataSourceConfig{
		Credentials: map[string]interface{}{
			"client_id":     "dingabc123",
			"client_secret": "secret456",
			"operator_id":   "user789",
			"base_url":      "http://127.0.0.1:8080",
		},
	})
	if err == nil {
		t.Fatal("expected error for unsafe base_url")
	}
}

func TestParseTime_RFC3339(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		wantHour int
		wantErr  bool
	}{
		{"RFC3339 with timezone", "2026-01-15T10:30:00+08:00", 10, false},
		{"RFC3339 UTC", "2026-01-15T02:30:00Z", 2, false},
		{"DingTalk documented minute precision", "2023-05-15T11:29Z", 11, false},
		{"alternative format", "2026-01-15T10:30:00Z", 10, false},
		{"empty string", "", 0, false},
		{"invalid format", "not-a-time", 0, false},
		{"date only", "2026-01-15", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := parseTime(tt.input)
			if tt.wantErr {
				return
			}
			if tt.input == "" || tt.input == "not-a-time" || tt.input == "2026-01-15" {
				if !got.IsZero() {
					t.Errorf("expected zero time for %q, got %v", tt.input, got)
				}
				return
			}
			if got.Hour() != tt.wantHour {
				t.Errorf("parseTime(%q).Hour() = %d, want %d", tt.input, got.Hour(), tt.wantHour)
			}
		})
	}
}

func TestWikiNode_UsesOfficialTimestampAndStatisticalInfo(t *testing.T) {
	body := `{
		"nodeId": "node-123",
		"name": "测试文档",
		"type": "FILE",
		"category": "ALIDOC",
		"modifiedTimestamp": 1684146540000,
		"statisticalInfo": {"wordCount": 321}
	}`

	var node WikiNode
	if err := json.Unmarshal([]byte(body), &node); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if got := node.modifiedAt(); got.IsZero() || got.Year() != 2023 {
		t.Fatalf("modifiedAt() = %v, want 2023 timestamp", got)
	}
	if got := node.wordCount(); got != 321 {
		t.Fatalf("wordCount() = %d, want 321", got)
	}
}

func TestSanitizeFileName_Valid(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Normal File", "Normal File"},
		{"File-With-Dash", "File-With-Dash"},
		{"中文文件", "中文文件"},
		{"日本語ファイル", "日本語ファイル"},
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

func TestSanitizeFileName_ReplacesInvalidChars(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"file/name", "file_name"},
		{"file\\name", "file_name"},
		{"file:name", "file_name"},
		{"file*name", "file_name"},
		{"file?name", "file_name"},
		{"file<name>", "file_name"},
		{"file|name", "file_name"},
		{"file\"name", "file_name"},
		{`path\to\file`, "path_to_file"},
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

func TestSanitizeFileName_ReplacesControlChars(t *testing.T) {
	if got := sanitizeFileName("line1\r\nline2\t"); got != "line1__line2" {
		t.Fatalf("sanitizeFileName() = %q, want line1__line2", got)
	}
}

func TestSanitizeFileName_Empty(t *testing.T) {
	got := sanitizeFileName("")
	if got != "untitled" {
		t.Errorf("sanitizeFileName(\"\") = %q, want %q", got, "untitled")
	}
}

func TestSanitizeFileName_TruncatesAtRuneBoundary(t *testing.T) {
	// Long Chinese title (each 测 is 3 bytes in UTF-8). Raw byte slicing at 200
	// would split a rune and produce invalid UTF-8.
	long := strings.Repeat("测试", 100) // 600 bytes
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

func TestSanitizeFileName_TruncatesLongASCII(t *testing.T) {
	long := strings.Repeat("a", 300)
	got := sanitizeFileName(long)
	if len(got) > 200 {
		t.Errorf("len = %d, want ≤ 200", len(got))
	}
}

func TestRedactClientID(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"dingabc123def", "ding...3def"},
		{"1234", "***"},
		{"", "***"},
		{"short", "***"},
		{"exactly8chars", "exac...hars"},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := redactClientID(tt.input)
			if got != tt.expected {
				t.Errorf("redactClientID(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestWikiWorkspace_JSON(t *testing.T) {
	body := `{
		"workspaceId": "ws-123",
		"corpId": "corp-456",
		"rootNodeId": "node-789",
		"name": "测试知识库",
		"type": "TEAM",
		"description": "这是一个测试",
		"url": "https://wiki.dingtalk.com/space/ws-123",
		"modifiedTime": "2026-01-15T10:00:00+08:00"
	}`

	var ws WikiWorkspace
	if err := json.Unmarshal([]byte(body), &ws); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if ws.WorkspaceID != "ws-123" {
		t.Errorf("WorkspaceID = %q, want %q", ws.WorkspaceID, "ws-123")
	}
	if ws.Name != "测试知识库" {
		t.Errorf("Name = %q, want %q", ws.Name, "测试知识库")
	}
	if ws.Type != "TEAM" {
		t.Errorf("Type = %q, want %q", ws.Type, "TEAM")
	}
}

func TestWikiNode_JSON(t *testing.T) {
	body := `{
		"nodeId": "node-123",
		"workspaceId": "ws-456",
		"name": "测试文档",
		"size": 1024,
		"type": "FILE",
		"category": "ALIDOC",
		"extension": "md",
		"url": "https://wiki.dingtalk.com/doc/node-123",
		"modifiedTime": "2026-01-15T10:00:00+08:00",
		"hasChildren": false,
		"wordCount": 500
	}`

	var node WikiNode
	if err := json.Unmarshal([]byte(body), &node); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if node.NodeID != "node-123" {
		t.Errorf("NodeID = %q, want %q", node.NodeID, "node-123")
	}
	if node.NodeType != "FILE" {
		t.Errorf("NodeType = %q, want %q", node.NodeType, "FILE")
	}
	if node.Category != "ALIDOC" {
		t.Errorf("Category = %q, want %q", node.Category, "ALIDOC")
	}
	if node.WordCount != 500 {
		t.Errorf("WordCount = %d, want %d", node.WordCount, 500)
	}
}

func TestDingTalkAPIError_Error(t *testing.T) {
	err := &dingtalkAPIError{Code: "400", Msg: "invalid request"}
	got := err.Error()
	want := "dingtalk api error: code=400 msg=invalid request"
	if got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}

func TestDingTalkErrorResponse_UsesOpenAPIMessageShape(t *testing.T) {
	resp := dingtalkErrorResponse{Code: "Forbidden.AccessDenied", Message: "permission denied"}
	if got := resp.errorCode(); got != "Forbidden.AccessDenied" {
		t.Errorf("errorCode() = %q", got)
	}
	if got := resp.errorMessage(); got != "permission denied" {
		t.Errorf("errorMessage() = %q", got)
	}
}

func TestDingTalkErrorResponse_UsesLegacyMessageShape(t *testing.T) {
	resp := dingtalkErrorResponse{ErrCode: 400, ErrMsg: "bad request"}
	if got := resp.errorCode(); got != "400" {
		t.Errorf("errorCode() = %q", got)
	}
	if got := resp.errorMessage(); got != "bad request" {
		t.Errorf("errorMessage() = %q", got)
	}
}

func TestDingTalkCursor_JSON(t *testing.T) {
	now := time.Now().Truncate(time.Second)
	cursor := &dingtalkCursor{
		LastSyncTime: now,
		WorkspaceTimes: map[string]map[string]time.Time{
			"ws-1": {"node-1": now.Add(-time.Hour), "node-2": now.Add(-2 * time.Hour)},
			"ws-2": {"node-3": now.Add(-time.Hour)},
		},
	}

	// Marshal and unmarshal
	data, err := json.Marshal(cursor)
	if err != nil {
		t.Fatalf("marshal error: %v", err)
	}

	var restored dingtalkCursor
	if err := json.Unmarshal(data, &restored); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if !restored.LastSyncTime.Equal(now) {
		t.Errorf("LastSyncTime = %v, want %v", restored.LastSyncTime, now)
	}
	if len(restored.WorkspaceTimes) != 2 {
		t.Errorf("len(WorkspaceTimes) = %d, want 2", len(restored.WorkspaceTimes))
	}
	if len(restored.WorkspaceTimes["ws-1"]) != 2 {
		t.Errorf("len(WorkspaceTimes[ws-1]) = %d, want 2", len(restored.WorkspaceTimes["ws-1"]))
	}
}

func TestAccessTokenResponse_JSON(t *testing.T) {
	body := `{
		"accessToken": "test-token-123",
		"expireIn": 7200
	}`

	var resp accessTokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if resp.AccessToken != "test-token-123" {
		t.Errorf("AccessToken = %q, want %q", resp.AccessToken, "test-token-123")
	}
	if resp.ExpireIn != 7200 {
		t.Errorf("ExpireIn = %d, want %d", resp.ExpireIn, 7200)
	}
}

func TestAccessTokenResponse_JSONLegacyField(t *testing.T) {
	body := `{
		"access_token": "legacy-token",
		"expireIn": 7200
	}`

	var resp accessTokenResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if resp.AccessToken != "legacy-token" {
		t.Errorf("AccessToken = %q, want legacy-token", resp.AccessToken)
	}
}

func TestWikiWorkspacesResponse_JSON(t *testing.T) {
	body := `{
		"workspaces": [
			{"workspaceId": "ws-1", "name": "知识库1"},
			{"workspaceId": "ws-2", "name": "知识库2"}
		],
		"nextToken": "next-page"
	}`

	var resp wikiWorkspacesResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(resp.Workspaces) != 2 {
		t.Errorf("len(Workspaces) = %d, want 2", len(resp.Workspaces))
	}
	if resp.NextToken != "next-page" {
		t.Errorf("NextToken = %q, want next-page", resp.NextToken)
	}
}

func TestWikiNodesResponse_JSON(t *testing.T) {
	body := `{
		"nodes": [
			{"nodeId": "node-1", "name": "文档1"},
			{"nodeId": "node-2", "name": "文档2"}
		],
		"nextToken": "token-abc"
	}`

	var resp wikiNodesResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if len(resp.Nodes) != 2 {
		t.Errorf("len(Nodes) = %d, want 2", len(resp.Nodes))
	}
	if resp.NextToken != "token-abc" {
		t.Errorf("NextToken = %q, want %q", resp.NextToken, "token-abc")
	}
}

func TestDocBlocksResponse_ResultData(t *testing.T) {
	body := `{
		"success": true,
		"result": {
			"data": [
				{"blockId": "block-1", "blockType": "paragraph", "text": "hello"}
			]
		}
	}`

	var resp docBlocksResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	blocks := resp.allBlocks()
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Text != "hello" {
		t.Errorf("Text = %q, want hello", blocks[0].Text)
	}
}

func TestDocBlocksResponse_SuccessFalse(t *testing.T) {
	body := `{
		"success": false,
		"code": "Forbidden.AccessDenied",
		"message": "permission denied"
	}`

	var resp docBlocksResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if err := resp.validate(); err == nil {
		t.Fatal("expected success=false to be rejected")
	}
}

func TestDocBlocksResponse_ResultList(t *testing.T) {
	body := `{
		"result": {
			"list": [
				{"blockId": "block-1", "blockType": "paragraph", "text": "from list"}
			]
		}
	}`

	var resp docBlocksResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	blocks := resp.allBlocks()
	if len(blocks) != 1 {
		t.Fatalf("len(blocks) = %d, want 1", len(blocks))
	}
	if blocks[0].Text != "from list" {
		t.Errorf("Text = %q, want from list", blocks[0].Text)
	}
}
