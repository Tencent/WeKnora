package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"strings"
	"sync"
	"testing"

	"github.com/Tencent/WeKnora/internal/agent/intentgate"
	"github.com/Tencent/WeKnora/internal/agent/tools"
	"github.com/Tencent/WeKnora/internal/logger"
	"github.com/Tencent/WeKnora/internal/types"
)

// fakeGate 是 engine 接缝测试的测试缝：实现 intentgate.Gate 接口，
// 返回预置 verdict/error，并记录每次调用的输入供断言。
type fakeGate struct {
	verdict intentgate.Verdict
	err     error

	mu    sync.Mutex
	calls []intentgate.ToolCallInput
}

func (f *fakeGate) Evaluate(_ context.Context, in intentgate.ToolCallInput) (intentgate.Verdict, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, in)
	return f.verdict, f.err
}

func (f *fakeGate) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.calls)
}

func (f *fakeGate) lastInput() intentgate.ToolCallInput {
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.calls) == 0 {
		return intentgate.ToolCallInput{}
	}
	return f.calls[len(f.calls)-1]
}

// intentGateTestEngine 装好一个真 toolRegistry 与一个计数工具，
// 返回 engine 与"工具是否被执行"的探针。
func intentGateTestEngine(t *testing.T) (*AgentEngine, *int) {
	t.Helper()
	engine := newTestEngine(t, &mockChat{})
	engine.toolRegistry = tools.NewToolRegistry()
	executed := new(int)
	engine.toolRegistry.RegisterTool(&orderedTestTool{
		BaseTool: tools.NewBaseTool("search_knowledge", "", json.RawMessage(`{"type":"object"}`)),
		run: func(context.Context) *types.ToolResult {
			*executed++
			return &types.ToolResult{Success: true}
		},
	})
	return engine, executed
}

func runGatedToolCall(engine *AgentEngine) types.ToolCall {
	tc := types.LLMToolCall{
		ID:       "call-1",
		Function: types.FunctionCall{Name: "search_knowledge", Arguments: `{"query":"活动方案"}`},
	}
	return engine.runToolCall(context.Background(), tc, 0, 0, 1, "session-1", "msg-1")
}

// TestIntentGateAllowExecutesTool 验收 [unit]：fake Gate 返回 allow → 工具被执行。
func TestIntentGateAllowExecutesTool(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{Action: intentgate.ActionAllow}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("allow verdict: tool must execute exactly once, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("allow verdict: tool call must succeed, got %+v", toolCall.Result)
	}
	if gate.callCount() != 1 {
		t.Fatalf("gate must be consulted exactly once per tool call, got %d", gate.callCount())
	}
	in := gate.lastInput()
	if in.ToolName != "search_knowledge" {
		t.Fatalf("gate input ToolName = %q, want search_knowledge", in.ToolName)
	}
	if in.SessionID != "session-1" {
		t.Fatalf("gate input SessionID = %q, want session-1", in.SessionID)
	}
	if string(in.Args) != `{"query":"活动方案"}` {
		t.Fatalf("gate input Args = %s", in.Args)
	}
}

// TestIntentGateObserveDenyStillExecutes 验收 [unit]：observe 模式 deny →
// 工具仍执行（不该拦的放行）且 verdict 被记录（该记的记下），双向断言。
func TestIntentGateObserveDenyStillExecutes(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{verdict: intentgate.Verdict{
		Action:   intentgate.ActionDeny,
		PolicyID: "pol-1",
		Reason:   "会话历史无删除意图",
		Layer:    intentgate.LayerRule,
	}}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	// observe 语义：deny 只记录不拦截，工具照常执行成功。
	if *executed != 1 {
		t.Fatalf("observe-mode deny must not block execution, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("observe-mode deny must not fail the tool call, got %+v", toolCall.Result)
	}
	// verdict 被记录：结构化日志含 action / policy / reason。
	out := buf.String()
	for _, want := range []string{"deny", "pol-1", "会话历史无删除意图"} {
		if !strings.Contains(out, want) {
			t.Fatalf("verdict log must contain %q, got:\n%s", want, out)
		}
	}
}

// TestIntentGateNilKeepsCurrentBehavior 验收 [unit]：Gate 为 nil 时
// 行为与现状完全一致——不调用任何 Gate，工具正常执行。
func TestIntentGateNilKeepsCurrentBehavior(t *testing.T) {
	engine, executed := intentGateTestEngine(t)
	if engine.intentGate != nil {
		t.Fatal("intentGate must default to nil")
	}

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("nil gate: tool must execute, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("nil gate: tool call must succeed, got %+v", toolCall.Result)
	}
}

// TestIntentGateErrorFailsOpen 设计 §9：observe 永远 fail-open——
// Evaluate 返回错误时工具照常执行，错误记 warn 日志。
func TestIntentGateErrorFailsOpen(t *testing.T) {
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	defer logger.SetOutput(os.Stdout)

	engine, executed := intentGateTestEngine(t)
	gate := &fakeGate{err: errors.New("policy store unavailable")}
	engine.SetIntentGate(gate)

	toolCall := runGatedToolCall(engine)

	if *executed != 1 {
		t.Fatalf("gate error must fail open, executed=%d", *executed)
	}
	if toolCall.Result == nil || !toolCall.Result.Success {
		t.Fatalf("gate error must not fail the tool call, got %+v", toolCall.Result)
	}
	if !strings.Contains(buf.String(), "policy store unavailable") {
		t.Fatalf("gate error must be logged, got:\n%s", buf.String())
	}
}
