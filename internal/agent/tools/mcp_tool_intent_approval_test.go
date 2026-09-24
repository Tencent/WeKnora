package tools

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/approval"
	"github.com/Tencent/WeKnora/internal/event"
	internalmcp "github.com/Tencent/WeKnora/internal/mcp"
	"github.com/Tencent/WeKnora/internal/types"
	"github.com/Tencent/WeKnora/internal/utils"
	sdkmcp "github.com/mark3labs/mcp-go/mcp"
	sdkserver "github.com/mark3labs/mcp-go/server"
	"github.com/stretchr/testify/require"
)

// intentForcedGate：工具未配置审批策略（NeedsApproval=false），用于验证
// T41 的 IntentGate 强制审批标记能独立触发 RequestAndWait。
type intentForcedGate struct {
	request approval.PendingRequest
	calls   int
	approve bool
}

func (*intentForcedGate) IsEnabled(context.Context, uint64, string, string) (bool, error) {
	return true, nil
}
func (*intentForcedGate) NeedsApproval(context.Context, uint64, string, string) bool { return false }
func (g *intentForcedGate) RequestAndWait(_ context.Context, req approval.PendingRequest) (approval.Decision, error) {
	g.calls++
	g.request = req
	return approval.Decision{Approved: g.approve, Reason: "test decision"}, nil
}

// intentApprovalFixture 起一个单工具（echo）的测试 MCP server 并注册到
// registry。返回 registry、可执行的 toolRef 与工具调用计数；测试方自行
// 构造执行 ctx（以便注入 IntentGate 强制审批标记）。
func intentApprovalFixture(t *testing.T, gate approval.MCPApproval) (*ToolRegistry, string, *atomic.Int32) {
	t.Helper()
	utils.SetSSRFWhitelistFromRaw("127.0.0.1")
	t.Cleanup(utils.ResetSSRFWhitelistForTest)

	server := sdkserver.NewMCPServer("intent-approval-test", "1", sdkserver.WithToolCapabilities(false))
	var calls atomic.Int32
	server.AddTool(
		sdkmcp.NewTool("echo", sdkmcp.WithString("text", sdkmcp.Required())),
		func(_ context.Context, request sdkmcp.CallToolRequest) (*sdkmcp.CallToolResult, error) {
			calls.Add(1)
			return sdkmcp.NewToolResultText("echo:" + request.GetArguments()["text"].(string)), nil
		},
	)
	transport := sdkserver.NewStreamableHTTPServer(server, sdkserver.WithStateLess(true))
	httpServer := httptest.NewServer(
		http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { transport.ServeHTTP(w, r) }),
	)
	t.Cleanup(httpServer.Close)

	manager := internalmcp.NewMCPManager(nil)
	t.Cleanup(manager.Shutdown)
	service := &types.MCPService{
		ID:            "intent-svc",
		TenantID:      7,
		Enabled:       true,
		Name:          "IntentSvc",
		URL:           &httpServer.URL,
		TransportType: types.MCPTransportHTTPStreamable,
	}
	ctx := catalogTestContext()
	registry := NewToolRegistry()
	_, err := RegisterMCPTools(ctx, registry, []*types.MCPService{service}, manager, gate, 0, nil, nil)
	require.NoError(t, err)
	page := discoverPage(ctx, t, registry, map[string]any{"mode": "list_tools", "server_id": service.ID})
	require.Len(t, page.Tools, 1)
	described := describeTool(ctx, t, registry, service.ID, page.Tools[0].Name)
	return registry, described.ToolRef, &calls
}

// execMCPWithCtx 用自定义 ctx 执行 echo 工具（ToolExecContext 含 EventBus）。
func execMCPWithCtx(t *testing.T, registry *ToolRegistry, toolRef, arguments string, decorate func(context.Context) context.Context) *types.ToolResult {
	t.Helper()
	ctx := catalogTestContext()
	ctx = WithToolExecContext(ctx, &ToolExecContext{
		EventBus:           event.NewEventBus(),
		ToolCallID:         "intent-call-1",
		SessionID:          "session-1",
		AssistantMessageID: "message-1",
		ApprovalCtx:        ctx,
	})
	if decorate != nil {
		ctx = decorate(ctx)
	}
	raw, _ := json.Marshal(map[string]any{"tool_ref": toolRef, "arguments": json.RawMessage(arguments)})
	result, err := registry.ExecuteTool(ctx, ToolCallMCPTool, raw)
	require.NoError(t, err)
	return result
}

// TestMCPIntentForcedApprovalApproved 验收 [unit]（issue #18）批准面：
// ctx 带 IntentGate 强制审批标记 + 审批人批准 → 工具真正执行且成功；
// 审批卡片的描述含策略理由。
func TestMCPIntentForcedApprovalApproved(t *testing.T) {
	gate := &intentForcedGate{approve: true}
	registry, toolRef, calls := intentApprovalFixture(t, gate)

	result := execMCPWithCtx(t, registry, toolRef, `{"text":"hi"}`, func(ctx context.Context) context.Context {
		return approval.WithIntentRequirement(ctx, "违反策略约束「高风险操作需人工确认」")
	})

	require.True(t, result.Success, result.Error)
	require.Contains(t, result.Output, "echo:hi")
	require.Equal(t, 1, gate.calls, "IntentGate 标记必须触发 RequestAndWait")
	require.Contains(t, gate.request.Description, "意图策略要求人工审批")
	require.Contains(t, gate.request.Description, "高风险操作需人工确认")
	require.Equal(t, "intent-call-1", gate.request.ToolCallID)
	require.Equal(t, int32(1), calls.Load(), "批准后工具必须真正执行")
}

// TestMCPIntentForcedApprovalRejected 验收 [unit]（issue #18）拒绝面：
// 审批人拒绝 → 工具未执行，错误文本含拒绝原因（agent 可见）。
func TestMCPIntentForcedApprovalRejected(t *testing.T) {
	gate := &intentForcedGate{approve: false}
	registry, toolRef, calls := intentApprovalFixture(t, gate)

	result := execMCPWithCtx(t, registry, toolRef, `{"text":"hi"}`, func(ctx context.Context) context.Context {
		return approval.WithIntentRequirement(ctx, "策略要求人工确认")
	})

	require.False(t, result.Success)
	require.Contains(t, result.Error, "test decision")
	require.Equal(t, int32(0), calls.Load(), "拒绝后工具绝不可执行")
}

// TestMCPNoIntentMarkerSkipsForcedGate：无标记且工具未配置审批 → 不触发
// RequestAndWait（拦截面最窄化校验，双向断言的放行面）。
func TestMCPNoIntentMarkerSkipsForcedGate(t *testing.T) {
	gate := &intentForcedGate{approve: true}
	registry, toolRef, calls := intentApprovalFixture(t, gate)

	result := execMCPWithCtx(t, registry, toolRef, `{"text":"plain"}`, nil)

	require.True(t, result.Success, result.Error)
	require.Equal(t, 0, gate.calls, "无 IntentGate 标记不得触发审批")
	require.Equal(t, int32(1), calls.Load())
}
