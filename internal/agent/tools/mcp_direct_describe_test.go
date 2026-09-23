package tools

// issue #35（T41 seam 盲区）验收 [unit]：
//   - 未 describe 的直注册工具调用在 MCPRegisteredTool.Execute 入口
//     fast-fail（与 call_mcp_tool 代理路径同文案），零挂起、零上游触达；
//   - describe 后同一注册名恢复可执行（fast-fail 只拦未 describe 状态）；
//   - IsMCP Tool 类型判定：直注册工具 / call_mcp_tool 代理为 MCP 通道，
//     内置工具与未知名字不是。

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// issue35Fixture 起一个真实 echo MCP server（httptest），返回注册好的
// registry、目标 service 与上游调用计数器。
func issue35Fixture(t *testing.T) (context.Context, *ToolRegistry, *types.MCPService, *atomic.Int32) {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	var calls atomic.Int32
	server := sdkserver.NewMCPServer("issue35", "1", sdkserver.WithToolCapabilities(false))
	server.AddTool(
		sdkmcp.NewTool("echo", sdkmcp.WithDescription("echo text back"), sdkmcp.WithString("text", sdkmcp.Required())),
		func(_ context.Context, r sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			calls.Add(1)
			return sdkmcp.NewToolResultText(fmt.Sprintf("echo: %v", r.GetArguments()["text"])), nil
		},
	)
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		transport.ServeHTTP(w, r)
	}))
	t.Cleanup(httpServer.Close)

	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID:            "issue35-svc",
		TenantID:      7,
		Enabled:       true,
		Name:          "echo",
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx := catalogTestContext()
	registry := NewToolRegistry()
	_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, nil, 0, nil, nil)
	require.NoError(t, err)
	return ctx, registry, service, &calls
}

// registerIssue35DirectTool 复刻 RefreshMCPTools 的直注册绑定（保留名字
// 与 ref，但不登记 describe），得到「registry 里有、knownCallableRef=false」
// 的 MCPRegisteredTool——正是会话历史重放后的形态。
func registerIssue35DirectTool(t *testing.T, ctx context.Context, r *ToolRegistry, service *types.MCPService) *MCPRegisteredTool {
	t.Helper()
	discovery, err := r.GetTool(ToolDiscoverMCPTools)
	require.NoError(t, err)
	c := discovery.(*MCPDiscoverTool).catalog
	snapshot, _, err := c.snapshot(ctx, service.ID, false)
	require.NoError(t, err)
	require.NotEmpty(t, snapshot)
	tool := snapshot[0]
	bound := NewMCPTool(tool.service, tool.mcpTool, tool.mcpManager, tool.gate, tool.authWaitTimeoutSeconds)
	bound.registeredName = mcpRegisteredName(tool)
	direct := &MCPRegisteredTool{MCPTool: bound, catalog: c, ref: mcpToolRef(tool)}
	r.RegisterTool(direct)
	return direct
}

func TestMCPRegisteredToolFastFailsBeforeDescribe(t *testing.T) {
	ctx, registry, service, calls := issue35Fixture(t)
	direct := registerIssue35DirectTool(t, ctx, registry, service)

	// 未 describe：fast-fail，与 call_mcp_tool 路径同文案，零挂起零上游触达。
	result, err := registry.ExecuteTool(ctx, direct.Name(), json.RawMessage(`{"text":"hi"}`))
	require.NoError(t, err)
	require.False(t, result.Success)
	require.Contains(t, result.Error, "schema has not been described")
	require.Contains(t, result.Error, `server_id="issue35-svc"`, "fast-fail 文案须带 describe 指引")
	require.Zero(t, calls.Load(), "fast-fail 不得触达上游 MCP server（e2e 挂起点）")

	// IsMCP Tool 通道判定。
	require.True(t, registry.IsMCPTool(direct.Name()), "直注册工具是 MCP 通道")
	require.True(t, registry.IsMCPTool(ToolCallMCPTool), "call_mcp_tool 代理是 MCP 通道")
	require.False(t, registry.IsMCPTool("search_knowledge"), "内置工具不是 MCP 通道")
	require.False(t, registry.IsMCPTool("mcp_unknown_hash"), "未注册名字不是 MCP 通道")

	// describe 后同一注册名恢复可执行：fast-fail 只拦未 describe 状态。
	describeTool(ctx, t, registry, service.ID, "echo")
	result, err = registry.ExecuteTool(ctx, direct.Name(), json.RawMessage(`{"text":"hi"}`))
	require.NoError(t, err)
	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "echo: hi")
	require.EqualValues(t, 1, calls.Load())
}

func TestIsMCPToolWithoutCatalog(t *testing.T) {
	registry := NewToolRegistry()
	require.False(t, registry.IsMCPTool(ToolCallMCPTool),
		"未安装 MCP catalog 时 call_mcp_tool 名字不应当作 MCP 通道")
	require.False(t, registry.IsMCPTool("anything"))
}
