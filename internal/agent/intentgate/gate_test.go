package intentgate

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/Tencent/WeKnora/internal/types"
)

// TestNoopGateAlwaysAllows 验收：NoopGate 恒返回 allow，
// 无论输入内容如何（含高危工具名与空输入）。
func TestNoopGateAlwaysAllows(t *testing.T) {
	var gate Gate = NewNoopGate()

	cases := []ToolCallInput{
		{},
		{
			TenantID:   1,
			SessionID:  "sess-1",
			ToolName:   "wiki_delete_page",
			Args:       json.RawMessage(`{"page_id":"p_8842"}`),
			UserPrompt: "把过时的活动方案删掉",
			History:    []types.Message{},
		},
		{
			TenantID:  2,
			ToolName:  "shell_exec",
			Args:      json.RawMessage(`{"command":"rm -rf ~"}`),
			Principal: types.Principal{},
		},
	}

	for i, in := range cases {
		v, err := gate.Evaluate(context.Background(), in)
		if err != nil {
			t.Fatalf("case %d: Evaluate returned error: %v", i, err)
		}
		if v.Action != ActionAllow {
			t.Fatalf("case %d: NoopGate must always allow, got %q", i, v.Action)
		}
	}
}

// TestActionJSONRoundTrip 验收：Verdict 四种取值
// （allow/deny/require_approval/uncertain）序列化往返一致。
func TestActionJSONRoundTrip(t *testing.T) {
	actions := []Action{ActionAllow, ActionDeny, ActionRequireApproval, ActionUncertain}
	for _, a := range actions {
		data, err := json.Marshal(a)
		if err != nil {
			t.Fatalf("marshal %q: %v", a, err)
		}
		var back Action
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("unmarshal %s: %v", data, err)
		}
		if back != a {
			t.Fatalf("round trip mismatch: %q -> %s -> %q", a, data, back)
		}
	}

	// 明确序列化形态：字符串值而非序号，保证 verdict 日志可读、
	// 落库枚举值与 JSON 一致。
	expect := map[Action]string{
		ActionAllow:           `"allow"`,
		ActionDeny:            `"deny"`,
		ActionRequireApproval: `"require_approval"`,
		ActionUncertain:       `"uncertain"`,
	}
	for a, want := range expect {
		data, _ := json.Marshal(a)
		if string(data) != want {
			t.Fatalf("action %q serialized as %s, want %s", a, data, want)
		}
	}
}

// TestVerdictJSONRoundTrip 验收：Verdict 结构体整体序列化往返一致。
func TestVerdictJSONRoundTrip(t *testing.T) {
	verdicts := []Verdict{
		{Action: ActionAllow},
		{Action: ActionDeny, PolicyID: "pol-1", Reason: "命中规则 r1", Layer: LayerRule},
		{Action: ActionRequireApproval, PolicyID: "pol-2", Reason: "高危工具", Layer: LayerJudge},
		{Action: ActionUncertain, Reason: "judge 输出解析失败", Layer: LayerJudge},
	}
	for i, v := range verdicts {
		data, err := json.Marshal(v)
		if err != nil {
			t.Fatalf("case %d marshal: %v", i, err)
		}
		var back Verdict
		if err := json.Unmarshal(data, &back); err != nil {
			t.Fatalf("case %d unmarshal: %v", i, err)
		}
		if back != v {
			t.Fatalf("case %d round trip mismatch: %+v -> %s -> %+v", i, v, data, back)
		}
	}
}

// TestUnknownActionRejected 未知 Action 值反序列化应报错，
// 防止脏数据静默进入 verdict 表。
func TestUnknownActionRejected(t *testing.T) {
	var a Action
	if err := json.Unmarshal([]byte(`"block"`), &a); err == nil {
		t.Fatal("unknown action value should fail to unmarshal")
	}
}

// TestGateInterfaceSatisfied 编译期断言：NoopGate 实现 Gate 接口。
func TestGateInterfaceSatisfied(t *testing.T) {
	var _ Gate = NewNoopGate()
}
