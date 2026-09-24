package agent

// issue #35（T41 seam 盲区）验收 [unit]：
//   - enforce require_approval 命中直注册 MCP 工具时走人工审批通道
//     （IntentRequirement 挂上执行 ctx → MCPTool.Execute 的 RequestAndWait），
//     产审批卡片所需的 PendingRequest（含策略理由与注册名）；
//   - MCPCallTarget 解析失败（target=nil）时 seam 回落 registry 类型判定
//     （IsMCPTool），绝不掉进 no_channel 静默放行分支；
//   - 未 describe 的直注册工具在 seam 放行后由执行体 fast-fail（同文案）——
//     「fast-fail 前不产卡」语义：不产审批卡片，错误直接还给模型。

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	agenttools "github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/models/chat"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// recordingApprovalGate 记录 RequestAndWait 收到的 PendingRequest（审批
// 卡片的数据源），一律批准。NeedsApproval 恒 false——强制审批只可能来自
// IntentGate 挂上的 IntentRequirement，正好隔离出 seam 行为。
type recordingApprovalGate struct {
	request approval.PendingRequest
}

func (*recordingApprovalGate) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return true, nil
}
func (*recordingApprovalGate) NeedsApproval(context.Context, uint64, string, string) bool {
	return false
}
func (g *recordingApprovalGate) RequestAndWait(_ context.Context, req approval.PendingRequest) (approval.Decision, error) {
	g.request = req
	return approval.Decision{Approved: true}, nil
}

// issue35MCPServer 起真实 echo MCP server（httptest），返回 service 模板
// （URL 指向该 server）与上游调用计数器。transport 用各自 registry 的
// manager 连接（生产形态），server 本身可被多个 registry 复用。
func issue35MCPServer(t *testing.T) (*types.MCPService, *atomic.Int32) {
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
	service := &types.MCPService{
		ID:            "svc-ra",
		TenantID:      7,
		Enabled:       true,
		Name:          "echo",
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	return service, &calls
}

// issue35Registry 用 production 形态（RegisterMCPTools + manager）装配
// 一个挂有 MCP catalog 的 registry。direct=true 走 full exposure（工具
// 全部直注册且 describe 随注册登记）；direct=false 走 deferred（只广告
// 来源，describe 前不注册）。
func issue35Registry(t *testing.T, ctx context.Context, service *types.MCPService, gate *recordingApprovalGate, direct bool) *agenttools.ToolRegistry {
	t.Helper()
	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	registry := agenttools.NewToolRegistry()
	_, err := agenttools.RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
	require.NoError(t, err)
	if direct {
		registry.PrepareMCPToolsDirect(ctx)
	} else {
		registry.PrepareMCPTools(ctx)
	}
	return registry
}

// directToolName 取 registry 里第一个 mcp_ 前缀的直注册名。
func directToolName(t *testing.T, registry *agenttools.ToolRegistry) string {
	t.Helper()
	for _, name := range registry.ListTools() {
		if strings.HasPrefix(name, "mcp_") {
			return name
		}
	}
	t.Fatal("registry 里应有 mcp_ 前缀直注册工具")
	return ""
}

func requireApprovalGateEngine(t *testing.T) (*AgentEngine, *bytes.Buffer) {
	t.Helper()
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	t.Cleanup(func() { logger.SetOutput(os.Stdout) })
	engine := newTestEngine(t, &mockChat{})
	engine.SetIntentGate(&fakeGate{verdict: intentgate.Verdict{
		Action:   intentgate.ActionRequireApproval,
		PolicyID: "pol-ra-direct",
		Mode:     types.VerdictModeEnforce,
		Layer:    intentgate.LayerJudge,
		Reason:   "策略判定：需人工确认",
	}})
	return engine, &buf
}

// TestIntentGateRequireApprovalDirectMCPUsesApprovalChannel：enforce 的
// require_approval 命中直注册 MCP 工具 → 审批通道被走（RequestAndWait
// 收到带策略理由的 PendingRequest），工具获批后真实执行，且绝不出现
// no_channel 告警。
func TestIntentGateRequireApprovalDirectMCPUsesApprovalChannel(t *testing.T) {
	gate := &recordingApprovalGate{}
	service, calls := issue35MCPServer(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	registry := issue35Registry(t, ctx, service, gate, true)
	directName := directToolName(t, registry)

	engine, buf := requireApprovalGateEngine(t)
	engine.toolRegistry = registry

	tc := types.LLMToolCall{
		ID:       "call-ra-direct",
		Function: types.FunctionCall{Name: directName, Arguments: `{"text":"hi"}`},
	}
	toolCall := engine.runToolCall(ctx, tc, 0, 0, 1, "session-1", "msg-1")

	if strings.Contains(buf.String(), "intentgate.require_approval_no_channel") {
		t.Fatalf("直注册 MCP 工具不得走 no_channel 分支，logs: %s", buf.String())
	}
	require.Equal(t, directName, gate.request.RegisteredToolName,
		"审批 PendingRequest 必须带直注册工具名（卡片可定位工具）")
	require.Contains(t, gate.request.Description, "[意图策略要求人工审批]",
		"卡片描述须标明策略强制审批")
	require.Contains(t, gate.request.Description, "策略判定：需人工确认",
		"卡片描述须含策略理由")
	require.EqualValues(t, 1, calls.Load(), "批准后工具必须真实执行")
	require.NotNil(t, toolCall.Result)
	require.True(t, toolCall.Result.Success, "获批执行必须成功: %+v", toolCall.Result)
}

// TestIntentGateApprovalChannelNilTargetFallback：target 解析失败时 seam
// 的回落判定（hasApprovalChannel）。生产里 target 与执行走同一授权上下文，
// target=nil 而执行可达的情形理论上不存在；该回落是 issue #35 e2e 实测
// 失配的防御——即便 target 缺失，registry 类型判定仍把审批标记挂上，绝
// 不静默掉进 no_channel 分支。
func TestIntentGateApprovalChannelNilTargetFallback(t *testing.T) {
	gate := &recordingApprovalGate{}
	service, _ := issue35MCPServer(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))
	registry := issue35Registry(t, ctx, service, gate, true)
	directName := directToolName(t, registry)

	engine, _ := requireApprovalGateEngine(t)
	engine.toolRegistry = registry

	require.False(t, engine.hasApprovalChannel(nil, "search_knowledge"),
		"内置工具 + nil target = 无审批通道")
	require.True(t, engine.hasApprovalChannel(nil, directName),
		"nil target 时直注册 MCP 工具仍判为有审批通道（IsMCPTool 回落）")
	require.True(t, engine.hasApprovalChannel(nil, agenttools.ToolCallMCPTool),
		"nil target 时 call_mcp_tool 代理仍判为有审批通道（IsMCPTool 回落）")

	// 非 MCP 工具的既有语义不变：no_channel 告警仍会出现（防回归）。
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	engine.toolRegistry = agenttools.NewToolRegistry()
	executed := new(int)
	engine.toolRegistry.RegisterTool(&orderedTestTool{
		BaseTool: agenttools.NewBaseTool("search_knowledge", "", json.RawMessage(`{"type":"object"}`)),
		run: func(context.Context) *types.ToolResult {
			*executed++
			return &types.ToolResult{Success: true}
		},
	})
	tc := types.LLMToolCall{
		ID:       "call-ra-builtin",
		Function: types.FunctionCall{Name: "search_knowledge", Arguments: `{"query":"x"}`},
	}
	toolCall := engine.runToolCall(context.Background(), tc, 0, 0, 1, "session-1", "msg-1")
	require.Equal(t, 1, *executed, "内置工具的 require_approval 仍放行执行（T41 语义不变）")
	require.True(t, toolCall.Result != nil && toolCall.Result.Success)
	require.Contains(t, buf.String(), "intentgate.require_approval_no_channel",
		"内置工具必须保留 no_channel 结构化告警")
}

// TestIntentGateRequireApprovalHistoryReplayedDirectTool：经会话历史重放
// 直注册的 MCP 工具（deferred 模式 + RememberMCPHistory——e2e T41 的模型
// 路径形态）在 enforce require_approval 下走人工审批通道：产卡片
// （PendingRequest 带注册名与策略理由），获批后真实执行。审批等待发生
// 在执行体内（RequestAndWait 先于参数解析与上游调用），这是 #35 把 seam
// 放宽到 registry 类型判定的收益面。
func TestIntentGateRequireApprovalHistoryReplayedDirectTool(t *testing.T) {
	gate := &recordingApprovalGate{}
	service, calls := issue35MCPServer(t)
	ctx := context.WithValue(context.Background(), types.TenantIDContextKey, uint64(7))

	// 先学直注册名（full exposure 下名字确定，跨 registry 复用）。
	directName := directToolName(t, issue35Registry(t, ctx, service, &recordingApprovalGate{}, true))

	// 新 registry 走 deferred + 历史重放：只登记名字、describe 状态随
	// RefreshMCPTools 的 rememberAdvertised 落地（注册即可调用）。
	registry := issue35Registry(t, ctx, service, gate, false)
	registry.RememberMCPHistory([]chat.Message{{
		Role: "assistant",
		ToolCalls: []chat.ToolCall{{
			Function: chat.FunctionCall{Name: directName, Arguments: "{}"},
		}},
	}})
	registry.RefreshMCPTools(ctx)
	require.True(t, registry.IsMCPTool(directName), "历史重放后直注册工具应已注册")

	engine, buf := requireApprovalGateEngine(t)
	engine.toolRegistry = registry

	tc := types.LLMToolCall{
		ID:       "call-ra-undescribed",
		Function: types.FunctionCall{Name: directName, Arguments: `{"text":"hi"}`},
	}
	toolCall := engine.runToolCall(ctx, tc, 0, 0, 1, "session-1", "msg-1")

	if strings.Contains(buf.String(), "intentgate.require_approval_no_channel") {
		t.Fatalf("直注册 MCP 工具不得走 no_channel 分支，logs: %s", buf.String())
	}
	require.Equal(t, directName, gate.request.RegisteredToolName,
		"审批 PendingRequest 必须带直注册工具名")
	require.Contains(t, gate.request.Description, "策略判定：需人工确认")
	require.EqualValues(t, 1, calls.Load(), "获批后工具必须真实执行")
	require.True(t, toolCall.Result != nil && toolCall.Result.Success, "%+v", toolCall.Result)
}
