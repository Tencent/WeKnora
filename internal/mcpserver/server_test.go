package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/types/interfaces"
	"github.com/gin-gonic/gin"
)

// recordingEndpointRepo captures last_used touches so the guard's background
// write can be asserted (and never dereferences a nil repository).
type recordingEndpointRepo struct {
	interfaces.MCPEndpointRepository
	touched chan string
}

func (r *recordingEndpointRepo) TouchLastUsed(_ context.Context, id string) error {
	select {
	case r.touched <- id:
	default:
	}
	return nil
}

// newTestEngine mounts the MCP server behind a middleware that injects the
// given endpoint straight onto the request context, standing in for
// middleware.MCPEndpointAuth so the transport, tool filter and call guard
// can be exercised without a database.
func newTestEngine(t *testing.T, ep *types.MCPEndpoint) *gin.Engine {
	engine, _ := newTestEngineWithRepo(t, ep)
	return engine
}

func newTestEngineWithRepo(t *testing.T, ep *types.MCPEndpoint) (*gin.Engine, *recordingEndpointRepo) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	repo := &recordingEndpointRepo{touched: make(chan string, 8)}
	srv := NewServer(nil, nil, nil, nil, nil, nil, nil, nil, nil, repo, nil, nil, nil)
	r := gin.New()
	inject := func(c *gin.Context) {
		if ep != nil {
			ctx := context.WithValue(c.Request.Context(), types.MCPEndpointContextKey, ep)
			ctx = context.WithValue(ctx, types.TenantIDContextKey, ep.TenantID)
			c.Request = c.Request.WithContext(ctx)
		}
		c.Next()
	}
	r.POST("/mcp/:endpoint_id", inject, gin.WrapH(srv.Handler()))
	return r, repo
}

func rpc(t *testing.T, r *gin.Engine, method string, params any) map[string]any {
	t.Helper()
	body, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  method,
		"params":  params,
	})
	req := httptest.NewRequest(http.MethodPost, "/mcp/ep-1", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("%s: status %d body %s", method, w.Code, w.Body.String())
	}
	raw := w.Body.String()
	// Streamable HTTP may answer as SSE; unwrap the data line if so.
	if strings.HasPrefix(strings.TrimSpace(raw), "event:") || strings.HasPrefix(strings.TrimSpace(raw), "data:") {
		for _, line := range strings.Split(raw, "\n") {
			if strings.HasPrefix(line, "data:") {
				raw = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
				break
			}
		}
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		t.Fatalf("%s: bad json %q: %v", method, raw, err)
	}
	return out
}

func toolNames(t *testing.T, resp map[string]any) []string {
	t.Helper()
	result, _ := resp["result"].(map[string]any)
	tools, _ := result["tools"].([]any)
	names := make([]string, 0, len(tools))
	for _, tl := range tools {
		m, _ := tl.(map[string]any)
		names = append(names, m["name"].(string))
	}
	return names
}

func TestToolsListIsFilteredByEndpoint(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, Enabled: true, RateLimitPerMinute: 100,
		Tools: types.StringArray{types.MCPEndpointToolListKnowledgeBases, types.MCPEndpointToolAsk},
	}
	r := newTestEngine(t, ep)
	names := toolNames(t, rpc(t, r, "tools/list", map[string]any{}))
	sort.Strings(names)
	if len(names) != 2 || names[0] != types.MCPEndpointToolAsk || names[1] != types.MCPEndpointToolListKnowledgeBases {
		t.Fatalf("filtered tools = %v", names)
	}
}

func TestToolsListEmptyWithoutEndpoint(t *testing.T) {
	r := newTestEngine(t, nil)
	if names := toolNames(t, rpc(t, r, "tools/list", map[string]any{})); len(names) != 0 {
		t.Fatalf("expected no tools without an endpoint, got %v", names)
	}
}

func TestCatalogMatchesSettingsCatalog(t *testing.T) {
	all := types.StringArray{}
	for _, def := range types.MCPEndpointToolCatalog() {
		all = append(all, def.Name)
	}
	ep := &types.MCPEndpoint{ID: "ep-1", TenantID: 1, Enabled: true, Tools: all}
	r := newTestEngine(t, ep)
	names := toolNames(t, rpc(t, r, "tools/list", map[string]any{}))
	if len(names) != len(all) {
		t.Fatalf("server exposes %d tools, catalog has %d: %v", len(names), len(all), names)
	}
	// tools/list is sorted by name; compare as sets.
	want := append([]string(nil), all...)
	sort.Strings(want)
	sort.Strings(names)
	for i := range want {
		if names[i] != want[i] {
			t.Fatalf("tool mismatch at %d: %q vs %q", i, names[i], want[i])
		}
	}
}

func TestCallHiddenToolIsRefused(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, Enabled: true, RateLimitPerMinute: 100,
		Tools: types.StringArray{types.MCPEndpointToolListKnowledgeBases},
	}
	r := newTestEngine(t, ep)
	resp := rpc(t, r, "tools/call", map[string]any{
		"name":      types.MCPEndpointToolDeleteDocument,
		"arguments": map[string]any{"knowledge_id": "k-1"},
	})
	result, _ := resp["result"].(map[string]any)
	if isErr, _ := result["isError"].(bool); !isErr {
		t.Fatalf("expected tool error, got %v", resp)
	}
	content, _ := result["content"].([]any)
	first, _ := content[0].(map[string]any)
	if !strings.Contains(first["text"].(string), "not enabled") {
		t.Fatalf("unexpected error text: %v", first["text"])
	}
}

func TestRateLimitPerEndpoint(t *testing.T) {
	ep := &types.MCPEndpoint{
		ID: "ep-1", TenantID: 1, Enabled: true, RateLimitPerMinute: 1,
		Tools: types.StringArray{types.MCPEndpointToolReadDocument},
	}
	r, repo := newTestEngineWithRepo(t, ep)
	call := func() string {
		resp := rpc(t, r, "tools/call", map[string]any{
			"name":      types.MCPEndpointToolReadDocument,
			"arguments": map[string]any{},
		})
		result, _ := resp["result"].(map[string]any)
		content, _ := result["content"].([]any)
		first, _ := content[0].(map[string]any)
		return first["text"].(string)
	}
	// First call passes the guard and fails inside the handler on validation.
	if text := call(); !strings.Contains(text, "knowledge_id is required") {
		t.Fatalf("first call: %q", text)
	}
	if text := call(); !strings.Contains(text, "rate limit") {
		t.Fatalf("second call should be rate limited: %q", text)
	}
	select {
	case id := <-repo.touched:
		if id != "ep-1" {
			t.Fatalf("touched endpoint = %q", id)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("expected last_used_at to be touched after a guarded call")
	}
}

func TestTouchLastUsedToleratesMissingRepository(_ *testing.T) {
	srv := &Server{}
	srv.touchLastUsed(context.Background(), "ep-1") // must not panic
}
